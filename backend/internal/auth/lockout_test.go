package auth

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
)

const (
	loginPath  = "/api/v1/auth/login"
	genericMsg = `"detail":"Invalid Employee ID or password"`
)

func body(password string) string {
	return `{"employeeId":"123456","password":"` + password + `"}`
}

// expectUserState loads the user with the given lock bookkeeping.
func (e *env) expectUserState(attempts int, lockedUntil any) {
	e.mock.ExpectQuery("SELECT id, employee_id").WithArgs("123456").
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "王小明", "TSID", "PARTICIPANT", "zh-TW", storedHash(e.t), false, attempts, lockedUntil))
}

// expectCount expects the failure transaction: read `stored` under lock, save `saved` with no lock.
func (e *env) expectCount(stored, saved int) {
	e.mock.ExpectBegin()
	e.mock.ExpectQuery("SELECT failed_attempts, locked_until FROM `user`").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"failed_attempts", "locked_until"}).AddRow(stored, nil))
	e.mock.ExpectExec("UPDATE `user` SET failed_attempts").WithArgs(saved, nil, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	e.mock.ExpectCommit()
}

func (e *env) loginAttempt(password string) int {
	return e.do(http.MethodPost, loginPath, body(password), nil).Code
}

func TestAUTH_002_FifthConsecutiveFailureLocksForFiveMinutes(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.expectUserState(4, nil)
	e.mock.ExpectBegin()
	e.mock.ExpectQuery("SELECT failed_attempts, locked_until FROM `user`").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"failed_attempts", "locked_until"}).AddRow(4, nil))
	// The counter restarts at lock time; locked_until is exactly now + 5 minutes.
	e.mock.ExpectExec("UPDATE `user` SET failed_attempts").WithArgs(0, t0.Add(5*time.Minute), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	e.mock.ExpectCommit()

	w := e.do(http.MethodPost, loginPath, body("Wrong-pass-9!"), nil)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), genericMsg, "the fifth failure looks like the first (AUTH-003)")
	require.NoError(t, e.mock.ExpectationsWereMet())
	require.Len(t, e.rec.inTx, 1, "lockout is audited atomically with the lock (AUD-001)")
	lock := e.rec.inTx[0]
	assert.Equal(t, "auth.lockout", lock.Action)
	assert.Equal(t, "123456", lock.TargetID)
	assert.Empty(t, lock.ActorEmployeeID, "the system applies the lock, not the account holder")
	require.Len(t, e.rec.standalone, 1)
	assert.Equal(t, map[string]any{"reason": "bad_password"}, e.rec.standalone[0].Changes)
}

func TestAUTH_002_FailuresBelowTheLimitOnlyCount(t *testing.T) {
	for stored := 0; stored < 4; stored++ {
		e := newEnv(t, 30*time.Minute, 8*time.Hour)
		e.expectUserState(stored, nil)
		e.expectCount(stored, stored+1)
		assert.Equal(t, http.StatusUnauthorized, e.loginAttempt("Wrong-pass-9!"))
		require.NoError(t, e.mock.ExpectationsWereMet())
		assert.Empty(t, e.rec.inTx, "no lockout record before the fifth failure")
	}
}

func TestAUTH_002_LockedAccountRejectsEvenTheCorrectPassword(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.expectUserState(0, t0.Add(time.Minute)) // still locked for one more minute

	w := e.do(http.MethodPost, loginPath, body(loginPassword), nil)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), genericMsg)
	assert.Empty(t, w.Result().Cookies(), "no session while locked")
	// No transaction is expected: a locked attempt neither counts nor extends the lock.
	require.NoError(t, e.mock.ExpectationsWereMet())
	require.Len(t, e.rec.standalone, 1)
	assert.Equal(t, audit.ResultFailure, e.rec.standalone[0].Result)
	assert.Equal(t, map[string]any{"reason": "account_locked"}, e.rec.standalone[0].Changes)
}

func TestAUTH_002_LockedWrongPasswordDoesNotExtendTheLock(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.expectUserState(0, t0.Add(time.Minute))
	assert.Equal(t, http.StatusUnauthorized, e.loginAttempt("Wrong-pass-9!"))
	require.NoError(t, e.mock.ExpectationsWereMet())
	assert.Empty(t, e.rec.inTx)
}

func TestAUTH_002_LockExpiresAfterFiveMinutesAndClearsOnSuccess(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.clock.Set(t0.Add(5 * time.Minute)) // the instant the lock ends
	e.expectUserState(0, t0.Add(5*time.Minute))
	e.mock.ExpectExec("UPDATE `user` SET failed_attempts").WithArgs(0, nil, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := e.do(http.MethodPost, loginPath, body(loginPassword), nil)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, e.mock.ExpectationsWereMet())
}

func TestAUTH_002_FailureAfterExpiredLockStartsACleanCount(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.clock.Set(t0.Add(6 * time.Minute))
	e.expectUserState(0, t0.Add(5*time.Minute))
	e.mock.ExpectBegin()
	e.mock.ExpectQuery("SELECT failed_attempts, locked_until FROM `user`").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"failed_attempts", "locked_until"}).AddRow(0, t0.Add(5*time.Minute)))
	e.mock.ExpectExec("UPDATE `user` SET failed_attempts").WithArgs(1, nil, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	e.mock.ExpectCommit()

	assert.Equal(t, http.StatusUnauthorized, e.loginAttempt("Wrong-pass-9!"))
	require.NoError(t, e.mock.ExpectationsWereMet())
	assert.Empty(t, e.rec.inTx, "one failure after an expired lock is not a lockout")
}

func TestAUTH_002_SuccessResetsTheConsecutiveCount(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.expectUserState(3, nil)
	e.mock.ExpectExec("UPDATE `user` SET failed_attempts").WithArgs(0, nil, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	assert.Equal(t, http.StatusOK, e.loginAttempt(loginPassword))
	require.NoError(t, e.mock.ExpectationsWereMet())
}

func TestAUTH_002_CleanSuccessWritesNothing(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.expectUserState(0, nil)
	assert.Equal(t, http.StatusOK, e.loginAttempt(loginPassword))
	require.NoError(t, e.mock.ExpectationsWereMet(), "no UPDATE when there is nothing to reset")
}

func TestAUTH_002_ConcurrentRequestAlreadyLockedTheAccount(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.expectUserState(4, nil) // our read predates the other request's lock
	e.mock.ExpectBegin()
	e.mock.ExpectQuery("SELECT failed_attempts, locked_until FROM `user`").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"failed_attempts", "locked_until"}).AddRow(0, t0.Add(4*time.Minute)))
	e.mock.ExpectCommit() // nothing written
	assert.Equal(t, http.StatusUnauthorized, e.loginAttempt("Wrong-pass-9!"))
	require.NoError(t, e.mock.ExpectationsWereMet())
	assert.Empty(t, e.rec.inTx, "the lock is audited once")
	assert.Equal(t, map[string]any{"reason": "account_locked"}, e.rec.standalone[0].Changes)
}

func TestAUTH_002_UnknownIDIsNeverLocked(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	for i := 0; i < maxFailedAttempts+1; i++ {
		e.mock.ExpectQuery("SELECT id, employee_id").WithArgs("999999").WillReturnRows(sqlmock.NewRows(userCols))
		assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodPost, loginPath, `{"employeeId":"999999","password":"x"}`, nil).Code)
	}
	require.NoError(t, e.mock.ExpectationsWereMet(), "no lock bookkeeping for an ID that has no account")
}

func TestAUTH_003_LockedAndUnlockedResponsesAreByteIdentical(t *testing.T) {
	bodyOf := func(setup func(*env)) string {
		e := newEnv(t, 30*time.Minute, 8*time.Hour)
		setup(e)
		w := e.do(http.MethodPost, loginPath, body(loginPassword+"x"), nil)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Empty(t, w.Result().Cookies())
		assert.Empty(t, w.Header().Get("Retry-After"), "no hint about when the lock ends")
		return w.Body.String()
	}
	wrong := bodyOf(func(e *env) { e.expectUserState(0, nil); e.expectCount(0, 1) })
	locked := bodyOf(func(e *env) { e.expectUserState(0, t0.Add(time.Minute)) })
	unknown := bodyOf(func(e *env) {
		e.mock.ExpectQuery("SELECT id, employee_id").WithArgs("123456").WillReturnRows(sqlmock.NewRows(userCols))
	})
	assert.Equal(t, wrong, locked)
	assert.Equal(t, wrong, unknown)
	assert.False(t, strings.Contains(locked, "lock"), "the word lock never reaches the client")
}

func TestAUTH_002_FailureWriteErrorIsAServerErrorWithoutLeak(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.expectUserState(0, nil)
	e.mock.ExpectBegin().WillReturnError(assert.AnError)
	w := e.do(http.MethodPost, loginPath, body("Wrong-pass-9!"), nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), assert.AnError.Error())
}
