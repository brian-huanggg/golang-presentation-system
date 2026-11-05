package event

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// 2026-12-31 23:59:59 in Taipei.
var deadline = time.Date(2026, 12, 31, 15, 59, 59, 0, time.UTC)

func TestCheck_Boundaries(t *testing.T) {
	p := NewPolicy(2026, deadline, platform.SystemClock{})
	tests := []struct {
		name    string
		now     time.Time
		allowed bool
	}{
		{"long before", deadline.Add(-30 * 24 * time.Hour), true},
		{"one second before", deadline.Add(-time.Second), true},
		{"exactly at deadline", deadline, true},
		{"one nanosecond after", deadline.Add(time.Nanosecond), false},
		{"day after", deadline.Add(24 * time.Hour), false},
		{"taipei midnight is after", time.Date(2027, 1, 1, 0, 0, 0, 0, Taipei), false},
		{"same instant in Taipei zone", deadline.In(Taipei), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Check(tc.now)
			if tc.allowed {
				assert.NoError(t, err)
				return
			}
			var prob *platform.Problem
			require.True(t, errors.As(err, &prob))
			assert.Equal(t, http.StatusForbidden, prob.Status)
			assert.Equal(t, CodeDeadlinePassed, prob.Code)
		})
	}
}

func TestCheckActor_AdminBypassAndClock(t *testing.T) {
	clk := platform.NewManualClock(deadline.Add(-time.Minute))
	p := NewPolicy(2026, deadline, clk)

	assert.NoError(t, p.CheckActor(false))
	clk.Advance(2 * time.Minute) // deadline passes mid-flow (DDL-005)
	assert.Error(t, p.CheckActor(false))
	assert.NoError(t, p.CheckActor(true), "admins are exempt (DDL-006)")
}

func TestAccessors(t *testing.T) {
	p := NewPolicy(2026, deadline, platform.SystemClock{})
	assert.Equal(t, 2026, p.Year())
	assert.Equal(t, "2026-12-31T23:59:59+08:00", p.Deadline().Format(time.RFC3339))
}

func TestNewPolicyFromConfig(t *testing.T) {
	c := platform.Config{EventYear: 2027, EventDeadline: deadline}
	p := NewPolicyFromConfig(c, platform.SystemClock{})
	assert.Equal(t, 2027, p.Year())
	assert.True(t, p.Deadline().Equal(deadline))
}
