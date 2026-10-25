package database

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
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
		(2,'Other','secret-host','secret-db','secret-user','secret-password','disable',1);
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

func (f *Fixture) Router(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.UseRawPath = true
	r.LoadHTMLGlob("../../templates/*.html")
	auth.RegisterRoutes(r, f.Platform, auth.SessionConfig{IdleTimeout: 30 * time.Minute, AbsoluteTimeout: 8 * time.Hour, RotationInterval: 15 * time.Minute, GracePeriod: 30 * time.Second})
	return r
}

func Login(t *testing.T, r *gin.Engine, username string) *http.Cookie {
	t.Helper()
	w := Request(r, "POST", "/login", url.Values{"username": {username}, "password": {"test-password"}}, nil)
	if w.Code != http.StatusSeeOther {
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

var csrfPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func Token(t *testing.T, body string) string {
	t.Helper()
	match := csrfPattern.FindStringSubmatch(body)
	if match == nil {
		t.Fatal("token CSRF tidak ditemukan")
	}
	return match[1]
}

func Exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
