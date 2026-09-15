// Package web embeds the built browser client. Run `make web` (or `npm run
// build` in this directory) before `go build` to include it; a binary built
// without it still serves the API and says so at /.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built client, or nil when dist/ holds no index.html.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// ExportSearchJS returns the bundled client-side search runtime that
// static site exports carry (minisearch plus the search page's wiring),
// or nil when this build has no web client; exports then ship without a
// search page.
func ExportSearchJS() []byte {
	b, err := dist.ReadFile("dist/export-search.js")
	if err != nil {
		return nil
	}
	return b
}
