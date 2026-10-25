-- Seluruh nama klub, pemain, jadwal, dan hasil pertandingan adalah data fiktif.
BEGIN;

INSERT INTO public.clubs (code, name, city, stadium)
VALUES
    ('GFC', 'Garuda FC', 'Bandung', 'Stadion Garuda'),
    ('CKU', 'Cakra United', 'Jakarta', 'Stadion Cakra'),
    ('SMF', 'Samudra FC', 'Semarang', 'Stadion Samudra'),
    ('BKA', 'Bukit Athletic', 'Malang', NULL),
    ('BTF', 'Bintang FC', 'Surabaya', 'Stadion Bintang'),
    ('RBU', 'Rimba United', 'Bogor', 'Stadion Rimba'),
    ('PSF', 'Pesisir FC', 'Makassar', NULL),
    ('RJF', 'Rajawali FC', 'Yogyakarta', 'Stadion Rajawali')
ON CONFLICT (code) DO NOTHING;

INSERT INTO public.players (club_id, squad_number, name, position, birth_date)
SELECT
    c.id,
    n,
    (ARRAY[
        'Aditya', 'Bima', 'Cahya', 'Dimas', 'Eka',
        'Farhan', 'Galih', 'Hafiz', 'Ilham', 'Joko',
        'Kevin', 'Luthfi', 'Muhammad', 'Naufal', 'Oka',
        'Putra', 'Rafi', 'Satria', 'Teguh', 'Umar',
        'Vicky', 'Wahyu', 'Yudha', 'Zaki', 'Bagas'
    ])[n] || ' ' ||
    (ARRAY[
        'Pratama', 'Saputra', 'Wijaya', 'Hidayat',
        'Santoso', 'Nugraha', 'Ramadhan', 'Setiawan',
        'Maulana', 'Kusuma', 'Wibowo', 'Firmansyah',
        'Gunawan', 'Permana', 'Fauzi', 'Kurniawan'
    ])[((n + c.id * 3) % 16 + 1)::integer],
    CASE
        WHEN n <= 2 THEN 'GK'
        WHEN n <= 10 THEN 'DF'
        WHEN n <= 18 THEN 'MF'
        ELSE 'FW'
    END,
    DATE '1990-01-01' + ((c.id * 173 + n * 137) % 5000)::integer
FROM public.clubs c
CROSS JOIN generate_series(1, 25) AS numbers(n)
ON CONFLICT (club_id, squad_number) DO NOTHING;

WITH fixtures AS (
    SELECT
        h.id AS home_id,
        a.id AS away_id,
        row_number() OVER (ORDER BY h.code, a.code) AS n
    FROM public.clubs h
    CROSS JOIN public.clubs a
    WHERE h.id <> a.id
)
INSERT INTO public.matches (home_club_id, away_club_id, played_at, home_goals, away_goals)
SELECT
    home_id,
    away_id,
    TIMESTAMPTZ '2025-08-01 19:00:00+07' + (n - 1) * INTERVAL '3 days',
    (n * 3) % 5,
    (n * 7) % 4
FROM fixtures
ON CONFLICT (home_club_id, away_club_id, played_at) DO NOTHING;

INSERT INTO public.appearances (match_id, player_id, minutes_played, rating)
SELECT
    m.id,
    p.id,
    90,
    CASE
        WHEN (m.id + p.id) % 7 = 0 THEN NULL
        ELSE (60 + (m.id + p.id) % 35)::numeric / 10
    END
FROM public.matches m
JOIN public.players p ON p.club_id IN (m.home_club_id, m.away_club_id)
WHERE p.squad_number IN (1, 3, 4, 5, 6, 11, 12, 13, 19, 20, 21)
ON CONFLICT (match_id, player_id) DO NOTHING;

COMMIT;
