package admin

import (
	"errors"
	"net/http"

	"tubes-wbd/src/auth"
)

type Group struct {
	ID           int64
	Name         string
	Description  string
	LeaderUserID *int64
	IsArchived   bool
}

type GroupMember struct {
	ID          int64
	Username    string
	DisplayName string
	IsActive    bool
}

var (
	errInvalidGroupName    = errors.New("Nama group wajib diisi dan maksimal 100 karakter.")
	errGroupNameExists     = errors.New("Nama group sudah digunakan.")
	errInvalidGroupID      = errors.New("ID group atau user tidak valid.")
	errGroupNotFound       = errors.New("Group tidak ditemukan.")
	errGroupUserNotFound   = errors.New("User tidak ditemukan.")
	errGroupMemberNotFound = errors.New("User bukan anggota group ini.")
	errGroupMemberExists   = errors.New("User sudah menjadi anggota group ini.")
	errInvalidGroupLeader  = errors.New("Leader harus anggota group dengan akun aktif.")
)

// groupHTTPError memetakan error service ke status HTTP dan pesan aman.
func groupHTTPError(err error) (int, string) {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, "Akses ditolak."
	case errors.Is(err, errInvalidGroupName), errors.Is(err, errInvalidGroupID), errors.Is(err, errInvalidGroupLeader):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, errGroupNameExists), errors.Is(err, errGroupMemberExists):
		return http.StatusConflict, err.Error()
	case errors.Is(err, errGroupNotFound), errors.Is(err, errGroupUserNotFound), errors.Is(err, errGroupMemberNotFound):
		return http.StatusNotFound, err.Error()
	default:
		return http.StatusInternalServerError, "Terjadi kesalahan saat mengakses group."
	}
}
