package team

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	teamLockSQL   = "SELECT 1 FROM team WHERE id = \\? AND deleted_at IS NULL FOR UPDATE"
	memberRoleSQL = "SELECT tm.role FROM team_member tm JOIN `user` u"
)

func TestIDEA_011_MembershipOfReportsTheCallersRole(t *testing.T) {
	cases := map[string]struct {
		role string
		want Membership
	}{
		"leader": {RoleLeader, Membership{Exists: true, Role: RoleLeader}},
		"member": {RoleMember, Membership{Exists: true, Role: RoleMember}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newParticipantFixture(t)
			f.mock.ExpectQuery(teamLockSQL).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
			f.mock.ExpectQuery(memberRoleSQL).WithArgs(int64(7), "100001").
				WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow(c.role))

			got, err := f.svc.MembershipOf(context.Background(), f.svc.db, 7, "100001")

			require.NoError(t, err)
			assert.Equal(t, c.want, got)
			require.NoError(t, f.mock.ExpectationsWereMet())
		})
	}
}

func TestIDEA_011_MembershipOfTellsAnOutsiderFromAMissingTeam(t *testing.T) {
	t.Run("team exists, person not on the roster", func(t *testing.T) {
		f := newParticipantFixture(t)
		f.mock.ExpectQuery(teamLockSQL).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		f.mock.ExpectQuery(memberRoleSQL).WillReturnRows(sqlmock.NewRows([]string{"role"}))

		got, err := f.svc.MembershipOf(context.Background(), f.svc.db, 7, "100009")

		require.NoError(t, err)
		assert.Equal(t, Membership{Exists: true}, got)
	})
	t.Run("unknown or dissolved team", func(t *testing.T) {
		f := newParticipantFixture(t)
		f.mock.ExpectQuery(teamLockSQL).WillReturnRows(sqlmock.NewRows([]string{"1"}))

		got, err := f.svc.MembershipOf(context.Background(), f.svc.db, 7, "100001")

		require.NoError(t, err)
		assert.Equal(t, Membership{}, got)
		require.NoError(t, f.mock.ExpectationsWereMet(), "no roster query for a team that is not there")
	})
}

func TestIDEA_011_MembershipOfPropagatesDatabaseErrors(t *testing.T) {
	t.Run("team read", func(t *testing.T) {
		f := newParticipantFixture(t)
		f.mock.ExpectQuery(teamLockSQL).WillReturnError(errors.New("db down"))
		_, err := f.svc.MembershipOf(context.Background(), f.svc.db, 7, "100001")
		require.Error(t, err)
	})
	t.Run("roster read", func(t *testing.T) {
		f := newParticipantFixture(t)
		f.mock.ExpectQuery(teamLockSQL).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		f.mock.ExpectQuery(memberRoleSQL).WillReturnError(errors.New("db down"))
		_, err := f.svc.MembershipOf(context.Background(), f.svc.db, 7, "100001")
		require.Error(t, err)
	})
}
