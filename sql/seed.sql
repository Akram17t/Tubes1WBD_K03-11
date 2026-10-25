-- Data demo untuk database platform. Jalankan setelah sql/001-010.
-- Password semua user demo: demo12345 (hash Argon2id, khusus demo).
-- Admin tidak ada di sini: dibuat otomatis dari ADMIN_USERNAME/ADMIN_PASSWORD saat aplikasi start.
-- Koneksi target-db dan pemberian akses dibuat lewat halaman admin, karena password koneksi
-- harus dienkripsi dengan CONNECTION_ENCRYPTION_KEY milik masing-masing environment.
BEGIN;

INSERT INTO users (username, password_hash, display_name)
VALUES
    ('viewer', '$argon2id$v=19$m=19456,t=2,p=1$1NLuSJr9udPv7tK9H2Gpqw$H6Bog2udGTCTcf0UOggG8wv4gG2qpIK5OvTlKsyHpIQ', 'Reinsen'),
    ('editor', '$argon2id$v=19$m=19456,t=2,p=1$1NLuSJr9udPv7tK9H2Gpqw$H6Bog2udGTCTcf0UOggG8wv4gG2qpIK5OvTlKsyHpIQ', 'Manu'),
    ('multi', '$argon2id$v=19$m=19456,t=2,p=1$1NLuSJr9udPv7tK9H2Gpqw$H6Bog2udGTCTcf0UOggG8wv4gG2qpIK5OvTlKsyHpIQ', 'Akram'),
    ('outsider', '$argon2id$v=19$m=19456,t=2,p=1$1NLuSJr9udPv7tK9H2Gpqw$H6Bog2udGTCTcf0UOggG8wv4gG2qpIK5OvTlKsyHpIQ', 'Wancoy')
ON CONFLICT (username) DO NOTHING;

INSERT INTO groups (name, description)
VALUES
    ('analyst', 'Boleh melihat schema, data, dan riwayat migration'),
    ('developer', 'Boleh membuat dan menjalankan migration')
ON CONFLICT (name) DO NOTHING;

INSERT INTO user_groups (user_id, group_id)
SELECT u.id, g.id
FROM users u
JOIN groups g ON (u.username, g.name) IN (
    ('viewer', 'analyst'),
    ('editor', 'developer'),
    ('multi', 'analyst'),
    ('multi', 'developer')
)
ON CONFLICT DO NOTHING;

COMMIT;
