// Package library walks QMD collections on disk: file trees, document
// listings, titles, file reads and qmd:// URI resolution. It only reads.
package library

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"

	"github.com/4rji/lantern/internal/qmdconfig"
)

// ErrOutsideRoot is returned when a path would escape its collection folder.
var ErrOutsideRoot = errors.New("path is outside the collection")

// builtinExcludes are folder names QMD never indexes, whatever the config says.
var builtinExcludes = map[string]bool{
	"node_modules": true, ".git": true, ".cache": true,
	"vendor": true, "dist": true, "build": true,
}

// titleReadLimit caps how much of a file is read to find its title.
const titleReadLimit = 64 << 10

// Doc is one markdown document in a collection.
type Doc struct {
	Collection string
	Path       string // slash-separated, relative to the collection root
	Title      string
	ModTime    time.Time
	Size       int64
}

// Dir returns the folder part of the document path ("" for the root).
func (d Doc) Dir() string {
	if dir := path.Dir(d.Path); dir != "." {
		return dir
	}
	return ""
}

// Name returns the file name.
func (d Doc) Name() string { return path.Base(d.Path) }

// Walk lists every document in the collection that matches its pattern and is
// not ignored, sorted by path.
func Walk(c *qmdconfig.Collection) ([]Doc, error) {
	root := c.Path
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("collection %s: %w", c.Name, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("collection %s: %s is not a folder", c.Name, root)
	}

	patterns := c.Patterns()
	var docs []Doc
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			// Unreadable subfolder or file: skip it rather than fail the whole walk.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (builtinExcludes[d.Name()] || matchAny(c.Ignore, rel)) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !matchAny(patterns, rel) || matchAny(c.Ignore, rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		docs = append(docs, Doc{
			Collection: c.Name,
			Path:       rel,
			Title:      titleOf(p, rel),
			ModTime:    info.ModTime(),
			Size:       info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk collection %s: %w", c.Name, err)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs, nil
}

// Matches reports whether rel (slash-separated) is a document of the collection.
func Matches(c *qmdconfig.Collection, rel string) bool {
	rel = strings.TrimPrefix(rel, "/")
	for _, seg := range strings.Split(path.Dir(rel), "/") {
		if builtinExcludes[seg] {
			return false
		}
	}
	return matchAny(c.Patterns(), rel) && !matchAny(c.Ignore, rel)
}

func matchAny(patterns []string, rel string) bool {
	for _, p := range patterns {
		if ok, _ := doublestar.Match(p, rel); ok {
			return true
		}
	}
	return false
}

func titleOf(abs, rel string) string {
	f, err := os.Open(abs)
	if err != nil {
		return FallbackTitle(rel)
	}
	defer f.Close()
	head, _ := io.ReadAll(io.LimitReader(f, titleReadLimit))
	return Title(head, rel)
}

// Title picks a document title: frontmatter `title`, else the first H1, else the file name.
func Title(content []byte, rel string) string {
	fm, body := SplitFrontmatter(content)
	if t := frontmatterTitle(fm); t != "" {
		return t
	}
	if t := firstH1(body); t != "" {
		return t
	}
	return FallbackTitle(rel)
}

// FallbackTitle is the file name without its extension.
func FallbackTitle(rel string) string {
	base := path.Base(rel)
	return strings.TrimSuffix(base, path.Ext(base))
}

// SplitFrontmatter separates a leading YAML frontmatter block ("---" ... "---")
// from the body. If there is none, fm is nil and body is content.
func SplitFrontmatter(content []byte) (fm, body []byte) {
	content = bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	end := bytes.IndexByte(content, '\n')
	if end < 0 || strings.TrimRight(string(content[:end]), " \t\r") != "---" {
		return nil, content
	}
	start := end + 1
	for pos := start; pos < len(content); {
		lineEnd, next := len(content), len(content)
		if nl := bytes.IndexByte(content[pos:], '\n'); nl >= 0 {
			lineEnd, next = pos+nl, pos+nl+1
		}
		line := strings.TrimRight(string(content[pos:lineEnd]), " \t\r")
		if line == "---" || line == "..." {
			return content[start:pos], content[next:]
		}
		pos = next
	}
	// Unterminated frontmatter: treat the whole thing as body.
	return nil, content
}

func frontmatterTitle(fm []byte) string {
	if len(fm) == 0 {
		return ""
	}
	var meta struct {
		Title any `yaml:"title"`
	}
	if err := yaml.Unmarshal(fm, &meta); err != nil || meta.Title == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(meta.Title))
}

// firstH1 finds the first level-1 heading (ATX "# x" or setext "x\n===")
// outside fenced code blocks.
func firstH1(body []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), titleReadLimit)
	var fence, prev string
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			prev = ""
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = trimmed[:3]
			prev = ""
			continue
		}
		indented := strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")
		if !indented && (trimmed == "#" || strings.HasPrefix(trimmed, "# ")) {
			t := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			t = strings.TrimSpace(strings.TrimRight(t, "#"))
			if t != "" {
				return t
			}
		}
		if prev != "" && trimmed != "" && strings.Trim(trimmed, "=") == "" {
			return prev
		}
		if indented {
			prev = ""
		} else {
			prev = trimmed
		}
	}
	return ""
}
