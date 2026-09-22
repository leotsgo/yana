// Package pdftext pulls plain text out of PDF files, in pure Go, so the
// scanner can index what an attachment says without an external binary.
// It answers two questions: how many pages, and what words are on them.
// A scanned image with no text layer answers "" — that is not an error.
package pdftext

import (
	"io"
	"strings"

	pdf "github.com/dslipak/pdf"
)

// MaxText caps how much text one file contributes to the index. A manual
// can run to hundreds of pages; the search index does not need all of it.
const MaxText = 2 << 20

// Extract reads the PDF at ra (size bytes) and returns its page count and
// its text, with pages joined by newlines. A page that fails to parse is
// skipped, not fatal: the rest of the file still indexes. A file that is
// not a PDF at all returns the error.
func Extract(ra io.ReaderAt, size int64) (pages int, text string, err error) {
	r, err := pdf.NewReader(ra, size)
	if err != nil {
		return 0, "", err
	}
	pages = r.NumPage()
	fonts := make(map[string]*pdf.Font)
	var b strings.Builder
	for i := 1; i <= pages; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		for _, name := range p.Fonts() {
			if _, ok := fonts[name]; !ok {
				f := p.Font(name)
				fonts[name] = &f
			}
		}
		pageText, perr := p.GetPlainText(fonts)
		if perr != nil {
			continue
		}
		if b.Len() >= MaxText {
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(pageText)
		if b.Len() > MaxText {
			s := b.String()[:MaxText]
			b.Reset()
			b.WriteString(s)
			break
		}
	}
	return pages, b.String(), nil
}
