// Package event provides the event year and deadline policy (ADR 0014).
package event

import (
	"net/http"
	"time"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// CodeDeadlinePassed is the stable problem code the frontend translates.
const CodeDeadlinePassed = "deadline_passed"

// Taipei is the display zone for the deadline (DDL-001, I18N-004). Internal
// time stays UTC. A fixed +08:00 zone avoids depending on tzdata in the image;
// Taiwan has no DST.
var Taipei = time.FixedZone("Asia/Taipei", 8*60*60)

// ErrDeadlinePassed is returned when a participant write arrives after the deadline.
func ErrDeadlinePassed() *platform.Problem {
	return platform.NewProblem(http.StatusForbidden, CodeDeadlinePassed,
		"The event deadline has passed; this action is no longer allowed.")
}

// Policy holds the event year and deadline, read once from configuration, and
// answers "is this participant action allowed now?". It is immutable and safe
// for concurrent use.
type Policy struct {
	year     int
	deadline time.Time // UTC
	clock    platform.Clock
}

// NewPolicy builds a Policy. Time comes from clock so tests need no sleeps.
func NewPolicy(year int, deadline time.Time, clock platform.Clock) *Policy {
	return &Policy{year: year, deadline: deadline.UTC(), clock: clock}
}

// NewPolicyFromConfig builds a Policy from the validated startup config.
func NewPolicyFromConfig(c platform.Config, clock platform.Clock) *Policy {
	return NewPolicy(c.EventYear, c.EventDeadline, clock)
}

// Year is the current event year; every team query filters on it (EVT-01).
func (p *Policy) Year() int { return p.year }

// Deadline returns the deadline in Asia/Taipei for display (DDL-002).
func (p *Policy) Deadline() time.Time { return p.deadline.In(Taipei) }

// Check reports whether a participant write is allowed at now. An action
// completing exactly at the deadline is still allowed; anything later is
// rejected (DDL-003, DDL-005). It returns nil or ErrDeadlinePassed().
func (p *Policy) Check(now time.Time) error {
	if now.After(p.deadline) {
		return ErrDeadlinePassed()
	}
	return nil
}

// CheckActor applies Check with the policy's clock, skipping it for Admins
// (DDL-006). Call it when the action completes, and again inside the committing
// transaction for uploads (DDL-005).
func (p *Policy) CheckActor(isAdmin bool) error {
	if isAdmin {
		return nil
	}
	return p.Check(p.clock.Now())
}
