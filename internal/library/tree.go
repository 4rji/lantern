package library

import (
	"sort"
	"strings"
	"time"
)

// Folder is a node of a collection's folder tree. Only folders that contain
// documents (directly or below) appear in the tree.
type Folder struct {
	Name     string // "" for the root
	Path     string // slash-separated, relative to the collection root; "" for the root
	Folders  []*Folder
	Docs     []Doc
	DocCount int       // documents in this folder and all subfolders
	ModTime  time.Time // most recent document change in this folder and below
}

// BuildTree arranges documents into folders. Folders and documents are sorted by name.
func BuildTree(docs []Doc) *Folder {
	root := &Folder{}
	index := map[string]*Folder{"": root}

	var get func(dir string) *Folder
	get = func(dir string) *Folder {
		if f, ok := index[dir]; ok {
			return f
		}
		parentPath, name := "", dir
		if i := strings.LastIndex(dir, "/"); i >= 0 {
			parentPath, name = dir[:i], dir[i+1:]
		}
		parent := get(parentPath)
		f := &Folder{Name: name, Path: dir}
		parent.Folders = append(parent.Folders, f)
		index[dir] = f
		return f
	}

	for _, d := range docs {
		f := get(d.Dir())
		f.Docs = append(f.Docs, d)
	}
	finish(root)
	return root
}

func finish(f *Folder) {
	sort.Slice(f.Folders, func(i, j int) bool {
		return strings.ToLower(f.Folders[i].Name) < strings.ToLower(f.Folders[j].Name)
	})
	sort.Slice(f.Docs, func(i, j int) bool {
		return strings.ToLower(f.Docs[i].Name()) < strings.ToLower(f.Docs[j].Name())
	})
	f.DocCount = len(f.Docs)
	for _, d := range f.Docs {
		if d.ModTime.After(f.ModTime) {
			f.ModTime = d.ModTime
		}
	}
	for _, sub := range f.Folders {
		finish(sub)
		f.DocCount += sub.DocCount
		if sub.ModTime.After(f.ModTime) {
			f.ModTime = sub.ModTime
		}
	}
}

// Find returns the folder at rel ("" or "/" for the root), or nil.
func (f *Folder) Find(rel string) *Folder {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return f
	}
	cur := f
	for _, name := range strings.Split(rel, "/") {
		var next *Folder
		for _, sub := range cur.Folders {
			if sub.Name == name {
				next = sub
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}
