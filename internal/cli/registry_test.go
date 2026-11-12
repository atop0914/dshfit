package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/atop0914/dshfit/internal/compat"
	"github.com/atop0914/dshfit/internal/profile"
)

// rejectingPackument answers every package with one release whose peer range
// cannot possibly accept the hosts used in these tests. That is the shape that
// makes the interesting sentence: "nothing published admits this host".
const rejectingPackument = `{
  "name": "any",
  "dist-tags": {"latest": "9.9.9"},
  "versions": {"9.9.9": {"version": "9.9.9",
    "peerDependencies": {"@deepseek-ai/dsh-app-boot": "^9.0.0"}}}
}`

func registryServer(t *testing.T, status int, body string) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// blockedCount is the number of blocked plugins in the fixture, read from the
// tool itself so the test cannot drift away from the fixture.
func blockedCount(t *testing.T) int {
	t.Helper()
	code, stdout, _ := run(t, "profile", "--host", "0.2.2-rc.1", "--json", driftFixture())
	if code != 1 {
		t.Fatalf("fixture exit = %d, want 1 (blocked plugins)", code)
	}
	var res profile.Result
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("fixture JSON: %v", err)
	}
	n := 0
	for _, e := range res.Entries {
		if e.Report != nil && e.Report.Verdict == compat.Blocked {
			n++
		}
	}
	return n
}

func TestProfileRegistryExplainsPublishedReleases(t *testing.T) {
	want := blockedCount(t)
	srv, hits := registryServer(t, 200, rejectingPackument)
	cache := t.TempDir()

	code, stdout, stderr := run(t, "profile", "--host", "0.2.2-rc.1", "--registry",
		"--registry-url", srv.URL, "--cache-dir", cache, driftFixture())
	if code != 1 {
		t.Fatalf("code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if got := int(atomic.LoadInt32(hits)); got != want {
		t.Errorf("registry hits = %d, want %d (one per blocked plugin, none for clean ones)", got, want)
	}
	for _, s := range []string{
		"registry — releases the publisher ships",
		"latest 9.9.9 does not admit 0.2.2-rc.1",
		"no published version admits 0.2.2-rc.1, out of 1 checked",
		"data as of ",
		"(from the registry)",
	} {
		if !strings.Contains(stdout, s) {
			t.Errorf("stdout missing %q:\n%s", s, stdout)
		}
	}
	// The local verdict must still be the thing that decides the run.
	if !strings.Contains(stdout, "will not load on 0.2.2-rc.1") {
		t.Errorf("the local verdict disappeared:\n%s", stdout)
	}
}

func TestProfileRegistryJSONCarriesTheAnswer(t *testing.T) {
	srv, _ := registryServer(t, 200, rejectingPackument)
	code, stdout, _ := run(t, "profile", "--host", "0.2.2-rc.1", "--registry",
		"--registry-url", srv.URL, "--cache-dir", t.TempDir(), "--json", driftFixture())
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	var res profile.Result
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("JSON: %v\n%s", err, stdout)
	}
	seen := 0
	for _, e := range res.Entries {
		if e.Registry == nil {
			continue
		}
		seen++
		if e.Registry.Package != e.Name {
			t.Errorf("answer names %q, entry is %q", e.Registry.Package, e.Name)
		}
		if e.Registry.LatestAdmits {
			t.Errorf("%s: latest admits the host, fixture says it must not", e.Name)
		}
		if e.Registry.Published != 1 {
			t.Errorf("%s: Published = %d", e.Name, e.Registry.Published)
		}
	}
	if seen == 0 {
		t.Fatal("no entry carries a registry answer")
	}
	// Only blocked plugins are annotated; a clean one with an answer would mean
	// the report is describing packages nobody asked about.
	for _, e := range res.Entries {
		if e.Registry != nil && (e.Report == nil || e.Report.Verdict != compat.Blocked) {
			t.Errorf("%s is not blocked but got a registry answer", e.Name)
		}
	}
}

// TestProfileRegistryFailureIsNotAVerdict is reverse assertion 1 at the CLI
// level: a broken registry must not turn into "no release works".
func TestProfileRegistryFailureIsNotAVerdict(t *testing.T) {
	srv, _ := registryServer(t, 500, "boom")
	code, stdout, _ := run(t, "profile", "--host", "0.2.2-rc.1", "--registry",
		"--registry-url", srv.URL, "--cache-dir", t.TempDir(), driftFixture())
	if code != 1 {
		t.Fatalf("code = %d, want 1 (the local verdict is unchanged by a network failure)", code)
	}
	if !strings.Contains(stdout, "could not be checked: registry lookup") {
		t.Errorf("stdout should report the failed lookup:\n%s", stdout)
	}
	if !strings.Contains(stdout, "nothing here says whether a released version would load") {
		t.Errorf("stdout should refuse to draw a conclusion:\n%s", stdout)
	}
	for _, banned := range []string{"does not admit", "no published version admits", "admits 0.2.2-rc.1"} {
		if strings.Contains(stdout, banned) {
			t.Errorf("a failed lookup must not produce %q:\n%s", banned, stdout)
		}
	}
}

// TestProfileRegistryCacheSurvivesNetworkLoss is reverse assertion 2 at the CLI
// level: data served from disk is labelled with its age, and it still works
// when the network is gone.
func TestProfileRegistryCacheSurvivesNetworkLoss(t *testing.T) {
	srv, _ := registryServer(t, 200, rejectingPackument)
	cache := t.TempDir()
	args := []string{"profile", "--host", "0.2.2-rc.1", "--registry",
		"--registry-url", srv.URL, "--cache-dir", cache, driftFixture()}

	if code, _, _ := run(t, args...); code != 1 {
		t.Fatalf("first run code = %d", code)
	}
	srv.Close() // the registry is now unreachable

	code, stdout, stderr := run(t, args...)
	if code != 1 {
		t.Fatalf("second run code = %d\nstderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "(from cache)") {
		t.Errorf("cached data must be labelled:\n%s", stdout)
	}
	if !strings.Contains(stdout, "data as of ") {
		t.Errorf("cached data must carry its timestamp:\n%s", stdout)
	}
	if strings.Contains(stdout, "(from the registry)") {
		t.Errorf("a cache hit must not claim to have just queried:\n%s", stdout)
	}
}

func TestProfileOfflineWithoutCacheSaysSo(t *testing.T) {
	srv, hits := registryServer(t, 200, rejectingPackument)
	code, stdout, _ := run(t, "profile", "--host", "0.2.2-rc.1", "--offline",
		"--registry-url", srv.URL, "--cache-dir", t.TempDir(), driftFixture())
	if code != 1 {
		t.Fatalf("code = %d, want 1 (offline still reports the local verdict)", code)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Errorf("--offline made %d request(s)", got)
	}
	if !strings.Contains(stdout, "could not be checked") {
		t.Errorf("offline miss must be reported:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no cached registry data") {
		t.Errorf("offline miss must say the cache is empty:\n%s", stdout)
	}
}

func TestProfileWithoutRegistryStaysOffline(t *testing.T) {
	srv, hits := registryServer(t, 200, rejectingPackument)
	code, stdout, _ := run(t, "profile", "--host", "0.2.2-rc.1",
		"--registry-url", srv.URL, "--cache-dir", t.TempDir(), driftFixture())
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Errorf("a plain profile run touched the network %d time(s)", got)
	}
	if strings.Contains(stdout, "releases the publisher ships") {
		t.Errorf("the registry section must be opt-in:\n%s", stdout)
	}
}

// TestProfileMarkdownOmitsRegistry pins the deliberate gap: the markdown
// renderer is for CI summaries of the local verdict, and a registry section
// there would make a CI job depend on the network.
func TestProfileMarkdownOmitsRegistry(t *testing.T) {
	srv, _ := registryServer(t, 200, rejectingPackument)
	code, stdout, _ := run(t, "profile", "--host", "0.2.2-rc.1", "--registry",
		"--registry-url", srv.URL, "--cache-dir", t.TempDir(),
		"--format", "markdown", driftFixture())
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(stdout, "releases the publisher ships") {
		t.Errorf("markdown should not grow a registry section:\n%s", stdout)
	}
}

func TestProfileRejectsBadRegistryURL(t *testing.T) {
	code, _, stderr := run(t, "profile", "--host", "0.2.2-rc.1", "--registry",
		"--registry-url", "://nope", driftFixture())
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "registry") {
		t.Errorf("stderr = %q", stderr)
	}
}

// TestProfileRegistryFoldsRepeatedRejections is the same lesson the root-cause
// grouping already learned: nine peers declaring one range is one problem, not
// nine lines. dsh-pet@0.4.8 really is shaped like this.
func TestProfileRegistryFoldsRepeatedRejections(t *testing.T) {
	var peers strings.Builder
	names := []string{"agent-default-model", "commands", "llm", "host-webserver", "credentials"}
	for i, n := range names {
		if i > 0 {
			peers.WriteString(",")
		}
		peers.WriteString(`"@deepseek-ai/dsh-` + n + `": "^9.0.0"`)
	}
	body := `{"dist-tags":{"latest":"9.9.9"},"versions":{"9.9.9":{"version":"9.9.9",
	  "peerDependencies":{` + peers.String() + `}}}}`
	srv, _ := registryServer(t, 200, body)
	plugins := blockedCount(t)

	code, stdout, _ := run(t, "profile", "--host", "0.2.2-rc.1", "--registry",
		"--registry-url", srv.URL, "--cache-dir", t.TempDir(), driftFixture())
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	// Names are sorted, so the three shown are the alphabetically first — the
	// fold must not reorder by anything else, or the same data would print
	// differently between runs.
	line := "rejects 5 peer(s) declaring ^9.0.0 (out-of-range): @deepseek-ai/dsh-agent-default-model, " +
		"@deepseek-ai/dsh-commands, @deepseek-ai/dsh-credentials, and 2 more"
	if !strings.Contains(stdout, line) {
		t.Errorf("stdout should fold the five identical rejections into one line:\n%s", stdout)
	}
	if n := strings.Count(stdout, "declaring ^9.0.0"); n != plugins {
		t.Errorf("^9.0.0 appears %d time(s), want %d (once per looked-up plugin)", n, plugins)
	}
}
