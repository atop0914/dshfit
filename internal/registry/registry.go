// Package registry reads the npm packument for one package and keeps a trimmed
// local copy of it.
//
// It answers exactly one question for the rest of the tool: which versions has
// the publisher shipped, and what peer ranges do they declare? Deciding whether
// those ranges accept a host is deliberately *not* done here — that judgment is
// made once, by compat.RejectingPeers, and the callers that compare against a
// host reuse it. Two places deciding "does this range accept 0.2.2-rc.1" is how
// a report starts contradicting itself.
//
// Two failure modes are kept apart because they lead to opposite conclusions:
//
//   - the registry answered and has no such package (ErrNotFound): the name is
//     unpublished or private, so there is no release to point at;
//   - the registry could not be read at all (network, timeout, non-2xx,
//     malformed or oversized body): nothing is known, and the caller must say
//     "could not check" rather than reuse the local verdict as if it were news.
//
// Nothing in this package returns an empty packument as a fallback. An empty
// version list reads downstream as "no compatible release exists", which is a
// conclusion, and one that would be fabricated.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/atop0914/dshfit/internal/semver"
)

// DefaultBaseURL is the registry npm itself uses. It is a variable of the
// Client rather than a constant in the request code so tests and mirrors can
// point elsewhere without touching the network path.
const DefaultBaseURL = "https://registry.npmjs.org"

// maxPackumentBytes bounds the body we are willing to read. Real packuments run
// from a few kilobytes to a couple of megabytes (dsh-pet is ~120 KiB); the cap
// is applied to the reader, so an endless response is cut off instead of being
// read into memory first.
const maxPackumentBytes = 16 << 20

// Errors the caller has to tell apart.
var (
	// ErrNotFound means the registry answered and does not know the package.
	ErrNotFound = errors.New("package not found in registry")
	// ErrNoCache means offline mode was asked for a package with no local copy.
	ErrNoCache = errors.New("no cached registry data")
)

// Version is one published version, trimmed to the fields that decide
// loadability. Everything else in a packument (tarball URLs, integrity hashes,
// publish timestamps, maintainers) is dropped: keeping it would make the cache
// large, and none of it can change the answer.
type Version struct {
	Version string            `json:"version"`
	Peers   map[string]string `json:"peers,omitempty"`
	Engines map[string]string `json:"engines,omitempty"`
}

// Packument is the trimmed registry document for one package.
type Packument struct {
	Name string `json:"name"`
	// DistLatest is `dist-tags.latest` as published. It is kept separate from
	// the version list because publishers use it to mean "this is the release
	// you get by default", and that is not always the highest version.
	DistLatest string `json:"dist_latest,omitempty"`
	// Versions is every parseable published version, ascending.
	Versions []Version `json:"versions"`
	// Unparseable lists version keys that are not semver (npm allows a version
	// field it cannot order). They are reported rather than silently dropped,
	// because dropping them would let the tool claim it saw every release.
	Unparseable []string `json:"unparseable,omitempty"`
}

// Latest returns the version a plain install would give: dist-tags.latest when
// it names a version we kept, otherwise the highest published version.
func (p Packument) Latest() (Version, bool) {
	if len(p.Versions) == 0 {
		return Version{}, false
	}
	if p.DistLatest != "" {
		for _, v := range p.Versions {
			if v.Version == p.DistLatest {
				return v, true
			}
		}
	}
	return p.Versions[len(p.Versions)-1], true
}

// Newest reports the versions newest first, i.e. the order a reader wants when
// asking "has the publisher fixed this yet".
func (p Packument) Newest() []Version {
	out := make([]Version, len(p.Versions))
	for i, v := range p.Versions {
		out[len(p.Versions)-1-i] = v
	}
	return out
}

// Sources a Snapshot can come from. The distinction is printed, because data
// read from disk an hour ago is not the same evidence as data read just now.
const (
	FromNetwork = "network"
	FromCache   = "cache"
)

// Snapshot is a packument plus how old it is and where it came from.
type Snapshot struct {
	Packument    Packument
	FetchedAt    time.Time
	From         string
	Stale        bool
	RefreshError string
	CacheError   string
}

// Options configures a Client. The zero value is not useful: a BaseURL is
// always needed, and an empty CacheDir means "do not cache".
type Options struct {
	BaseURL  string
	CacheDir string
	// TTL is how long a cached packument counts as fresh. Zero means the
	// default (24h). A non-positive value set explicitly disables freshness —
	// every run refetches — while the cache still serves offline runs.
	TTL time.Duration
	// Offline forbids network use entirely: cache hits are served even when
	// stale, and a miss is ErrNoCache. It never silently falls back to network.
	Offline bool
	// HTTP is the client used for requests. Nil gets one with a 10s timeout —
	// a registry lookup that hangs is worse than one that fails, because the
	// report produces no output at all.
	HTTP *http.Client
	// Now is the clock. Nil uses time.Now.
	Now func() time.Time
}

// DefaultTTL is how long a cached packument is treated as current. A day is
// long enough that repeated runs in a CI job do not refetch, and short enough
// that a release published today is not missed for a week.
const DefaultTTL = 24 * time.Hour

// Client reads packuments and maintains the cache.
type Client struct {
	opts Options
	http *http.Client
}

// New builds a Client from opts.
func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, fmt.Errorf("registry: BaseURL is required")
	}
	if _, err := url.Parse(opts.BaseURL); err != nil {
		return nil, fmt.Errorf("registry: bad BaseURL %q: %w", opts.BaseURL, err)
	}
	c := &Client{opts: opts}
	c.http = opts.HTTP
	if c.http == nil {
		c.http = &http.Client{Timeout: 10 * time.Second}
	}
	return c, nil
}

func (c *Client) now() time.Time {
	if c.opts.Now != nil {
		return c.opts.Now()
	}
	return time.Now()
}

func (c *Client) ttl() time.Duration {
	switch {
	case c.opts.TTL == 0:
		return DefaultTTL
	case c.opts.TTL < 0:
		return 0
	default:
		return c.opts.TTL
	}
}

// Lookup returns the packument for pkg, from cache when it is fresh and from
// the network otherwise.
//
// host only participates in the cache key: the packument itself is the same for
// every host, but the file records which question it was fetched to answer, so
// a run against a different host cannot be served a document labelled with the
// wrong target. A mismatch on read is a miss, not a hit.
func (c *Client) Lookup(ctx context.Context, pkg, host string) (Snapshot, error) {
	if strings.TrimSpace(pkg) == "" {
		return Snapshot{}, fmt.Errorf("registry: package name is empty")
	}
	now := c.now()

	var stale *record
	if rec, err := c.loadRecord(pkg, host); err != nil {
		// A corrupt cache entry is not a reason to stop: it is a reason to
		// refetch. The error is kept so an offline run can explain itself.
		if c.opts.Offline {
			return Snapshot{}, fmt.Errorf("%w for %q, and the cached copy is unreadable: %v", ErrNoCache, pkg, err)
		}
	} else if rec != nil {
		if fresh(rec.FetchedAt, now, c.ttl()) {
			return Snapshot{Packument: rec.Packument, FetchedAt: rec.FetchedAt, From: FromCache}, nil
		}
		stale = rec
	}

	if c.opts.Offline {
		if stale == nil {
			return Snapshot{}, fmt.Errorf("%w for %q at host %s; run once without --offline to populate it", ErrNoCache, pkg, host)
		}
		return Snapshot{
			Packument: stale.Packument, FetchedAt: stale.FetchedAt, From: FromCache, Stale: true,
		}, nil
	}

	pack, err := c.fetch(ctx, pkg)
	if err != nil {
		if stale != nil {
			// The refresh failed but an older copy exists. It is served with
			// both flags set, so the report can print the age and the failure
			// together — what must never happen is printing it as current data.
			return Snapshot{
				Packument: stale.Packument, FetchedAt: stale.FetchedAt, From: FromCache,
				Stale: true, RefreshError: err.Error(),
			}, nil
		}
		return Snapshot{}, err
	}

	snap := Snapshot{Packument: pack, FetchedAt: now, From: FromNetwork}
	if c.opts.CacheDir == "" {
		return snap, nil
	}
	if err := c.storeRecord(record{
		Package: pkg, Host: host, Registry: c.opts.BaseURL, FetchedAt: now, Packument: pack,
	}); err != nil {
		// A cache we cannot write is worth a sentence, not a failed run: the
		// answer itself is already correct.
		snap.CacheError = err.Error()
	}
	return snap, nil
}

func fresh(fetchedAt, now time.Time, ttl time.Duration) bool {
	if ttl <= 0 {
		// Zero tolerance means every cached entry is refetched; it still
		// serves as an offline copy.
		return false
	}
	age := now.Sub(fetchedAt)
	// A timestamp in the future means the clock moved; it cannot be evidence
	// of freshness, so it is treated as stale rather than trusted.
	return age >= 0 && age <= ttl
}

func (c *Client) fetch(ctx context.Context, pkg string) (Packument, error) {
	endpoint := strings.TrimRight(c.opts.BaseURL, "/") + "/" + url.PathEscape(pkg)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Packument{}, fmt.Errorf("registry request for %q: %w", pkg, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Packument{}, fmt.Errorf("registry lookup for %q failed: %w", pkg, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Packument{}, fmt.Errorf("%q: %w (the name is unpublished or private)", pkg, ErrNotFound)
	case resp.StatusCode != http.StatusOK:
		return Packument{}, fmt.Errorf("registry lookup for %q: HTTP %d %s", pkg, resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPackumentBytes+1))
	if err != nil {
		return Packument{}, fmt.Errorf("read packument for %q: %w", pkg, err)
	}
	if len(body) > maxPackumentBytes {
		return Packument{}, fmt.Errorf("packument for %q exceeds %d MiB; the response is not a packument we will parse",
			pkg, maxPackumentBytes>>20)
	}
	return Parse(pkg, body)
}

// Parse trims a raw packument body into the shape this tool uses.
func Parse(pkg string, body []byte) (Packument, error) {
	var raw struct {
		Name     string                 `json:"name"`
		DistTags map[string]string      `json:"dist-tags"`
		Versions map[string]versionJSON `json:"versions"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Packument{}, fmt.Errorf("parse packument for %q: %w", pkg, err)
	}
	if len(raw.Versions) == 0 {
		return Packument{}, fmt.Errorf("packument for %q lists no versions; an empty list is not the same as "+
			"\"no compatible release exists\", so it is reported as unreadable", pkg)
	}

	name := raw.Name
	if name == "" {
		name = pkg
	}
	out := Packument{Name: name, DistLatest: raw.DistTags["latest"], Versions: make([]Version, 0, len(raw.Versions))}
	for key, v := range raw.Versions {
		ver := v.Version
		if ver == "" {
			ver = key
		}
		parsed, err := semver.Parse(ver)
		if err != nil {
			// Sorted, so the report does not change shape between runs.
			out.Unparseable = append(out.Unparseable, key)
			continue
		}
		out.Versions = append(out.Versions, Version{
			Version: parsed.String(),
			Peers:   v.PeerDependencies,
			Engines: v.Engines,
		})
	}
	sort.Slice(out.Versions, func(i, j int) bool {
		a, errA := semver.Parse(out.Versions[i].Version)
		b, errB := semver.Parse(out.Versions[j].Version)
		if errA != nil || errB != nil {
			return out.Versions[i].Version < out.Versions[j].Version
		}
		return semver.Compare(a, b) < 0
	})
	sort.Strings(out.Unparseable)
	if len(out.Versions) == 0 {
		return Packument{}, fmt.Errorf("packument for %q has %d version(s) and none of them parse as semver; "+
			"there is nothing to compare against a host", pkg, len(raw.Versions))
	}
	return out, nil
}

type versionJSON struct {
	Version          string            `json:"version"`
	PeerDependencies map[string]string `json:"peerDependencies"`
	Engines          map[string]string `json:"engines"`
}
