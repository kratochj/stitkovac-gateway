// Package lab contains isolated test doubles; it is never linked into the agent.
package lab

import (
	"bytes"
	"fmt"
)

func Document(kind, uid string) []byte {
	title, height := "TESTOVACI STITEK", 142
	if kind == "receipt" {
		title, height = "TESTOVACI UCTENKA", 360
	}
	stream := fmt.Sprintf("BT /F1 14 Tf 16 %d Td (%s) Tj 0 -24 Td /F1 10 Tf (Stitkovac Gateway Lab) Tj 0 -20 Td (Pouze simulace - nejde o prodej.) Tj 0 -20 Td (%s) Tj ET", height-28, title, uid)
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 283 %d] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", height), "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return out.Bytes()
}
