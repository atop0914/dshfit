package registry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// petPackument is a real-shaped excerpt: three versions, a dist-tag that is not
// the highest version, peer ranges that differ per version, and fields we drop.
const petPackument = `{
  "_id": "dsh-pet",
  "dist-tags": {"latest": "0.4.8", "test": "0.4.1"},
  "versions": {
    "0.4.8": {"name": "dsh-pet", "version": "0.4.8",
      "peerDependencies": {"@deepseek-ai/dsh": "^0.4.0"},
      "engines": {"dsh": ">=0.4.0"},
      "dist": {"tarball": "https://example.invalid/dsh-pet-0.4.8.tgz"}},
    "0.1.0": {"name": "dsh-pet", "version": "0.1.0",
      "peerDependencies": {"@deepseek-ai/dsh": "^0.1.0"}},
    "0.2.0-rc.1": {"name": "dsh-pet", "version": "0.2.0-rc.1",
      "peerDependencies": {"@deepseek-ai/dsh": ">=0.2.0-rc.1 <0.3.0"}}
  }
}`

// server starts an httptest server that always answers with body/status and
// counts hits, so a test can assert "the cache was used, the network was not".
func server(t *testing.T, status int, body string) (*httptest.Server, *int32) {
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

func newClient(t *testing.T, base, dir string, opts func(*Options)) *Client {
	t.Helper()
	o := Options{BaseURL: base, CacheDir: dir, TTL: time.Hour}
	if opts != nil {
		opts(&o)
	}
	c, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestFetchTrimsAndSortsVersions(t *testing.T) {
	srv, _ := server(t, 200, petPackument)
	c := newClient(t, srv.URL, "", nil)

	snap, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if snap.From != FromNetwork {
		t.Errorf("From = %q, want %q", snap.From, FromNetwork)
	}
	if snap.Stale {
		t.Error("a fresh fetch must not be marked stale")
	}
	got := make([]string, 0, 3)
	for _, v := range snap.Packument.Versions {
		got = append(got, v.Version)
	}
	// Ascending, so "the last one" is the highest published version.
	want := "0.1.0,0.2.0-rc.1,0.4.8"
	if strings.Join(got, ",") != want {
		t.Errorf("versions = %v, want %s (ascending)", got, want)
	}
	if snap.Packument.DistLatest != "0.4.8" {
		t.Errorf("DistLatest = %q", snap.Packument.DistLatest)
	}
	latest, ok := snap.Packument.Latest()
	if !ok || latest.Version != "0.4.8" {
		t.Fatalf("Latest() = %+v, %v", latest, ok)
	}
	if latest.Peers["@deepseek-ai/dsh"] != "^0.4.0" {
		t.Errorf("peer range not kept: %+v", latest.Peers)
	}
	if latest.Engines["dsh"] != ">=0.4.0" {
		t.Errorf("engines not kept: %+v", latest.Engines)
	}
}

// TestFetchSeparatesNotFoundFromUnreachable is the honesty gate for the whole
// package: "npm does not have this name" and "npm could not be reached" lead to
// opposite things to print, and one of them is a reason to recommend nothing.
func TestFetchSeparatesNotFoundFromUnreachable(t *testing.T) {
	srv, _ := server(t, 404, `{"error":"Not found"}`)
	c := newClient(t, srv.URL, "", nil)

	_, err := c.Lookup(context.Background(), "dsh-nope", "0.2.2-rc.1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "unpublished or private") {
		t.Errorf("error should say why absence matters, got %q", err)
	}

	// Unreachable host: a closed server, so the transport fails.
	dead, _ := server(t, 200, petPackument)
	deadURL := dead.URL
	dead.Close()
	c2 := newClient(t, deadURL, "", nil)
	_, err = c2.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err == nil {
		t.Fatal("a closed server must be an error, not an empty answer")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("an unreachable registry must not be reported as a missing package: %v", err)
	}
}

func TestFetchRejectsOversizedBody(t *testing.T) {
	// Bigger than the cap, and shaped so a tolerant reader would keep going.
	huge := `{"versions":{"0.1.0":{"version":"0.1.0"}}, "pad":"` + strings.Repeat("x", maxPackumentBytes) + `"}`
	srv, _ := server(t, 200, huge)
	c := newClient(t, srv.URL, "", nil)

	_, err := c.Lookup(context.Background(), "dsh-big", "0.2.2-rc.1")
	if err == nil {
		t.Fatal("an oversized body must be refused")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want a size complaint", err)
	}
}

func TestFetchTimeoutIsAnError(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, petPackument)
	}))
	t.Cleanup(slow.Close)

	c := newClient(t, slow.URL, "", func(o *Options) {
		o.HTTP = &http.Client{Timeout: 40 * time.Millisecond}
	})
	_, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err == nil {
		t.Fatal("a timeout must be an error, not an empty packument")
	}
}

func TestFetchRejectsMalformedJSON(t *testing.T) {
	srv, _ := server(t, 200, `{"versions":`)
	c := newClient(t, srv.URL, "", nil)
	if _, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1"); err == nil {
		t.Fatal("truncated JSON must be an error")
	}
}

func TestParseRejectsPackumentWithoutVersions(t *testing.T) {
	srv, _ := server(t, 200, `{"name":"dsh-pet","versions":{}}`)
	c := newClient(t, srv.URL, "", nil)
	_, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err == nil {
		t.Fatal("an empty version list must be an error, not an answer")
	}
	if !strings.Contains(err.Error(), "no versions") {
		t.Errorf("err = %v", err)
	}
}

func TestParseReportsUnparseableVersions(t *testing.T) {
	body := `{"dist-tags":{"latest":"nightly"},"versions":{
	  "0.1.0":{"version":"0.1.0"},
	  "nightly":{"version":"nightly"},
	  "": {"version":""}}}`
	// Parse is pure: no client, no server, no cache.
	pack, err := Parse("dsh-x", []byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pack.Versions) != 1 || pack.Versions[0].Version != "0.1.0" {
		t.Fatalf("versions = %+v", pack.Versions)
	}
	// The two unusable keys are recorded, not hidden: a tool that quietly drops
	// versions can claim it looked at everything.
	if strings.Join(pack.Unparseable, ",") != ",nightly" {
		t.Errorf("Unparseable = %v", pack.Unparseable)
	}
	// dist-tags.latest names a version we could not keep, so the real latest
	// falls back to the highest kept version instead of printing "nightly".
	latest, ok := pack.Latest()
	if !ok || latest.Version != "0.1.0" {
		t.Errorf("Latest() = %+v, %v", latest, ok)
	}
}

func TestCacheHitSkipsNetwork(t *testing.T) {
	dir := t.TempDir()
	srv, hits := server(t, 200, petPackument)
	now := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)
	c := newClient(t, srv.URL, dir, func(o *Options) { o.Now = func() time.Time { return now } })

	first, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err != nil {
		t.Fatalf("first Lookup: %v", err)
	}
	second, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err != nil {
		t.Fatalf("second Lookup: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("network hits = %d, want 1 (the second run must come from cache)", got)
	}
	if second.From != FromCache {
		t.Errorf("From = %q, want %q", second.From, FromCache)
	}
	if second.FetchedAt != first.FetchedAt {
		t.Errorf("cached FetchedAt = %v, want the original %v", second.FetchedAt, first.FetchedAt)
	}
	if second.Stale {
		t.Error("a fresh cache hit must not be marked stale")
	}
	// The cache file has to say when it was written; without it "cached" is
	// indistinguishable from "current".
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache dir = %v, %v", entries, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	if !strings.Contains(string(raw), `"fetched_at": "2026-11-12T20:00:00Z"`) {
		t.Errorf("cache file does not record its fetch time:\n%s", raw)
	}
}

func TestCacheKeyIncludesHost(t *testing.T) {
	dir := t.TempDir()
	srv, hits := server(t, 200, petPackument)
	c := newClient(t, srv.URL, dir, nil)

	if _, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1"); err != nil {
		t.Fatalf("Lookup A: %v", err)
	}
	if _, err := c.Lookup(context.Background(), "dsh-pet", "0.3.0"); err != nil {
		t.Fatalf("Lookup B: %v", err)
	}
	// Same package, different target host: the file describes a question, so a
	// different question must not reuse it silently.
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Errorf("network hits = %d, want 2 (host is part of the cache key)", got)
	}
	// The key itself is checked, not just the behaviour: two hosts have to end
	// up in two files. Without this the record-level validation would hide a
	// key that ignores the host.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cache dir: %v", err)
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("cache files = %v, want one per host", names)
	}
}

func TestStaleCacheIsRefetched(t *testing.T) {
	dir := t.TempDir()
	var version atomic.Value
	version.Store(petPackument)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = io.WriteString(w, version.Load().(string))
	}))
	t.Cleanup(srv.Close)

	now := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)
	c := newClient(t, srv.URL, dir, func(o *Options) {
		o.TTL = time.Hour
		o.Now = func() time.Time { return now }
	})
	if _, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1"); err != nil {
		t.Fatalf("first: %v", err)
	}

	// Two hours later the entry is stale, so the network is asked again.
	now = now.Add(2 * time.Hour)
	snap, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("network hits = %d, want 2", got)
	}
	if snap.From != FromNetwork || snap.Stale {
		t.Errorf("From = %q stale = %v, want a fresh network answer", snap.From, snap.Stale)
	}
}

func TestRefreshFailureServesStaleWithReason(t *testing.T) {
	dir := t.TempDir()
	srv, _ := server(t, 200, petPackument)
	now := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)
	c := newClient(t, srv.URL, dir, func(o *Options) {
		o.TTL = time.Minute
		o.Now = func() time.Time { return now }
	})
	if _, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv.Close() // the registry is now unreachable
	now = now.Add(time.Hour)
	snap, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err != nil {
		t.Fatalf("a stale copy should still be served, got error: %v", err)
	}
	if !snap.Stale {
		t.Error("data that could not be refreshed must be marked stale")
	}
	if snap.From != FromCache {
		t.Errorf("From = %q, want %q", snap.From, FromCache)
	}
	if snap.RefreshError == "" {
		t.Error("the refresh failure must be reported; otherwise the old data looks current")
	}
	if len(snap.Packument.Versions) != 3 {
		t.Errorf("stale copy lost versions: %+v", snap.Packument.Versions)
	}
}

func TestOfflineWithoutCacheIsAnError(t *testing.T) {
	srv, hits := server(t, 200, petPackument)
	c := newClient(t, srv.URL, t.TempDir(), func(o *Options) { o.Offline = true })

	_, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if !errors.Is(err, ErrNoCache) {
		t.Fatalf("err = %v, want ErrNoCache", err)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Errorf("offline mode made %d network request(s)", got)
	}
	// The message has to say what to do, because an empty answer here would be
	// read as "nothing published".
	if !strings.Contains(err.Error(), "--offline") {
		t.Errorf("err = %v, want a hint about --offline", err)
	}
}

func TestOfflineServesStaleCache(t *testing.T) {
	dir := t.TempDir()
	srv, _ := server(t, 200, petPackument)
	now := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)
	online := newClient(t, srv.URL, dir, func(o *Options) { o.Now = func() time.Time { return now } })
	if _, err := online.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv.Close()

	later := now.Add(72 * time.Hour)
	off := newClient(t, srv.URL, dir, func(o *Options) {
		offline := true
		o.Offline = offline
		o.Now = func() time.Time { return later }
	})
	snap, err := off.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err != nil {
		t.Fatalf("offline with cache: %v", err)
	}
	if !snap.Stale || snap.From != FromCache {
		t.Errorf("From = %q stale = %v, want cache/stale", snap.From, snap.Stale)
	}
	if snap.FetchedAt != now {
		t.Errorf("FetchedAt = %v, want the original %v", snap.FetchedAt, now)
	}
}

func TestFutureTimestampIsNotFresh(t *testing.T) {
	dir := t.TempDir()
	srv, hits := server(t, 200, petPackument)
	now := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)
	c := newClient(t, srv.URL, dir, func(o *Options) { o.Now = func() time.Time { return now } })
	if _, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The clock moved backwards (this container has done it): the entry claims
	// to be from the future, which is not evidence of freshness.
	earlier := now.Add(-48 * time.Hour)
	c2 := newClient(t, srv.URL, dir, func(o *Options) { o.Now = func() time.Time { return earlier } })
	snap, err := c2.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Errorf("network hits = %d, want 2 (a future timestamp must not count as fresh)", got)
	}
	if snap.From != FromNetwork {
		t.Errorf("From = %q", snap.From)
	}
}

func TestCorruptCacheIsRefetched(t *testing.T) {
	dir := t.TempDir()
	srv, hits := server(t, 200, petPackument)
	c := newClient(t, srv.URL, dir, nil)
	if _, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache dir: %v %v", entries, err)
	}
	if err := os.WriteFile(filepath.Join(dir, entries[0].Name()), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err != nil {
		t.Fatalf("a corrupt entry should be refetched, got: %v", err)
	}
	if snap.From != FromNetwork {
		t.Errorf("From = %q", snap.From)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Errorf("network hits = %d, want 2", got)
	}
}

func TestCorruptCacheOfflineExplains(t *testing.T) {
	dir := t.TempDir()
	srv, _ := server(t, 200, petPackument)
	c := newClient(t, srv.URL, dir, nil)
	if _, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("cache dir = %v", entries)
	}
	if err := os.WriteFile(filepath.Join(dir, entries[0].Name()), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	off := newClient(t, srv.URL, dir, func(o *Options) { o.Offline = true })
	_, err := off.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err == nil {
		t.Fatal("a corrupt cache offline must be an error")
	}
	if !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("err = %v, want it to say the cached copy is unreadable", err)
	}
}

// TestCacheRecordForAnotherQuestionIsRejected covers a copied cache file: the
// name on disk is a hash, so if a record from another host lands under this
// key the only safe answer is to distrust it.
func TestCacheRecordForAnotherQuestionIsRejected(t *testing.T) {
	dir := t.TempDir()
	srv, hits := server(t, 200, petPackument)
	c := newClient(t, srv.URL, dir, nil)
	if _, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// The planted record is *fresh* and names the same mirror on purpose: with
	// either of those wrong, the TTL or the mirror check would reject it and
	// this test would pass even with the host check removed — which is exactly
	// what a first version of this test did.
	freshStamp := time.Now().UTC().Format(time.RFC3339)
	planted := `{
	  "package": "dsh-pet", "host": "0.9.9", "registry": "` + srv.URL + `",
	  "fetched_at": "` + freshStamp + `",
	  "packument": {"name":"dsh-pet","versions":[{"version":"0.0.1"}]}}`
	if err := os.WriteFile(c.cachePath("dsh-pet", "0.2.2-rc.1"), []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Errorf("network hits = %d, want 2 (a record for another host must not be served)", got)
	}
	if snap.From != FromNetwork {
		t.Errorf("From = %q, want a fresh fetch", snap.From)
	}
	// If the planted copy had been served, the answer would be this 0.0.1.
	if len(snap.Packument.Versions) != 3 {
		t.Errorf("served the planted record: %+v", snap.Packument.Versions)
	}
}

func TestCacheWriteFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, _ := server(t, 200, petPackument)
	// CacheDir is a regular file, so MkdirAll fails.
	c := newClient(t, srv.URL, blocked, nil)

	snap, err := c.Lookup(context.Background(), "dsh-pet", "0.2.2-rc.1")
	if err != nil {
		t.Fatalf("Lookup: %v (an unwritable cache must not fail the answer)", err)
	}
	if snap.CacheError == "" {
		t.Error("an unwritable cache must be reported, not swallowed")
	}
	if len(snap.Packument.Versions) != 3 {
		t.Error("the answer itself must still be complete")
	}
}
