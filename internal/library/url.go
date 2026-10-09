package library

import (
	"net/url"
	"strings"
)

// WebPath is the dashboard URL path for a document or folder in a collection:
// /c/<collection>/<rel/path>, with each segment escaped. Folders end with "/".
func WebPath(collection, rel string, folder bool) string {
	var b strings.Builder
	b.WriteString("/c/")
	b.WriteString(url.PathEscape(collection))
	b.WriteString("/")
	rel = strings.Trim(rel, "/")
	if rel != "" {
		segs := strings.Split(rel, "/")
		for i, s := range segs {
			segs[i] = url.PathEscape(s)
		}
		b.WriteString(strings.Join(segs, "/"))
		if folder {
			b.WriteString("/")
		}
	}
	return b.String()
}
