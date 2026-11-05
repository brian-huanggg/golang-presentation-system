package upload

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DDL-005: a caller may veto between the scan and the store, and a veto stores nothing.
func TestPipelineAcceptWith_BeforeStore(t *testing.T) {
	ok := func(context.Context, Result) error { return nil }
	pdf := sample(t, "valid.pdf")

	t.Run("veto after a clean scan stores nothing and never commits", func(t *testing.T) {
		st := newMem()
		p := &Pipeline{Scanner: fakeScanner{}, Store: st}
		veto := errors.New("deadline passed")
		committed := false
		_, err := p.AcceptWith(context.Background(), "a.pdf", "application/pdf", pdf,
			func(context.Context) error { return veto },
			func(context.Context, Result) error { committed = true; return nil })
		assert.ErrorIs(t, err, veto)
		assert.Empty(t, st.objs)
		assert.Empty(t, st.deleted, "nothing was stored, so there is nothing to delete")
		assert.False(t, committed)
	})
	t.Run("runs after validation and the scan, not before", func(t *testing.T) {
		st := newMem()
		called := false
		hook := func(context.Context) error { called = true; return nil }

		// An invalid file never reaches the hook.
		p := &Pipeline{Scanner: fakeScanner{}, Store: st}
		_, err := p.AcceptWith(context.Background(), "a.exe", "application/pdf", pdf, hook, ok)
		require.Error(t, err)
		assert.False(t, called, "validation comes first")

		// Nor does an infected one.
		p = &Pipeline{Scanner: fakeScanner{err: errMalware}, Store: st}
		_, err = p.AcceptWith(context.Background(), "a.pdf", "application/pdf", pdf, hook, ok)
		assert.True(t, IsMalware(err))
		assert.False(t, called, "the scan comes first")
	})
	t.Run("a passing hook lets the file through", func(t *testing.T) {
		st := newMem()
		p := &Pipeline{Scanner: fakeScanner{}, Store: st}
		res, err := p.AcceptWith(context.Background(), "a.pdf", "application/pdf", pdf,
			func(context.Context) error { return nil }, ok)
		require.NoError(t, err)
		assert.Equal(t, pdf, st.objs[res.Key])
	})
}
