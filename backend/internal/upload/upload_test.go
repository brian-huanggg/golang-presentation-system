package upload

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

func sample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

func code(err error) string {
	var p *platform.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return ""
}

func TestValidate(t *testing.T) {
	const pptxT = mimePPTX
	cases := []struct {
		name, file, ctype, sample string
		want                      Kind
		wantCode                  string
	}{
		{"valid pptx", "deck.pptx", pptxT, "valid.pptx", KindPPTX, ""},
		{"valid pdf", "deck.pdf", "application/pdf", "valid.pdf", KindPDF, ""},
		{"upper-case ext and params", "DECK.PDF", "application/pdf; charset=binary", "valid.pdf", KindPDF, ""},
		{"macro part", "a.pptx", pptxT, "reject-macro.pptx", "", CodeMacroRejected},
		{"macro-enabled type", "a.pptx", pptxT, "reject-macro-type.pptx", "", CodeMacroRejected},
		{"pdf bytes named pptx", "a.pptx", pptxT, "reject-fake.pptx", "", CodeContentMismatch},
		{"zip bytes named pdf", "a.pdf", "application/pdf", "reject-fake.pdf", "", CodeContentMismatch},
		{"plain zip", "a.pptx", pptxT, "reject-plain-zip.pptx", "", CodeContentMismatch},
		{"corrupt zip", "a.pptx", pptxT, "reject-garbage.pptx", "", CodeContentMismatch},
		{"pptm extension", "a.pptm", pptxT, "valid.pptx", "", CodeFormatRejected},
		{"double extension", "a.pdf.exe", "application/pdf", "valid.pdf", "", CodeFormatRejected},
		{"declared type mismatch", "a.pdf", pptxT, "valid.pdf", "", CodeTypeMismatch},
		{"missing declared type", "a.pdf", "", "valid.pdf", "", CodeTypeMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k, err := Validate(c.file, c.ctype, sample(t, c.sample))
			if c.wantCode == "" {
				require.NoError(t, err)
				assert.Equal(t, c.want, k)
				return
			}
			assert.Equal(t, c.wantCode, code(err))
		})
	}

	t.Run("empty", func(t *testing.T) {
		_, err := Validate("a.pdf", "application/pdf", nil)
		assert.Equal(t, CodeFileEmpty, code(err))
	})
	t.Run("size boundary", func(t *testing.T) {
		pdf := func(n int) []byte { return append([]byte("%PDF-"), make([]byte, n-5)...) }
		_, err := Validate("a.pdf", "application/pdf", pdf(MaxFileSize))
		assert.NoError(t, err)
		_, err = Validate("a.pdf", "application/pdf", pdf(MaxFileSize+1))
		assert.Equal(t, CodeFileTooLarge, code(err))
	})
}

// eicarPPTX is a structurally valid PPTX carrying the EICAR test string as an
// extra part. clamd unpacks archives, so it is detected inside the ZIP.
func eicarPPTX(t *testing.T) []byte {
	t.Helper()
	// Split so this source file itself is not flagged by scanners.
	eicar := `X5O!P%@AP[4\PZX54(P^)7CC)7}$` + `EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`
	src := sample(t, "valid.pptx")
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	require.NoError(t, err)
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		fw, _ := w.Create(f.Name)
		fw.Write(b)
	}
	fw, _ := w.Create("ppt/media/note.com")
	fw.Write([]byte(eicar))
	require.NoError(t, w.Close())
	return out.Bytes()
}

// fakeClamd speaks just enough INSTREAM to test our framing and reply parsing.
func fakeClamd(t *testing.T, reply string) (addr string, got *[]byte) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })
	var received []byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		cmd := make([]byte, len("zINSTREAM\x00"))
		if _, err := io.ReadFull(c, cmd); err != nil {
			return
		}
		for {
			var h [4]byte
			if _, err := io.ReadFull(c, h[:]); err != nil {
				return
			}
			n := binary.BigEndian.Uint32(h[:])
			if n == 0 {
				break
			}
			buf := make([]byte, n)
			if _, err := io.ReadFull(c, buf); err != nil {
				return
			}
			received = append(received, buf...)
		}
		c.Write([]byte(reply + "\x00"))
	}()
	t.Cleanup(func() { <-done })
	return l.Addr().String(), &received
}

func TestClamAVProtocol(t *testing.T) {
	data := bytes.Repeat([]byte("a"), chunkSize*2+17) // forces 3 chunks

	t.Run("clean", func(t *testing.T) {
		addr, got := fakeClamd(t, "stream: OK")
		require.NoError(t, ClamAV{Addr: addr}.Scan(context.Background(), data))
		assert.Equal(t, data, *got)
	})
	t.Run("found", func(t *testing.T) {
		addr, _ := fakeClamd(t, "stream: Eicar-Test-Signature FOUND")
		err := ClamAV{Addr: addr}.Scan(context.Background(), data)
		assert.True(t, IsMalware(err))
		assert.NotContains(t, platform.AsProblem(err).Detail, "Eicar") // no signature leak
	})
	t.Run("clamd error fails closed", func(t *testing.T) {
		addr, _ := fakeClamd(t, "INSTREAM size limit exceeded. ERROR")
		err := ClamAV{Addr: addr}.Scan(context.Background(), data)
		assert.Equal(t, CodeScanUnavailable, code(err))
	})
	t.Run("unreachable fails closed", func(t *testing.T) {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		addr := l.Addr().String()
		l.Close()
		err := ClamAV{Addr: addr}.Scan(context.Background(), data)
		assert.Equal(t, CodeScanUnavailable, code(err))
	})
}

// --- pipeline with fakes ---

type fakeScanner struct{ err error }

func (f fakeScanner) Scan(context.Context, []byte) error { return f.err }

type memStore struct {
	objs      map[string][]byte
	putErr    error
	deleteErr error
	deleted   []string
}

func newMem() *memStore { return &memStore{objs: map[string][]byte{}} }
func (m *memStore) Put(_ context.Context, k string, d []byte, _ string) error {
	if m.putErr != nil {
		return m.putErr
	}
	m.objs[k] = d
	return nil
}
func (m *memStore) Delete(_ context.Context, k string) error {
	m.deleted = append(m.deleted, k)
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.objs, k)
	return nil
}

func TestPipeline(t *testing.T) {
	ok := func(context.Context, Result) error { return nil }
	pdf := sample(t, "valid.pdf")

	t.Run("accepts and stores under random key", func(t *testing.T) {
		st := newMem()
		p := &Pipeline{Scanner: fakeScanner{}, Store: st}
		res, err := p.Accept(context.Background(), "my deck.pdf", "application/pdf", pdf, ok)
		require.NoError(t, err)
		assert.Len(t, res.Key, 32)
		assert.NotContains(t, res.Key, "deck")
		assert.Equal(t, pdf, st.objs[res.Key])
	})
	t.Run("invalid file touches nothing", func(t *testing.T) {
		st := newMem()
		p := &Pipeline{Scanner: fakeScanner{}, Store: st}
		_, err := p.Accept(context.Background(), "a.pptx", mimePPTX, sample(t, "reject-macro.pptx"), ok)
		assert.Equal(t, CodeMacroRejected, code(err))
		assert.Empty(t, st.objs)
	})
	t.Run("infected touches nothing", func(t *testing.T) {
		st := newMem()
		p := &Pipeline{Scanner: fakeScanner{err: errMalware}, Store: st}
		_, err := p.Accept(context.Background(), "a.pdf", "application/pdf", pdf, ok)
		assert.True(t, IsMalware(err))
		assert.Empty(t, st.objs)
	})
	t.Run("commit failure deletes object", func(t *testing.T) {
		st := newMem()
		p := &Pipeline{Scanner: fakeScanner{}, Store: st}
		boom := errors.New("db down")
		_, err := p.Accept(context.Background(), "a.pdf", "application/pdf", pdf,
			func(context.Context, Result) error { return boom })
		assert.ErrorIs(t, err, boom)
		assert.Empty(t, st.objs)
		assert.Len(t, st.deleted, 1)
	})
	t.Run("compensation survives cancelled request context", func(t *testing.T) {
		st := newMem()
		p := &Pipeline{Scanner: fakeScanner{}, Store: st}
		ctx, cancel := context.WithCancel(context.Background())
		_, err := p.Accept(ctx, "a.pdf", "application/pdf", pdf,
			func(context.Context, Result) error { cancel(); return ctx.Err() })
		assert.Error(t, err)
		assert.Empty(t, st.objs)
	})
	t.Run("failed compensation is only logged", func(t *testing.T) {
		st := newMem()
		st.deleteErr = errors.New("minio gone")
		p := &Pipeline{Scanner: fakeScanner{}, Store: st}
		_, err := p.Accept(context.Background(), "a.pdf", "application/pdf", pdf,
			func(context.Context, Result) error { return errors.New("db") })
		assert.EqualError(t, err, "db") // original error, not the cleanup one
	})
	t.Run("put failure", func(t *testing.T) {
		st := newMem()
		st.putErr = errors.New("s3 down")
		p := &Pipeline{Scanner: fakeScanner{}, Store: st}
		_, err := p.Accept(context.Background(), "a.pdf", "application/pdf", pdf, ok)
		assert.Error(t, err)
		assert.Len(t, st.deleted, 1)
	})
}

