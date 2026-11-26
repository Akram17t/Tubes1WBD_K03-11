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
