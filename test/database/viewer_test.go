package database_test

import (
	"context"
	"database/sql"
	"errors"
	"html"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"tubes-wbd/src/database"
	testdb "tubes-wbd/test/database"
)

func TestViewerMetadataAndPagination(t *testing.T) {
	f := testdb.New(t)
	ctx := context.Background()
	columns, err := database.GetColumns(ctx, f.Target, "public", "players")
	if err != nil {
		t.Fatal(err)
	}
	if columns[3].DataType != "numeric(3,1)" || !columns[3].Nullable {
		t.Fatalf("tipe/nullability salah: %+v", columns[3])
	}
	constraints, err := database.GetConstraints(ctx, f.Target, "public", "players")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, constraint := range constraints {
		kinds[constraint.Type] = true
	}
	if !kinds["PRIMARY KEY"] || !kinds["FOREIGN KEY"] || !kinds["CHECK"] {
		t.Fatalf("constraint tidak lengkap: %+v", constraints)
	}
	indexes, err := database.GetIndexes(ctx, f.Target, "public", "players")
	if err != nil || len(indexes) != 2 {
		t.Fatalf("index: %+v, %v", indexes, err)
	}
	seen := map[int64]bool{}
	for offset := 0; offset < 123; offset += 50 {
		page, err := database.GetTableData(ctx, f.Target, "public", "players", 50, offset)
		if err != nil || len(page.Rows) != min(50, 123-offset) {
			t.Fatalf("page %d: %+v, %v", offset, page, err)
		}
		for _, row := range page.Rows {
			id := row[0].(int64)
			if seen[id] {
				t.Fatal("baris pagination berulang")
			}
			seen[id] = true
		}
	}
	for _, page := range [][2]int{{0, 0}, {501, 0}, {50, -1}} {
		_, err := database.GetTableData(ctx, f.Target, "public", "players", page[0], page[1])
		if !errors.Is(err, database.ErrInvalidPagination) {
			t.Fatalf("pagination tidak valid diterima: %v", err)
		}
	}
	_, err = database.GetColumns(ctx, f.Target, "public", "players; DROP TABLE teams")
	if !errors.Is(err, database.ErrTableNotFound) {
		t.Fatalf("identifier tidak valid: %v", err)
	}
	testdb.Exec(t, f.Target, `CREATE TABLE public._dbplatform_migrations (id integer)`)
	tables, err := database.GetTables(ctx, f.Target, "public")
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if table == "_dbplatform_migrations" {
			t.Fatal("tabel tracking terlihat")
		}
	}
	_, err = database.GetTableData(ctx, f.Target, "public", "_dbplatform_migrations", 50, 0)
	if !errors.Is(err, database.ErrTableNotFound) {
		t.Fatal("tracking bisa dibuka langsung")
	}
}

func TestViewerRoutesPermissionsAndEscapedIdentifiers(t *testing.T) {
	f := testdb.New(t)
	calls := 0
	provider := func(ctx context.Context, id int64) (*sql.DB, error) {
		calls++
		return f.Provider(ctx, id)
	}
	r := f.Router(t)
	database.RegisterRoutes(r, f.Platform, provider)
	viewer := testdb.Login(t, r, "viewer")
	for _, username := range []string{"outsider", "admin"} {
		cookie := testdb.Login(t, r, username)
		if w := testdb.Request(r, "GET", "/databases/1/schema", nil, cookie); w.Code != 403 {
			t.Fatalf("%s: dapat %d", username, w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("target dibuka sebelum cek izin")
	}
	if w := testdb.Request(r, "GET", "/databases/1/schema", nil, nil); w.Code != 303 {
		t.Fatal("viewer tanpa login harus diarahkan ke login")
	}
	for _, path := range []string{"/databases/1", "/databases/1/schema?table=players", "/databases/1/tables/players/data?limit=50&offset=50"} {
		w := testdb.Request(r, "GET", path, nil, viewer)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Logout") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/databases/0/schema", "/databases/abc/schema", "/databases/1/tables/players/data?limit=no", "/databases/1/tables/players/data?limit=501", "/databases/1/tables/players/data?offset=-1"} {
		if w := testdb.Request(r, "GET", path, nil, viewer); w.Code != 400 {
			t.Fatalf("%s: harus 400, dapat %d", path, w.Code)
		}
	}
	for _, path := range []string{"/databases/1/schema?schema=missing", "/databases/1/tables/missing/data"} {
		if w := testdb.Request(r, "GET", path, nil, viewer); w.Code != 404 {
			t.Fatalf("%s: harus 404, dapat %d", path, w.Code)
		}
	}
	testdb.Exec(t, f.Target, `CREATE TABLE public."odd/table""name" (value text); INSERT INTO public."odd/table""name" VALUES ('<script>alert(1)</script>')`)
	path := "/databases/1/tables/" + url.PathEscape("odd/table\"name") + "/data"
	w := testdb.Request(r, "GET", path, nil, viewer)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>alert") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatalf("nama tabel ter-escape: %d %s", w.Code, w.Body.String())
	}
	testdb.Exec(t, f.Target, `DROP SCHEMA IF EXISTS empty_schema; CREATE SCHEMA empty_schema`)
	w = testdb.Request(r, "GET", "/databases/1/schema?schema=empty_schema", nil, viewer)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Tidak ada tabel") {
		t.Fatal("schema kosong tidak ditangani")
	}
	r2 := f.Router(t)
	database.RegisterRoutes(r2, f.Platform, nil)
	cookie := testdb.Login(t, r2, "viewer")
	if w := testdb.Request(r2, "GET", "/databases/1/schema", nil, cookie); w.Code != 503 {
		t.Fatalf("provider belum ada: %d", w.Code)
	}
}

func TestViewerJSONAndGeneratedTableLinks(t *testing.T) {
	f := testdb.New(t)
	ctx := context.Background()
	name := "a/b+c d%?"
	testdb.Exec(t, f.Target, `CREATE TABLE public."a/b+c d%?" (id bigint PRIMARY KEY, payload jsonb, original json, binary_data bytea);
		INSERT INTO public."a/b+c d%?" VALUES (1,'{"a":1}','{"b":"<script>alert(1)</script>"}',decode('00ff','hex')),(2,NULL,NULL,NULL);
		INSERT INTO public."a/b+c d%?" (id,payload) SELECT n,'{}' FROM generate_series(3,51) n`)
	data, err := database.GetTableData(ctx, f.Target, "public", name, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if data.Rows[0][1] != `{"a": 1}` || data.Rows[0][2] != `{"b":"<script>alert(1)</script>"}` || data.Rows[0][3] != `\x00ff` || data.Rows[1][1] != nil {
		t.Fatalf("nilai JSON/binary/NULL berubah: %+v", data.Rows[:2])
	}
	r := f.Router(t)
	database.RegisterRoutes(r, f.Platform, f.Provider)
	cookie := testdb.Login(t, r, "viewer")
	page := testdb.Request(r, "GET", "/databases/1/schema?"+url.Values{"table": {name}}.Encode(), nil, cookie)
	if page.Code != 200 {
		t.Fatalf("schema: %d %s", page.Code, page.Body.String())
	}
	for _, label := range []string{"Lihat data", "Berikutnya"} {
		link := regexp.MustCompile(`href="([^"]+)"[^>]*>` + label + `</a>`).FindStringSubmatch(page.Body.String())
		if link == nil {
			t.Fatalf("tautan %s tidak ditemukan", label)
		}
		page = testdb.Request(r, "GET", html.UnescapeString(link[1]), nil, cookie)
		if page.Code != 200 {
			t.Fatalf("tautan %s rusak: %d %s", label, page.Code, page.Body.String())
		}
		if label == "Lihat data" && (!strings.Contains(html.UnescapeString(page.Body.String()), `{"a": 1}`) || !strings.Contains(page.Body.String(), "&lt;script&gt;") || strings.Contains(page.Body.String(), "<script>alert")) {
			t.Fatal("JSON tidak ditampilkan sebagai teks yang aman")
		}
	}
}
