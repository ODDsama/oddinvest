-- Мультивалютний грошовий рахунок. Депозити отримують валюту (старі =
-- UAH). Конвертації — окрема таблиця: «віддав from_amount [from_currency]
-- → отримав to_amount [to_currency]» (курс неявний = from/to).
ALTER TABLE deposits ADD COLUMN currency TEXT NOT NULL DEFAULT 'UAH';

CREATE TABLE conversions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    date          TEXT    NOT NULL,
    from_currency TEXT    NOT NULL,
    from_amount   INTEGER NOT NULL,   -- мінорні, > 0
    to_currency   TEXT    NOT NULL,
    to_amount     INTEGER NOT NULL,   -- мінорні, > 0
    note          TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_conversions_date ON conversions(date);
