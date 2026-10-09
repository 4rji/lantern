package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/4rji/lantern/internal/qmdconfig"
)

// URIScheme prefixes QMD document identifiers: qmd://<collection>/<rel/path>.
const URIScheme = "qmd://"

// maxReadSize caps how much of a single file is read into memory.
const maxReadSize = 20 << 20

// ErrUnknownCollection is returned for a collection name that is not in the config.
var ErrUnknownCollection = errors.New("unknown collection")

// SafeJoin joins rel onto root and guarantees the result stays inside root,
// both lexically and after resolving symlinks.
func SafeJoin(root, rel string) (string, error) {
	root = filepath.Clean(root)
	joined := filepath.Join(root, filepath.FromSlash(rel))
	if !within(root, joined) {
		return "", ErrOutsideRoot
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return joined, nil // root missing: the caller's read will fail
	}
	realPath, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return joined, nil // target missing: the caller's read will fail
	}
	if !within(realRoot, realPath) {
		return "", ErrOutsideRoot
	}
	return joined, nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// ReadFile reads a file inside a collection. rel is slash-separated.
func ReadFile(c *qmdconfig.Collection, rel string) ([]byte, error) {
	p, err := SafeJoin(c.Path, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a folder", rel)
	}
	if info.Size() > maxReadSize {
		return nil, fmt.Errorf("%s is too large (%d bytes)", rel, info.Size())
	}
	return os.ReadFile(p)
}

// Stat describes a path inside a collection (file or folder).
func Stat(c *qmdconfig.Collection, rel string) (fs.FileInfo, error) {
	p, err := SafeJoin(c.Path, rel)
	if err != nil {
		return nil, err
	}
	return os.Stat(p)
}

// ParseURI splits "qmd://<collection>/<rel/path>" into its parts.
func ParseURI(uri string) (collection, rel string, err error) {
	rest, ok := strings.CutPrefix(uri, URIScheme)
	if !ok {
		return "", "", fmt.Errorf("not a qmd:// URI: %q", uri)
	}
	collection, rel, _ = strings.Cut(rest, "/")
	rel = strings.TrimLeft(rel, "/")
	if collection == "" || rel == "" {
		return "", "", fmt.Errorf("incomplete qmd:// URI: %q", uri)
	}
	return collection, rel, nil
}

// Resolve maps a qmd:// URI to its collection, cleaned relative path, and
// absolute path on disk, enforcing containment.
func Resolve(cfg *qmdconfig.Config, uri string) (c *qmdconfig.Collection, rel, abs string, err error) {
	name, rel, err := ParseURI(uri)
	if err != nil {
		return nil, "", "", err
	}
	c = cfg.Collections[name]
	if c == nil {
		return nil, "", "", fmt.Errorf("%w: %s", ErrUnknownCollection, name)
	}
	abs, err = SafeJoin(c.Path, rel)
	if err != nil {
		return nil, "", "", err
	}
	rel, _ = filepath.Rel(c.Path, abs)
	return c, filepath.ToSlash(rel), abs, nil
}
