package profile

import (
	"fmt"
	"os"
	"path/filepath"
)

// LoadListing reads a listing from a file and returns the directory its
// relative package paths resolve against.
//
// The base is the listing's own directory, not the working directory of
// whatever process happens to be asking: pnpm prints absolute paths, so this
// only changes the answer for a hand-written listing — which is exactly the
// case where guessing the wrong root silently judges the wrong directories.
//
// This lives here, and not in either caller, because the terminal and the
// sidecar protocol must not be able to disagree about what a listing argument
// means. A second copy of this rule would drift, and the first symptom would be
// a panel reporting a different verdict than `dshfit profile` on the same file.
func LoadListing(path string) ([]Project, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("read listing: %w", err)
	}
	defer func() { _ = f.Close() }()

	projects, err := Parse(f)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	base := ""
	if abs, err := filepath.Abs(path); err == nil {
		base = filepath.Dir(abs)
	}
	return projects, base, nil
}
