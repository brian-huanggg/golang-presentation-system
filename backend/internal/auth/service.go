package auth

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

const (
	actionRegister  = "auth.register"
	targetUser      = "user"
	roleParticipant = "PARTICIPANT"
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

// Service holds the auth business rules. Construct it once in main.go.
type Service struct {
	db     *sqlx.DB
	gate   DeadlineGate
	hasher Hasher
	audit  Auditor
	log    *slog.Logger
}

func NewService(db *sqlx.DB, gate DeadlineGate, hasher Hasher, auditor Auditor, log *slog.Logger) *Service {
	return &Service{db: db, gate: gate, hasher: hasher, audit: auditor, log: log}
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
