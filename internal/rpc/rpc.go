// Package rpc is the line protocol a panel uses to ask the questions the
// command line already answers.
//
// It is newline-delimited JSON: one request per line on stdin, one response per
// line on stdout, nothing else. A panel cannot scrape a human report — it has no
// way to tell "no findings" from "the report was cut off" — so the same
// profile.Result and profile.Diff values the CLI renders are serialized whole.
//
// Three rules shape this file:
//
//   - stdout carries protocol and only protocol. Diagnostics go to stderr, so a
//     stray debug print cannot corrupt the stream a client is parsing.
//   - A bad line is answered, not fatal. A sidecar that exits on one malformed
//     request turns a client bug into a dead panel.
//   - Nothing is judged here. profile.Assess and profile.Compare are the only
//     judges in this repository; a second opinion in the protocol layer is how a
//     panel ends up contradicting the CLI on the same listing.
package rpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/atop0914/dshfit/internal/profile"
	"github.com/atop0914/dshfit/internal/version"
)

// Protocol is the wire format version, reported by the `version` method. A
// client that does not recognise it should refuse the sidecar rather than guess
// at field meanings it may be reading wrong.
const Protocol = 1

// DefaultMaxLine bounds one request line. A listing for a few hundred packages
// is a few hundred KiB; 8 MiB is far past that and still small enough that a
// runaway client cannot exhaust memory through this channel.
const DefaultMaxLine = 8 << 20

// DefaultWorkers is how many requests may be judged at once. Assessment is
// CPU-bound and touches the filesystem, so a handful of workers is enough to
// keep a panel responsive while bounding memory.
const DefaultWorkers = 4

// Error codes. They are stable strings because a client branches on them: a
// wrong method is a client bug, while invalid input is something the user can
// fix.
const (
	// CodeBadRequest is a malformed line, a missing id, or params of the wrong
	// shape.
	CodeBadRequest = "bad-request"
	// CodeUnknownMethod means the sidecar does not answer that method. It is an
	// error and never an empty result: an empty result is indistinguishable from
	// "asked, and everything is fine".
	CodeUnknownMethod = "unknown-method"
	// CodeInvalidInput means the request was well formed but could not be
	// answered: an unparseable host version, an unreadable listing.
	CodeInvalidInput = "invalid-input"
	// CodeInternal is a response that could not be serialized.
	CodeInternal = "internal"
)

// Error is the structured failure a client shows the user.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// request is one line of stdin.
//
// ID is echoed verbatim rather than retyped as an int: a client is free to use
// strings, and coercing them here would hand back an id the client cannot match.
type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// response is one line of stdout. Exactly one of Result and Error is set; the
// id is always present, and null when the request could not be parsed far
// enough to know it. A null id is an honest "unpairable", not a guess.
type response struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// query is the params object shared by profile and diff. Unused fields are
// ignored so a newer client can send fields this sidecar does not know yet.
type query struct {
	Host string `json:"host"`
	From string `json:"from"`
	To   string `json:"to"`
	// Listing is either the listing text itself or a path to one. See listing().
	Listing string `json:"listing"`
	// Base is the directory relative package paths resolve against. It only
	// applies to an inline listing: a path already answers that question, and
	// letting a caller override it would be a second, silently different rule.
	Base string `json:"base"`
}

// versionInfo is the answer to `version`.
type versionInfo struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

// Config tunes one Serve call. The zero value is usable.
type Config struct {
	// MaxLine is the largest accepted request line in bytes. Zero means
	// DefaultMaxLine.
	MaxLine int
	// Workers is how many requests may be in flight. Zero means DefaultWorkers.
	Workers int
}

func (c Config) maxLine() int {
	if c.MaxLine <= 0 {
		return DefaultMaxLine
	}
	return c.MaxLine
}

func (c Config) workers() int {
	if c.Workers <= 0 {
		return DefaultWorkers
	}
	return c.Workers
}

// errLineTooLong is the sentinel readLine returns for an over-long request. The
// line has already been discarded when it is returned, so the caller can answer
// the error and keep reading from a request boundary.
var errLineTooLong = errors.New("request line exceeds the limit")

// Serve answers requests from in until EOF and returns nil.
//
// A non-nil error means stdout itself broke: there is nowhere left to report
// anything, and the process should exit non-zero. Per-request failures are
// responses, never errors from here — that distinction is what keeps one bad
// request from ending the session.
//
// Responses may arrive in any order; clients pair them by id. A single writer
// owns stdout, so lines never interleave.
func Serve(in io.Reader, out io.Writer, cfg Config) error {
	r := bufio.NewReader(in)
	workers := cfg.workers()
	maxLine := cfg.maxLine()

	responses := make(chan response, workers)
	writeErr := make(chan error, 1)
	go writeResponses(out, responses, writeErr)

	sem := make(chan struct{}, workers)
	var inflight sync.WaitGroup

	var readErr error
	for {
		line, err := readLine(r, maxLine)
		switch {
		case errors.Is(err, errLineTooLong):
			// One error for the whole line, not one per discarded chunk, and the
			// session continues: the client can retry with a shorter listing.
			responses <- response{Error: &Error{
				Code: CodeBadRequest,
				Message: fmt.Sprintf("request line is longer than %d bytes; "+
					"send the listing as a path instead of inline text", maxLine),
			}}
			continue
		case errors.Is(err, io.EOF):
			goto drain
		case err != nil:
			readErr = err
			goto drain
		}
		if strings.TrimSpace(line) == "" {
			// A blank line carries no request. Answering it would be inventing a
			// request the client never made.
			continue
		}

		req, bad := decodeRequest(line)
		if bad != nil {
			responses <- response{ID: req.ID, Error: bad}
			continue
		}

		sem <- struct{}{}
		inflight.Add(1)
		go func(req request) {
			defer inflight.Done()
			defer func() { <-sem }()
			responses <- handle(req)
		}(req)
	}

drain:
	inflight.Wait()
	close(responses)
	if err := <-writeErr; err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	if readErr != nil {
		return fmt.Errorf("read request: %w", readErr)
	}
	return nil
}

// writeResponses is the only writer of out. It exists so that concurrent
// handlers cannot interleave two JSON documents on one line — a failure mode
// that produces a stream which parses fine until it does not.
func writeResponses(out io.Writer, responses <-chan response, errCh chan<- error) {
	w := bufio.NewWriter(out)
	var failure error
	for resp := range responses {
		if failure != nil {
			// Keep draining: the producers are blocked on this channel, and
			// abandoning it would hang Serve instead of returning the error.
			continue
		}
		line, err := encodeResponse(resp)
		if err != nil {
			failure = err
			continue
		}
		if _, err := w.Write(line); err != nil {
			failure = err
			continue
		}
		// Flush per response: a client reads line by line, so a response held
		// in the buffer is a panel that waits until the buffer fills.
		if err := w.Flush(); err != nil {
			failure = err
		}
	}
	if failure == nil {
		failure = w.Flush()
	}
	errCh <- failure
}

// encodeResponse marshals one response to a single line.
func encodeResponse(resp response) ([]byte, error) {
	line, err := json.Marshal(resp)
	if err != nil {
		// The only field that can fail to marshal is Result, and a client that
		// gets no line at all cannot tell "failed" from "still working". So the
		// failure is reported in the protocol, with the id it belongs to.
		fallback, ferr := json.Marshal(response{ID: resp.ID, Error: &Error{
			Code:    CodeInternal,
			Message: "response could not be serialized: " + err.Error(),
		}})
		if ferr != nil {
			return nil, fmt.Errorf("marshal error response: %w", ferr)
		}
		return append(fallback, '\n'), nil
	}
	return append(line, '\n'), nil
}

// decodeRequest parses one line into a request, or returns the error to answer.
// The partial request is returned alongside the error so the id can be echoed
// when it was readable.
func decodeRequest(line string) (request, *Error) {
	var req request
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return request{}, &Error{
			Code:    CodeBadRequest,
			Message: "line is not a JSON object: " + err.Error(),
		}
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return req, &Error{
			Code: CodeBadRequest,
			Message: "request has no id; every request needs one, because the only way to pair a " +
				"response with its request is the id it echoes back",
		}
	}
	if strings.TrimSpace(req.Method) == "" {
		return req, &Error{Code: CodeBadRequest, Message: "request has no method"}
	}
	return req, nil
}

// handle answers one well-formed request. Every branch returns a response: a
// path that returns nothing would leave the client waiting for a line that is
// never written.
func handle(req request) response {
	if req.Method == "version" {
		return response{ID: req.ID, Result: versionInfo{
			Name:     "dshfit",
			Version:  version.Value(),
			Protocol: Protocol,
		}}
	}

	var q query
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &q); err != nil {
			return response{ID: req.ID, Error: &Error{
				Code:    CodeBadRequest,
				Message: "params is not an object: " + err.Error(),
			}}
		}
	}

	switch req.Method {
	case "profile":
		return answerProfile(req.ID, q)
	case "diff":
		return answerDiff(req.ID, q)
	default:
		return response{ID: req.ID, Error: &Error{
			Code: CodeUnknownMethod,
			Message: fmt.Sprintf("unknown method %q; this sidecar answers profile, diff and version",
				req.Method),
		}}
	}
}

func answerProfile(id json.RawMessage, q query) response {
	if q.Host == "" {
		return response{ID: id, Error: &Error{
			Code:    CodeBadRequest,
			Message: "params.host is required: the harness version to judge against",
		}}
	}
	projects, base, err := resolveListing(q)
	if err != nil {
		return response{ID: id, Error: &Error{Code: CodeInvalidInput, Message: err.Error()}}
	}
	// The listing is judged once, by the same Assess the CLI calls. A panel that
	// re-derived verdicts from raw manifests would eventually disagree with
	// `dshfit profile` on the same file, and there would be no way to tell which
	// answer was the product.
	res, err := profile.Assess(projects, q.Host, base)
	if err != nil {
		return response{ID: id, Error: &Error{Code: CodeInvalidInput, Message: err.Error()}}
	}
	return response{ID: id, Result: res}
}

func answerDiff(id json.RawMessage, q query) response {
	// Neither side defaults. This method hands out upgrade advice; guessing
	// either host would answer a question about a version nobody named.
	if q.From == "" || q.To == "" {
		return response{ID: id, Error: &Error{
			Code:    CodeBadRequest,
			Message: "params.from and params.to are both required: the host you run today and the one you are considering",
		}}
	}
	projects, base, err := resolveListing(q)
	if err != nil {
		return response{ID: id, Error: &Error{Code: CodeInvalidInput, Message: err.Error()}}
	}
	before, err := profile.Assess(projects, q.From, base)
	if err != nil {
		return response{ID: id, Error: &Error{Code: CodeInvalidInput, Message: err.Error()}}
	}
	after, err := profile.Assess(projects, q.To, base)
	if err != nil {
		return response{ID: id, Error: &Error{Code: CodeInvalidInput, Message: err.Error()}}
	}
	return response{ID: id, Result: profile.Compare(before, after)}
}

// resolveListing turns params.listing into projects plus the base directory
// relative package paths resolve against.
//
// A listing is told apart from a path by its first non-space byte, which is the
// only reliable difference: a JSON array always starts with '[' and a path
// never does. `-` is refused because stdin is the request channel here, so
// "read it from stdin" would deadlock the very pipe it is meant to read.
func resolveListing(q query) ([]profile.Project, string, error) {
	text := strings.TrimSpace(q.Listing)
	switch {
	case text == "":
		return nil, "", errors.New("params.listing is required: the listing text, or a path to one")
	case text == "-":
		return nil, "", errors.New("`-` is not a listing here: stdin carries requests. " +
			"Pass the listing text, or a path to it")
	case text[0] == '[' || text[0] == '{':
		projects, err := profile.Parse(strings.NewReader(text))
		if err != nil {
			return nil, "", err
		}
		// An inline listing has no directory of its own, so the caller supplies
		// one; empty means "relative paths cannot be resolved", which Assess
		// reports as an unknown package instead of judging the wrong directory.
		return projects, q.Base, nil
	default:
		return profile.LoadListing(text)
	}
}

// readLine reads one newline-terminated line, bounded by max bytes.
//
// bufio.Scanner cannot be used: on an over-long line it stops scanning for
// good, so one huge request would end the session. ReadSlice lets the line be
// discarded up to its newline, which keeps the stream on a request boundary.
func readLine(r *bufio.Reader, max int) (string, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) && !errors.Is(err, io.EOF) {
			return "", err
		}
		buf = append(buf, chunk...)
		if len(buf) > max {
			if derr := discardRest(r, err); derr != nil {
				return "", derr
			}
			return "", errLineTooLong
		}
		switch {
		case err == nil:
			return strings.TrimRight(string(buf), "\r\n"), nil
		case errors.Is(err, io.EOF):
			if len(buf) == 0 {
				return "", io.EOF
			}
			// A last line without a trailing newline is still a request.
			return strings.TrimRight(string(buf), "\r\n"), nil
		}
	}
}

// discardRest consumes the remainder of an over-long line so the next read
// starts where the next request starts.
//
// The nil case is not a formality: ReadSlice returns a nil error when the
// delimiter was in the chunk it just handed back, which means the whole line has
// already been consumed. Draining there would eat the *next* request and answer
// the wrong question — silently, since the client would simply never see the
// reply it asked for.
func discardRest(r *bufio.Reader, lastErr error) error {
	if lastErr == nil || errors.Is(lastErr, io.EOF) {
		return nil
	}
	for {
		_, err := r.ReadSlice('\n')
		switch {
		case err == nil, errors.Is(err, io.EOF):
			return nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return err
		}
	}
}
