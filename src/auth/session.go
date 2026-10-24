package auth

import (
	"context"
	"database/sql"
)

// naikin privilege_version user, nanti session-nya dirotasi di request berikutnya.
func BumpPrivilege(ctx context.Context, tx *sql.Tx, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE users
		SET privilege_version = privilege_version + 1, updated_at = now()
		WHERE id = ANY($1)
	`, userIDs)
	return err
}

// cabut semua session aktif milik user, mereka harus login ulang.
func RevokeSessions(ctx context.Context, tx *sql.Tx, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = now()
		WHERE user_id = ANY($1) AND revoked_at IS NULL
	`, userIDs)
	return err
}
