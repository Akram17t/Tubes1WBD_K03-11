package admin

import (
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"

	"tubes-wbd/src/auth"
)

type groupStore interface {
	list(context.Context) ([]Group, error)
	create(context.Context, string, string) error
	find(context.Context, int64) (Group, error)
	update(context.Context, int64, string, string) error
	members(context.Context, int64) ([]GroupMember, error)
	availableUsers(context.Context, int64) ([]GroupMember, error)
}

type groupService struct {
	db    *sql.DB
	store groupStore
}

func (s groupService) authorize(ctx context.Context, user *auth.User) error {
	return auth.Authorize(ctx, s.db, user, auth.ManagePlatform, 0)
}

func (s groupService) list(ctx context.Context, user *auth.User) ([]Group, error) {
	if err := s.authorize(ctx, user); err != nil {
		return nil, err
	}
	return s.store.list(ctx)
}

func (s groupService) create(ctx context.Context, user *auth.User, name, description string) error {
	if err := s.authorize(ctx, user); err != nil {
		return err
	}
	name, description, err := groupInput(name, description)
	if err != nil {
		return err
	}
	return s.store.create(ctx, name, description)
}

func (s groupService) edit(ctx context.Context, user *auth.User, id int64) (groupEditData, error) {
	var data groupEditData
	if err := s.authorize(ctx, user); err != nil {
		return data, err
	}
	if id <= 0 {
		return data, errInvalidGroupID
	}
	var err error
	if data.Group, err = s.store.find(ctx, id); err != nil {
		return data, err
	}
	if data.Members, err = s.store.members(ctx, id); err != nil {
		return data, err
	}
	data.AvailableUsers, err = s.store.availableUsers(ctx, id)
	return data, err
}

func (s groupService) update(ctx context.Context, user *auth.User, id int64, name, description string) error {
	if err := s.authorize(ctx, user); err != nil {
		return err
	}
	if id <= 0 {
		return errInvalidGroupID
	}
	name, description, err := groupInput(name, description)
	if err != nil {
		return err
	}
	// Changing labels does not change permissions or revoke sessions.
	return s.store.update(ctx, id, name, description)
}

func (s groupService) deleteGroup(ctx context.Context, user *auth.User, id int64) error {
	if err := s.authorize(ctx, user); err != nil {
		return err
	}
	if id <= 0 {
		return errInvalidGroupID
	}
	return s.withGroupTransaction(ctx, id, func(repository groupRepository, tx *sql.Tx) error {
		members, err := repository.members(ctx, id)
		if err != nil {
			return err
		}
		if err := repository.deleteGroup(ctx, id); err != nil {
			return err
		}
		ids := make([]int64, 0, len(members))
		for _, member := range members {
			ids = append(ids, member.ID)
		}
		// FK CASCADE removes memberships and access grants in this transaction.
		return auth.RevokeSessions(ctx, tx, ids)
	})
}

func (s groupService) addMember(ctx context.Context, user *auth.User, groupID, userID int64) error {
	if err := s.authorize(ctx, user); err != nil {
		return err
	}
	if groupID <= 0 || userID <= 0 {
		return errInvalidGroupID
	}
	return s.withGroupTransaction(ctx, groupID, func(repository groupRepository, tx *sql.Tx) error {
		if err := repository.lockUser(ctx, userID); err != nil {
			return err
		}
		if err := repository.addMember(ctx, groupID, userID); err != nil {
			return err
		}
		return auth.BumpPrivilege(ctx, tx, []int64{userID})
	})
}

func (s groupService) removeMember(ctx context.Context, user *auth.User, groupID, userID int64) error {
	if err := s.authorize(ctx, user); err != nil {
		return err
	}
	if groupID <= 0 || userID <= 0 {
		return errInvalidGroupID
	}
	return s.withGroupTransaction(ctx, groupID, func(repository groupRepository, tx *sql.Tx) error {
		if err := repository.removeMember(ctx, groupID, userID); err != nil {
			return err
		}
		return auth.RevokeSessions(ctx, tx, []int64{userID})
	})
}

func (s groupService) withGroupTransaction(ctx context.Context, groupID int64, operation func(groupRepository, *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	repository := groupRepository{db: tx}
	if err := repository.lock(ctx, groupID); err != nil {
		return err
	}
	if err := operation(repository, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func groupInput(name, description string) (string, string, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 {
		return "", "", errInvalidGroupName
	}
	return name, description, nil
}
