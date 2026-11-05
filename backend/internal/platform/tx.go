package platform

import (
	"context"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// WithTx runs fn in one transaction. It commits when fn returns nil and rolls
// back on an error or a panic (the panic is re-raised). Features perform their
// change and write the audit row inside fn so both commit or roll back together
// (Architecture 5.3).
func WithTx(ctx context.Context, db *sqlx.DB, fn func(tx *sqlx.Tx) error) (err error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback() // the panic is the primary failure; do not mask it
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
