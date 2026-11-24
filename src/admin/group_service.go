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
	create(context.Context, string, string) (Group, error)
	find(context.Context, int64) (Group, error)
	update(context.Context, int64, string, string) error
	members(context.Context, int64) ([]GroupMember, error)
	availableUsers(context.Context, int64) ([]GroupMember, error)
}

type groupService struct {
	db    *sql.DB
	store groupStore
}

// authorize memastikan pengelola group memiliki hak Platform Admin.
func (s groupService) authorize(ctx context.Context, user *auth.User) error {
	return auth.Authorize(ctx, s.db, user, auth.ManagePlatform, 0)
}

// list memberikan daftar group aktif kepada admin yang berwenang.
func (s groupService) list(ctx context.Context, user *auth.User) ([]Group, error) {
	if err := s.authorize(ctx, user); err != nil {
		return nil, err
	}
	return s.store.list(ctx)
}

// create memvalidasi input admin sebelum menyimpan group baru.
func (s groupService) create(ctx context.Context, user *auth.User, name, description string) (Group, error) {
	if err := s.authorize(ctx, user); err != nil {
		return Group{}, err
	}
	name, description, err := groupInput(name, description)
	if err != nil {
		return Group{}, err
	}
	return s.store.create(ctx, name, description)
}

// edit menyiapkan group, anggota, dan pilihan user untuk halaman pengelolaan.
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

// update memvalidasi perubahan label group tanpa mengubah izin anggotanya.
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

// deleteGroup mengarsipkan group dan mencabut session seluruh anggotanya dalam satu transaction.
func (s groupService) deleteGroup(ctx context.Context, user *auth.User, id int64) error {
	if err := s.authorize(ctx, user); err != nil {
		return err
	}
	if id <= 0 {
		return errInvalidGroupID
	}
	return s.withGroupTransaction(ctx, id, func(repository groupRepository, tx *sql.Tx) error {
		// Ambil ID anggota sebelum relasinya dicabut agar session mereka ikut ditutup.
		members, err := repository.members(ctx, id)
		if err != nil {
			return err
		}
		if err := repository.archive(ctx, id); err != nil {
			return err
		}
		ids := make([]int64, 0, len(members))
		for _, member := range members {
			ids = append(ids, member.ID)
		}
		return auth.RevokeSessions(ctx, tx, ids)
	})
}

// addMember menambahkan anggota dan meminta rotasi session setelah privilege bertambah.
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

// removeMember mengeluarkan anggota dan mencabut session-nya secara atomic.
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

// withGroupTransaction menjalankan perubahan setelah mengunci group dan rollback bila gagal.
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

// setLeader mengganti leader, mencabut session leader lama, dan menaikkan privilege leader baru.
func (s groupService) setLeader(ctx context.Context, user *auth.User, groupID int64, leaderID *int64) error {
	if err := s.authorize(ctx, user); err != nil {
		return err
	}
	if groupID <= 0 || (leaderID != nil && *leaderID <= 0) {
		return errInvalidGroupID
	}
	return s.withGroupTransaction(ctx, groupID, func(repository groupRepository, tx *sql.Tx) error {
		group, err := repository.find(ctx, groupID)
		if err != nil {
			return err
		}
		return changeGroupLeader(ctx, repository, tx, group, leaderID)
	})
}

// changeGroupLeader menerapkan perubahan leader dan session dalam transaction caller yang memegang lock group.
func changeGroupLeader(ctx context.Context, repository groupRepository, tx *sql.Tx, group Group, leaderID *int64) error {
	if err := repository.setLeader(ctx, group.ID, leaderID); err != nil {
		return err
	}
	// Perubahan leader dan dampaknya pada session harus tersimpan secara atomic.
	if group.LeaderUserID != nil && (leaderID == nil || *group.LeaderUserID != *leaderID) {
		if err := auth.RevokeSessions(ctx, tx, []int64{*group.LeaderUserID}); err != nil {
			return err
		}
	}
	if leaderID != nil && (group.LeaderUserID == nil || *group.LeaderUserID != *leaderID) {
		return auth.BumpPrivilege(ctx, tx, []int64{*leaderID})
	}
	return nil
}

// groupInput merapikan teks dan memvalidasi panjang serta encoding nama group.
func groupInput(name, description string) (string, string, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 {
		return "", "", errInvalidGroupName
	}
	return name, description, nil
}
