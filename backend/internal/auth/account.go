package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

const (
	fieldCurrentPassword  = "currentPassword"
	fieldNewPassword      = "newPassword"
	codePasswordIncorrect = "password_incorrect"
)

// ChangePassword changes the caller's password (REG-007) and ends every other session
// of that user (AUTH-007). It is open after the deadline (DDL-004, DDL-006), so it does
// not consult the DeadlineGate.
//
// A wrong current password counts toward the lockout (AUTH-002): an attacker holding a
// hijacked session could otherwise guess the password through this endpoint without
// limit. While the account is locked, even the right password gets the same answer and
// neither counts nor extends the lock, exactly as in Login.
func (s *Service) ChangePassword(ctx context.Context, current, newPassword, sourceIP string) error {
	row, err := s.sessionUser(ctx)
	if err != nil {
		return err
	}

	// Verified before the new password is judged, so the policy messages are not an
	// oracle for someone who does not know the current password.
	ok, err := s.hasher.Verify(ctx, current, row.PasswordHash)
	if err != nil {
		return fmt.Errorf("verify password for user %d: %w", row.ID, err)
	}
	if locked := row.LockedUntil.Valid && s.clock.Now().Before(row.LockedUntil.Time); locked || !ok {
		reason := "account_locked"
		if !locked {
			reason = "bad_password"
			wasLocked, err := s.recordFailedAttempt(ctx, row, sourceIP)
			if err != nil {
				return err
			}
			if wasLocked {
				reason = "account_locked"
			}
		}
		s.auditPasswordFailure(ctx, row, sourceIP, reason)
		return ErrValidation([]platform.FieldError{{Field: fieldCurrentPassword, Code: codePasswordIncorrect}})
	}

	if codes := checkPassword(newPassword); len(codes) > 0 {
		fields := make([]platform.FieldError, 0, len(codes))
		for _, c := range codes {
			fields = append(fields, platform.FieldError{Field: fieldNewPassword, Code: c})
		}
		return ErrValidation(fields)
	}

	// Hash before the transaction: it is slow and must not hold a DB connection.
	hash, err := s.hasher.Hash(ctx, newPassword)
	if err != nil {
		return err
	}

	err = platform.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
		if err := updatePassword(ctx, tx, row.ID, row.CredentialVersion, hash); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			ActorEmployeeID: row.EmployeeID, ActorRole: row.Role, Action: actionPassword,
			TargetType: targetUser, TargetID: row.EmployeeID,
			Result: audit.ResultSuccess, SourceIP: sourceIP,
			// AUD-006: never the password or its hash.
			Changes: map[string]any{
				"before": map[string]any{"mustChangePassword": row.MustChangePassword},
				"after":  map[string]any{"mustChangePassword": false},
			},
		})
	})
	switch {
	case errors.Is(err, errCredentialsChanged):
		return ErrCredentialsConflict()
	case err != nil:
		return err
	}

	// From here the change is committed and the version check (ADR 0018) already rejects
	// every older session on its next request. The caller's own session is moved to the
	// new version; its token is not renewed because that would also reset the absolute
	// 8 hour limit (AUTH-005).
	s.sessions.Put(ctx, keyCredVersion, row.CredentialVersion+1)
	s.invalidateOtherSessions(ctx, row.ID)
	return nil
}

// invalidateOtherSessions destroys the user's sessions except the caller's, so they
// vanish at once instead of at their next request (ADR 0007, ADR 0018). It is cleanup,
// not the guarantee: a failure is logged and does not fail the password change.
func (s *Service) invalidateOtherSessions(ctx context.Context, userID int64) {
	current := s.sessions.Token(ctx)
	destroyed := 0
	err := s.sessions.Iterate(ctx, func(sctx context.Context) error {
		if s.sessions.GetInt64(sctx, keyUserID) != userID || s.sessions.Token(sctx) == current {
			return nil
		}
		if err := s.sessions.Destroy(sctx); err != nil {
			return err
		}
		destroyed++
		return nil
	})
	if err != nil {
		s.log.ErrorContext(ctx, "invalidate other sessions after password change", "userID", userID, "destroyed", destroyed, "error", err)
	}
}

// auditPasswordFailure records a rejected change. A write failure is logged and does
// not change the answer.
func (s *Service) auditPasswordFailure(ctx context.Context, row userRow, sourceIP, reason string) {
	if err := s.audit.RecordStandalone(ctx, audit.Entry{
		ActorEmployeeID: row.EmployeeID, ActorRole: row.Role, Action: actionPassword,
		TargetType: targetUser, TargetID: row.EmployeeID,
		Result: audit.ResultFailure, SourceIP: sourceIP,
		Changes: map[string]any{"reason": reason},
	}); err != nil {
		s.log.ErrorContext(ctx, "audit password change failure", "error", err)
	}
}

// UpdatePreferences saves the preferred language (I18N-002). Not audited: AUD-001 does
// not list it and it is a high-frequency toggle.
func (s *Service) UpdatePreferences(ctx context.Context, language string) (SessionUser, error) {
	row, err := s.sessionUser(ctx)
	if err != nil {
		return SessionUser{}, err
	}
	if code := checkLanguage(language); code != "" {
		return SessionUser{}, ErrValidation([]platform.FieldError{{Field: "preferredLanguage", Code: code}})
	}
	if err := updatePreferredLanguage(ctx, s.db, row.ID, language); err != nil {
		return SessionUser{}, err
	}
	row.PreferredLanguage = language
	return toSessionUser(row), nil
}
