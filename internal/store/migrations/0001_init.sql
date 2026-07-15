-- STRICT-таблиці: суворі типи SQLite; гроші — цілі в мінорних одиницях;
-- дати — TEXT ISO-8601. Дизайн портабельний під майбутню міграцію на Postgres
-- (при переїзді STRICT прибирається, INTEGER->BIGINT, все інше без змін).

CREATE TABLE bonds (
    isin         TEXT PRIMARY KEY,
    nominal      INTEGER NOT NULL,
    currency     TEXT    NOT NULL,
    rate_bp      INTEGER NOT NULL,
    maturity     TEXT    NOT NULL,
    descr        TEXT    NOT NULL DEFAULT '',
    fetched_at   TEXT    NOT NULL
) STRICT;

CREATE INDEX idx_bonds_maturity ON bonds(maturity);
CREATE INDEX idx_bonds_currency ON bonds(currency);

CREATE TABLE payments (
    isin         TEXT    NOT NULL REFERENCES bonds(isin) ON DELETE CASCADE,
    pay_date     TEXT    NOT NULL,
    pay_type     INTEGER NOT NULL,
    per_bond     INTEGER NOT NULL,
    PRIMARY KEY (isin, pay_date, pay_type)
) STRICT;

CREATE INDEX idx_payments_date ON payments(pay_date);

CREATE TABLE lots (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    isin           TEXT    NOT NULL,
    qty            INTEGER NOT NULL CHECK (qty > 0),
    price_per_bond INTEGER NOT NULL,
    currency       TEXT    NOT NULL,
    buy_date       TEXT    NOT NULL,
    channel        TEXT    NOT NULL DEFAULT '',
    note           TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_lots_isin ON lots(isin);

CREATE TABLE sales (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    lot_id          INTEGER NOT NULL REFERENCES lots(id) ON DELETE CASCADE,
    sale_date       TEXT    NOT NULL,
    qty             INTEGER NOT NULL CHECK (qty > 0),
    clean_per_bond  INTEGER NOT NULL,
    accrued         INTEGER NOT NULL DEFAULT 0,
    currency        TEXT    NOT NULL,
    note            TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_sales_lot ON sales(lot_id);

CREATE TABLE payment_status (
    isin       TEXT NOT NULL,
    pay_date   TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('received','reinvested')),
    marked_at  TEXT NOT NULL,
    PRIMARY KEY (isin, pay_date)
) STRICT;

CREATE TABLE snapshots (
    date            TEXT PRIMARY KEY,
    invested_uah    INTEGER NOT NULL,
    nominal_uah_eq  INTEGER NOT NULL,
    usd_share_bp    INTEGER NOT NULL,
    uninvested_uah  INTEGER NOT NULL
) STRICT;

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

CREATE TABLE fx_rates (
    code     TEXT NOT NULL,
    rate_e4  INTEGER NOT NULL,
    date     TEXT NOT NULL,
    PRIMARY KEY (code, date)
) STRICT;
