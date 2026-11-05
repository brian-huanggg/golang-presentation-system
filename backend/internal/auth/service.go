package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

const (
	actionRegister  = "auth.register"
	actionLogin     = "auth.login"
	actionLogout    = "auth.logout"
	actionLockout   = "auth.lockout"
	targetUser      = "user"
	roleParticipant = "PARTICIPANT"
	roleAdmin       = "ADMIN"
)

const (
	// maxFailedAttempts consecutive wrong passwords lock the account for lockDuration
	// (AUTH-002). Fixed by the PRD, so constants rather than configuration.
	maxFailedAttempts = 5
	lockDuration      = 5 * time.Minute
)

// DeadlineGate answers "may a participant write now?". It is defined here, at the
// consumer, because auth must not import event (ADR 0003); event.Policy satisfies it.
type DeadlineGate interface {
	CheckActor(isAdmin bool) error
}

// Auditor is the part of audit.Recorder that auth uses.
type Auditor interface {
	Record(ctx context.Context, tx sqlx.ExecerContext, e audit.Entry) error
	RecordStandalone(ctx context.Context, e audit.Entry) error
}

// Sessions is the part of *scs.SessionManager that auth uses (ADR 0007), so the
// store can change without touching the service and tests can fake it.
type Sessions interface {
	RenewToken(ctx context.Context) error
	Put(ctx context.Context, key string, val any)
	GetInt64(ctx context.Context, key string) int64
	GetString(ctx context.Context, key string) string
	Destroy(ctx context.Context) error
}

// Service holds the auth business rules. Construct it once in main.go.
type Service struct {
	db       *sqlx.DB
	gate     DeadlineGate
	hasher   Hasher
	sessions Sessions
	audit    Auditor
	clock    platform.Clock
	log      *slog.Logger
	// dummyHash is verified against when the Employee ID is unknown, so an unknown
	// account costs the same time as a wrong password (AUTH-003: no timing oracle).
	dummyHash string
}

func NewService(db *sqlx.DB, gate DeadlineGate, hasher Hasher, sessions Sessions, auditor Auditor, clock platform.Clock, log *slog.Logger) *Service {
	s := &Service{db: db, gate: gate, hasher: hasher, sessions: sessions, audit: auditor, clock: clock, log: log}
	// Hashed once at startup with the current parameters. If this fails the unknown-ID
	// path degrades to a fast mismatch; log it rather than refuse to start.
	h, err := hasher.Hash(context.Background(), "timing-equalizer-not-a-real-password")
	if err != nil {
		log.Error("compute dummy hash; login timing for unknown IDs is not equalized", "error", err)
	}
	s.dummyHash = h
	return s
}

// RegisteredUser is what a successful registration returns. It never carries the hash.
type RegisteredUser struct {
	EmployeeID        string
	FullName          string
	Department        string
	PreferredLanguage string
	Email             string
}

// Register creates a Participant account (REG-001..003, REG-006, REG-009).
// sourceIP is recorded in the audit trail only.
func (s *Service) Register(ctx context.Context, in RegisterInput, sourceIP string) (RegisteredUser, error) {
	// REG-006 / DDL-003: the visitor is never an Admin, so the deadline always applies.
	// It is checked first so a closed event reveals nothing about which IDs exist.
	if err := s.gate.CheckActor(false); err != nil {
		s.auditFailure(ctx, in.EmployeeID, sourceIP, "deadline_passed")
		return RegisteredUser{}, err
	}

	u, fieldErrs := validateRegister(in)
	if len(fieldErrs) > 0 {
		return RegisteredUser{}, ErrValidation(fieldErrs)
	}

	// Hash before opening the transaction: it is slow and must not hold a DB connection.
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return RegisteredUser{}, err
	}

	err = platform.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
		if err := insertUser(ctx, tx, u, hash); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			ActorEmployeeID: u.EmployeeID.String(),
			ActorRole:       roleParticipant,
			Action:          actionRegister,
			TargetType:      targetUser,
			TargetID:        u.EmployeeID.String(),
			Result:          audit.ResultSuccess,
			SourceIP:        sourceIP,
			// AUD-006: never the password or its hash.
			Changes: map[string]any{"after": map[string]any{
				"department": u.Department.String(), "preferredLanguage": u.PreferredLanguage,
				"emailProvided": u.Email != "",
			}},
		})
	})
	switch {
	case errors.Is(err, errDuplicateEmployeeID):
		s.auditFailure(ctx, u.EmployeeID.String(), sourceIP, "employee_id_taken")
		return RegisteredUser{}, ErrEmployeeIDTaken()
	case err != nil:
		return RegisteredUser{}, err
	}
	return RegisteredUser{
		EmployeeID: u.EmployeeID.String(), FullName: u.FullName, Department: u.Department.String(),
		PreferredLanguage: u.PreferredLanguage, Email: u.Email,
	}, nil
}

// auditFailure records a rejected registration. The visitor is unauthenticated, so
// there is no actor; the claimed ID is only the target. A failure to write the
// record is logged and does not change the response the visitor gets.
func (s *Service) auditFailure(ctx context.Context, claimedID, sourceIP, reason string) {
	e := audit.Entry{
		Action: actionRegister, Result: audit.ResultFailure, SourceIP: sourceIP,
		Changes: map[string]any{"reason": reason},
	}
	// Only a well-formed ID becomes the target; arbitrary input stays out of the log table.
	if id, err := platform.ParseEmployeeID(claimedID); err == nil {
		e.TargetType, e.TargetID = targetUser, id.String()
	}
	if err := s.audit.RecordStandalone(ctx, e); err != nil {
		s.log.ErrorContext(ctx, "audit registration failure", "error", err)
	}
}

// SessionUser is the identity returned by login and /auth/me. It never carries the hash.
type SessionUser struct {
	EmployeeID         string
	FullName           string
	Role               string
	Department         string // "" for Admin
	PreferredLanguage  string
	MustChangePassword bool
}

func toSessionUser(u userRow) SessionUser {
	return SessionUser{
		EmployeeID: u.EmployeeID, FullName: u.FullName, Role: u.Role,
		Department: u.Department.String, PreferredLanguage: u.PreferredLanguage,
		MustChangePassword: u.MustChangePassword,
	}
}

// Login authenticates by Employee ID and password and starts a session
// (AUTH-001, AUTH-003, AUTH-005). It is open after the deadline (DDL-004), so it
// does not consult the DeadlineGate. Every credential failure returns the same
// ErrInvalidCredentials; the real reason goes only to the audit trail.
//
// The session is created in the request's scs context; scs persists it when the
// response is written, so a failure here leaves no session behind.
func (s *Service) Login(ctx context.Context, employeeID, password, sourceIP string) (SessionUser, error) {
	id, err := platform.ParseEmployeeID(employeeID)
	if err != nil {
		// Not 6 digits: an email address lands here, which is how AUTH-001 is enforced.
		s.auditLoginFailure(ctx, "", sourceIP, "invalid_employee_id")
		return SessionUser{}, ErrInvalidCredentials()
	}

	row, err := findUserByEmployeeID(ctx, s.db, id.String())
	switch {
	case errors.Is(err, errUserNotFound):
		// Burn the same Argon2id time as a real check before answering.
		if _, verr := s.hasher.Verify(ctx, password, s.dummyHash); verr != nil && ctx.Err() != nil {
			return SessionUser{}, verr
		}
		s.auditLoginFailure(ctx, id.String(), sourceIP, "unknown_user")
		return SessionUser{}, ErrInvalidCredentials()
	case err != nil:
		return SessionUser{}, err
	}

	// The hash is always verified, even for a locked account, so the response time
	// never tells a caller that the account is locked (AUTH-003).
	ok, err := s.hasher.Verify(ctx, password, row.PasswordHash)
	locked := row.LockedUntil.Valid && s.clock.Now().Before(row.LockedUntil.Time)
	if err != nil && !locked {
		// A stored hash we cannot parse is our bug, not the caller's mistake.
		return SessionUser{}, fmt.Errorf("verify password for user %d: %w", row.ID, err)
	}
	if locked {
		// Even the right password gets the generic answer while locked, and the
		// attempt neither counts nor extends the lock.
		if ctx.Err() != nil {
			return SessionUser{}, ctx.Err()
		}
		s.auditLoginFailure(ctx, id.String(), sourceIP, "account_locked")
		return SessionUser{}, ErrInvalidCredentials()
	}
	if !ok {
		reason := "bad_password"
		wasLocked, err := s.recordFailedAttempt(ctx, row, sourceIP)
		if err != nil {
			return SessionUser{}, err
		}
		if wasLocked { // a concurrent request locked it between our read and now
			reason = "account_locked"
		}
		s.auditLoginFailure(ctx, id.String(), sourceIP, reason)
		return SessionUser{}, ErrInvalidCredentials()
	}

	// "Consecutive" failures: a success starts the count over. Skipped when there is
	// nothing to clear, so ordinary logins do not write.
	if row.FailedAttempts > 0 || row.LockedUntil.Valid {
		if err := saveLockState(ctx, s.db, row.ID, 0, time.Time{}); err != nil {
			return SessionUser{}, err
		}
	}

	// Fixation defence: a new token (and a fresh 8 h deadline) for the authenticated session.
	if err := s.sessions.RenewToken(ctx); err != nil {
		return SessionUser{}, fmt.Errorf("renew session token: %w", err)
	}
	s.sessions.Put(ctx, keyUserID, row.ID)
	s.sessions.Put(ctx, keyEmployeeID, row.EmployeeID)
	s.sessions.Put(ctx, keyRole, row.Role)

	if err := s.audit.RecordStandalone(ctx, audit.Entry{
		ActorEmployeeID: row.EmployeeID, ActorRole: row.Role, Action: actionLogin,
		TargetType: targetUser, TargetID: row.EmployeeID,
		Result: audit.ResultSuccess, SourceIP: sourceIP,
	}); err != nil {
		// AUD-001 wants every login recorded: fail closed rather than hand out an unaudited session.
		if derr := s.sessions.Destroy(ctx); derr != nil {
			s.log.ErrorContext(ctx, "destroy session after audit failure", "error", derr)
		}
		return SessionUser{}, fmt.Errorf("audit login: %w", err)
	}
	return toSessionUser(row), nil
}

// recordFailedAttempt counts one wrong password and, on the maxFailedAttempts-th,
// locks the account and audits the lockout (AUD-001) in the same transaction. The
// row lock serialises concurrent guesses. When the lock was already set by a
// concurrent request it changes nothing and reports alreadyLocked. The counter
// restarts from zero at lock time, so an expired lock needs no separate reset.
func (s *Service) recordFailedAttempt(ctx context.Context, row userRow, sourceIP string) (alreadyLocked bool, err error) {
	err = platform.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
		st, err := lockStateForUpdate(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if st.LockedUntil.Valid && now.Before(st.LockedUntil.Time) {
			alreadyLocked = true
			return nil
		}
		attempts := st.FailedAttempts + 1
		if attempts < maxFailedAttempts {
			return saveLockState(ctx, tx, row.ID, attempts, time.Time{})
		}
		until := now.Add(lockDuration)
		if err := saveLockState(ctx, tx, row.ID, 0, until); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			// No actor: the lock is applied by the system, not by the account holder.
			Action:     actionLockout,
			TargetType: targetUser, TargetID: row.EmployeeID,
			Result: audit.ResultSuccess, SourceIP: sourceIP,
			Changes: map[string]any{"after": map[string]any{
				"failedAttempts": maxFailedAttempts, "lockedUntil": until.Format(time.RFC3339),
			}},
		})
	})
	return alreadyLocked, err
}

// Logout ends the caller's session (AUTH-006). Destroy deletes it from the store
// before returning, so the old token is dead on the next request. It is
// idempotent: with no live session it does nothing and records nothing.
func (s *Service) Logout(ctx context.Context, sourceIP string) error {
	empID, role := s.sessions.GetString(ctx, keyEmployeeID), s.sessions.GetString(ctx, keyRole)
	if s.sessions.GetInt64(ctx, keyUserID) == 0 {
		return nil
	}
	if err := s.sessions.Destroy(ctx); err != nil {
		return fmt.Errorf("destroy session: %w", err)
	}
	// Invalidation already happened, so a failed audit write is logged, not surfaced:
	// the user must never be left logged in because the log table hiccuped.
	if err := s.audit.RecordStandalone(ctx, audit.Entry{
		ActorEmployeeID: empID, ActorRole: role, Action: actionLogout,
		TargetType: targetUser, TargetID: empID,
		Result: audit.ResultSuccess, SourceIP: sourceIP,
	}); err != nil {
		s.log.ErrorContext(ctx, "audit logout", "error", err)
	}
	return nil
}

// CurrentUser returns the logged-in user from the session, re-read from the
// database so a changed name or language is never stale. It answers
// ErrUnauthenticated when there is no live session (expired sessions are simply
// absent from the store, so scs hands back an empty one).
func (s *Service) CurrentUser(ctx context.Context) (SessionUser, error) {
	uid := s.sessions.GetInt64(ctx, keyUserID)
	if uid == 0 {
		return SessionUser{}, ErrUnauthenticated()
	}
	row, err := findUserByID(ctx, s.db, uid)
	if errors.Is(err, errUserNotFound) {
		// The account vanished (engineer deleted it): the session must not outlive it.
		if derr := s.sessions.Destroy(ctx); derr != nil {
			s.log.ErrorContext(ctx, "destroy session of missing user", "error", derr)
		}
		return SessionUser{}, ErrUnauthenticated()
	}
	if err != nil {
		return SessionUser{}, err
	}
	return toSessionUser(row), nil
}

// auditLoginFailure records a rejected login. claimedID is empty when the input was
// not a well-formed Employee ID, keeping arbitrary text out of the log table. A
// write failure is logged and does not change the generic answer (AUTH-003).
func (s *Service) auditLoginFailure(ctx context.Context, claimedID, sourceIP, reason string) {
	e := audit.Entry{
		Action: actionLogin, Result: audit.ResultFailure, SourceIP: sourceIP,
		Changes: map[string]any{"reason": reason},
	}
	if claimedID != "" {
		e.TargetType, e.TargetID = targetUser, claimedID
	}
	if err := s.audit.RecordStandalone(ctx, e); err != nil {
		s.log.ErrorContext(ctx, "audit login failure", "error", err)
	}
}
