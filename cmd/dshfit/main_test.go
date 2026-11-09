package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// buildBinary compiles the command into a temporary directory.
//
// The point of going through the binary is that everything else in this repo
// tests cli.Run, which is a function. main() is three lines of wiring that no
// test covers until something runs the real process: os.Args, os.Exit and the
// actual stdout a shell sees. A tool whose exit code is its contract has to be
// checked once from the outside.
func buildBinary(t *testing.T) string {
	t.Helper()
	name := "dshfit"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func runBinary(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut strings.Builder
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("run %s %v: %v", bin, args, err)
	}
	return out.String(), errOut.String(), code
}

// TestDiffOverADriftProfile is the end-to-end shape of the Day 5 deliverable:
// a real process, a real listing, two hosts, exit code 1, and the plugin that
// is blocked on both hosts absent from the output.
func TestDiffOverADriftProfile(t *testing.T) {
	bin := buildBinary(t)
	fixture := filepath.Join("..", "..", "testdata", "profile", "drift", "list.json")

	stdout, stderr, code := runBinary(t, bin, "diff",
		"--from", "0.2.1-rc.3", "--to", "0.2.2-rc.1", fixture)

	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%s", code, stderr)
	}
	for _, want := range []string{
		"will stop loading — 1",
		"dsh-pinned@1.0.0  ok → blocked",
		"improves — 1",
		"dsh-ships@1.0.0  blocked → ok",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "dsh-stable") {
		t.Errorf("a plugin blocked on both hosts is not a difference:\n%s", stdout)
	}
	// Nothing on stderr for a run that found what it was looking for: a
	// diagnostic on the happy path is how people learn to ignore stderr.
	if stderr != "" {
		t.Errorf("stderr should be empty on a successful run, got %q", stderr)
	}
}

// TestDiffOverAProfileThatDoesNotDrift pins the other end: identical behaviour
// on both hosts is one line and exit 0, through the real process too.
func TestDiffOverAProfileThatDoesNotDrift(t *testing.T) {
	bin := buildBinary(t)
	fixture := filepath.Join("..", "..", "testdata", "profile", "web", "list.json")

	stdout, stderr, code := runBinary(t, bin, "diff",
		"--from", "0.2.1-rc.3", "--to", "0.2.2-rc.1", fixture)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "no differences") {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Contains(stdout, "will stop loading") {
		t.Errorf("no sections when nothing differs:\n%s", stdout)
	}
}

// TestUsageReachesTheProcess covers the one thing click-through tests cannot:
// the usage text is written to stderr and the exit code is 2, so a shell script
// can tell "misused" from "found problems".
func TestUsageReachesTheProcess(t *testing.T) {
	bin := buildBinary(t)
	stdout, stderr, code := runBinary(t, bin, "diff", "--from", "0.2.1-rc.3")

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--from and --to are both required") {
		t.Errorf("stderr = %q", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout should stay clean on a usage error, got %q", stdout)
	}
}

// TestServeOverAPipe drives the real process the way a panel does: stdin stays
// open, one request goes in, one response comes out, and only then the next
// request is written.
//
// Reading a response before closing stdin is the point. A function-level test
// cannot tell a flushed line from one sitting in a buffer, and a panel that has
// to close stdin to see an answer would have to restart the sidecar for every
// keystroke.
func TestServeOverAPipe(t *testing.T) {
	bin := buildBinary(t)
	fixture, err := filepath.Abs(filepath.Join("..", "..", "testdata", "profile", "drift", "list.json"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	cmd := exec.Command(bin, "serve")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// A process left running would hang the whole test binary, so it is killed on
	// every exit path; the wait below then reports the real exit code.
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	reader := bufio.NewReader(stdout)
	requests := []string{
		`{"id":1,"method":"version"}`,
		`{"id":2,"method":"`,
		`{"id":3,"method":"profile","params":{"host":"0.2.2-rc.1","listing":"` + filepath.ToSlash(fixture) + `"}}`,
	}
	for i, line := range requests {
		if _, err := io.WriteString(stdin, line+"\n"); err != nil {
			t.Fatalf("write request %d: %v", i, err)
		}
		raw, err := readLineWithTimeout(t, reader)
		if err != nil {
			t.Fatalf("read response %d: %v; stderr=%s", i, err, stderr.String())
		}
		if !json.Valid([]byte(raw)) {
			t.Fatalf("response %d is not one JSON document: %q", i, raw)
		}
		var resp struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("decode response %d: %v", i, err)
		}
		switch i {
		case 0:
			if resp.Error != nil {
				t.Fatalf("version failed: %s", raw)
			}
		case 1:
			// The malformed line is answered, and the process is still there to
			// answer the third — which is what the next iteration proves.
			if resp.Error == nil || resp.Error.Code != "bad-request" {
				t.Fatalf("malformed line = %s, want a bad-request error", raw)
			}
		case 2:
			if resp.Error != nil {
				t.Fatalf("profile after a bad line failed: %s", raw)
			}
			if !bytes.Contains(resp.Result, []byte(`"counts"`)) {
				t.Fatalf("profile result has no counts: %s", raw)
			}
		}
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("serve exited non-zero after a clean EOF: %v; stderr=%s", err, stderr.String())
	}
	// stderr carries diagnostics, and there are none on this run. A note here
	// would mean the process wrote something other than protocol to stdout too.
	if stderr.String() != "" {
		t.Errorf("stderr should be empty, got %q", stderr.String())
	}
}

// readLineWithTimeout bounds the wait so a sidecar that never answers fails the
// test instead of hanging it.
func readLineWithTimeout(t *testing.T, r *bufio.Reader) (string, error) {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := r.ReadString('\n')
		done <- result{strings.TrimSpace(line), err}
	}()
	select {
	case got := <-done:
		return got.line, got.err
	case <-time.After(30 * time.Second):
		return "", errors.New("no response within 30s")
	}
}
