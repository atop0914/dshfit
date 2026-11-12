package profile

import (
	"context"
	"fmt"
	"time"

	"github.com/atop0914/dshfit/internal/compat"
	"github.com/atop0914/dshfit/internal/registry"
	"github.com/atop0914/dshfit/internal/semver"
)

// RegistryAnswer is the second half of the upgrade question: the local verdict
// says "this installed version will not load", and this says what the publisher
// has shipped since.
//
// It exists because a plain profile run cannot answer the most useful follow-up
// — "is there a release I can move to?" — from disk alone. What it must never
// do is stand in for a verdict: Error means the registry was unreadable, and
// that says nothing about whether a compatible release exists.
type RegistryAnswer struct {
	Package string `json:"package"`
	Host    string `json:"host"`
	// Latest is the version a plain install would give (dist-tags.latest).
	Latest string `json:"latest,omitempty"`
	// LatestAdmits reports whether Latest's DSH peer ranges accept Host.
	LatestAdmits bool `json:"latest_admits"`
	// LatestRejections explains a false LatestAdmits, using the same range
	// engine the local verdict uses.
	LatestRejections []compat.PeerRejection `json:"latest_rejections,omitempty"`
	// Admitting lists the newest published versions whose ranges accept Host,
	// newest first, capped. Empty when LatestAdmits (the answer is one version)
	// or when nothing published accepts Host.
	Admitting []string `json:"admitting,omitempty"`
	// Published is how many parseable versions the registry listed, so a
	// "nothing accepts it" claim has a denominator.
	Published int `json:"published"`
	// Unparseable counts published version keys that are not semver and were
	// therefore never compared. Non-zero means the check saw almost everything.
	Unparseable int `json:"unparseable,omitempty"`

	AsOf time.Time `json:"as_of"`
	// From is "network" or "cache".
	From string `json:"from,omitempty"`
	// Stale means the data could not be refreshed (or offline mode forbade it)
	// and its age is the only thing vouching for it.
	Stale bool `json:"stale,omitempty"`
	// RefreshError is why a refresh failed while a cached copy was used.
	RefreshError string `json:"refresh_error,omitempty"`
	// CacheError is why the cache could not be written; the answer is still
	// from the network.
	CacheError string `json:"cache_error,omitempty"`
	// Error means the registry could not be read. Never a verdict.
	Error string `json:"error,omitempty"`
}

// maxAdmitting caps how many alternatives are listed. A publisher with forty
// releases would otherwise print forty lines the reader cannot use; the newest
// five answer "can I move to something published recently".
const maxAdmitting = 5

// LookupFunc reads registry data for one package at one host version. It is a
// parameter so this package does not construct HTTP clients, and so tests can
// answer without a server.
type LookupFunc func(ctx context.Context, pkg, host string) (registry.Snapshot, error)

// AnnotateRegistry fills Entry.Registry for every blocked plugin.
//
// Only blocked plugins are looked up. A plugin that loads does not need the
// publisher's release list to act on, and fetching for every package in a
// twenty-plugin profile would turn a local check into twenty network calls.
//
// Failures are per package and never abort the run: a registry that cannot be
// reached must not cost the reader the verdicts that were already computed.
func AnnotateRegistry(ctx context.Context, res *Result, lookup LookupFunc) {
	host, hostErr := semver.Parse(res.Host)
	for i := range res.Entries {
		e := &res.Entries[i]
		if e.Status != StatusPlugin || e.Report == nil || e.Report.Verdict != compat.Blocked {
			continue
		}
		if hostErr != nil {
			e.Registry = &RegistryAnswer{Package: e.Name, Host: res.Host, Error: hostErr.Error()}
			continue
		}
		snap, err := lookup(ctx, e.Name, res.Host)
		if err != nil {
			e.Registry = &RegistryAnswer{Package: e.Name, Host: res.Host, Error: err.Error()}
			continue
		}
		answer := buildAnswer(e.Name, host, snap)
		e.Registry = &answer
	}
}

func buildAnswer(pkg string, host semver.Version, snap registry.Snapshot) RegistryAnswer {
	a := RegistryAnswer{
		Package:      pkg,
		Host:         host.String(),
		Published:    len(snap.Packument.Versions),
		Unparseable:  len(snap.Packument.Unparseable),
		AsOf:         snap.FetchedAt,
		From:         snap.From,
		Stale:        snap.Stale,
		RefreshError: snap.RefreshError,
		CacheError:   snap.CacheError,
	}
	latest, ok := snap.Packument.Latest()
	if !ok {
		// A packument with no versions is refused when it is read, so reaching
		// here means the caller passed something hand-built. Say so instead of
		// printing "latest <empty> accepts you".
		a.Error = fmt.Sprintf("the registry data for %q lists no version, so nothing can be compared to %s", pkg, host)
		return a
	}
	a.Latest = latest.Version
	a.LatestRejections = compat.RejectingPeers(latest.Peers, host)
	a.LatestAdmits = len(a.LatestRejections) == 0
	if a.LatestAdmits {
		return a
	}
	for _, v := range snap.Packument.Newest() {
		if len(a.Admitting) >= maxAdmitting {
			break
		}
		if len(compat.RejectingPeers(v.Peers, host)) == 0 {
			a.Admitting = append(a.Admitting, v.Version)
		}
	}
	return a
}
