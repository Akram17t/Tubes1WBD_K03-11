-- Data demo untuk database platform. Jalankan setelah sql/001-010.
-- Password semua user demo: demo12345 (hash bcrypt, khusus demo).
-- Admin tidak ada di sini: dibuat otomatis dari ADMIN_USERNAME/ADMIN_PASSWORD saat aplikasi start.
-- Koneksi target-db dan pemberian akses dibuat lewat halaman admin, karena password koneksi
-- harus dienkripsi dengan CONNECTION_ENCRYPTION_KEY milik masing-masing environment.
BEGIN;

INSERT INTO users (username, password_hash, display_name)
VALUES
    ('viewer', '$2a$10$.OE.M44YbV9QChePQOuc1u7Kh/fMUcDa74/KUpchN6QhI1sf93Suu', 'Reinsen'),
    ('editor', '$2a$10$.OE.M44YbV9QChePQOuc1u7Kh/fMUcDa74/KUpchN6QhI1sf93Suu', 'Manu'),
    ('multi', '$2a$10$.OE.M44YbV9QChePQOuc1u7Kh/fMUcDa74/KUpchN6QhI1sf93Suu', 'Akram'),
    ('outsider', '$2a$10$.OE.M44YbV9QChePQOuc1u7Kh/fMUcDa74/KUpchN6QhI1sf93Suu', 'Wancoy')
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
