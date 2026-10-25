package database_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/database"
	testdb "tubes-wbd/test/database"
)

func TestConnectionServiceRejectsDuplicateTargetsAndPreservesPassword(t *testing.T) {
	f := testdb.New(t)
	service := f.Connections(t)
	admin := &auth.User{ID: 4, IsPlatformAdmin: true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	input := testdb.ConnectionInput(t, os.Getenv("TEST_TARGET_DATABASE_URL"))
	var encrypted string
	if err := f.Platform.QueryRow(`SELECT password_encrypted FROM database_connections WHERE id=1`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encrypted, "v1:") || strings.Contains(encrypted, input.Password) {
		t.Fatal("password tidak tersimpan terenkripsi")
	}
	pool, err := service.OpenTarget(ctx, f.Viewer, 1, auth.ViewData)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []*auth.User{f.Outsider, admin} {
		if _, err := service.OpenTarget(ctx, user, 1, auth.ViewData); !errors.Is(err, auth.ErrForbidden) {
			t.Fatalf("target dibuka tanpa grant: %v", err)
		}
	}
	input.Name = "Football renamed"
	input.Password = ""
	if err := service.Update(ctx, admin, 1, input); err != nil {
		t.Fatal(err)
	}
	var saved string
	if err := f.Platform.QueryRow(`SELECT password_encrypted FROM database_connections WHERE id=1`).Scan(&saved); err != nil || saved != encrypted {
		t.Fatal("update tanpa password mengubah kredensial")
	}
	reopened, err := service.Target(ctx, 1)
	if err != nil || reopened == pool {
		t.Fatalf("pool konfigurasi lama tidak diganti: %v", err)
	}
	input.Name, input.Password = "Duplicate", "different-password"
	input.Host = strings.ToUpper(input.Host)
	input.DBUsername = "another-user"
	if err := service.Create(ctx, admin, input); !errors.Is(err, database.ErrConnectionTargetExists) {
		t.Fatalf("target ganda diterima: %v", err)
	}
	if err := service.Update(ctx, admin, 2, input); !errors.Is(err, database.ErrConnectionTargetExists) {
		t.Fatalf("update ke target ganda diterima: %v", err)
	}
	r := f.Router(t)
	database.RegisterRoutes(r, f.Platform, service.Target)
	database.RegisterAdminRoutes(r, service)
	cookie := testdb.Login(t, r, "admin")
	page := testdb.Request(r, "GET", "/databases", nil, cookie)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "/admin/databases") {
		t.Fatal("navigasi pengelolaan koneksi tidak tersedia")
	}
	form := url.Values{"name": {input.Name}, "host": {input.Host}, "port": {strconv.Itoa(input.Port)}, "database_name": {input.DatabaseName}, "db_username": {input.DBUsername}, "ssl_mode": {input.SSLMode}, "password": {input.Password}, "csrf_token": {testdb.Token(t, page.Body.String())}}
	w := testdb.Request(r, "POST", "/admin/databases", form, cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), database.ErrConnectionTargetExists.Error()) || strings.Contains(w.Body.String(), input.Password) {
		t.Fatalf("respons target ganda tidak aman: %d %s", w.Code, w.Body.String())
	}
}

func TestConnectionServiceRejectsPlatformAsTarget(t *testing.T) {
	f := testdb.New(t)
	service := f.Connections(t)
	admin := &auth.User{ID: 4, IsPlatformAdmin: true}
	input := testdb.ConnectionInput(t, os.Getenv("TEST_DATABASE_URL"))
	for _, host := range []string{input.Host, "localhost"} {
		input.Host = host
		if err := service.Update(context.Background(), admin, 1, input); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		_, err := service.Target(ctx, 1)
		if !errors.Is(err, database.ErrInvalidConnection) || ctx.Err() != nil {
			cancel()
			t.Fatalf("database platform tidak ditolak: %v", err)
		}
		cancel()
	}
	r := f.Router(t)
	database.RegisterRoutes(r, f.Platform, service.Target)
	editor := testdb.Login(t, r, "editor")
	if w := testdb.Request(r, "GET", "/databases/1/schema", nil, editor); w.Code != 503 {
		t.Fatalf("viewer membuka database platform: %d", w.Code)
	}
}
