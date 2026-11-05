package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

var fixedNow = time.Date(2026, 11, 5, 6, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*Recorder, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	db := sqlx.NewDb(raw, "mysql")
	return NewRecorder(db, platform.NewManualClock(fixedNow)), mock
}

func okEntry() Entry {
	return Entry{
		ActorEmployeeID: "123456", ActorRole: "PARTICIPANT",
		Action: "team.create", TargetType: "team", TargetID: "7",
		Result: ResultSuccess, SourceIP: "10.0.0.1",
		Changes: map[string]any{"after": map[string]any{"name": "A"}},
	}
}

func TestRecord_UsesCallersTx(t *testing.T) {
	rec, mock := setup(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO audit_record").
		WithArgs(fixedNow, "123456", "PARTICIPANT", "team.create", "team", "7",
			"success", "10.0.0.1", `{"after":{"name":"A"}}`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectRollback() // the caller owns the tx; Record must not commit

	tx, err := rec.db.BeginTxx(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, rec.Record(context.Background(), tx, okEntry()))
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecord_EmptyOptionalsBecomeNull(t *testing.T) {
	rec, mock := setup(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO audit_record").
		WithArgs(fixedNow, nil, nil, "auth.login", nil, nil, "failure", nil, nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, rec.RecordStandalone(context.Background(),
		Entry{Action: "auth.login", Result: ResultFailure}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecordStandalone_CommitsOwnTx(t *testing.T) {
	rec, mock := setup(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO audit_record").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, rec.RecordStandalone(context.Background(), okEntry()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecordStandalone_InsertFailureRollsBack(t *testing.T) {
	rec, mock := setup(t)
	boom := errors.New("db down")
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO audit_record").WillReturnError(boom)
	mock.ExpectRollback()

	err := rec.RecordStandalone(context.Background(), okEntry())
	require.ErrorIs(t, err, boom)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInvalidEntries_NeverReachDB(t *testing.T) {
	cases := map[string]Entry{
		"no action":      {Result: ResultSuccess},
		"blank action":   {Action: "  ", Result: ResultSuccess},
		"bad result":     {Action: "x", Result: "maybe"},
		"password key":   {Action: "x", Result: ResultSuccess, Changes: map[string]any{"password": "p"}},
		"nested session": {Action: "x", Result: ResultSuccess, Changes: map[string]any{"a": []any{map[string]any{"Session_ID": "s"}}}},
		"password_hash":  {Action: "x", Result: ResultSuccess, Changes: map[string]any{"after": map[string]any{"password_hash": "h"}}},
		"unmarshalable":  {Action: "x", Result: ResultSuccess, Changes: make(chan int)},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			rec, mock := setup(t) // no expectations: any DB call would fail the test
			require.ErrorIs(t, rec.RecordStandalone(context.Background(), e), ErrInvalidEntry)
			assert.NoError(t, mock.ExpectationsWereMet())

			mock.ExpectBegin()
			tx, err := rec.db.BeginTxx(context.Background(), nil)
			require.NoError(t, err)
			require.ErrorIs(t, rec.Record(context.Background(), tx, e), ErrInvalidEntry)
		})
	}
}
