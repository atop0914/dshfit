package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atop0914/dshfit/internal/semver"
)

// record is one cache file. It stores the question it answered (package, host,
// registry) next to the answer, so a file can be validated on read instead of
// being trusted because of where it sits.
type record struct {
	Package   string    `json:"package"`
	Host      string    `json:"host"`
	Registry  string    `json:"registry"`
	FetchedAt time.Time `json:"fetched_at"`
	Packument Packument `json:"packument"`
}

// loadRecord returns the cached record for (pkg, host), or nil when there is
// none. A missing file is not an error; a file we cannot read or that describes
// a different question is, because both mean the cache cannot be used and only
// the caller knows whether the network is allowed to replace it.
func (c *Client) loadRecord(pkg, host string) (*record, error) {
	if c.opts.CacheDir == "" {
		return nil, nil
	}
	path := c.cachePath(pkg, host)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cached registry data at %s: %w", path, err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("cached registry data at %s is corrupt: %w", path, err)
	}
	if err := validate(&rec, pkg, host, c.opts.BaseURL); err != nil {
		return nil, fmt.Errorf("cached registry data at %s %w", path, err)
	}
	return &rec, nil
}

// validate decides whether a decoded record may be used to answer (pkg, host)
// against registry.
//
// It is separate from loadRecord because the file's bytes are third-party input:
// a cache directory is a plain directory of JSON that a user may edit, a backup
// tool may truncate, or another version of this tool may have written in a shape
// this one does not understand. Testing that a hand-edited file means "miss and
// refetch" needs a way to feed arbitrary bytes in without touching the disk, and
// a fuzz target cannot build a filesystem per iteration.
func validate(rec *record, pkg, host, registry string) error {
	if rec.Package != pkg || rec.Host != host || rec.Registry != registry {
		return fmt.Errorf("describes %s@%s from %s, not %s@%s from %s",
			rec.Package, rec.Host, rec.Registry, pkg, host, registry)
	}
	if rec.FetchedAt.IsZero() {
		return fmt.Errorf("has no fetch time, so its age is unknown")
	}
	if len(rec.Packument.Versions) == 0 {
		return fmt.Errorf("lists no versions, so it cannot answer anything")
	}
	// A record that decodes is not yet a record that can be used: every version
	// in it has to be orderable, because the answer is a comparison against a
	// host. An entry that does not parse would be silently skipped downstream,
	// which is how a cache turns into "the publisher shipped fewer versions".
	for _, v := range rec.Packument.Versions {
		if _, err := semver.Parse(v.Version); err != nil {
			return fmt.Errorf("lists version %q which is not a version (%v)", v.Version, err)
		}
	}
	return nil
}

// storeRecord writes the record atomically: a temp file in the same directory,
// then a rename. A half-written cache file would be read as "this package has
// three versions" by the next run, which is worse than having no cache.
func (c *Client) storeRecord(rec record) error {
	path := c.cachePath(rec.Package, rec.Host)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cached registry data: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create cache file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write cache file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close cache file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace cache file: %w", err)
	}
	return nil
}

// cachePath names one entry.
//
// The key is package + host + registry, and the hash is of exactly those three,
// so a different host or a different mirror cannot be served this file even if
// the readable part collides. The readable part is there because a cache
// directory full of hex names cannot be inspected by a user who wants to know
// what the tool remembers.
func (c *Client) cachePath(pkg, host string) string {
	key := pkg + "\x00" + host + "\x00" + c.opts.BaseURL
	sum := sha256.Sum256([]byte(key))
	suffix := hex.EncodeToString(sum[:])[:8]
	name := cacheName(pkg) + "@" + cacheName(host) + "-" + suffix + ".json"
	return filepath.Join(c.opts.CacheDir, name)
}

func cacheName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "empty"
	}
	// Long scoped names would otherwise blow past the 255-byte filename limit
	// together with the hash suffix.
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}
