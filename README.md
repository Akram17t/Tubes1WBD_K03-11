# Tubes WBD

Kerangka awal proyek Go. Fitur aplikasi belum diimplementasikan.

## Struktur repo

```text
.
|-- src/
|   |-- main.go
|   |-- config.go
|   |-- routes.go
|   |-- auth/
|   |   |-- auth.go
|   |   |-- session.go
|   |   |-- access.go
|   |-- admin/
|   |   |-- users.go
|   |   |-- groups.go
|   |-- database/
|   |   |-- connection.go
|   |   |-- viewer.go
|   |-- migration/
|       |-- migration.go
|-- templates/
|   |-- layout.html
|   |-- login.html
|   |-- admin.html
|   |-- databases.html
|   |-- viewer.html
|   |-- migrations.html
|-- static/
|   |-- style.css
|   |-- script.js
|-- sql/
|   |-- init.sql
|   |-- seed.sql
|-- .env.example
|-- .gitignore
|-- go.mod
|-- go.sum
|-- README.md
```

## Menjalankan

Gunakan Go 1.25 atau yang lebih baru, lalu jalankan dari root repo:

```sh
go run ./src
```

Saat ini program langsung selesai karena fungsi `main` masih kosong.
File template, aset statis, dan SQL disediakan sebagai placeholder.
`go.sum` masih kosong karena belum ada dependensi eksternal.
