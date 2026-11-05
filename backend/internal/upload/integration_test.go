package upload

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests run against the local docker-compose ClamAV and MinIO.
// They skip unless CLAMAV_ADDR and MINIO_* are set (e.g. `set -a; . ../.env`).

func integrationEnv(t *testing.T) (ClamAV, *MinIO) {
	t.Helper()
	addr := os.Getenv("CLAMAV_ADDR")
	ep, ak, sk := os.Getenv("MINIO_ENDPOINT"), os.Getenv("MINIO_ACCESS_KEY"), os.Getenv("MINIO_SECRET_KEY")
	if addr == "" || ep == "" || ak == "" || sk == "" {
		t.Skip("CLAMAV_ADDR / MINIO_* not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	av := ClamAV{Addr: addr}
	require.NoError(t, av.Ping(ctx), "clamd not ready")
	m, err := NewMinIO(ep, ak, sk, "spike-"+strings.ToLower(t.Name()[:4])+"-test", os.Getenv("MINIO_USE_SSL") == "true")
	require.NoError(t, err)
	require.NoError(t, m.EnsureBucket(ctx))
	return av, m
}

func TestIntegrationPipeline(t *testing.T) {
	av, m := integrationEnv(t)
	ctx := context.Background()
	p := &Pipeline{Scanner: av, Store: m}
	noop := func(context.Context, Result) error { return nil }

	t.Run("valid files are scanned, stored and retrievable state", func(t *testing.T) {
		for _, c := range []struct{ file, ctype, sample string }{
			{"deck.pptx", mimePPTX, "valid.pptx"},
			{"deck.pdf", "application/pdf", "valid.pdf"},
		} {
			res, err := p.Accept(ctx, c.file, c.ctype, sample(t, c.sample), noop)
			require.NoError(t, err, c.file)
			ok, err := m.Exists(ctx, res.Key)
			require.NoError(t, err)
			assert.True(t, ok)
			require.NoError(t, m.Delete(ctx, res.Key))
		}
	})

	t.Run("rejects sample files before storing", func(t *testing.T) {
		for _, c := range []struct{ file, ctype, sample, code string }{
			{"a.pptx", mimePPTX, "reject-macro.pptx", CodeMacroRejected},
			{"a.pptx", mimePPTX, "reject-macro-type.pptx", CodeMacroRejected},
			{"a.pdf", "application/pdf", "reject-fake.pdf", CodeContentMismatch},
			{"a.pptx", mimePPTX, "reject-fake.pptx", CodeContentMismatch},
		} {
			_, err := p.Accept(ctx, c.file, c.ctype, sample(t, c.sample), noop)
			assert.Equal(t, c.code, code(err), c.sample)
		}
	})

	t.Run("EICAR inside a valid PPTX is rejected and never stored", func(t *testing.T) {
		var stored string
		_, err := p.Accept(ctx, "a.pptx", mimePPTX, eicarPPTX(t),
			func(_ context.Context, r Result) error { stored = r.Key; return nil })
		assert.True(t, IsMalware(err), "got %v", err)
		assert.Empty(t, stored)
	})

	t.Run("commit failure removes the object from MinIO", func(t *testing.T) {
		var key string
		boom := errors.New("tx failed")
		_, err := p.Accept(ctx, "a.pdf", "application/pdf", sample(t, "valid.pdf"),
			func(_ context.Context, r Result) error { key = r.Key; return boom })
		require.ErrorIs(t, err, boom)
		require.NotEmpty(t, key)
		ok, err := m.Exists(ctx, key)
		require.NoError(t, err)
		assert.False(t, ok, "object must be compensated away")
	})
}
