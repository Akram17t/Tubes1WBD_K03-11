# Tugas Besar IF3110 Pengembangan Aplikasi Berbasis Web: Milestone 1

Pada tugas besar ini, kami diminta untuk membuat Database Management Platform berbasis web yang mengatur akses pengguna ke database PostgreSQL bersama. Pengguna tidak diberi kredensial database secara langsung. Setiap pengguna harus login ke platform, lalu platform yang menentukan apa saja yang boleh dilihat atau diubah oleh pengguna tersebut.

Platform Admin mengelola user, group, koneksi database target, dan pemberian akses. Akses ke sebuah database selalu diberikan kepada group, tidak pernah langsung kepada user, dengan dua tingkatan: **View**, yang memungkinkan melihat schema, data tabel, dan riwayat migration, serta **Edit**, yang juga memungkinkan membuat, mengubah, dan menjalankan migration. Jika seorang user tergabung dalam beberapa group, yang berlaku adalah tingkat akses tertinggi. Perubahan schema hanya dilakukan melalui migration, yang dijalankan di dalam transaction, diserialisasi per database, dan dicatat beserta setiap percobaan eksekusinya.

## Prerequisites

- Go 1.26
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

Nilai lainnya (`PLATFORM_DATABASE_URL` dan pengaturan session) sudah sesuai dengan setup Docker di atas.

### 5. Jalankan aplikasi

```sh
go run ./src
```

Setelah terminal menampilkan `HTTP server listening on :8080`, buka http://localhost:8080 lalu login sebagai `admin` dengan password dari `.env`.

### 6. Sambungkan database contoh

Koneksi contoh tidak dimasukkan ke seed karena password-nya harus dienkripsi dengan kunci masing-masing. Sebagai admin:

1. Buka **Koneksi**, klik **Tambah koneksi**, lalu isi: nama `target-db`, host `localhost`, port `5432`, database `target_db`, username `postgres`, password `postgres`, SSL mode `disable`.
2. Buka koneksi yang baru dibuat, lalu klik **Uji koneksi tersimpan** untuk memeriksanya.
3. Buka **Group**, pilih **analyst**, lalu **Akses database**, dan berikan akses `target-db` dengan tingkat `view`.
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

### Login

![Login](screenshots/login.png)

Halaman login berisi input username dan password. Username tidak membedakan huruf besar dan kecil. Jika kredensial benar, session baru dibuat dan pengguna diarahkan ke daftar database. Password salah, username yang tidak terdaftar, dan akun yang dinonaktifkan semuanya menampilkan pesan error yang sama, sehingga halaman tidak membocorkan akun mana saja yang ada. Setiap login menerbitkan token session baru dan mencabut session sebelumnya di browser tersebut.

### Daftar Database

![Daftar database](screenshots/databases.png)

Halaman ini hanya menampilkan database aktif yang dapat diakses pengguna melalui group-nya. Database tanpa pemberian akses tidak muncul, dan membukanya langsung lewat URL menghasilkan 403.

### Ringkasan Database

![Ringkasan database](screenshots/database.png)

Halaman ringkasan menampilkan database yang dipilih beserta tautan ke viewer schema dan daftar migration.

### Viewer Schema

![Viewer schema](screenshots/schema.png)

Pengguna dengan akses View atau Edit dapat menelusuri schema dan tabel pada database target. Untuk setiap tabel, halaman ini menampilkan kolom beserta tipe PostgreSQL dan status nullable, constraint (primary key, foreign key, unique, check), serta index. Nama schema dan tabel divalidasi terhadap database target sebelum query dibuat.

### Data Tabel

![Data tabel](screenshots/data.png)

Isi tabel ditampilkan dengan pagination. Ukuran halaman dibatasi di server, sehingga sebuah request tidak bisa memuat seluruh isi tabel besar sekaligus.

### Daftar Migration

![Daftar migration](screenshots/migrations.png)

Halaman ini menampilkan seluruh migration pada database beserta status (draft, running, success, atau failed) dan version-nya. Pengguna dengan akses View hanya dapat membaca daftar; tombol buat migration hanya muncul untuk pengguna dengan akses Edit, dan backend tetap memeriksa izin pada setiap request.

### Buat dan Edit Migration

![Buat migration](screenshots/migration-new.png)

Pengguna dengan akses Edit dapat menulis migration berisi judul, deskripsi (opsional), dan SQL yang akan dijalankan. Migration dapat diubah selama masih berstatus draft atau setelah gagal. Migration yang sudah berhasil tidak dapat diubah atau dijalankan ulang.

### Detail Migration

![Detail migration](screenshots/migration-detail.png)

Halaman detail menampilkan SQL migration, status, version yang diperoleh, dan riwayat setiap percobaan eksekusi. Menjalankan migration berarti menjalankan SQL-nya di dalam satu transaction pada database target. Hanya satu eksekusi yang dapat berjalan per database dalam satu waktu, dan setiap eksekusi yang berhasil mendapat nomor version berikutnya.

### Hasil Eksekusi

![Hasil eksekusi](screenshots/execution.png)

Setiap percobaan eksekusi dicatat beserta pelaksana, waktu mulai dan selesai, serta hasilnya. Jika SQL gagal, transaction di-rollback sehingga database target tidak berubah, dan pesan error ditampilkan tanpa membocorkan kredensial koneksi. Migration kemudian dapat diperbaiki lalu dijalankan ulang.

### Administrasi User

![Administrasi user](screenshots/users.png)

Platform Admin dapat melihat seluruh akun beserta peran dan statusnya, membuat user baru, serta mengaktifkan atau menonaktifkan akun. Menonaktifkan akun langsung mencabut seluruh session milik akun tersebut.

### Buat dan Edit User

![Edit user](screenshots/user-form.png)

Admin dapat mengatur username, nama tampilan, password, dan status Platform Admin. Password disimpan sebagai hash bcrypt dan harus terdiri dari 8 sampai 72 karakter. Username yang sudah dipakai menghasilkan 409. Mengganti password atau mencabut status admin akan mencabut session user tersebut, sedangkan menjadikan user sebagai admin akan merotasi session-nya pada request berikutnya. Admin tidak dapat menonaktifkan akunnya sendiri atau mencabut status adminnya sendiri.

### Daftar Group

![Daftar group](screenshots/groups.png)

Daftar group berisi tautan ke halaman edit setiap group dan pengaturan akses database-nya.

### Edit Group dan Anggota

![Edit group](screenshots/group-edit.png)

Di halaman ini admin dapat mengganti nama group, menambah atau mengeluarkan anggota, dan menghapus group. Menambah anggota menaikkan privilege version anggota tersebut sehingga session-nya dirotasi, sedangkan mengeluarkan anggota atau menghapus group langsung mencabut session pengguna yang terdampak. Menambahkan anggota yang sama dua kali menghasilkan 409.

### Akses Database Group

![Akses database group](screenshots/database-access.png)

Admin memberikan akses View atau Edit ke database aktif kepada sebuah group, mengubah tingkatnya, atau mencabutnya. Memberikan atau menaikkan akses akan merotasi session anggota, sedangkan menurunkan atau mencabut akses akan mencabut session mereka.

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

Session disimpan di server, sedangkan browser hanya menyimpan token acak di cookie yang bersifat HttpOnly dan SameSite=Lax (serta Secure di production). Database hanya menyimpan hash SHA-256 dari setiap token. Token dirotasi saat login, setiap interval rotasi, dan setiap kali hak akses pengguna meningkat. Token lama tetap berlaku selama grace period untuk menangani request yang berjalan bersamaan, dan rotasi tidak pernah memperpanjang absolute timeout. Aplikasi menolak berjalan jika interval rotasi tidak lebih pendek dari idle timeout, atau grace period tidak lebih pendek dari interval rotasi. Setiap form yang mengubah data membawa token CSRF, dan form login dilindungi dengan pemeriksaan header Origin.

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

## Built With

- [Go](https://go.dev/) - Bahasa pemrograman
- [Gin](https://gin-gonic.com/) - Web framework HTTP
- [html/template](https://pkg.go.dev/html/template) - Render halaman di sisi server
- [PostgreSQL](https://www.postgresql.org/) - Database platform dan database target
- [pgx](https://github.com/jackc/pgx) - Driver PostgreSQL
- [bcrypt](https://pkg.go.dev/golang.org/x/crypto/bcrypt) - Hash password
- [MDB Bootstrap](https://mdbootstrap.com/) - Tampilan halaman

## Contributing

| Nama                    | NIM      |
| :---------------------- | :------- |
| Nashiruddin Akram       | 13524090 |
| Reinsen Silitonga       | 13524093 |
| Manuel Thimoty Silalahi | 13524102 |

### Fitur

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

## Acknowledgments

Kami ingin berterima kasih kepada Tuhan Yang Maha Esa atas rahmat-Nya, orang tua dan keluarga kami yang selalu mendukung kami, bapak dan ibu dosen yang telah mengajari kami, serta para asisten yang telah mempersiapkan tugas ini dengan baik. Tanpa dukungan dan bantuan dari pihak-pihak tersebut, platform ini mungkin tidak akan dapat diselesaikan. Kami berharap hasil yang kami buat dapat menjadi dasar yang berguna bagi orang lain di kemudian hari.
