# Tugas Besar IF3110 Pengembangan Aplikasi Berbasis Web: Milestone 2

Pada tugas besar ini, kami diminta untuk membuat Database Management Platform berbasis web yang mengatur akses pengguna ke database PostgreSQL bersama. Pengguna tidak diberi kredensial database secara langsung. Setiap pengguna harus login ke platform, lalu platform yang menentukan apa saja yang boleh dilihat atau diubah oleh pengguna tersebut.

Platform Admin mengelola user, group, koneksi database target, dan pemberian akses. Akses ke sebuah database selalu diberikan kepada group, tidak pernah langsung kepada user, dengan dua tingkatan: **View**, yang memungkinkan melihat schema, data tabel, dan riwayat migration, serta **Edit**, yang juga memungkinkan membuat, mengubah, dan menjalankan migration. Jika seorang user tergabung dalam beberapa group, yang berlaku adalah tingkat akses tertinggi. Perubahan schema dan data hanya dilakukan melalui migration yang memiliki SQL up dan SQL down.

Pada Milestone 2, menjalankan migration selalu melalui request yang harus disetujui leader setiap group yang punya akses ke database tersebut. Setelah disetujui, migration masuk antrean dan dijalankan oleh proses worker terpisah, sehingga request HTTP tidak menunggu SQL selesai. Migration besar dapat dijalankan per batch agar lock tabel singkat, dilanjutkan (resume) jika berhenti di tengah, atau dikompensasi. Setiap percobaan eksekusi tercatat di history, dan rollback maupun forward mengikuti urutan linear.

## Arsitektur

```
Browser (SPA React) --/api JSON--> Server API (Go, APP_ROLE=api) --+--> PostgreSQL platform
                                                                    |    (user, group, request, antrean job, history)
                                    Worker (Go, APP_ROLE=worker) ---+
                                         |
                                         +--> PostgreSQL target (database yang dikelola)
```

- **SPA** di `frontend/` hanya berbicara dengan API JSON dan tidak pernah terhubung langsung ke PostgreSQL. Hasil build-nya disajikan oleh server API.
- **Server API** menangani login, session, CSRF, administrasi, viewer, serta pengajuan dan approval migration. Eksekusi hanya dimasukkan ke antrean.
- **Antrean** berupa tabel di database platform yang di-claim worker dengan `FOR UPDATE SKIP LOCKED`, tanpa broker tambahan.
- **Worker** mengambil job, mengunci database target, memeriksa ulang aturan dan izin, lalu menjalankan SQL dan mencatat hasil serta progress per batch. Satu database dijalankan serial, database berbeda dapat berjalan paralel.

## Prerequisites

- Go 1.26
- Node.js 22 (untuk build SPA)
- Docker (untuk menjalankan PostgreSQL 16)
- Git

## Setup

Jalankan semua perintah dari root repository. Setiap langkah memiliki versi Linux/Mac (bash) dan versi Windows (PowerShell).

### 1. Clone repository

```sh
git clone https://github.com/Akram17t/Tubes1WBD_K03-11.git
cd Tubes1WBD_K03-11
```

### 2. Jalankan PostgreSQL dan buat database

Linux/Mac:

```sh
docker run -d --name dbplatform-pg -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:16
until docker exec dbplatform-pg pg_isready -U postgres; do sleep 1; done
docker exec dbplatform-pg psql -U postgres -c "CREATE DATABASE platform" -c "CREATE DATABASE target_db"
```

Windows (PowerShell):

```powershell
docker run -d --name dbplatform-pg -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:16
do { Start-Sleep 1; docker exec dbplatform-pg pg_isready -U postgres } until ($LASTEXITCODE -eq 0)
docker exec dbplatform-pg psql -U postgres -c "CREATE DATABASE platform" -c "CREATE DATABASE target_db"
```

`platform` adalah database milik aplikasi (user, group, session, migration). `target_db` adalah database contoh yang akan dikelola melalui platform.

### 3. Isi schema dan data demo

Linux/Mac:

```sh
for f in sql/0*.sql sql/seed.sql; do docker exec -i dbplatform-pg psql -q -v ON_ERROR_STOP=1 -U postgres -d platform < "$f"; done
for f in sql/demo/init.sql sql/demo/seed.sql; do docker exec -i dbplatform-pg psql -q -v ON_ERROR_STOP=1 -U postgres -d target_db < "$f"; done
```

Windows (PowerShell):

```powershell
foreach ($f in (Get-ChildItem sql\0*.sql) + (Get-Item sql\seed.sql)) {
    Get-Content -Raw $f | docker exec -i dbplatform-pg psql -q -v ON_ERROR_STOP=1 -U postgres -d platform
}
foreach ($f in "sql\demo\init.sql", "sql\demo\seed.sql") {
    Get-Content -Raw $f | docker exec -i dbplatform-pg psql -q -v ON_ERROR_STOP=1 -U postgres -d target_db
}
```

Perulangan pertama membuat tabel platform serta user dan group demo. Perulangan kedua mengisi `target_db` dengan data fiktif berupa klub sepak bola, pemain, pertandingan, dan penampilan pemain.

### 4. Buat file `.env`

Salin file contoh:

```sh
cp .env.example .env              # Linux/Mac
Copy-Item .env.example .env       # Windows (PowerShell)
```

Lalu buka `.env` dan ubah nilai berikut:

1. **`CONNECTION_ENCRYPTION_KEY`**: kunci acak 32 byte dalam format base64, dipakai untuk mengenkripsi password koneksi yang disimpan. Buat di komputer sendiri, lalu tempel hasilnya (sekitar 44 karakter, berakhiran `=`):

   ```sh
   openssl rand -base64 32
   ```

   ```powershell
   $key = New-Object byte[] 32
   [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($key)
   [Convert]::ToBase64String($key)
   ```

   Jangan ubah kunci ini setelah membuat koneksi, karena password koneksi yang sudah tersimpan tidak akan bisa dibuka lagi.

2. **`ADMIN_PASSWORD`**: ganti `ganti-password-ini` dengan password milik sendiri untuk Platform Admin pertama. `ADMIN_USERNAME` bawaannya `admin`.

   ```
   ADMIN_USERNAME=admin
   ADMIN_PASSWORD=password-milik-sendiri
   ```

   Akun admin hanya dibuat sekali, yaitu saat aplikasi pertama kali dijalankan. Mengubah `ADMIN_PASSWORD` setelahnya tidak berpengaruh; ganti password melalui halaman **User**.

Nilai lainnya (`PLATFORM_DATABASE_URL`, pengaturan session, dan pengaturan worker) sudah sesuai dengan setup Docker di atas. Variabel yang dapat diatur:

| Variabel | Default | Kegunaan |
| :-- | :-- | :-- |
| `APP_ROLE` | `api` | Peran proses: `api` atau `worker` |
| `WORKER_CONCURRENCY` | `2` | Jumlah job yang dikerjakan paralel oleh satu worker |
| `WORKER_POLL_INTERVAL` | `1s` | Jeda worker memeriksa antrean |
| `WORKER_HEARTBEAT_INTERVAL` | `5s` | Interval worker menandai job masih dikerjakan |
| `JOB_STALE_AFTER` | `30s` | Batas heartbeat basi sebelum recovery memeriksa job |
| `BATCH_SIZE_DEFAULT` / `BATCH_SIZE_MAX` | `1000` / `10000` | Ukuran batch bawaan dan maksimum |
| `LOG_LEVEL` | `info` | Level application log (`debug`, `info`, `warn`, `error`) |
| `TRACE_EXPORTER` | `console` | `console` menulis span ke stderr, `none` mematikannya |

### Upgrade dari Milestone 1

Database platform milik Milestone 1 dapat dipakai tanpa dibuat ulang. Jalankan file schema yang belum pernah dijalankan (`011` sampai `015`) sekali saja; data user, group, koneksi, dan riwayat migration lama tetap ada:

```sh
for f in sql/011_*.sql sql/012_*.sql sql/013_*.sql sql/014_*.sql sql/015_*.sql; do docker exec -i dbplatform-pg psql -q -v ON_ERROR_STOP=1 -U postgres -d platform < "$f"; done
```

### 5. Jalankan aplikasi

Tampilan aplikasi berupa SPA React di folder `frontend/`. Build dulu sekali (ulangi setiap ada perubahan frontend), lalu jalankan server API dan worker migration di dua terminal:

```sh
cd frontend && npm ci && npm run build && cd ..
APP_ROLE=api go run ./src
APP_ROLE=worker go run ./src   # terminal kedua
```

Server API menyajikan hasil build dari `frontend/dist`. Setelah terminal menampilkan `http.server_started`, buka http://localhost:8080 lalu login sebagai `admin` dengan password dari `.env`.

Saat mengembangkan frontend, jalankan `npm run dev` di folder `frontend` lalu buka http://localhost:5173. Vite meneruskan request `/api` ke server Go di port 8080.

### 6. Sambungkan database contoh

Koneksi contoh tidak dimasukkan ke seed karena password-nya harus dienkripsi dengan kunci masing-masing. Sebagai admin:

1. Buka **Koneksi**, klik **Tambah koneksi**, lalu isi: nama `target-db`, host `localhost`, port `5432`, database `target_db`, username `postgres`, password `postgres`, SSL mode `disable`.
2. Klik **Uji koneksi** pada koneksi yang baru dibuat untuk memeriksanya.
3. Buka **Group**, pilih **analyst**, lalu di bagian **Akses database** pilih `target-db` dengan tingkat `view` dan klik **Beri akses**.
4. Lakukan hal yang sama untuk **developer** dengan tingkat `edit`.

User demo sekarang dapat login dengan password `demo12345`:

| Username | Group                 | Akses ke target-db                    |
| :------- | :-------------------- | :------------------------------------ |
| viewer   | analyst               | View                                  |
| editor   | developer             | Edit                                  |
| multi    | analyst dan developer | Edit (tingkat tertinggi yang berlaku) |
| outsider | tidak ada             | Tidak ada                             |

Satu browser hanya dapat login sebagai satu user dalam satu waktu, karena semua tab berbagi cookie session yang sama. Untuk memakai dua akun sekaligus, gunakan jendela biasa dan jendela incognito, atau dua browser yang berbeda.

## Documentation

Tampilan aplikasi berupa SPA React yang berkomunikasi dengan server Go lewat API JSON. Setiap halaman memakai layout responsif dan menampilkan keadaan loading, kosong, error, dan akses ditolak. Tombol yang mengubah data dinonaktifkan selama request berjalan supaya tidak terkirim dua kali.

![Tampilan di layar HP](screenshots/mobile.png)

### Login

![Login](screenshots/login.png)

Halaman login berisi input username dan password. Username tidak membedakan huruf besar dan kecil. Jika kredensial benar, session baru dibuat dan pengguna diarahkan ke daftar database. Password salah, username yang tidak terdaftar, dan akun yang dinonaktifkan semuanya menampilkan pesan error yang sama, sehingga halaman tidak membocorkan akun mana saja yang ada. Setiap login menerbitkan token session baru dan mencabut session sebelumnya di browser tersebut.

### Daftar Database

![Daftar database](screenshots/databases.png)

Halaman ini hanya menampilkan database aktif yang dapat diakses pengguna melalui group-nya, beserta tingkat aksesnya. Database tanpa pemberian akses tidak muncul, dan membukanya langsung lewat URL menghasilkan pesan akses ditolak.

### Struktur Database

![Struktur database](screenshots/schema.png)

Setiap database punya empat tab: Struktur, Migration, Request, dan History. Tab Struktur menampilkan daftar schema dan tabel pada database target. Jika database sedang ditahan karena migration yang berhenti di tengah, alasannya ditampilkan di atas tab.

### Struktur Tabel

![Struktur tabel](screenshots/table.png)

Untuk setiap tabel ditampilkan kolom beserta tipe PostgreSQL dan status nullable, constraint (primary key, foreign key, unique, check), serta index. Nama schema dan tabel divalidasi terhadap database target sebelum query dibuat.

### Data Tabel

![Data tabel](screenshots/data.png)

Isi tabel ditampilkan dengan pagination. Ukuran halaman dibatasi di server, sehingga sebuah request tidak bisa memuat seluruh isi tabel besar sekaligus.

### Daftar Migration

![Daftar migration](screenshots/migrations.png)

Migration adalah definisi berisi SQL up dan SQL down. Daftar ini menampilkan mode (normal atau batch), penanda irreversible dan terkunci, serta posisi migration: belum pernah diterapkan, aktif, sudah di-rollback, atau tergantikan. Posisi dihitung dari riwayat eksekusi yang sukses. Tombol buat migration hanya muncul untuk pengguna dengan akses Edit, dan backend tetap memeriksa izin pada setiap request.

### Buat dan Edit Migration

![Buat migration](screenshots/migration-new.png)

Pengguna dengan akses Edit menulis judul, deskripsi (opsional), SQL up, dan SQL down. SQL divalidasi dengan parser PostgreSQL sebelum disimpan. Mode batch memecah perubahan per kelompok ID agar lock tabel singkat; SQL-nya harus berbentuk `UPDATE schema.tabel ... WHERE kolom_key = ANY($1::bigint[])`. Migration yang ditandai irreversible wajib menyertakan alasan dan tidak dapat di-rollback. Definisi dapat diubah selama belum dipakai request; setelah itu definisi dikunci dan perbaikan dibuat sebagai migration baru. Jika definisi sudah diubah orang lain, penyimpanan ditolak supaya perubahan tidak saling menimpa.

### Detail Migration

![Detail migration](screenshots/migration-detail.png)

Halaman detail menampilkan definisi dan tombol aksi yang berlaku untuk posisi migration saat ini: ajukan up untuk migration yang belum diterapkan, ajukan rollback untuk migration aktif teratas, atau ajukan forward untuk menerapkan ulang migration yang baru di-rollback. Rollback dan forward mengikuti urutan linear, jadi hanya migration teratas yang dapat dibatalkan.

### Daftar Request

![Daftar request](screenshots/requests.png)

Menjalankan migration selalu melalui request. Request menyimpan snapshot definisi saat diajukan, sehingga yang di-review leader sama persis dengan yang dijalankan.

### Review dan Approval

![Detail request](screenshots/request-detail.png)

Setelah request di-submit, leader setiap group yang punya akses ke database tersebut memberikan approval atas nama group-nya. Halaman ini menampilkan snapshot SQL yang akan dijalankan dan status approval per group. Request baru dapat dieksekusi setelah semua group menyetujui, dan pengaju menekan tombol eksekusi secara terpisah.

### Job dan Progress

![Job sukses](screenshots/job.png)

Eksekusi berjalan di proses worker terpisah sebagai job. Halaman job memperbarui statusnya otomatis setiap dua detik selama job masih antre atau berjalan, dan berhenti memperbarui saat tab tidak terlihat. Progress dihitung dari row dan batch yang benar-benar sudah di-commit, bukan dari timer.

![Job gagal](screenshots/job-failed.png)

Jika SQL gagal, pesan error dan SQLSTATE ditampilkan tanpa membocorkan kredensial koneksi. Migration batch yang berhenti setelah sebagian batch di-commit berstatus "berhenti di tengah" dan menahan migration lain pada database tersebut sampai job dilanjutkan (resume) atau dikompensasi. Kompensasi menjalankan SQL down hanya untuk batch yang sudah di-commit, dan seperti request lain harus di-review terlebih dahulu. Status yang belum pasti tidak pernah ditampilkan sebagai gagal, dan aplikasi tidak pernah menyarankan menjalankan SQL secara manual.

### History

![History](screenshots/history.png)

History mencatat setiap percobaan eksekusi, termasuk yang gagal dan retry, beserta pelaksana dan waktunya. Opsi Hide rollback menyembunyikan pasangan eksekusi sukses yang sudah di-rollback beserta rollback-nya; kegagalan tetap ditampilkan.

### Detail Eksekusi

![Detail eksekusi](screenshots/execution.png)

Detail eksekusi menampilkan SQL yang benar-benar dijalankan, error jika ada, relasi ke eksekusi yang dibatalkan, dan untuk mode batch, catatan setiap batch berupa cakupan ID, jumlah row, status, dan SQL-nya.

### Administrasi User

![Administrasi user](screenshots/users.png)

Platform Admin dapat melihat seluruh akun beserta peran dan statusnya, membuat user baru, serta mengaktifkan atau menonaktifkan akun. Menonaktifkan akun langsung mencabut seluruh session milik akun tersebut dan melepas jabatan leader group yang dipegangnya.

### Buat dan Edit User

![Edit user](screenshots/user-form.png)

Admin dapat mengatur username, nama tampilan, password, dan status Platform Admin. Password disimpan sebagai hash Argon2id dan harus terdiri dari 8 sampai 72 karakter. Username yang sudah dipakai ditolak. Mengganti password atau mencabut status admin akan mencabut session user tersebut, sedangkan menjadikan user sebagai admin akan merotasi session-nya pada request berikutnya. Admin tidak dapat menonaktifkan akunnya sendiri atau mencabut status adminnya sendiri.

### Daftar Group

![Daftar group](screenshots/groups.png)

Daftar group berisi tautan ke halaman detail setiap group.

### Detail Group, Anggota, dan Akses Database

![Detail group](screenshots/group-detail.png)

Di halaman ini admin mengganti nama dan deskripsi group, menambah atau mengeluarkan anggota, memilih leader, dan mengarsipkan group. Leader harus anggota aktif group tersebut, dan dialah yang memberi approval migration atas nama group. Menambah anggota menaikkan privilege version anggota tersebut sehingga session-nya dirotasi, sedangkan mengeluarkan anggota atau mengarsipkan group langsung mencabut session pengguna yang terdampak.

Bagian akses database memberikan akses View atau Edit ke database aktif, mengubah tingkatnya, atau mencabutnya. Memberikan atau menaikkan akses akan merotasi session anggota, sedangkan menurunkan atau mencabut akses akan mencabut session mereka.

### Daftar Koneksi Database

![Daftar koneksi database](screenshots/connections.png)

Admin mengelola koneksi PostgreSQL yang dapat dibuka oleh platform. Menonaktifkan koneksi memblokir seluruh akses ke koneksi tersebut dan mencabut session pengguna yang memiliki akses melaluinya.

### Buat dan Edit Koneksi

![Edit koneksi](screenshots/connection-form.png)

Sebuah koneksi menyimpan host, port, nama database, username, password, dan SSL mode. Password dienkripsi dengan AES-256-GCM sebelum disimpan dan tidak pernah ditampilkan lagi; mengosongkannya saat edit berarti password lama tetap dipakai. Admin dapat menguji koneksi yang tersimpan, dan pengujian yang gagal tidak pernah membocorkan kredensial. Target yang sama tidak dapat didaftarkan dua kali, dan database milik platform sendiri tidak dapat dijadikan target.

### Session dan Keamanan

| Pengaturan       | Default  | Variabel env                |
| :--------------- | :------- | :-------------------------- |
| Idle timeout     | 30 menit | `SESSION_IDLE_TIMEOUT`      |
| Absolute timeout | 8 jam    | `SESSION_ABSOLUTE_TIMEOUT`  |
| Interval rotasi  | 15 menit | `SESSION_ROTATION_INTERVAL` |
| Grace period     | 30 detik | `SESSION_GRACE_PERIOD`      |

Session disimpan di server, sedangkan browser hanya menyimpan token acak di cookie yang bersifat HttpOnly dan SameSite=Lax (serta Secure di production). Database hanya menyimpan hash SHA-256 dari setiap token. Token dirotasi saat login, setiap interval rotasi, dan setiap kali hak akses pengguna meningkat. Token lama tetap berlaku selama grace period untuk menangani request yang berjalan bersamaan, dan rotasi tidak pernah memperpanjang absolute timeout. Aplikasi menolak berjalan jika interval rotasi tidak lebih pendek dari idle timeout, atau grace period tidak lebih pendek dari interval rotasi. Setiap request API yang mengubah data membawa token CSRF di header `X-CSRF-Token`, dan login dilindungi dengan pemeriksaan header Origin.

### Pemulihan Job dan Lock

Selama job berjalan, worker memegang advisory lock dengan key `database_id` pada dua koneksi khusus, satu di database platform dan satu di database target, serta memperbarui `heartbeat_at` setiap `WORKER_HEARTBEAT_INTERVAL`. Setiap update state job memakai `worker_token` sebagai fencing, sehingga worker lama yang sudah diambil alih tidak dapat lagi menulis hasil.

Jika worker mati atau koneksinya putus, pemulihan berjalan otomatis oleh worker yang masih hidup:

1. Job `running` dengan heartbeat lebih tua dari `JOB_STALE_AFTER`, atau job `outcome_unknown` yang sudah jatuh tempo, hanya menjadi **kandidat** pemulihan.
2. Worker mencoba mengambil advisory lock platform untuk database tersebut. Lock **tidak pernah direbut berdasarkan waktu**. Selama session proses lama masih hidup, lock tetap dipegangnya dan job dibiarkan. PostgreSQL melepas advisory lock otomatis saat session pemiliknya berakhir.
3. Setelah lock didapat, job diambil alih dengan `worker_token` baru. Jika belum ada execution `running`, SQL belum pernah dikirim, jadi job dikembalikan ke antrean tanpa efek apa pun.
4. Jika execution `running` ada, worker mengambil lock target lalu membaca ledger target (`_dbplatform_migrations` dan `_dbplatform_batch_ledger`). Marker ditemukan berarti transaction sudah commit, sehingga execution dicatat sukses tanpa menjalankan SQL lagi. Untuk batch, progress diambil dari batch yang tercatat di ledger, lalu job dilanjutkan dari batch berikutnya. Tanpa marker, attempt dicatat terputus dan job di-retry sesuai batas attempt.
5. Jika target belum dapat dihubungi atau lock target masih dipegang proses lain, status tetap `outcome_unknown` dan database tertahan sampai bukti dapat dibaca. Status ini tidak pernah ditampilkan sebagai gagal.

Jika job tertahan lama, periksa apakah lock masih dipegang session lain:

```sql
-- Jalankan di database platform dan di database target; ganti 1 dengan database_id.
SELECT l.pid, l.granted, a.application_name, a.state, a.backend_start, a.query
FROM pg_locks l JOIN pg_stat_activity a USING (pid)
WHERE l.locktype = 'advisory' AND l.classid = 0 AND l.objid = 1 AND l.objsubid = 1;
```

Jika pemegang lock adalah worker yang sudah tidak merespons (misalnya proses digantung, bukan sedang menjalankan SQL panjang), hentikan proses worker tersebut. Jika session-nya tetap tersisa, akhiri dengan `SELECT pg_terminate_backend(<pid>)`. Transaction yang belum commit akan di-rollback oleh PostgreSQL, lalu worker lain memulihkan job secara otomatis melalui ledger. Jangan pernah menghapus baris ledger `_dbplatform_*` atau mengubah status job secara manual, karena keduanya menjadi satu-satunya bukti apakah SQL sudah commit.

Jika perubahan perlu dibatalkan tetapi SQL down tidak aman (migration irreversible, atau down akan menghapus data), lakukan perbaikan terkontrol lewat migration baru: buat definisi baru berisi SQL perbaikan yang sudah ditinjau, ajukan, minta approval semua leader, lalu eksekusi seperti biasa. Dengan cara ini perbaikan tetap tercatat di history dan ledger, tanpa SQL yang dijalankan langsung di luar platform.

### Logging dan Tracing

Kedua proses menulis application log berformat JSON ke stdout dan span OpenTelemetry ke stderr. Setiap request API mendapat `request_id`, dan seluruh query SQL (platform maupun target, termasuk query read-only) dicatat dengan parameter yang disamarkan. Saat request di-execute, trace context W3C disimpan bersama job, lalu worker melanjutkannya, sehingga satu `trace_id` menghubungkan request HTTP sampai commit setiap batch di worker. Password, cookie, token session, token CSRF, dan DSN tidak pernah ditulis ke log.

Contoh dari satu job migration batch (200 row, 2 batch). Log API saat tombol eksekusi ditekan:

```json
{"time":"2026-11-26T20:17:24.192405488+07:00","level":"INFO","event":"workflow.execute","database_id":1,"request_entry_id":1,"status":"queued","request_id":"http-727a19ff6c3f74b7","actor_id":"2","trace_id":"d091c6993164202b79e49721497a03f5","span_id":"33329ee664694544"}
{"time":"2026-11-26T20:17:24.19070503+07:00","level":"INFO","event":"job.enqueue_staged","job_id":1,"request_entry_id":1,"database_id":1,"migration_id":1,"revision":1,"operation":"up","execution_actor_id":2,"status":"queued","request_id":"http-727a19ff6c3f74b7","actor_id":"2","trace_id":"d091c6993164202b79e49721497a03f5","span_id":"7da9230e790546d4"}
```

Log worker untuk job yang sama, dengan `trace_id` yang sama:

```json
{"time":"2026-11-26T20:17:25.194421279+07:00","level":"INFO","event":"job.claimed","job_id":1,"request_entry_id":1,"database_id":1,"migration_id":1,"revision":1,"operation":"up","attempt":0,"worker_id":"fedora-75760-96856287d318","status":"running","queue_wait_ms":1027,"actor_id":"2","trace_id":"d091c6993164202b79e49721497a03f5","span_id":"af03c29f82fd43cd"}
{"time":"2026-11-26T20:17:26.992494099+07:00","level":"INFO","event":"sql.query","query":"UPDATE public.players SET squad_number = squad_number WHERE id = ANY($1::bigint[])","params":["$1:[]int64"],"duration_ms":2.023,"rows":100,"status":"ok","db":"target","actor_id":"2","trace_id":"d091c6993164202b79e49721497a03f5","span_id":"c8daf20e920f533c"}
{"time":"2026-11-26T20:17:26.994557154+07:00","level":"INFO","event":"execution.batch_committed","job_id":1,"request_entry_id":1,"database_id":1,"migration_id":1,"revision":1,"operation":"up","attempt":1,"execution_id":1,"worker_id":"fedora-75760-96856287d318","batch_number":1,"direction":"up","rows":100,"actor_id":"2","trace_id":"d091c6993164202b79e49721497a03f5","span_id":"40fa11112d494a5a"}
{"time":"2026-11-26T20:17:27.023054518+07:00","level":"INFO","event":"job.attempt_finished","job_id":1,"request_entry_id":1,"database_id":1,"migration_id":1,"revision":1,"operation":"up","attempt":1,"worker_id":"fedora-75760-96856287d318","status":"success","error_code":"","actor_id":"2","trace_id":"d091c6993164202b79e49721497a03f5","span_id":"9933721f0cde5bbe"}
```

Susunan span dalam trace tersebut (span `sql.query` di setiap langkah tidak ditampilkan):

```
POST /api/databases/:database_id/requests/:request_id/execute   [api]
└─ workflow.execute                                    25.8 ms  [api]
   └─ workflow.enqueue                                  8.5 ms  [api]
      ├─ queue.wait                                  1027.7 ms  [worker]
      └─ worker.job                                  1828.9 ms  [worker]
         ├─ worker.lock_wait                           17.1 ms
         ├─ worker.revalidate                        1723.9 ms
         ├─ worker.execute                             78.5 ms
         │  ├─ execution.batch (batch 1)                7.7 ms
         │  │  └─ execution.commit                      0.7 ms
         │  ├─ execution.batch (batch 2)                5.7 ms
         │  │  └─ execution.commit                      0.7 ms
         │  └─ execution.commit                         0.5 ms
         └─ worker.finalize                             7.0 ms
```

Span `execution.batch` untuk batch pertama, dengan `SpanID` yang sama seperti `span_id` pada log `execution.batch_committed` di atas:

```json
{
  "Name": "execution.batch",
  "SpanContext": { "TraceID": "d091c6993164202b79e49721497a03f5", "SpanID": "40fa11112d494a5a" },
  "Parent": { "TraceID": "d091c6993164202b79e49721497a03f5", "SpanID": "fd3e4881b1a5e38f" },
  "StartTime": "2026-11-26T20:17:26.986818308+07:00",
  "EndTime": "2026-11-26T20:17:26.994543879+07:00",
  "Attributes": [
    { "Key": "job_id", "Value": { "Type": "INT64", "Value": 1 } },
    { "Key": "execution_id", "Value": { "Type": "INT64", "Value": 1 } },
    { "Key": "database_id", "Value": { "Type": "INT64", "Value": 1 } },
    { "Key": "attempt", "Value": { "Type": "INT64", "Value": 1 } },
    { "Key": "operation", "Value": { "Type": "STRING", "Value": "up" } },
    { "Key": "batch_number", "Value": { "Type": "INT64", "Value": 1 } },
    { "Key": "direction", "Value": { "Type": "STRING", "Value": "up" } },
    { "Key": "ids", "Value": { "Type": "INT64", "Value": 100 } }
  ],
  "Status": { "Code": "Unset", "Description": "" }
}
```

Untuk menyimpan log dan span secara terpisah saat menjalankan aplikasi: `APP_ROLE=worker go run ./src > worker.log 2> worker-span.json`.

## Testing

Integration test membutuhkan dua database khusus test yang namanya mengandung kata `test`. Test mengosongkan data setiap kali dijalankan, jadi jangan pernah mengarahkannya ke database demo.

Linux/Mac:

```sh
docker exec dbplatform-pg psql -U postgres -c "CREATE DATABASE platform_test" -c "CREATE DATABASE target_test"
for f in sql/0*.sql; do docker exec -i dbplatform-pg psql -q -v ON_ERROR_STOP=1 -U postgres -d platform_test < "$f"; done

TEST_DATABASE_URL="postgres://postgres:postgres@localhost:5432/platform_test?sslmode=disable" \
TEST_TARGET_DATABASE_URL="postgres://postgres:postgres@localhost:5432/target_test?sslmode=disable" \
go test -count=1 ./...
```

Windows (PowerShell):

```powershell
docker exec dbplatform-pg psql -U postgres -c "CREATE DATABASE platform_test" -c "CREATE DATABASE target_test"
foreach ($f in Get-ChildItem sql\0*.sql) {
    Get-Content -Raw $f | docker exec -i dbplatform-pg psql -q -v ON_ERROR_STOP=1 -U postgres -d platform_test
}

$env:TEST_DATABASE_URL = "postgres://postgres:postgres@localhost:5432/platform_test?sslmode=disable"
$env:TEST_TARGET_DATABASE_URL = "postgres://postgres:postgres@localhost:5432/target_test?sslmode=disable"
go test -count=1 ./...
```

Tanpa kedua variabel tersebut, integration test akan dilewati.

Test frontend (fungsi bantu SPA dan mock API untuk development) dijalankan terpisah:

```sh
cd frontend && npm ci && npm test
```

### Benchmark dataset dan migration batch

Pengujian 26 November 2026 memakai dataset `public.batch_demo` dengan ID 1–1.000.000 dan nilai awal nol. UPDATE biasa diukur terlebih dahulu, kemudian dataset yang sama direset ke nol dan diproses melalui API → approval dua leader → queue → worker, dengan batch size 10.000.

| Pengukuran | Hasil |
| --- | --- |
| Jumlah row | 1.000.000 |
| UPDATE seluruh row tanpa batch | 3.979,943 ms (>1 detik) |
| Waktu HTTP enqueue batch | 94 ms |
| Waktu wall dari enqueue sampai verifikasi | 25,516 detik |
| Batch committed | 100 |
| Verifikasi target | 1.000.000 row tepat bernilai 1, ID tetap 1–1.000.000 |

[Bukti benchmark batch](docs/benchmarks/batch-benchmark.json) menyimpan pengukuran normal, response job, sampel progress, dan verifikasi target. [Baseline normal terdahulu](docs/benchmarks/normal-2026-11-26.json) tetap tersedia terpisah. Pengukuran batch ini memakai Windows 11, i7-13650HX, RAM terlihat sekitar 12 GB, SSD SN740, PostgreSQL 18.6, dan Go 1.26.0, dengan console tracing/log info aktif. Persiapan fixture lain berjalan pada mesin yang sama; hasil ini bukan perbandingan performa yang terisolasi. Waktu wall juga mencakup polling/verifikasi. Batch lebih lambat pada workload ini karena scope persisten, banyak transaction, ledger, dan instrumentasi; manfaat yang dibuktikan adalah eksekusi asynchronous serta progress, bukan bahwa batching selalu lebih cepat.

Untuk mengulang, buat database khusus `wbd_batch_benchmark` dan set `BENCHMARK_DATABASE_URL` ke database itu. Generator membatasi nama database tersebut dan membangun ulang tabel `public.batch_demo`; jangan menyimpan data lain di tabel itu.

```powershell
$env:BENCHMARK_DATABASE_URL='postgres://postgres:postgres@localhost:5432/wbd_batch_benchmark?sslmode=disable'
go run ./scripts/benchmark -action generate -rows 1000000
go run ./scripts/benchmark -action run
go run ./scripts/benchmark -action reset
```

Perintah `run` memverifikasi jumlah row dan gagal jika UPDATE tidak melebihi satu detik. Setelah `reset`, sambungkan database tersebut lewat aplikasi, beri akses kedua group, dan buat migration mode batch: schema `public`, tabel `batch_demo`, key `id`, size `10000`. SQL up/down:

```sql
UPDATE public.batch_demo SET value=value+1 WHERE id=ANY($1::bigint[]);
-- SQL down
UPDATE public.batch_demo SET value=value-1 WHERE id=ANY($1::bigint[]);
```

Submit, approve kedua leader, execute, dan pantau job sampai sukses. Periksa target dengan `SELECT count(*), count(*) FILTER (WHERE value=1), min(id), max(id) FROM public.batch_demo`; hasilnya harus `1000000, 1000000, 1, 1000000`.

### Bonus: stress testing k6

[scripts/k6/stress.js](scripts/k6/stress.js) menyiapkan migration UPDATE baru pada dua target secara bergantian, lalu submit dan meminta approval editor serta viewer sebagai leader dua group. Persiapan selesai sebelum pengukuran beban. Setiap pengguna virtual membaca daftar database/history, enqueue request approved, mengirim enqueue duplikat untuk memeriksa job ID yang sama, lalu polling sampai worker selesai. Banyak pengguna virtual berjalan bersamaan, sehingga API dan job diuji pada database yang sama maupun berbeda. HTTP 202 belum dihitung sebagai keberhasilan migration; job baru dihitung sukses setelah backend melaporkan `success`.

Baseline pengujian ditetapkan 2 virtual user (VU). Beban naik 2 → 8 → 24 VU, kemudian turun ke 2 VU untuk mengamati pemulihan. Tiap fase berjalan 20 detik, dengan jeda drain 15 detik antar-fase dan think time 500 ms per iterasi. Threshold k6: API p95 <1.000 ms, error API <1%, job success >99%, seluruh check lolos, dan setiap fase menyelesaikan job. Respons HTTP yang tidak sesuai atau job yang tidak selesai sukses membuat check gagal.

Gunakan dua database target khusus test dan jalankan sekali SQL berikut pada masing-masing. Delay 100 ms sengaja ditambahkan agar antrean worker dapat diamati; workload ini terpisah dari benchmark 1 juta row.

```sql
CREATE TABLE public.load_counter (id BIGINT PRIMARY KEY, value BIGINT NOT NULL);
INSERT INTO public.load_counter VALUES (1,0);
CREATE FUNCTION public.load_delay() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_sleep(0.1); RETURN NEW; END $$;
CREATE TRIGGER load_delay BEFORE UPDATE ON public.load_counter
FOR EACH ROW EXECUTE FUNCTION public.load_delay();
```

Daftarkan kedua target lewat aplikasi dan beri analyst akses View serta developer akses Edit. Gunakan akun seed editor/viewer sebagai leader, lalu nyalakan API dan worker seperti setup di atas. Install k6 dan jalankan dari root repo:

```sh
k6 run -e K6_BASE_URL=http://localhost:8080 -e K6_DATABASE_IDS=2,3 scripts/k6/stress.js
```

Ganti URL dan ID sesuai target test; kedua ID harus berbeda. Password akun seed default `demo12345`, dapat diganti lewat `K6_PASSWORD` jika kedua akun memakai password lain yang sama. Setiap run menambah migration/history dan menaikkan counter target; data tidak dihapus otomatis. Ringkasan tiap fase ditulis ke terminal.

Run terakhir pada 26 November 2026 memakai backend `31ccad6`, lingkungan Windows/PostgreSQL/Go seperti benchmark di atas, dan k6 2.4.0. API, worker, database, dan k6 berbagi mesin. Worker concurrency 2, polling 100 ms, `LOG_LEVEL=warn`, dan `TRACE_EXPORTER=none`. Setup/approval serta login dikecualikan dari metric API. Total waktu termasuk setup sekitar 192,97 detik; empat fase selesai dengan 338 iterasi dan nol iterasi terputus.

| Fase | VU | API p50/p95 (ms) | Request/s | Error API | Job sukses | Job/s | Queue p95 (ms) | Durasi job p95 (ms) |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Normal | 2 | 5,94 / 70,34 | 15,85 | 0% | 35 | 1,75 | 130,20 | 1.370,40 |
| Elevated | 8 | 4,44 / 15,12 | 67,98 | 0% | 132 | 6,35 | 844,90 | 405,50 |
| Peak | 24 | 4,32 / 114,95 | 174,71 | 0% | 128 | 5,41 | 6.229,95 | 803,70 |
| Recovery | 2 | 5,53 / 60,66 | 15,85 | 0% | 43 | 2,15 | 84,70 | 585,60 |

K6 berakhir dengan exit code 0; seluruh threshold lolos dan 14.445 check berhasil tanpa kegagalan. Verifikasi database yang dibatasi pada run ini menemukan 338 job sukses, masing-masing dengan tepat satu execution sukses. Counter dua target naik 170 dan 168, total 338, sesuai jumlah job tanpa UPDATE duplikat.

Pada normal, queue p95 sekitar 130 ms. Di elevated, laju penyelesaian naik ke 6,35 job/s tetapi antrean bertambah. Di peak, request mencapai 174,71/s, queue p95 naik ke 6,23 detik, dan penyelesaian turun menjadi 5,41 job/s; API p95 tetap di bawah threshold. Ini menunjukkan penumpukan pekerjaan pada konfigurasi pengujian tersebut. Di recovery, sesudah beban turun dan diberi waktu drain, queue p95 kembali 84,70 ms. Variasi mesin bersama dapat memengaruhi hasil, sehingga angka ini belum menetapkan kapasitas maksimum atau stabilitas produksi jangka panjang.

Queue wait dihitung dari `queued_at` ke claim pertama (`started_at`); durasi job dari `started_at` ke `finished_at`, termasuk kemungkinan tunggu lock/finalisasi. Throughput memakai waktu fase termasuk penyelesaian iterasi terakhir, bukan durasi seluruh run seperti rate submetric bawaan k6. Request/s mencakup polling. Model pengguna yang menunggu job lalu tidur 500 ms membatasi offered load ketika job melambat. Pemulihan diamati setelah jeda drain, bukan dari perpindahan beban tanpa jeda; overhead console tracing/observability terpusat juga belum diukur saat k6.

### Bonus: SAST dengan CodeQL

CodeQL menganalisis kode tanpa menjalankan aplikasi. Kode diubah menjadi database analisis, lalu query keamanan menelusuri aliran data dari input pengguna (source) ke titik berbahaya (sink), misalnya query SQL, path file, respons HTTP, atau log.

**Konfigurasi.** Scan berjalan di GitHub Actions lewat [.github/workflows/codeql.yml](.github/workflows/codeql.yml) pada setiap push dan pull request ke `main`, setiap Senin, serta manual (`workflow_dispatch`). Query yang dipakai adalah suite `security-extended`, yang menambah query keamanan berpresisi menengah di atas suite default ([.github/codeql/codeql-config.yml](.github/codeql/codeql-config.yml)). Hasil muncul di tab *Security → Code scanning*, dan salinan SARIF setiap run disimpan sebagai artifact `codeql-sarif-<bahasa>`.

**Cakupan.**

| Bahasa | Mode build | Kode yang dianalisis |
| --- | --- | --- |
| Go | manual (`go build ./...`) | `src/` (API, worker, migration engine, auth, admin, viewer, telemetry), `scripts/benchmark`, dan `test/` |
| JavaScript/TypeScript | none | SPA React di `frontend/src` dan `frontend/mock`. `frontend/dist` (hasil build) dan `node_modules` dikecualikan |

SQL di folder `sql/` tidak dianalisis karena CodeQL tidak mendukung PL/pgSQL. Hasil bersih juga tidak berarti aplikasi pasti aman: CodeQL hanya menemukan pola yang dimodelkan query-nya, misalnya tidak memeriksa logika otorisasi per endpoint, yang diuji lewat integration test.

**Hasil scan pertama** (27 November 2026, CodeQL 2.27.6, suite `security-extended`, commit `06485f2`). JavaScript/TypeScript: 103 rule dijalankan pada 163 file, **0 temuan**. Go: 35 rule dijalankan pada 126 file, **7 temuan**:

| # | Rule | Severity | Lokasi | Potensi dampak | Keputusan |
| --- | --- | --- | --- | --- | --- |
| 1 | `go/sql-injection` | High (8.8) | `src/database/viewer.go:231` | Parameter `schema` dan `table` dari query string masuk ke teks `SELECT * FROM ...`. Jika tidak divalidasi, pengguna dapat menyisipkan SQL ke database target. | **Diperbaiki.** Sebelumnya input sudah dicek ke `information_schema` dan di-quote manual, sehingga tidak dapat dieksploitasi. Kode diperkuat: nama tabel sekarang diambil dari baris catalog dan di-quote oleh PostgreSQL (`format('%I.%I', ...)`), sehingga teks query tidak lagi dirangkai dari input request. |
| 2 | `go/log-injection` | Medium (6.1) | `src/auth/api.go:50` | Username dari body login ditulis ke log `auth.login_failed`; baris baru di input dapat memalsukan entri log pada logger teks. | **False positive.** Logger memakai `slog.NewJSONHandler`, yang meng-escape `\n`, `\r`, dan tanda kutip, sehingga input selalu tetap satu nilai string dalam satu baris JSON. Username dicatat dengan sengaja untuk audit login gagal. |
| 3 | `go/log-injection` | Medium (6.1) | `src/admin/users_api.go:85` | Sama seperti di atas. | **False positive.** Field yang ditulis hanya ID user dan boolean admin; keduanya bukan teks bebas dan tetap di-escape oleh handler JSON. |
| 4 | `go/log-injection` | Medium (6.1) | `src/api/middleware.go:21` | Route ditulis ke log `http.panic`. | **False positive.** Nilai berasal dari `c.FullPath()`, yaitu pola route yang didaftarkan server (misalnya `/api/databases/:database_id`), bukan path mentah dari pengguna; output juga JSON. |
| 5 | `go/log-injection` | Medium (6.1) | `src/api/response.go:116` | Route ditulis ke log `http.internal_error`. | **False positive.** Sama dengan #4. |
| 6 | `go/log-injection` | Medium (6.1) | `src/auth/csrf.go:114` | Route ditulis ke log `auth.csrf_rejected`. | **False positive.** Sama dengan #4; token CSRF sendiri tidak pernah ditulis ke log. |
| 7 | `go/log-injection` | Medium (6.1) | `src/telemetry/middleware.go:72` | Route ditulis ke access log `http.request`. | **False positive.** Sama dengan #4. |

Temuan false positive ditandai *Dismiss → False positive* di tab Code scanning beserta alasan di atas.

**Scan ulang** setelah perbaikan #1 (konfigurasi dan suite yang sama): JavaScript/TypeScript tetap 0 temuan, Go turun dari 7 menjadi 6 temuan. `go/sql-injection` di `viewer.go` tidak lagi muncul, dan yang tersisa hanya enam `go/log-injection` yang sudah diputuskan false positive. Perbaikan diverifikasi dengan integration test viewer (`go test ./test/database/`), termasuk penolakan nama schema/tabel yang tidak ada.

## Built With

- [Go](https://go.dev/) - Bahasa pemrograman
- [Gin](https://gin-gonic.com/) - Web framework HTTP
- [React](https://react.dev/) + [Vite](https://vite.dev/) - SPA frontend
- [React Router](https://reactrouter.com/) - Routing halaman di SPA
- [PostgreSQL](https://www.postgresql.org/) - Database platform dan database target
- [pgx](https://github.com/jackc/pgx) - Driver PostgreSQL
- [Argon2id](https://pkg.go.dev/golang.org/x/crypto/argon2) - Hash password
- [Bootstrap](https://getbootstrap.com/) - Tampilan halaman

## Contributing

| Nama                    | NIM      |
| :---------------------- | :------- |
| Nashiruddin Akram       | 13524090 |
| Reinsen Silitonga       | 13524093 |
| Manuel Thimoty Silalahi | 13524102 |

### Fitur Milestone 2

- SPA seluruh halaman (desktop dan mobile), API auth dan administrasi user, session dan CSRF, fondasi logging dan tracing: Reinsen
- Pengajuan dan approval migration, leader group, API history dan Hide rollback, API group, akses, koneksi, dan viewer, dataset dan benchmark: Nashiruddin Akram
- Definisi migration dan validasi SQL, up/down/irreversible, aturan linear rollback dan forward, antrean dan worker, eksekusi, retry, dan recovery, batch, resume, dan kompensasi: Manuel Thimoty

### Fitur Milestone 1

- Login, logout, dan manajemen session: Reinsen
- Rotasi session dan perlindungan CSRF: Reinsen
- Aturan hak akses: Reinsen
- Administrasi user: Reinsen
- Administrasi group dan anggota: Manuel Thimoty
- Akses database melalui group: Manuel Thimoty
- Administrasi koneksi database: Manuel Thimoty
- Daftar database, viewer schema, dan data tabel: Nashiruddin Akram
- Migration, eksekusi, dan recovery: Nashiruddin Akram

### Schema Database

- users, sessions, session_tokens: Reinsen
- groups, user_groups, database_connections, group_database_access: Manuel Thimoty
- migrations, migration_executions: Nashiruddin Akram
- Leader dan arsip group (`011`), request dan approval (`013`): Nashiruddin Akram
- Definisi migration (`012`), job (`014`), history dan batch attempt (`015`): Manuel Thimoty

## Acknowledgments

Kami ingin berterima kasih kepada Tuhan Yang Maha Esa atas rahmat-Nya, orang tua dan keluarga kami yang selalu mendukung kami, bapak dan ibu dosen yang telah mengajari kami, serta para asisten yang telah mempersiapkan tugas ini dengan baik. Tanpa dukungan dan bantuan dari pihak-pihak tersebut, platform ini mungkin tidak akan dapat diselesaikan. Kami berharap hasil yang kami buat dapat menjadi dasar yang berguna bagi orang lain di kemudian hari.
