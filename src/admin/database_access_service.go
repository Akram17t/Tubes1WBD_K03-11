package admin

import (
	"context"
	"database/sql"
	"errors"

	"tubes-wbd/src/auth"
)

var (
	errInvalidDatabaseID      = errors.New("ID database tidak valid.")
	errInvalidAccessLevel     = errors.New("Access level harus view atau edit.")
	errAccessDatabaseNotFound = errors.New("Database tidak ditemukan.")
	errAccessDatabaseInactive = errors.New("Database nonaktif tidak dapat diberi atau diubah aksesnya.")
	errDatabaseAccessNotFound = errors.New("Pemberian akses tidak ditemukan.")
)

type databaseAccessPage struct {
	Group     Group
	Grants    []DatabaseGrant
	Databases []AccessDatabase
}

type databaseAccessService struct{ db *sql.DB }

func (s databaseAccessService) page(ctx context.Context, user *auth.User, groupID int64) (databaseAccessPage, error) {
	var page databaseAccessPage
	if err := auth.Authorize(ctx, s.db, user, auth.ManagePlatform, 0); err != nil {
		return page, err
	}
	if groupID <= 0 {
		return page, errInvalidGroupID
	}
	var err error
	if page.Group, err = (groupRepository{db: s.db}).find(ctx, groupID); err != nil {
		return page, err
	}
	repository := databaseAccessRepository{db: s.db}
	if page.Grants, err = repository.grants(ctx, groupID); err != nil {
		return page, err
	}
	page.Databases, err = repository.databases(ctx)
	return page, err
}

func (s databaseAccessService) save(ctx context.Context, user *auth.User, groupID, databaseID int64, level string) error {
	if err := auth.Authorize(ctx, s.db, user, auth.ManagePlatform, 0); err != nil {
		return err
	}
	if groupID <= 0 {
		return errInvalidGroupID
	}
	if databaseID <= 0 {
		return errInvalidDatabaseID
	}
	if level != "view" && level != "edit" {
		return errInvalidAccessLevel
	}
	return (groupService{db: s.db}).withGroupTransaction(ctx, groupID, func(_ groupRepository, tx *sql.Tx) error {
		repository := databaseAccessRepository{db: tx}
		if err := repository.lockDatabase(ctx, databaseID, true); err != nil {
			return err
		}
		previous, err := repository.currentLevel(ctx, groupID, databaseID)
		if err != nil || previous == level {
			return err
		}
		if err := repository.save(ctx, groupID, databaseID, level, user.ID); err != nil {
			return err
		}
		ids, err := repository.memberIDs(ctx, groupID)
		if err != nil {
			return err
		}
		if previous == "edit" && level == "view" {
			return auth.RevokeSessions(ctx, tx, ids)
		}
		return auth.BumpPrivilege(ctx, tx, ids)
	})
}

func (s databaseAccessService) revoke(ctx context.Context, user *auth.User, groupID, databaseID int64) error {
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
