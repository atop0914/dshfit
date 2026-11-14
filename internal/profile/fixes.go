package profile

import (
	"github.com/atop0914/dshfit/internal/codes"
	"github.com/atop0914/dshfit/internal/fix"
)

// attachFix asks the solver for machine-checked rewrites of a group's range.
//
// Only the two version-range codes are solved, and that restriction is the point
// rather than an omission. A missing patch file, a row without an id, or an
// engines.dsh window has no range algebra: there is nothing to compute, and a
// generated "fix" for them would be advice with no evidence behind it. Their Fix
// text is the whole answer.
func attachFix(g *Group, host string) {
	switch g.Code {
	case codes.PeerRangeExcludesHost, codes.UnparseablePeerRange:
	default:
		return
	}
	res, err := fix.Solve(host, g.Range)
	if err != nil {
		// Two different refusals land here — a range that does not parse, and a
		// range that already admits the host — and both mean the same thing for
		// this row: there is no bound to widen, and saying so is the answer.
		g.FixRefusal = err.Error()
		return
	}
	g.FixCandidates = res.Candidates
	g.FixTried = res.Tried
}
