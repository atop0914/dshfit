package manifest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// npmRoot is the single directory npm places a package under inside its
// tarballs, so every entry path starts with "package/". It is not a convention
// dshfit may ignore: without it there is no way to tell the package root from a
// path, and a reader that strips "the first segment, whatever it is" turns
// package/app/../evil into a write outside the extraction directory.
const npmRoot = "package"

// Limits on what a single archive may cost. Both exist so that a malicious or
// corrupt tarball fails loudly instead of exhausting memory; the numbers are far
// above anything a real DSH plugin ships (the largest in the ecosystem is a few
// megabytes), so they reject abuse without rejecting plugins.
const (
	maxEntries      = 100_000
	maxUnpackedSize = 64 << 20
)

// typeRegLegacy is tar.TypeRegA ('\x00'), spelled out because the exported alias
// is deprecated and still appears in archives written by older tools. Ignoring
// it would make this reader reject perfectly ordinary tarballs.
const typeRegLegacy = 0

// LoadTarball reads an npm package archive without unpacking it to disk.
//
// The archive answers a question a directory cannot: whether a path that
// package.json's files[] declares is actually *in* the published artifact.
// .npmignore, a skipped prepare step, or a stale files[] entry all produce a
// package that installs and silently omits files its own manifest lists.
//
// Everything it cannot read is an error. A partially-read archive would produce a
// verdict about a package that does not exist.
func LoadTarball(file string) (*Plugin, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: not a gzip stream: %w", file, err)
	}
	defer func() { _ = gz.Close() }()

	var (
		tr      = tar.NewReader(gz)
		blobs   = make(map[string][]byte)
		entries []string
		seen    = make(map[string]bool)
		unsafe  []UnsafeEntry
		total   int64
		count   int
		// outside counts entries that exist but are not under package/, which is
		// how an archive that uses some other layout gets distinguished from one
		// that is merely shaped wrong in a single entry.
		outside int
	)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		count++
		if count > maxEntries {
			return nil, fmt.Errorf("read %s: more than %d archive entries", file, maxEntries)
		}

		name := strings.TrimPrefix(strings.ReplaceAll(hdr.Name, "\\", "/"), "./")
		if name == "" {
			continue
		}

		if reason := unsafeEntry(name, hdr.Typeflag); reason != "" {
			unsafe = append(unsafe, UnsafeEntry{Path: hdr.Name, Reason: reason})
			continue
		}

		rel, ok := trimNPMRoot(name)
		if !ok {
			outside++
			unsafe = append(unsafe, UnsafeEntry{Path: hdr.Name, Reason: ReasonOutside})
			continue
		}
		if rel == "" {
			continue // the package root directory itself
		}
		if hdr.Typeflag == tar.TypeDir {
			entries = addEntry(entries, seen, rel)
			continue
		}

		total += hdr.Size
		if total > maxUnpackedSize {
			return nil, fmt.Errorf("read %s: more than %d bytes uncompressed", file, int64(maxUnpackedSize))
		}
		// The tar reader clamps each body to the header's declared size, so a
		// header that lies low cannot push this past the limit.
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, tr); err != nil {
			return nil, fmt.Errorf("read %s: entry %q: %w", file, hdr.Name, err)
		}
		blobs[rel] = buf.Bytes()
		entries = addEntry(entries, seen, rel)
	}

	if count == 0 {
		return nil, fmt.Errorf("read %s: the archive has no entries", file)
	}
	if len(entries) == 0 {
		if outside > 0 {
			return nil, fmt.Errorf("read %s: %d entries and none under %q/, so the package root "+
				"cannot be determined (first: %q)", file, outside, npmRoot+"/", unsafe[0].Path)
		}
		return nil, fmt.Errorf("read %s: no entries under %q/", file, npmRoot+"/")
	}

	body, ok := blobs["package.json"]
	if !ok {
		return nil, fmt.Errorf("read %s: no %s/package.json in the archive", file, npmRoot)
	}
	p, err := parsePlugin(body, file+"!/"+npmRoot+"/package.json")
	if err != nil {
		return nil, err
	}
	sortStrings(entries)
	p.Source = TarballSource
	p.Tarball = file
	p.Entries = entries
	p.Unsafe = unsafe
	p.mem = blobs
	return p, nil
}

// unsafeEntry classifies an entry before its path is interpreted at all. The
// order matters: a path is checked before its type, because "package/../../etc"
// is an escape attempt whether or not it is also a symlink.
func unsafeEntry(name string, tf byte) string {
	if path.IsAbs(name) || strings.HasPrefix(name, "/") {
		return ReasonOutside
	}
	// "C:/..." and "C:\..." are absolute on the machine that would extract the
	// archive, even though they look relative to a POSIX tar reader.
	if len(name) >= 2 && name[1] == ':' {
		if c := name[0]; (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return ReasonOutside
		}
	}
	// Any ".." segment is refused, not just one that escapes after cleaning.
	// npm never writes one, so the false-positive surface is empty, and
	// normalising it away would mean silently accepting a path whose intent the
	// author did not express.
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return ReasonOutside
		}
	}
	switch tf {
	case tar.TypeDir, tar.TypeReg, typeRegLegacy:
		return ""
	case tar.TypeSymlink, tar.TypeLink:
		return ReasonLink
	default:
		return ReasonSpecial
	}
}

// trimNPMRoot strips the fixed "package/" prefix. It reports false for an entry
// that is not under the root; it never guesses at a different layout.
func trimNPMRoot(name string) (string, bool) {
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == npmRoot {
		return "", true
	}
	if !strings.HasPrefix(trimmed, npmRoot+"/") {
		return "", false
	}
	return strings.TrimPrefix(trimmed, npmRoot+"/"), true
}

func addEntry(entries []string, seen map[string]bool, rel string) []string {
	if seen[rel] {
		return entries
	}
	seen[rel] = true
	return append(entries, rel)
}

// hasGzipMagic peeks at the first two bytes. Content decides before the file name
// does, because "npm pack" output is routinely renamed and a .tgz suffix on a
// non-gzip file must not become a claim about what the file is.
func hasGzipMagic(file string) (bool, error) {
	f, err := os.Open(file)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", file, err)
	}
	defer func() { _ = f.Close() }()
	var magic [2]byte
	n, err := io.ReadFull(f, magic[:])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read %s: %w", file, err)
	}
	if n < 2 {
		return false, nil
	}
	return magic[0] == 0x1f && magic[1] == 0x8b, nil
}
