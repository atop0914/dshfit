package profile

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atop0914/dshfit/internal/compat"
	"github.com/atop0914/dshfit/internal/registry"
)

var asOf = time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)

// packument builds an ascending packument for tests. Newest() reverses it, so
// the order matters and is stated once here.
func packument(name string, latest string, versions ...registry.Version) registry.Packument {
	return registry.Packument{Name: name, DistLatest: latest, Versions: versions}
}

func ver(v, peerRange string) registry.Version {
	return registry.Version{Version: v, Peers: map[string]string{"@deepseek-ai/dsh-app-boot": peerRange}}
}

func blockedResult(host string, names ...string) Result {
	res := Result{Host: host}
	for _, n := range names {
		res.Entries = append(res.Entries, Entry{
			Name:    n,
			Version: "1.0.0",
			Status:  StatusPlugin,
			Report:  &compat.Report{Plugin: n, Host: host, Verdict: compat.Blocked},
		})
	}
	return res
}

// TestAnnotateRegistryOnlyLooksUpBlocked is the cost guard: a twenty-plugin
// profile must not become twenty network calls, and a plugin that loads has no
// use for the publisher's release list.
func TestAnnotateRegistryOnlyLooksUpBlocked(t *testing.T) {
	res := blockedResult("0.2.2-rc.1", "dsh-pet")
	res.Entries = append(res.Entries,
		Entry{Name: "dsh-clean", Status: StatusPlugin, Report: &compat.Report{Verdict: compat.OK}},
		Entry{Name: "dsh-data", Status: StatusNotAPlugin},
		Entry{Name: "dsh-broken", Status: StatusUnknown},
	)

	var asked []string
	AnnotateRegistry(context.Background(), &res, func(_ context.Context, pkg, host string) (registry.Snapshot, error) {
		asked = append(asked, pkg+"@"+host)
		return registry.Snapshot{Packument: packument(pkg, "0.4.8", ver("0.4.8", "^0.4.0")), FetchedAt: asOf, From: registry.FromNetwork}, nil
	})
	if len(asked) != 1 || asked[0] != "dsh-pet@0.2.2-rc.1" {
		t.Fatalf("looked up %v, want only the blocked plugin", asked)
	}
	for _, e := range res.Entries[1:] {
		if e.Registry != nil {
			t.Errorf("%s got a registry answer it did not need", e.Name)
		}
	}
}

func TestAnnotateRegistryLatestAdmits(t *testing.T) {
	res := blockedResult("0.3.0", "dsh-pet")
	AnnotateRegistry(context.Background(), &res, func(_ context.Context, pkg, _ string) (registry.Snapshot, error) {
		return registry.Snapshot{
			Packument: packument(pkg, "0.4.8", ver("0.3.0", "^0.3.0"), ver("0.4.8", "^0.3.0")),
			FetchedAt: asOf, From: registry.FromNetwork,
		}, nil
	})
	a := res.Entries[0].Registry
	if a == nil {
		t.Fatal("no registry answer")
	}
	if !a.LatestAdmits {
		t.Fatalf("LatestAdmits = false, rejections = %+v", a.LatestRejections)
	}
	if a.Latest != "0.4.8" {
		t.Errorf("Latest = %q", a.Latest)
	}
	if len(a.Admitting) != 0 {
		t.Errorf("Admitting should be empty when the latest already works, got %v", a.Admitting)
	}
	if a.Published != 2 {
		t.Errorf("Published = %d", a.Published)
	}
}

// TestAnnotateRegistryFindsOlderReleaseThatAdmits is the deliverable: the local
// copy is blocked, latest is blocked, and there is still a published release
// that works — with its number.
func TestAnnotateRegistryFindsOlderReleaseThatAdmits(t *testing.T) {
	res := blockedResult("0.3.0", "dsh-pet")
	AnnotateRegistry(context.Background(), &res, func(_ context.Context, pkg, _ string) (registry.Snapshot, error) {
		return registry.Snapshot{
			Packument: packument(pkg, "0.4.8",
				ver("0.3.0", "^0.3.0"), ver("0.3.5", "^0.3.0"), ver("0.4.8", "^0.4.0")),
			FetchedAt: asOf, From: registry.FromCache,
		}, nil
	})
	a := res.Entries[0].Registry
	if a.LatestAdmits {
		t.Fatal("latest 0.4.8 must not admit 0.3.0")
	}
	if got := strings.Join(a.Admitting, ","); got != "0.3.5,0.3.0" {
		t.Errorf("Admitting = %q, want newest first", got)
	}
	if len(a.LatestRejections) != 1 || a.LatestRejections[0].Package != "@deepseek-ai/dsh-app-boot" {
		t.Errorf("LatestRejections = %+v", a.LatestRejections)
	}
}

func TestAnnotateRegistryNothingAdmits(t *testing.T) {
	res := blockedResult("0.3.0", "dsh-orphan")
	AnnotateRegistry(context.Background(), &res, func(_ context.Context, pkg, _ string) (registry.Snapshot, error) {
		return registry.Snapshot{
			Packument: packument(pkg, "1.0.0", ver("0.1.0", "^0.1.0"), ver("1.0.0", "^1.0.0")),
			FetchedAt: asOf, From: registry.FromNetwork,
		}, nil
	})
	a := res.Entries[0].Registry
	if a.LatestAdmits || len(a.Admitting) != 0 {
		t.Fatalf("nothing should admit 0.3.0: %+v", a)
	}
	if a.Published != 2 {
		t.Errorf("Published = %d, want the denominator for 'nothing admits it'", a.Published)
	}
}

// TestAnnotateRegistryFailureIsNotAVerdict is reverse assertion 1: when the
// registry cannot be read, the answer must be an error, not "no release works".
func TestAnnotateRegistryFailureIsNotAVerdict(t *testing.T) {
	res := blockedResult("0.2.2-rc.1", "dsh-pet")
	AnnotateRegistry(context.Background(), &res, func(_ context.Context, pkg, _ string) (registry.Snapshot, error) {
		return registry.Snapshot{}, errors.New("registry lookup for \"dsh-pet\" failed: dial tcp: i/o timeout")
	})
	a := res.Entries[0].Registry
	if a == nil {
		t.Fatal("a failed lookup must still be reported, not dropped")
	}
	if a.Error == "" {
		t.Fatal("Error must carry the failure; otherwise the entry looks answered")
	}
	if a.LatestAdmits || len(a.Admitting) != 0 || a.Latest != "" {
		t.Errorf("a failed lookup must not produce version claims: %+v", a)
	}
}

func TestAnnotateRegistryCarriesStaleness(t *testing.T) {
	res := blockedResult("0.2.2-rc.1", "dsh-pet")
	AnnotateRegistry(context.Background(), &res, func(_ context.Context, pkg, _ string) (registry.Snapshot, error) {
		return registry.Snapshot{
			Packument: packument(pkg, "0.4.8", ver("0.4.8", "^0.4.0")),
			FetchedAt: asOf, From: registry.FromCache, Stale: true, RefreshError: "HTTP 502",
		}, nil
	})
	a := res.Entries[0].Registry
	if !a.Stale || a.RefreshError != "HTTP 502" || a.From != registry.FromCache {
		t.Fatalf("staleness lost: %+v", a)
	}
	if a.AsOf != asOf {
		t.Errorf("AsOf = %v, want the cache timestamp", a.AsOf)
	}
}

func TestAnnotateRegistryCapsAdmittingList(t *testing.T) {
	versions := make([]registry.Version, 0, 8)
	for i := 0; i < 8; i++ {
		versions = append(versions, ver("0.3."+string(rune('0'+i)), "^0.3.0"))
	}
	versions = append(versions, ver("0.4.0", "^0.4.0"))
	res := blockedResult("0.3.0", "dsh-pet")
	AnnotateRegistry(context.Background(), &res, func(_ context.Context, pkg, _ string) (registry.Snapshot, error) {
		return registry.Snapshot{
			Packument: packument(pkg, "0.4.0", versions...),
			FetchedAt: asOf, From: registry.FromNetwork,
		}, nil
	})
	a := res.Entries[0].Registry
	if len(a.Admitting) != maxAdmitting {
		t.Fatalf("Admitting = %v, want %d entries", a.Admitting, maxAdmitting)
	}
	if a.Admitting[0] != "0.3.7" {
		t.Errorf("Admitting[0] = %q, want the newest admitting version first", a.Admitting[0])
	}
}

// TestAnnotateRegistryUsesTheSameGate is the anti-divergence test: the peer
// range `^0.2.1-rc.3` refuses 0.2.2-rc.1 by the prerelease gate, so the
// registry path must reach the same conclusion the local path reaches. If this
// ever fails while the local verdict passes, the two halves of the report
// disagree about the same range.
func TestAnnotateRegistryUsesTheSameGate(t *testing.T) {
	res := blockedResult("0.2.2-rc.1", "dsh-pinned")
	AnnotateRegistry(context.Background(), &res, func(_ context.Context, pkg, _ string) (registry.Snapshot, error) {
		return registry.Snapshot{
			Packument: packument(pkg, "1.0.0", ver("1.0.0", "^0.2.1-rc.3")),
			FetchedAt: asOf, From: registry.FromNetwork,
		}, nil
	})
	a := res.Entries[0].Registry
	if a.LatestAdmits {
		t.Fatal("^0.2.1-rc.3 must not admit 0.2.2-rc.1 (prerelease gate)")
	}
	if len(a.LatestRejections) != 1 || a.LatestRejections[0].Reason != "prerelease-gate" {
		t.Fatalf("rejection = %+v, want the gate named", a.LatestRejections)
	}
	if len(a.Admitting) != 0 {
		t.Errorf("the only published version is the rejecting one, got %v", a.Admitting)
	}
}
