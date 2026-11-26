package database_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/database"
	testdb "tubes-wbd/test/database"
)

// user biasa ga boleh buka atau ngubah koneksi database
func TestConnectionAdminRoutesRejectNonAdmin(t *testing.T) {
	f := testdb.New(t)
	service := f.Connections(t)
	r := f.Router(t)
	database.RegisterAdminRoutes(r, service)
	editor := testdb.Login(t, r, "editor")

	for _, path := range []string{"/api/admin/connections", "/api/admin/connections/1"} {
		if w := testdb.Request(r, http.MethodGet, path, nil, editor); w.Code != http.StatusForbidden {
			t.Errorf("GET %s oleh user biasa: dapat %d, harusnya 403", path, w.Code)
		}
	}
}

// uji koneksi dengan password salah gagal tanpa nampilin kredensial
func TestConnectionTestFailureHidesCredentials(t *testing.T) {
	f := testdb.New(t)
	service := f.Connections(t)
	admin := &auth.User{ID: 4, IsPlatformAdmin: true}

	input := testdb.ConnectionInput(t, os.Getenv("TEST_TARGET_DATABASE_URL"))
	input.Password = "password-salah-rahasia-77"
	if err := service.Update(context.Background(), admin, 1, input); err != nil {
		t.Fatal(err)
	}

	r := f.Router(t)
	database.RegisterAdminRoutes(r, service)
	cookie := testdb.Login(t, r, "admin")
	w := apiRequest(r, http.MethodPost, "/api/admin/connections/1/test", "", testdb.CSRF(t, r, cookie), cookie)
	if w.Code < 400 {
		t.Fatalf("uji koneksi password salah: dapat %d, harusnya gagal", w.Code)
	}
	if strings.Contains(w.Body.String(), input.Password) {
		t.Error("response uji koneksi ga boleh nampilin password")
	}
}

// koneksi nonaktif ga bisa dipakai dan session user yang punya akses dicabut
func TestInactiveConnectionIsRejected(t *testing.T) {
	f := testdb.New(t)
	service := f.Connections(t)
	admin := &auth.User{ID: 4, IsPlatformAdmin: true}
	ctx := context.Background()

	r := f.Router(t)
	database.RegisterAdminRoutes(r, service)
	testdb.Login(t, r, "editor")

	if err := service.SetActive(ctx, admin, 1, false); err != nil {
		t.Fatal(err)
	}

	if _, err := service.OpenTarget(ctx, f.Editor, 1, auth.ViewData); !errors.Is(err, database.ErrConnectionInactive) {
		t.Errorf("buka koneksi nonaktif: dapat %v, harusnya ErrConnectionInactive", err)
	}
	if err := service.Test(ctx, admin, 1); !errors.Is(err, database.ErrConnectionInactive) {
		t.Errorf("uji koneksi nonaktif: dapat %v, harusnya ErrConnectionInactive", err)
	}

	var active int
	f.Platform.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = 1 AND revoked_at IS NULL`).Scan(&active)
	if active != 0 {
		t.Error("nonaktifin koneksi harus nyabut session user yang punya akses")
	}

	if err := service.SetActive(ctx, admin, 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.OpenTarget(ctx, f.Editor, 1, auth.ViewData); err != nil {
		t.Errorf("koneksi yang diaktifin lagi harus bisa dibuka: %v", err)
	}
}
