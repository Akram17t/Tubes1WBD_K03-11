package admin

import (
	"context"
	"database/sql"
	"errors"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/telemetry"

	"go.opentelemetry.io/otel/attribute"
)

var (
	errInvalidDatabaseID      = errors.New("ID database tidak valid.")
	errInvalidAccessLevel     = errors.New("Access level harus view atau edit.")
	errAccessDatabaseNotFound = errors.New("Database tidak ditemukan.")
	errAccessDatabaseInactive = errors.New("Database nonaktif tidak dapat diberi atau diubah aksesnya.")
	errDatabaseAccessNotFound = errors.New("Pemberian akses tidak ditemukan.")
)

type databaseAccessService struct{ db *sql.DB }

// group memeriksa hak admin dan memastikan group masih dapat dikelola.
func (s databaseAccessService) group(ctx context.Context, user *auth.User, groupID int64) (Group, error) {
	return (groupService{db: s.db, store: groupRepository{db: s.db}}).find(ctx, user, groupID)
}

// listPage mengambil satu halaman grant milik group aktif.
func (s databaseAccessService) listPage(ctx context.Context, user *auth.User, groupID int64, limit, offset int) ([]DatabaseGrant, error) {
	if _, err := s.group(ctx, user, groupID); err != nil {
		return nil, err
	}
	return (databaseAccessRepository{db: s.db}).grantsPage(ctx, groupID, limit, offset)
}

// get membaca satu grant tanpa membuka koneksi target atau mengekspos credential-nya.
func (s databaseAccessService) get(ctx context.Context, user *auth.User, groupID, databaseID int64) (DatabaseGrant, error) {
	if _, err := s.group(ctx, user, groupID); err != nil {
		return DatabaseGrant{}, err
	}
	if databaseID <= 0 {
		return DatabaseGrant{}, errInvalidDatabaseID
	}
	return (databaseAccessRepository{db: s.db}).grant(ctx, groupID, databaseID)
}

// save mengubah grant dan dampak session secara atomic lalu mengembalikan grant tersimpan.
func (s databaseAccessService) save(ctx context.Context, user *auth.User, groupID, databaseID int64, level string) (saved DatabaseGrant, err error) {
	safeLevel := "invalid"
	if level == "view" || level == "edit" {
		safeLevel = level
	}

	if user != nil {
		ctx = telemetry.WithActor(ctx, user.ID)
	}
	ctx, finish := telemetry.StartActivity(ctx, "admin.group.access.save", attribute.Int64("group_id", groupID), attribute.Int64("database_id", databaseID), attribute.String("access_level", safeLevel))
	defer func() { finish(err) }()

	if err := auth.Authorize(ctx, s.db, user, auth.ManagePlatform, 0); err != nil {
		return DatabaseGrant{}, err
	}
	if groupID <= 0 {
		return DatabaseGrant{}, errInvalidGroupID
	}
	if databaseID <= 0 {
		return DatabaseGrant{}, errInvalidDatabaseID
	}
	if level != "view" && level != "edit" {
		return DatabaseGrant{}, errInvalidAccessLevel
	}
	var grant DatabaseGrant
	err = (groupService{db: s.db}).withGroupTransaction(ctx, groupID, func(_ groupRepository, tx *sql.Tx) error {
		repository := databaseAccessRepository{db: tx}
		if err := repository.lockDatabase(ctx, databaseID, true); err != nil {
			return err
		}
		previous, err := repository.currentLevel(ctx, groupID, databaseID)
		if err != nil {
			return err
		}
		// PUT berulang dengan level yang sama tidak menambah privilege atau mencabut session lagi.
		if previous != level {
			if err := repository.save(ctx, groupID, databaseID, level, user.ID); err != nil {
				return err
			}
			ids, err := repository.memberIDs(ctx, groupID)
			if err != nil {
				return err
			}
			if previous == "edit" && level == "view" {
				err = auth.RevokeSessions(ctx, tx, ids)
			} else {
				err = auth.BumpPrivilege(ctx, tx, ids)
			}
			if err != nil {
				return err
			}
		}
		// Baca hasil sebelum lock dilepas supaya response sesuai perubahan yang di-commit.
		grant, err = repository.grant(ctx, groupID, databaseID)
		return err
	})
	return grant, err
}

// revoke mencabut grant dan seluruh session anggota walaupun koneksi target sedang nonaktif.
func (s databaseAccessService) revoke(ctx context.Context, user *auth.User, groupID, databaseID int64) (err error) {
	if user != nil {
		ctx = telemetry.WithActor(ctx, user.ID)
	}
	ctx, finish := telemetry.StartActivity(ctx, "admin.group.access.revoke", attribute.Int64("group_id", groupID), attribute.Int64("database_id", databaseID))
	defer func() { finish(err) }()

	if err := auth.Authorize(ctx, s.db, user, auth.ManagePlatform, 0); err != nil {
		return err
	}
	if groupID <= 0 {
		return errInvalidGroupID
	}
	if databaseID <= 0 {
		return errInvalidDatabaseID
	}
	return (groupService{db: s.db}).withGroupTransaction(ctx, groupID, func(_ groupRepository, tx *sql.Tx) error {
		repository := databaseAccessRepository{db: tx}
		// Revocation remains possible for an inactive connection.
		if err := repository.lockDatabase(ctx, databaseID, false); err != nil {
			return err
		}
		if err := repository.revoke(ctx, groupID, databaseID); err != nil {
			return err
		}
		ids, err := repository.memberIDs(ctx, groupID)
		if err != nil {
			return err
		}
		return auth.RevokeSessions(ctx, tx, ids)
	})
}
