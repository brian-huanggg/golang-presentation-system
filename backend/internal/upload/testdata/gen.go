//go:build ignore

// Generates the sample files used by the upload tests. Run from this package:
//
//	go run testdata/gen.go
//
// The EICAR test file is NOT written to disk: antivirus on developer machines
// would delete it. Tests build it in memory (see eicarPPTX in upload_test.go).
package main

import (
	"archive/zip"
	"bytes"
	"os"
)

const ctMain = `application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml`
const ctMacro = `application/vnd.ms-powerpoint.presentation.macroEnabled.main+xml`

func contentTypes(main string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/ppt/presentation.xml" ContentType="` + main + `"/></Types>`
}

func build(entries map[string]string, order []string) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, n := range order {
		f, _ := w.Create(n)
		f.Write([]byte(entries[n]))
	}
	w.Close()
	return b.Bytes()
}

func pptx(ct string, extra map[string]string) []byte {
	e := map[string]string{
		"[Content_Types].xml":  contentTypes(ct),
		"ppt/presentation.xml": `<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`,
	}
	order := []string{"[Content_Types].xml", "ppt/presentation.xml"}
	for k, v := range extra {
		e[k] = v
		order = append(order, k)
	}
	return build(e, order)
}

func pdf() []byte {
	return []byte("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[]/Count 0>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")
}

func main() {
	files := map[string][]byte{
		"valid.pptx":             pptx(ctMain, nil),
		"valid.pdf":              pdf(),
		"reject-macro.pptx":      pptx(ctMain, map[string]string{"ppt/vbaProject.bin": "\xd0\xcf\x11\xe0"}),
		"reject-macro-type.pptx": pptx(ctMacro, nil),
		"reject-fake.pdf":        []byte("PK\x03\x04 this is not a pdf"),
		"reject-fake.pptx":       pdf(),
		"reject-plain-zip.pptx":  build(map[string]string{"a.txt": "hi"}, []string{"a.txt"}),
		"reject-garbage.pptx":    []byte("PK\x03\x04garbage-not-a-real-zip"),
	}
	for n, d := range files {
		if err := os.WriteFile("testdata/"+n, d, 0o644); err != nil {
			panic(err)
		}
	}
}
