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
	if rec.Package != pkg || rec.Host != host || rec.Registry != c.opts.BaseURL {
		return nil, fmt.Errorf("cached registry data at %s describes %s@%s from %s, not %s@%s from %s",
			path, rec.Package, rec.Host, rec.Registry, pkg, host, c.opts.BaseURL)
	}
	if rec.FetchedAt.IsZero() {
		return nil, fmt.Errorf("cached registry data at %s has no fetch time, so its age is unknown", path)
	}
	if len(rec.Packument.Versions) == 0 {
		return nil, fmt.Errorf("cached registry data at %s lists no versions, so it cannot answer anything", path)
	}
	return &rec, nil
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
