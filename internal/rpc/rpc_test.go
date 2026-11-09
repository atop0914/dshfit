package rpc

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atop0914/dshfit/internal/profile"
)

// driftListing is the fixture with a real pair of changes: one plugin that stops
// loading, one that starts. Tests that only need "some listing" use it too,
// because a fixture with movement proves more than an empty one.
func driftListing(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "testdata", "profile", "drift", "list.json")
}

// reply is one decoded response line.
type reply struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// serve runs one session over the given lines and returns the responses in the
// order they were written.
func serve(t *testing.T, cfg Config, lines ...string) []reply {
	t.Helper()
	var out bytes.Buffer
	if err := Serve(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out, cfg); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var replies []reply
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var r reply
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("response is not one JSON document per line: %v\n%s", err, line)
		}
		replies = append(replies, r)
	}
	return replies
}

// byID indexes responses so a test never depends on arrival order: the protocol
// promises pairing by id, not ordering.
func byID(t *testing.T, replies []reply) map[string]reply {
	t.Helper()
	out := make(map[string]reply, len(replies))
	for _, r := range replies {
		out[string(r.ID)] = r
	}
	return out
}

// requestLine is a compact way to write one line of stdin.
func requestLine(t *testing.T, id any, method string, params any) string {
	t.Helper()
	obj := map[string]any{"id": id, "method": method}
	if params != nil {
		obj["params"] = params
	}
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return string(b)
}

func TestServeAnswersProfileDiffAndVersion(t *testing.T) {
	listing := driftListing(t)
	replies := serve(t, Config{},
		requestLine(t, 1, "profile", map[string]any{"host": "0.2.2-rc.1", "listing": listing}),
		requestLine(t, "two", "diff", map[string]any{"from": "0.2.1-rc.3", "to": "0.2.2-rc.1", "listing": listing}),
		requestLine(t, 3, "version", nil),
	)
	if len(replies) != 3 {
		t.Fatalf("got %d responses, want 3", len(replies))
	}
	got := byID(t, replies)
	for _, id := range []string{"1", `"two"`, "3"} {
		if _, ok := got[id]; !ok {
			t.Fatalf("no response for id %s; got %v", id, got)
		}
		if got[id].Error != nil {
			t.Fatalf("id %s failed: %s", id, got[id].Error.Message)
		}
	}

	// Decoding into the real types is the assertion: the panel's TypeScript
	// types are hand-copied from these json tags, so a field that only exists in
	// the response would be invisible while a field that disappeared would make
	// the panel silently render zeroes.
	var res profile.Result
	if err := json.Unmarshal(got["1"].Result, &res); err != nil {
		t.Fatalf("profile result is not a profile.Result: %v\n%s", err, got["1"].Result)
	}
	if res.Host != "0.2.2-rc.1" {
		t.Errorf("host = %q", res.Host)
	}
	if res.Counts.Packages != len(res.Entries) {
		t.Errorf("counts.Packages = %d but %d entries; the summary must count what it lists",
			res.Counts.Packages, len(res.Entries))
	}
	if res.Counts.Blocked == 0 {
		t.Error("the drift fixture has blocked plugins; a zero here means nothing was judged")
	}

	var d profile.Diff
	if err := json.Unmarshal(got[`"two"`].Result, &d); err != nil {
		t.Fatalf("diff result is not a profile.Diff: %v\n%s", err, got[`"two"`].Result)
	}
	if d.From != "0.2.1-rc.3" || d.To != "0.2.2-rc.1" {
		t.Errorf("diff hosts = %s -> %s", d.From, d.To)
	}
	if d.Counts.Worse != 1 || d.Counts.Better != 1 {
		t.Errorf("diff counts = %+v, want one worse and one better", d.Counts)
	}

	var v versionInfo
	if err := json.Unmarshal(got["3"].Result, &v); err != nil {
		t.Fatalf("version result: %v", err)
	}
	if v.Name != "dshfit" || v.Protocol != Protocol {
		t.Errorf("version = %+v", v)
	}
}

func TestBadLineIsAnsweredAndTheSessionContinues(t *testing.T) {
	// The reverse assertion is the second request: if a malformed line ended the
	// session, this test would see one response instead of two. A sidecar that
	// dies on a client bug turns a typo into a dead panel.
	replies := serve(t, Config{},
		`{"id":1,"method":`,
		requestLine(t, 2, "version", nil),
	)
	if len(replies) != 2 {
		t.Fatalf("got %d responses, want 2 (a bad line must not end the session)", len(replies))
	}
	first := replies[0]
	if first.Error == nil || first.Error.Code != CodeBadRequest {
		t.Fatalf("first response = %+v, want a bad-request error", first)
	}
	if string(first.ID) != "null" {
		t.Errorf("an unparseable line has no id to echo; got %s", first.ID)
	}
	second := byID(t, replies)["2"]
	if second.Error != nil {
		t.Fatalf("the request after a bad line failed: %s", second.Error.Message)
	}
	if len(second.Result) == 0 {
		t.Fatal("the request after a bad line produced no result")
	}
}

func TestUnknownMethodIsAnErrorNotAnEmptyResult(t *testing.T) {
	// An empty result and "asked, nothing wrong" are the same bytes. A client
	// cannot tell them apart, so an unknown method has to be an error.
	replies := serve(t, Config{}, requestLine(t, 7, "matrices", nil))
	if len(replies) != 1 {
		t.Fatalf("got %d responses, want 1", len(replies))
	}
	r := replies[0]
	if r.Error == nil {
		t.Fatalf("unknown method answered with a result: %s", r.Result)
	}
	if r.Error.Code != CodeUnknownMethod {
		t.Errorf("code = %q, want %q", r.Error.Code, CodeUnknownMethod)
	}
	if len(r.Result) != 0 {
		t.Errorf("an error response must carry no result, got %s", r.Result)
	}
	for _, m := range []string{"profile", "diff", "version"} {
		if !strings.Contains(r.Error.Message, m) {
			t.Errorf("message should name the methods it does answer, missing %q: %s", m, r.Error.Message)
		}
	}
}

func TestUnparseableHostIsAnsweredAndTheSessionContinues(t *testing.T) {
	replies := serve(t, Config{},
		requestLine(t, 1, "profile", map[string]any{"host": "banana", "listing": driftListing(t)}),
		requestLine(t, 2, "profile", map[string]any{"host": "0.2.2-rc.1", "listing": driftListing(t)}),
	)
	got := byID(t, replies)
	if got["1"].Error == nil {
		t.Fatal("an unparseable host was judged instead of refused")
	}
	if got["1"].Error.Code != CodeInvalidInput {
		t.Errorf("code = %q, want %q", got["1"].Error.Code, CodeInvalidInput)
	}
	if got["2"].Error != nil {
		t.Fatalf("a bad host poisoned the next request: %s", got["2"].Error.Message)
	}
}

func TestResponsesPairByID(t *testing.T) {
	// Two requests, two different result shapes. Whichever order they come back
	// in, each id must carry its own answer: a shared buffer or a response built
	// from the wrong request would show up as a diff shape under the profile id.
	listing := driftListing(t)
	replies := serve(t, Config{Workers: 4},
		requestLine(t, 11, "profile", map[string]any{"host": "0.2.2-rc.1", "listing": listing}),
		requestLine(t, 22, "diff", map[string]any{"from": "0.2.1-rc.3", "to": "0.2.2-rc.1", "listing": listing}),
	)
	if len(replies) != 2 {
		t.Fatalf("got %d responses, want 2", len(replies))
	}
	got := byID(t, replies)

	var asProfile profile.Result
	if err := json.Unmarshal(got["11"].Result, &asProfile); err != nil {
		t.Fatalf("id 11 is not a profile result: %v\n%s", err, got["11"].Result)
	}
	if asProfile.Host == "" {
		t.Error("id 11 has no host; it was paired with the wrong request")
	}
	if bytes.Contains(got["11"].Result, []byte(`"changes"`)) {
		t.Error("id 11 carries a diff payload")
	}

	var asDiff profile.Diff
	if err := json.Unmarshal(got["22"].Result, &asDiff); err != nil {
		t.Fatalf("id 22 is not a diff result: %v\n%s", err, got["22"].Result)
	}
	if asDiff.To != "0.2.2-rc.1" {
		t.Errorf("id 22 to = %q", asDiff.To)
	}
	if bytes.Contains(got["22"].Result, []byte(`"entries"`)) {
		t.Error("id 22 carries a profile payload")
	}
}

func TestOverlongLineIsAnsweredAndDiscarded(t *testing.T) {
	// MaxLine is small enough that the request below overflows it. The second
	// request proves the rest of the long line was thrown away: if it were left
	// in the stream, the next "line" would be the tail of the junk and would
	// produce a parse error instead of a version result.
	long := `{"id":1,"method":"version","params":{"pad":"` + strings.Repeat("x", 512) + `"}}`
	replies := serve(t, Config{MaxLine: 128}, long, requestLine(t, 2, "version", nil))
	if len(replies) != 2 {
		t.Fatalf("got %d responses, want 2", len(replies))
	}
	if replies[0].Error == nil || replies[0].Error.Code != CodeBadRequest {
		t.Fatalf("overlong line = %+v, want a bad-request error", replies[0])
	}
	if !strings.Contains(replies[0].Error.Message, "longer than") {
		t.Errorf("message should say the line was too long: %s", replies[0].Error.Message)
	}
	if got := byID(t, replies)["2"]; got.Error != nil || len(got.Result) == 0 {
		t.Fatalf("the request after an overlong line failed: %+v", got)
	}
}

func TestInlineListingAndPathAgree(t *testing.T) {
	// The panel sends the listing it already has; the CLI sends a path. Both must
	// resolve to the same assessment, including the base directory that relative
	// package paths resolve against — otherwise the same file produces two
	// different verdicts depending on who asked.
	path := driftListing(t)
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	replies := serve(t, Config{},
		requestLine(t, "p", "profile", map[string]any{"host": "0.2.2-rc.1", "listing": path}),
		requestLine(t, "t", "profile", map[string]any{
			"host":    "0.2.2-rc.1",
			"listing": string(text),
			"base":    filepath.Dir(abs),
		}),
	)
	got := byID(t, replies)
	var fromPath, fromText profile.Result
	if err := json.Unmarshal(got[`"p"`].Result, &fromPath); err != nil {
		t.Fatalf("path result: %v", err)
	}
	if err := json.Unmarshal(got[`"t"`].Result, &fromText); err != nil {
		t.Fatalf("inline result: %v", err)
	}
	if fromPath.Counts != fromText.Counts {
		t.Errorf("counts differ: path %+v vs inline %+v", fromPath.Counts, fromText.Counts)
	}
	if len(fromPath.Entries) != len(fromText.Entries) {
		t.Fatalf("entries differ: path %d vs inline %d", len(fromPath.Entries), len(fromText.Entries))
	}
	for i := range fromPath.Entries {
		if fromPath.Entries[i].Name != fromText.Entries[i].Name {
			t.Errorf("entry %d: path %q vs inline %q", i,
				fromPath.Entries[i].Name, fromText.Entries[i].Name)
		}
	}
}

func TestMissingIDIsRejected(t *testing.T) {
	// Without an id the response cannot be paired with anything, so a client that
	// omits it would wait forever for an answer it cannot recognise.
	replies := serve(t, Config{}, `{"method":"version"}`)
	if len(replies) != 1 {
		t.Fatalf("got %d responses, want 1", len(replies))
	}
	if replies[0].Error == nil || replies[0].Error.Code != CodeBadRequest {
		t.Fatalf("missing id = %+v, want a bad-request error", replies[0])
	}
	if !strings.Contains(replies[0].Error.Message, "id") {
		t.Errorf("message should name the missing id: %s", replies[0].Error.Message)
	}
}

func TestStdinListingIsRefused(t *testing.T) {
	// `-` means stdin in the CLI. Here stdin carries requests, so accepting it
	// would make the sidecar read its own request stream as a listing.
	replies := serve(t, Config{},
		requestLine(t, 1, "profile", map[string]any{"host": "0.2.2-rc.1", "listing": "-"}),
	)
	if replies[0].Error == nil || replies[0].Error.Code != CodeInvalidInput {
		t.Fatalf("listing `-` = %+v, want invalid-input", replies[0])
	}
	if !strings.Contains(replies[0].Error.Message, "stdin") {
		t.Errorf("message should explain that stdin carries requests: %s", replies[0].Error.Message)
	}
}

func TestBlankLinesAreNotRequests(t *testing.T) {
	// Answering a blank line would invent a response the client cannot pair.
	replies := serve(t, Config{}, "", "   ", requestLine(t, 1, "version", nil))
	if len(replies) != 1 {
		t.Fatalf("got %d responses, want 1", len(replies))
	}
	if string(replies[0].ID) != "1" {
		t.Errorf("id = %s", replies[0].ID)
	}
}

func TestLastLineWithoutNewlineIsAnswered(t *testing.T) {
	var out bytes.Buffer
	// No trailing newline: the request is still complete.
	if err := Serve(strings.NewReader(requestLine(t, 9, "version", nil)), &out, Config{}); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var r reply
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &r); err != nil {
		t.Fatalf("response: %v\n%s", err, out.String())
	}
	if string(r.ID) != "9" {
		t.Errorf("id = %s, want 9", r.ID)
	}
}

func TestServeReturnsNilOnCleanEOF(t *testing.T) {
	if err := Serve(strings.NewReader(""), &bytes.Buffer{}, Config{}); err != nil {
		t.Fatalf("empty input: %v", err)
	}
}
