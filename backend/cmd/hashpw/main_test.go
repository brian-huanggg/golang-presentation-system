package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
)

func TestNFR_SEC_003_hashpw_outputs_verifiable_PHC(t *testing.T) {
	for _, in := range []string{"Secret!pw-Value9\n", "Secret!pw-Value9\r\n", "Secret!pw-Value9"} {
		var out bytes.Buffer
		require.NoError(t, run(strings.NewReader(in), &out))
		phc := strings.TrimSpace(out.String())
		require.True(t, strings.HasPrefix(phc, "$argon2id$v=19$m=19456,t=2,p=1$"), phc)
		ok, err := auth.NewArgon2idHasher().Verify(context.Background(), "Secret!pw-Value9", phc)
		require.NoError(t, err)
		require.True(t, ok)
	}
}

func TestNFR_SEC_003_hashpw_keeps_inner_spaces_and_salts(t *testing.T) {
	var a, b bytes.Buffer
	require.NoError(t, run(strings.NewReader(" a b \n"), &a))
	require.NoError(t, run(strings.NewReader(" a b \n"), &b))
	require.NotEqual(t, a.String(), b.String())
	ok, _ := auth.NewArgon2idHasher().Verify(context.Background(), " a b ", strings.TrimSpace(a.String()))
	require.True(t, ok)
}

func TestNFR_SEC_003_hashpw_rejects_bad_input(t *testing.T) {
	for _, in := range []string{"", "\n", strings.Repeat("x", maxPasswordBytes+1) + "\n"} {
		var out bytes.Buffer
		require.Error(t, run(strings.NewReader(in), &out))
		require.Empty(t, out.String())
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestNFR_SEC_003_hashpw_read_error(t *testing.T) {
	require.Error(t, run(failReader{}, &bytes.Buffer{}))
}
