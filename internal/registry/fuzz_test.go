package registry

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atop0914/dshfit/internal/semver"
)

// packumentSeeds are real-shaped bodies: what npm actually returns for a DSH
// plugin, plus the shapes that stress the trimming rules (a version key that is
// not semver, a `version` field that disagrees with its key, an empty object).
var packumentSeeds = []struct {
	pkg  string
	body []byte
}{
	{"dsh-pet", []byte(`{"name":"dsh-pet","dist-tags":{"latest":"0.4.8"},"versions":{
		"0.4.8":{"version":"0.4.8","peerDependencies":{"@deepseek-ai/dsh":"^0.4.0"},"engines":{"dsh":">=0.4.0"}},
		"0.4.1":{"version":"0.4.1","peerDependencies":{"@deepseek-ai/dsh":"^0.2.0-rc.1"}},
		"0.3.0":{"version":"0.3.0","peerDependencies":{"@deepseek-ai/dsh":">=0.2.0-rc.1 <0.3.0"}}}}`)},
	{"dsh-context", []byte(`{"name":"dsh-context","versions":{"0.2.2-rc.1":{"version":"0.2.2-rc.1"}}}`)},
	{"dsh-key-mismatch", []byte(`{"versions":{"1.0.0":{"version":"9.9.9"},"v2.0.0":{},"not-a-version":{"version":"beta"}}}`)},
	// Every version key present, none of them orderable: the case the second
	// emptiness check exists for, and the one that would otherwise turn into
	// "the publisher shipped nothing that fits" without a single release seen.
	{"dsh-all-unparseable", []byte(`{"versions":{"beta":{"version":"beta"},"latest":{"version":"latest"}}}`)},
	{"dsh-empty", []byte(`{"name":"dsh-empty","versions":{}}`)},
	{"dsh-null", []byte(`null`)},
	{"dsh-not-an-object", []byte(`[]`)},
}

// FuzzRegistryParse asks whether a stranger's JSON can take down the run.
//
// A packument is fetched by this tool from a registry nobody here controls, then
// trimmed and compared. The failure mode that matters is not a wrong version
// list, it is a panic while parsing the fourth package of twenty — the user is
// left with no report at all, from the tool whose whole argument is that nobody
// should have to discover breakage one plugin at a time.
//
// The second assertion is the machine form of "an empty result must never pass
// for an answer": a successful parse always carries at least one version, so
// "no compatible release exists" can only be said after seeing a release.
func FuzzRegistryParse(f *testing.F) {
	for _, s := range packumentSeeds {
		f.Add(s.pkg, s.body)
	}
	f.Fuzz(func(t *testing.T, pkg string, body []byte) {
		p, err := Parse(pkg, body)
		if err != nil {
			if len(p.Versions) != 0 || p.Name != "" || p.DistLatest != "" {
				t.Fatalf("Parse failed but returned a partial packument: %+v", p)
			}
			return
		}
		if len(p.Versions) == 0 {
			t.Fatal("a successful parse produced an empty version list")
		}
		// What the caller does next is order them and compare against a host, so
		// "sorted" and "every entry parses" have to hold here or the comparison
		// downstream is built on a slice this package never promised.
		for i, v := range p.Versions {
			parsed, perr := semver.Parse(v.Version)
			if perr != nil {
				t.Fatalf("version %d (%q) does not parse: %v", i, v.Version, perr)
			}
			if i > 0 {
				prev, _ := semver.Parse(p.Versions[i-1].Version)
				if semver.Compare(prev, parsed) > 0 {
					t.Fatalf("versions are not ascending at %d: %s then %s", i, prev, parsed)
				}
			}
		}
		latest, ok := p.Latest()
		if !ok {
			t.Fatal("a non-empty packument has no latest version")
		}
		if p.DistLatest != "" && latest.Version == p.DistLatest {
			return // the dist-tag names a version we kept, which is the point of keeping it
		}
		if latest.Version != p.Versions[len(p.Versions)-1].Version {
			t.Fatalf("Latest() returned %s, not the highest version %s",
				latest.Version, p.Versions[len(p.Versions)-1].Version)
		}
	})
}

// FuzzCacheRecord feeds arbitrary bytes at the position of a cache file.
//
// The cache directory is third-party input in the most literal sense: it is a
// plain directory of JSON in the user's home, which they may edit, a backup tool
// may truncate, and a future version of this tool may have written. The rule
// that must survive all of that is "a bad record is a miss, not a crash and not
// a made-up answer" — an unreadable cache is refetched, and an offline run says
// so instead of reporting an empty packument.
func FuzzCacheRecord(f *testing.F) {
	for _, s := range packumentSeeds {
		f.Add(s.body, "dsh-pet", "0.2.2-rc.1")
	}
	f.Add([]byte(`{"package":"dsh-pet","host":"0.2.2-rc.1","registry":"https://registry.npmjs.org",
		"fetched_at":"2026-11-12T00:00:00Z","packument":{"name":"dsh-pet","versions":[{"version":"0.4.8"}]}}`),
		"dsh-pet", "0.2.2-rc.1")
	// A record that decodes and names the right question but holds a version
	// that is not a version: the case the validation loop exists for.
	f.Add([]byte(`{"package":"dsh-pet","host":"0.2.2-rc.1","registry":"https://registry.npmjs.org",
		"fetched_at":"2026-11-12T00:00:00Z","packument":{"name":"dsh-pet","versions":[{"version":"latest-stable"}]}}`),
		"dsh-pet", "0.2.2-rc.1")

	f.Fuzz(func(t *testing.T, body []byte, pkg, host string) {
		// A blank name is rejected by Lookup before it ever looks at the cache,
		// so there is no cache behaviour to observe. The first version of this
		// guard tested `pkg == ""`, and a fuzzer reached it with a single space
		// within a minute — the empty-string case was the one shape we had
		// thought of.
		if strings.TrimSpace(pkg) == "" || strings.TrimSpace(host) == "" {
			return
		}
		dir := t.TempDir()
		c, err := New(Options{BaseURL: DefaultBaseURL, CacheDir: dir, TTL: time.Hour})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		path := c.cachePath(pkg, host)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatalf("plant cache file: %v", err)
		}

		rec, err := c.loadRecord(pkg, host)
		if err != nil {
			if rec != nil {
				t.Fatalf("loadRecord failed but returned a record: %+v", rec)
			}
			// A bad record must leave the next run with nothing to serve, so an
			// offline run explains itself rather than answering from garbage.
			off, oerr := New(Options{BaseURL: DefaultBaseURL, CacheDir: dir, Offline: true})
			if oerr != nil {
				t.Fatalf("New: %v", oerr)
			}
			if _, lerr := off.Lookup(context.Background(), pkg, host); !errors.Is(lerr, ErrNoCache) {
				t.Fatalf("an unreadable cache must be a miss offline, got %v", lerr)
			}
			return
		}
		// The file exists, so "no cache" is not a permitted answer: that would
		// make an unreadable entry indistinguishable from an absent one, and
		// silently drop the offline copy the user paid for with a network run.
		if rec == nil {
			t.Fatal("an existing cache file read as 'no cache'")
		}
		if rec.Package != pkg || rec.Host != host {
			t.Fatalf("record answers %s@%s, asked for %s@%s", rec.Package, rec.Host, pkg, host)
		}
		if rec.FetchedAt.IsZero() || len(rec.Packument.Versions) == 0 {
			t.Fatalf("usable record has no fetch time or no versions: %+v", rec)
		}
	})
}
