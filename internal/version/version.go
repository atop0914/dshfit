// Package version is the single source of build identity.
package version

import (
	"runtime/debug"
	"strings"
)

// Overridden at build time with -ldflags. Empty means "derive it".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func resolve() (v, commit, date string) {
	v, commit, date = Version, Commit, Date
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "dev" || v == "" {
			if info.Main.Version != "" && info.Main.Version != "(devel)" {
				v = info.Main.Version
			}
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if commit == "" && len(s.Value) >= 7 {
					commit = s.Value[:7]
				}
			case "vcs.time":
				if date == "" {
					date = s.Value
				}
			}
		}
	}
	if v == "" {
		v = "dev"
	}
	return v, commit, date
}

// Value is the bare build identity: the version alone, without the commit and
// date String adds for a human reading a terminal line. A machine-readable
// field should not need to be parsed back out of a formatted sentence.
func Value() string {
	v, _, _ := resolve()
	return v
}

// String renders the identity, omitting fields that are genuinely unknown
// rather than printing placeholders for them. A commit already contained in the
// version string (git describe --always) is not repeated.
func String() string {
	v, commit, date := resolve()
	parts := []string{"dshfit " + v}
	if commit != "" && !strings.Contains(v, commit) {
		parts = append(parts, commit)
	}
	if date != "" {
		parts = append(parts, date)
	}
	return strings.Join(parts, " ")
}
