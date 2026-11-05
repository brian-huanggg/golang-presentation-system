package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validEnv() map[string]string {
	return map[string]string{
		"MARIADB_DATABASE": "esg", "MARIADB_USER": "u", "MARIADB_PASSWORD": "s3cret-pw",
		"MINIO_ENDPOINT": "localhost:9000", "MINIO_ACCESS_KEY": "ak", "MINIO_SECRET_KEY": "s3cret-key",
		"ADMINS_FILE": "/tmp/admins.json", "EVENT_YEAR": "2026",
		"EVENT_DEADLINE": "2026-12-31T23:59:59+08:00",
	}
}

func load(m map[string]string) (Config, error) { return Load(func(k string) string { return m[k] }) }

func TestLoad_Valid(t *testing.T) {
	c, err := load(validEnv())
	require.NoError(t, err)
	assert.Equal(t, 2026, c.EventYear)
	assert.Equal(t, time.Date(2026, 12, 31, 15, 59, 59, 0, time.UTC), c.EventDeadline)
	assert.Equal(t, ":8080", c.HTTPAddr)
	assert.Equal(t, 3306, c.DB.Port)
	assert.Equal(t, 30*time.Minute, c.SessionIdleTimeout, "AUTH-005 default")
	assert.Equal(t, 8*time.Hour, c.SessionLifetime, "AUTH-005 default")
}

func TestLoad_SessionTimeouts(t *testing.T) {
	m := validEnv()
	m["SESSION_IDLE_TIMEOUT"], m["SESSION_LIFETIME"] = "5m", "1h"
	c, err := load(m)
	require.NoError(t, err)
	assert.Equal(t, 5*time.Minute, c.SessionIdleTimeout)
	assert.Equal(t, time.Hour, c.SessionLifetime)

	for name, kv := range map[string][2]string{
		"garbage":         {"SESSION_IDLE_TIMEOUT", "soon"},
		"zero":            {"SESSION_LIFETIME", "0s"},
		"negative":        {"SESSION_IDLE_TIMEOUT", "-1m"},
		"idle > lifetime": {"SESSION_IDLE_TIMEOUT", "9h"},
	} {
		t.Run(name, func(t *testing.T) {
			m := validEnv()
			m[kv[0]] = kv[1]
			_, err := load(m)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "SESSION_")
		})
	}
}

func TestLoad_ReportsAllProblems(t *testing.T) {
	_, err := load(map[string]string{})
	require.Error(t, err)
	for _, want := range []string{"MARIADB_DATABASE", "MINIO_SECRET_KEY", "ADMINS_FILE", "EVENT_YEAR", "EVENT_DEADLINE"} {
		assert.Contains(t, err.Error(), want)
	}
}

func TestLoad_DeadlineRules(t *testing.T) {
	for name, v := range map[string]string{
		"no offset": "2026-12-31T23:59:59", "zulu": "2026-12-31T23:59:59Z", "garbage": "tomorrow",
	} {
		t.Run(name, func(t *testing.T) {
			m := validEnv()
			m["EVENT_DEADLINE"] = v
			_, err := load(m)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "EVENT_DEADLINE")
		})
	}
	// A deadline in the past is allowed (read-only system).
	m := validEnv()
	m["EVENT_DEADLINE"] = "2020-01-01T00:00:00+08:00"
	_, err := load(m)
	assert.NoError(t, err)
}

func TestConfig_DoesNotLeakSecrets(t *testing.T) {
	c, err := load(validEnv())
	require.NoError(t, err)
	out := fmt.Sprintf("%v %+v %#v", c, c, c)
	assert.NotContains(t, out, "s3cret-pw")
	assert.NotContains(t, out, "s3cret-key")
}

func TestClocks(t *testing.T) {
	assert.Equal(t, time.UTC, SystemClock{}.Now().Location())
	c := NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("x", 8*3600)))
	assert.Equal(t, time.UTC, c.Now().Location())
	c.Advance(time.Hour)
	assert.Equal(t, time.Date(2025, 12, 31, 17, 0, 0, 0, time.UTC), c.Now())
}

func newMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return sqlx.NewDb(db, "mysql"), mock
}

func TestWithTx(t *testing.T) {
	t.Run("commits on nil", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectBegin()
		mock.ExpectCommit()
		require.NoError(t, WithTx(context.Background(), db, func(*sqlx.Tx) error { return nil }))
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("rolls back on error and returns it", func(t *testing.T) {
		db, mock := newMockDB(t)
		boom := errors.New("boom")
		mock.ExpectBegin()
		mock.ExpectRollback()
		err := WithTx(context.Background(), db, func(*sqlx.Tx) error { return boom })
		assert.ErrorIs(t, err, boom)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("rolls back on panic and re-panics", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectBegin()
		mock.ExpectRollback()
		assert.PanicsWithValue(t, "oops", func() {
			_ = WithTx(context.Background(), db, func(*sqlx.Tx) error { panic("oops") })
		})
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("begin failure", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectBegin().WillReturnError(errors.New("down"))
		err := WithTx(context.Background(), db, func(*sqlx.Tx) error { t.Fatal("fn must not run"); return nil })
		assert.ErrorContains(t, err, "begin tx")
	})
	t.Run("commit failure", func(t *testing.T) {
		db, mock := newMockDB(t)
		mock.ExpectBegin()
		mock.ExpectCommit().WillReturnError(errors.New("conflict"))
		err := WithTx(context.Background(), db, func(*sqlx.Tx) error { return nil })
		assert.ErrorContains(t, err, "commit tx")
	})
}

func TestWriteProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	p := NewProblem(http.StatusConflict, "team_member_taken", "Employee 123456 is already in a team.")
	p.Errors = []FieldError{{Field: "members[0]", Code: "already_in_team"}}
	WriteProblem(rec, "/api/v1/teams", p)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, ProblemContentType, rec.Header().Get("Content-Type"))
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "Conflict", got["title"])
	assert.Equal(t, "team_member_taken", got["code"])
	assert.Equal(t, "/api/v1/teams", got["instance"])
	assert.NotContains(t, got, "type") // defaults to about:blank
	assert.Empty(t, p.Instance, "shared Problem must not be mutated")
}

func TestAsProblem_HidesInternalErrors(t *testing.T) {
	p := AsProblem(fmt.Errorf("wrap: %w", errors.New("dial tcp 10.0.0.1: secret detail")))
	assert.Equal(t, 500, p.Status)
	assert.NotContains(t, p.Detail, "secret")

	own := NewProblem(404, "not_found", "x")
	assert.Same(t, own, AsProblem(fmt.Errorf("ctx: %w", own)))
}

func TestParseLogLevel(t *testing.T) {
	_, ok := ParseLogLevel("loud")
	assert.False(t, ok)
	l, ok := ParseLogLevel(" WARN ")
	assert.True(t, ok)
	assert.Equal(t, "WARN", l.String())
}

func TestNewLogger_JSON(t *testing.T) {
	var b strings.Builder
	NewLogger(&b, 0).Info("hello", "k", "v")
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(b.String()), &got))
	assert.Equal(t, "hello", got["msg"])
}

func TestValueTypes(t *testing.T) {
	for _, s := range []string{"123456", "000001"} {
		_, err := ParseEmployeeID(s)
		assert.NoError(t, err, s)
	}
	for _, s := range []string{"", "12345", "1234567", "12345a", " 12345", "１２３４５６", "12 456"} {
		_, err := ParseEmployeeID(s)
		assert.Error(t, err, "%q", s)
	}
	for _, s := range []string{"TSID", "BCID", "DEID", "NTAD"} {
		_, err := ParseDepartment(s)
		assert.NoError(t, err)
	}
	_, err := ParseDepartment("tsid")
	assert.Error(t, err)
	_, err = ParseTrack("smart_innovation")
	assert.NoError(t, err)
	_, err = ParseTrack("other")
	assert.Error(t, err)
	for _, s := range []string{"engineering", "administration", "manufacturing_process", "transportation", "public_safety"} {
		_, err := ParseAwardCategory(s)
		assert.NoError(t, err)
	}
	_, err = ParseAwardCategory("sales")
	assert.Error(t, err)
}
