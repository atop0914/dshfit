package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
