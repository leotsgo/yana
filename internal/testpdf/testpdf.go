// Package testpdf builds tiny valid PDFs for tests: N pages, one line of
// text each, correct xref offsets. It exists so scanner and server tests
// can exercise real extraction without binary fixtures.
package testpdf

import (
	"fmt"
	"strings"
)

// Build returns a PDF whose page i (from 1) shows lines[i-1].
func Build(lines ...string) []byte {
	var b strings.Builder
	offsets := map[int]int{} // object number -> byte offset

	write := func(n int, body string) {
		offsets[n] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	writeStream := func(n int, data string) {
		offsets[n] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", n, len(data)+1, data)
	}

	b.WriteString("%PDF-1.4\n")
	write(1, "<< /Type /Catalog /Pages 2 0 R >>")

	n := len(lines)
	kids := make([]string, n)
	for i := range kids {
		kids[i] = fmt.Sprintf("%d 0 R", 3+2*i)
	}
	write(2, fmt.Sprintf("<< /Type /Pages /Kids [ %s ] /Count %d >>", strings.Join(kids, " "), n))

	next := 3
	for _, line := range lines {
		content := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", escape(line))
		pageObj := next
		contentObj := next + 1
		write(pageObj, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [ 0 0 612 792 ] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>",
			contentObj, 3+2*n))
		writeStream(contentObj, content)
		next += 2
	}
	fontObj := next
	write(fontObj, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	size := fontObj + 1
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n", size)
	b.WriteString("0000000000 65535 f \n")
	for i := 1; i < size; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", size, xref)
	return []byte(b.String())
}

func escape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return r.Replace(s)
}
