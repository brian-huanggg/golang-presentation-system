package upload

import (
	"archive/zip"
	"bytes"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// MaxFileSize is the hard limit from IDEA-003.
const MaxFileSize = 5 << 20

// Kind is the validated format of an upload.
type Kind string

const (
	KindPPTX Kind = "pptx"
	KindPDF  Kind = "pdf"
)

const (
	mimePPTX = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	mimePDF  = "application/pdf"
)

// Stable problem codes; the frontend translates these (IDEA-007).
const (
	CodeFileEmpty       = "upload_file_empty"
	CodeFileTooLarge    = "upload_file_too_large"
	CodeFormatRejected  = "upload_format_rejected"
	CodeTypeMismatch    = "upload_type_mismatch"
	CodeContentMismatch = "upload_content_mismatch"
	CodeMacroRejected   = "upload_macro_rejected"
	CodeMalware         = "upload_malware_detected"
	CodeScanUnavailable = "upload_scan_unavailable"
)

// maxContentTypesSize bounds how much of [Content_Types].xml we read, so a
// crafted entry cannot make us inflate an arbitrary amount of data.
const maxContentTypesSize = 1 << 20

// maxZipEntries bounds the central directory we are willing to walk.
const maxZipEntries = 20000

func reject(status int, code, detail string) *platform.Problem {
	return platform.NewProblem(status, code, detail)
}

// Validate checks IDEA-002/003/004: the extension, the declared Content-Type
// and the actual bytes must all agree on one allowed format. The whole file is
// already in memory (Architecture 8.2), so nothing is streamed here.
func Validate(filename, declaredType string, data []byte) (Kind, error) {
	if len(data) == 0 {
		return "", reject(http.StatusUnprocessableEntity, CodeFileEmpty, "The file is empty.")
	}
	if len(data) > MaxFileSize {
		return "", reject(http.StatusRequestEntityTooLarge, CodeFileTooLarge, "The file exceeds 5 MB.")
	}

	extKind, ok := kindFromExtension(filename)
	if !ok {
		return "", reject(http.StatusUnsupportedMediaType, CodeFormatRejected, "Only .pptx and .pdf files are accepted.")
	}

	// A missing or unparsable Content-Type is a mismatch, not a pass: IDEA-004
	// requires the declared type to match.
	mt, _, err := mime.ParseMediaType(declaredType)
	if err != nil || mt != mimeFor(extKind) {
		return "", reject(http.StatusUnsupportedMediaType, CodeTypeMismatch, "The declared file type does not match the file extension.")
	}

	switch extKind {
	case KindPDF:
		// Header must be at offset 0; readers that tolerate junk before %PDF-
		// are a classic polyglot trick, so we are stricter on purpose.
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return "", reject(http.StatusUnprocessableEntity, CodeContentMismatch, "The file content is not a PDF.")
		}
	case KindPPTX:
		if err := validatePPTX(data); err != nil {
			return "", err
		}
	}
	return extKind, nil
}

func kindFromExtension(filename string) (Kind, bool) {
	// Only the final extension counts, so "a.pdf.exe" and "a.pptx.pptm" fail.
	switch strings.ToLower(path.Ext(filename)) {
	case ".pptx":
		return KindPPTX, true
	case ".pdf":
		return KindPDF, true
	}
	return "", false
}

func mimeFor(k Kind) string {
	if k == KindPPTX {
		return mimePPTX
	}
	return mimePDF
}

func validatePPTX(data []byte) error {
	// ZIP local-file-header magic. Checked first so a non-zip gets a clear
	// "content mismatch" instead of a parser error string.
	if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return reject(http.StatusUnprocessableEntity, CodeContentMismatch, "The file content is not a PowerPoint (.pptx) file.")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(zr.File) > maxZipEntries {
		return reject(http.StatusUnprocessableEntity, CodeContentMismatch, "The file is not a valid PowerPoint (.pptx) container.")
	}

	var contentTypes, presentation *zip.File
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		// vbaProject.bin is the macro payload. Match anywhere in the
		// archive, not just ppt/, so relocated copies are caught too.
		if strings.HasSuffix(name, "vbaproject.bin") {
			return reject(http.StatusUnprocessableEntity, CodeMacroRejected, "Files containing macros are not accepted.")
		}
		switch name {
		case "[content_types].xml":
			contentTypes = f
		case "ppt/presentation.xml":
			presentation = f
		}
	}
	if contentTypes == nil || presentation == nil {
		return reject(http.StatusUnprocessableEntity, CodeContentMismatch, "The file is a ZIP archive but not a PowerPoint (.pptx) file.")
	}

	// A .pptm renamed to .pptx has no vbaProject.bin only if the macros were
	// stripped, but its main part still declares the macroEnabled content type.
	ct, err := readLimited(contentTypes, maxContentTypesSize)
	if err != nil {
		return reject(http.StatusUnprocessableEntity, CodeContentMismatch, "The PowerPoint content types part is unreadable.")
	}
	if bytes.Contains(bytes.ToLower(ct), []byte("macroenabled")) {
		return reject(http.StatusUnprocessableEntity, CodeMacroRejected, "Macro-enabled presentations are not accepted.")
	}
	if !bytes.Contains(ct, []byte("presentationml.presentation.main+xml")) {
		return reject(http.StatusUnprocessableEntity, CodeContentMismatch, "The file is not a PowerPoint presentation.")
	}
	return nil
}

func readLimited(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, io.ErrUnexpectedEOF
	}
	return b, nil
}
