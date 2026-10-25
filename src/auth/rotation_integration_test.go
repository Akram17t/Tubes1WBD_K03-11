package auth

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"testing"
	"time"
)

// hitung token current (belum masuk grace) di semua session
func currentTokenCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	db.QueryRow(`SELECT count(*) FROM session_tokens WHERE grace_expires_at IS NULL`).Scan(&count)
	return count
}

// mundurin waktu rotasi terakhir biar session dianggap sudah waktunya dirotasi
func makeRotationDue(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `UPDATE sessions SET last_rotated_at = now() - interval '16 minutes'`)
}

// bikin grace semua token lama sudah habis (issued_at ikut dimundurin biar lolos CHECK)
func expireGrace(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `
		UPDATE session_tokens
		SET issued_at = now() - interval '1 minute', grace_expires_at = now() - interval '1 second'
		WHERE grace_expires_at IS NOT NULL
	`)
}

// request biasa sebelum 15 menit ga dapat cookie baru
func TestNoRotationBeforeInterval(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")
	cookie := loginAs(t, r, "editor", "rahasia123")

	w := send(r, http.MethodGet, "/databases", nil, cookie)
	if w.Code != http.StatusOK || sessionCookieFrom(w) != nil {
		t.Errorf("belum waktunya rotasi: dapat %d, cookie baru %v", w.Code, sessionCookieFrom(w))
	}
}

// rotasi berkala ngasih token baru, absolute expiration tetap,
// token lama jalan selama grace lalu ditolak
func TestPeriodicRotation(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")
	oldCookie := loginAs(t, r, "editor", "rahasia123")

	var absoluteBefore, absoluteAfter time.Time
	db.QueryRow(`SELECT absolute_expires_at FROM sessions`).Scan(&absoluteBefore)

	makeRotationDue(t, db)
	w := send(r, http.MethodGet, "/databases", nil, oldCookie)
	newCookie := sessionCookieFrom(w)
	if w.Code != http.StatusOK || newCookie == nil || newCookie.Value == oldCookie.Value {
		t.Fatalf("rotasi: dapat %d, harusnya 200 dengan cookie baru", w.Code)
	}

	db.QueryRow(`SELECT absolute_expires_at FROM sessions`).Scan(&absoluteAfter)
	if !absoluteAfter.Equal(absoluteBefore) {
		t.Errorf("absolute expiration berubah: %v jadi %v", absoluteBefore, absoluteAfter)
	}
	if currentTokenCount(t, db) != 1 {
		t.Errorf("harusnya cuma 1 token current, dapat %d", currentTokenCount(t, db))
	}

	// token lama masih dalam grace: diterima, dan ga bikin rotasi lagi
	w = send(r, http.MethodGet, "/databases", nil, oldCookie)
	if w.Code != http.StatusOK || sessionCookieFrom(w) != nil {
		t.Errorf("token lama dalam grace: dapat %d, harusnya 200 tanpa cookie baru", w.Code)
	}

	// grace habis: token lama ditolak, token baru tetap jalan
	expireGrace(t, db)
	if w := send(r, http.MethodGet, "/databases", nil, oldCookie); w.Code != http.StatusSeeOther {
		t.Errorf("token lama setelah grace: dapat %d, harusnya 303 ke /login", w.Code)
	}
	if w := send(r, http.MethodGet, "/databases", nil, newCookie); w.Code != http.StatusOK {
		t.Errorf("token baru: dapat %d, harusnya 200", w.Code)
	}
}

// rotasi berikutnya ngebuang token yang grace-nya sudah habis
func TestRotationCleansExpiredTokens(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")
	cookie := loginAs(t, r, "editor", "rahasia123")

	for i := 0; i < 3; i++ {
		makeRotationDue(t, db)
		cookie = sessionCookieFrom(send(r, http.MethodGet, "/databases", nil, cookie))
		if cookie == nil {
			t.Fatalf("rotasi ke-%d ga ngasih cookie baru", i+1)
		}
		expireGrace(t, db)
	}

	var total int
	db.QueryRow(`SELECT count(*) FROM session_tokens`).Scan(&total)
	if total > 2 {
		t.Errorf("token mati harusnya dibuang, masih ada %d baris", total)
	}
}

// privilege naik bikin token dirotasi di request berikutnya
func TestRotationOnPrivilegeChange(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	userID := insertUserWithPassword(t, db, "editor", "rahasia123")
	cookie := loginAs(t, r, "editor", "rahasia123")

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := BumpPrivilege(ctx, tx, []int64{userID}); err != nil {
		t.Fatal(err)
	}
	tx.Commit()

	w := send(r, http.MethodGet, "/databases", nil, cookie)
	if w.Code != http.StatusOK || sessionCookieFrom(w) == nil {
		t.Fatalf("privilege naik: dapat %d, harusnya 200 dengan cookie baru", w.Code)
	}

	var sessionVersion int
	db.QueryRow(`SELECT privilege_version FROM sessions`).Scan(&sessionVersion)
	if sessionVersion != 1 {
		t.Errorf("privilege_version session harusnya disamain jadi 1, dapat %d", sessionVersion)
	}
}

// banyak request barengan pakai token yang sama cuma boleh bikin satu token current
func TestConcurrentRotation(t *testing.T) {
	db := openTestDB(t)
	r := newAuthRouter(db)
	insertUserWithPassword(t, db, "editor", "rahasia123")
	cookie := loginAs(t, r, "editor", "rahasia123")
	makeRotationDue(t, db)

	const requests = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	newCookies := 0
	failed := 0

	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := send(r, http.MethodGet, "/databases", nil, cookie)
			mu.Lock()
			defer mu.Unlock()
			if w.Code != http.StatusOK {
				failed++
			}
			if sessionCookieFrom(w) != nil {
				newCookies++
			}
		}()
	}
	wg.Wait()

	if failed != 0 {
		t.Errorf("%d request gagal, harusnya semua 200", failed)
	}
	if newCookies != 1 {
		t.Errorf("harusnya cuma 1 response yang ngasih cookie baru, dapat %d", newCookies)
	}
	if currentTokenCount(t, db) != 1 {
		t.Errorf("harusnya cuma 1 token current, dapat %d", currentTokenCount(t, db))
	}
}
