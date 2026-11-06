package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atop0914/dshfit/internal/profile"
)

// profileFixture is the listing that exercises every outcome at once: a real
// plugin blocked by the prerelease gate, a plugin with an out-of-range peer, a
// plugin that is fine, a data package, and a package whose directory is gone.
func profileFixture() string {
	return filepath.Join("..", "..", "testdata", "profile", "web", "list.json")
}

func TestProfileRequiresHost(t *testing.T) {
	code, _, stderr := run(t, "profile", profileFixture())
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--host is required") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestProfileRequiresExactlyOneListing(t *testing.T) {
	for _, args := range [][]string{
		{"profile", "--host", "0.2.2-rc.1"},
		{"profile", "--host", "0.2.2-rc.1", "a.json", "b.json"},
	} {
		code, _, stderr := run(t, args...)
		if code != 2 {
			t.Fatalf("%v: code = %d, want 2", args, code)
		}
		if !strings.Contains(stderr, "exactly one") {
			t.Errorf("%v: stderr = %q", args, stderr)
		}
	}
}

// TestProfileTextGroupsByRootCause is the deliverable: two causes, not two
// per-plugin blocks, and each cause carries a prescription.
func TestProfileTextGroupsByRootCause(t *testing.T) {
	code, stdout, stderr := run(t, "profile", "--host", "0.2.2-rc.1", profileFixture())
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%s", code, stderr)
	}
	for _, want := range []string{
		"blocked by root cause",
		"reason=prerelease-gate",
		"reason=out-of-range",
		"plugins (1): dsh-pet",
		"plugins (1): dsh-narrow-range",
		"fix: ",
		"no findings — 1 plugin(s)",
		"dsh-context@0.82.1",
		"not a plugin",
		"could not be judged",
		"5 package(s): 1 ok, 0 warn, 2 blocked, 1 unknown, 1 not a plugin",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	// The nine identical peers of dsh-pet must land in one group, not nine.
	if n := strings.Count(stdout, "reason=prerelease-gate"); n != 1 {
		t.Errorf("prerelease-gate appears %d times, want 1:\n%s", n, stdout)
	}
}

// TestProfileUnknownIsNotSilentlyOk is the honesty gate on the exit code. A
// package that could not be read must not produce a zero exit, because a caller
// cannot tell that from "everything is compatible".
func TestProfileUnknownIsNotSilentlyOk(t *testing.T) {
	dir := t.TempDir()
	listing := filepath.Join(dir, "list.json")
	body := `[{"name":"web","path":".","dependencies":{
      "dsh-missing":{"version":"1.0.0","path":"./plugins/dsh-missing"}}}]`
	if err := os.WriteFile(listing, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := run(t, "profile", "--host", "0.2.2-rc.1", listing)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stdout, "that is not the same as compatible") {
		t.Errorf("stdout should say what an unknown package means:\n%s", stdout)
	}
	if !strings.Contains(stdout, "0 ok") {
		t.Errorf("stdout should still report the ok count as zero:\n%s", stdout)
	}
}

// TestProfileEmptyListingExitsZero: an empty profile is a real answer, and it
// must be distinguishable from an unreadable one (which exits 2).
func TestProfileEmptyListingExitsZero(t *testing.T) {
	dir := t.TempDir()
	listing := filepath.Join(dir, "list.json")
	if err := os.WriteFile(listing, []byte(`[{"name":"web","path":"."}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := run(t, "profile", "--host", "0.2.2-rc.1", listing)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "0 package(s): 0 ok, 0 warn, 0 blocked, 0 unknown, 0 not a plugin") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestProfileRejectsWrongShapeWithExitTwo(t *testing.T) {
	dir := t.TempDir()
	listing := filepath.Join(dir, "list.json")
	if err := os.WriteFile(listing, []byte(`{"name":"web"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run(t, "profile", "--host", "0.2.2-rc.1", listing)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "not an array") {
		t.Errorf("stderr should name the shape: %q", stderr)
	}
}

func TestProfileReadsStdin(t *testing.T) {
	data, err := os.ReadFile(profileFixture())
	if err != nil {
		t.Fatal(err)
	}
	// Stdin input has no listing directory, so relative paths resolve against
	// the working directory: the fixture still parses, and the packages are
	// reported rather than dropped.
	code, stdout, stderr := runWithStdin(t, string(data), "profile", "--host", "0.2.2-rc.1", "-")
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "5 package(s)") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestProfileJSON(t *testing.T) {
	code, stdout, stderr := run(t, "profile", "--host", "0.2.2-rc.1", "--json", profileFixture())
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%s", code, stderr)
	}
	var res profile.Result
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout)
	}
	if res.Host != "0.2.2-rc.1" {
		t.Errorf("host = %q", res.Host)
	}
	if res.Counts.Packages != 5 || res.Counts.Blocked != 2 || res.Counts.Unknown != 1 ||
		res.Counts.NotAPlugin != 1 || res.Counts.OK != 1 {
		t.Errorf("counts = %+v", res.Counts)
	}
	if res.Counts.Plugins != res.Counts.OK+res.Counts.Warn+res.Counts.Blocked {
		t.Errorf("plugins must equal ok+warn+blocked: %+v", res.Counts)
	}
	// Nothing that could not be judged may appear as a root cause.
	for _, g := range res.Groups {
		if g.Code == "manifest-unreadable" {
			t.Errorf("an unreadable manifest became a compatibility cause: %+v", g)
		}
	}
	// The issue-level reason is part of the JSON contract, not just the text.
	found := false
	for _, g := range res.Groups {
		if g.Code == "peer-range-excludes-host" && g.Reason == "prerelease-gate" {
			found = true
		}
	}
	if !found {
		t.Errorf("groups = %+v, want a prerelease-gate group", res.Groups)
	}
}

func TestProfileBadHostExitsTwo(t *testing.T) {
	code, _, stderr := run(t, "profile", "--host", "not-a-version", profileFixture())
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "host version") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestProfileMissingFileExitsTwo(t *testing.T) {
	code, _, stderr := run(t, "profile", "--host", "0.2.2-rc.1", filepath.Join(t.TempDir(), "nope.json"))
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "read listing") {
		t.Errorf("stderr = %q", stderr)
	}
}
