package admin

import (
	"context"
	"database/sql"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/telemetry"

	"go.opentelemetry.io/otel/attribute"
)

type groupChanges struct {
	Name           *string
	Description    *string
	LeaderUserID   *int64
	SetDescription bool
	SetLeader      bool
}

// find mengambil satu group aktif setelah memeriksa hak pengelolaan platform.
func (s groupService) find(ctx context.Context, user *auth.User, id int64) (Group, error) {
	if err := s.authorize(ctx, user); err != nil {
		return Group{}, err
	}
	if id <= 0 {
		return Group{}, errInvalidGroupID
	}
	return s.store.find(ctx, id)
}

// listPage mengambil satu halaman group tanpa memuat seluruh daftar ke memory.
func (s groupService) listPage(ctx context.Context, user *auth.User, limit, offset int) ([]Group, error) {
	if err := s.authorize(ctx, user); err != nil {
		return nil, err
	}
	return (groupRepository{db: s.db}).listPage(ctx, limit, offset)
}

// membersPage membaca anggota beserta group untuk menentukan informasi leader pada response.
func (s groupService) membersPage(ctx context.Context, user *auth.User, id int64, limit, offset int) (Group, []GroupMember, error) {
	group, err := s.find(ctx, user, id)
	if err != nil {
		return Group{}, nil, err
	}
	members, err := (groupRepository{db: s.db}).membersPage(ctx, id, limit, offset)
	return group, members, err
}

// patch mengubah field yang dikirim tanpa menimpa field lain atau meninggalkan perubahan sebagian.
func (s groupService) patch(ctx context.Context, user *auth.User, id int64, changes groupChanges) (updated Group, err error) {
	event := "admin.group.update"
	attrs := []attribute.KeyValue{attribute.Int64("group_id", id), attribute.Bool("name_provided", changes.Name != nil), attribute.Bool("description_provided", changes.SetDescription)}
	if changes.SetLeader {
		event = "admin.group.leader.set"
		leader := int64(0)
		if changes.LeaderUserID != nil {
			leader = *changes.LeaderUserID
		}
		attrs = append(attrs, attribute.Int64("leader_user_id", leader), attribute.Bool("leader_cleared", changes.LeaderUserID == nil))
	}
	if user != nil {
		ctx = telemetry.WithActor(ctx, user.ID)
	}
	ctx, finish := telemetry.StartActivity(ctx, event, attrs...)
	defer func() { finish(err) }()

	if err := s.authorize(ctx, user); err != nil {
		return Group{}, err
	}
	if id <= 0 {
		return Group{}, errInvalidGroupID
	}
	var group Group
	err = s.withGroupTransaction(ctx, id, func(repository groupRepository, tx *sql.Tx) error {
		var err error
		group, err = repository.find(ctx, id)
		if err != nil {
			return err
		}
		// Field nullable dibedakan antara tidak dikirim dan sengaja dikosongkan.
		if changes.Name != nil {
			group.Name = *changes.Name
		}
		if changes.SetDescription {
			group.Description = ""
			if changes.Description != nil {
				group.Description = *changes.Description
			}
		}
		group.Name, group.Description, err = groupInput(group.Name, group.Description)
		if err != nil {
			return err
		}
		if changes.Name != nil || changes.SetDescription {
			if err := repository.update(ctx, id, group.Name, group.Description); err != nil {
				return err
			}
		}
		// Validasi leader, perubahan label, dan dampak session memakai transaction yang sama.
		if changes.SetLeader {
			if err := changeGroupLeader(ctx, repository, tx, group, changes.LeaderUserID); err != nil {
				return err
			}
		}
		group, err = repository.find(ctx, id)
		return err
	})
	return group, err
}
