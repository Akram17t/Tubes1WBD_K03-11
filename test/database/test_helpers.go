package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"tubes-wbd/src/auth"
	appdb "tubes-wbd/src/database"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type Fixture struct {
	Platform, Target         *sql.DB
	Editor, Viewer, Outsider *auth.User
}

func New(t *testing.T) *Fixture {
	t.Helper()
	f := &Fixture{Platform: Open(t), Target: Target(t), Editor: &auth.User{ID: 1}, Viewer: &auth.User{ID: 2}, Outsider: &auth.User{ID: 3}}
	hash, err := auth.HashPassword("test-password")
	if err != nil {
		t.Fatal(err)
	}
	Exec(t, f.Platform, `INSERT INTO users (id,username,password_hash,display_name,is_platform_admin)
		VALUES (1,'editor',$1,'Editor',false),(2,'viewer',$1,'Viewer',false),
		(3,'outsider',$1,'Outsider',false),(4,'admin',$1,'Admin',true)`, hash)
	Exec(t, f.Platform, `
		INSERT INTO database_connections (id,name,host,database_name,db_username,password_encrypted,ssl_mode,created_by)
		VALUES (1,'Football','secret-host','secret-db','secret-user','secret-password','disable',1),
		(2,'Other','secret-host','secret-other-db','secret-user','secret-password','disable',1);
		SELECT setval(pg_get_serial_sequence('database_connections','id'),2,true);
		INSERT INTO groups (id,name) VALUES (1,'editors'),(2,'viewers');
		INSERT INTO user_groups (user_id,group_id) VALUES (1,1),(2,2);
		INSERT INTO group_database_access (group_id,database_id,access_level,granted_by)
		VALUES (1,1,'edit',1),(1,2,'edit',1),(2,1,'view',1);
	`)
	Exec(t, f.Target, `
		CREATE TABLE public.teams (id BIGINT PRIMARY KEY,name VARCHAR(100) NOT NULL UNIQUE);
		CREATE TABLE public.players (id BIGINT PRIMARY KEY,team_id BIGINT REFERENCES public.teams(id),
			name VARCHAR(100) NOT NULL,rating NUMERIC(3,1) CHECK(rating BETWEEN 0 AND 10));
		CREATE INDEX players_team_id_idx ON public.players(team_id);
		INSERT INTO public.teams VALUES (1,'Garuda'),(2,'Cakra');
		INSERT INTO public.players SELECT n,n%2+1,'Pemain '||n,CASE WHEN n%7=0 THEN NULL ELSE 7.5 END
		FROM generate_series(1,123) AS n;
	`)
	return f
}

func (f *Fixture) Provider(_ context.Context, id int64) (*sql.DB, error) {
	if id != 1 {
		return nil, errors.New("target tidak tersedia")
	}
	return f.Target, nil
}

func ConnectionInput(t *testing.T, dsn string) appdb.ConnectionInput {
	t.Helper()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	sslMode := u.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "prefer"
	}
	password := config.Password
	if password == "" {
		password = "integration-password"
	}
	return appdb.ConnectionInput{Name: "Football", Host: config.Host, Port: int(config.Port), DatabaseName: config.Database, DBUsername: config.User, SSLMode: sslMode, Password: password}
}

func (f *Fixture) Connections(t *testing.T) *appdb.ConnectionService {
	t.Helper()
	service, err := appdb.NewConnectionService(f.Platform, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	if err := service.Update(context.Background(), &auth.User{ID: 4, IsPlatformAdmin: true}, 1, ConnectionInput(t, os.Getenv("TEST_TARGET_DATABASE_URL"))); err != nil {
		t.Fatal(err)
	}
	return service
}

func (f *Fixture) Router(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.UseRawPath = true
	auth.RegisterRoutes(r, f.Platform, auth.SessionConfig{IdleTimeout: 30 * time.Minute, AbsoluteTimeout: 8 * time.Hour, RotationInterval: 15 * time.Minute, GracePeriod: 30 * time.Second})
	return r
}

// Login masuk lewat API JSON dan balikin cookie session-nya.
func Login(t *testing.T, r *gin.Engine, username string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest("POST", auth.LoginPath, strings.NewReader(`{"username":"`+username+`","password":"test-password"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login %s: %d", username, w.Code)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "session" {
			return cookie
		}
	}
	t.Fatal("cookie session tidak ditemukan")
	return nil
}

func Request(r *gin.Engine, method, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Origin", "http://example.com")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// CSRF ngambil token CSRF session ini dari /api/auth/me, sama kayak SPA.
func CSRF(t *testing.T, r *gin.Engine, cookie *http.Cookie) string {
	t.Helper()
	w := Request(r, "GET", "/api/auth/me", nil, cookie)
	var body struct {
		Data struct {
			CSRFToken string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Data.CSRFToken == "" {
		t.Fatalf("token CSRF tidak ditemukan: %d %s", w.Code, w.Body.String())
	}
	return body.Data.CSRFToken
}

func Exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
