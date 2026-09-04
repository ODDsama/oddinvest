-- foreign_keys: off
-- Портфелі: вимір «чий» у кожній таблиці користувацьких даних.
--
-- Доти застосунок був одноосібним за побудовою: жодна таблиця не знала,
-- кому належить рядок, і кожне читання означало «вся база». Другий
-- портфель (з іншою стратегією, своїми брокерами, своєю подушкою й своїми
-- боргами) потребує колонки portfolio_id скрізь, де лежить те, що ввела
-- людина. Довідкове — bonds, payments, fx_rates, ovdp_auctions, funds,
-- fund_prices, secrets — лишається спільним: це «що воно таке», а не «що я
-- маю».
--
-- КОЛОНКА В ДІТЕЙ ТЕЖ. sales, deposit_topups, goal_ops, debt_ops, debt_marks,
-- npf_ops, npf_nav, plan_receipts, plan_flow_revisions можна було б
-- скоупити через батька, але тоді кожен їхній запит мусив би нести JOIN,
-- а пропущений JOIN нічим не відрізняється від правильного, доки другий
-- портфель порожній. З колонкою кожен запит однаковий: WHERE portfolio_id=?,
-- і сторож у scope_guard_test.go перевіряє сам текст SQL.
--
-- ON DELETE CASCADE на portfolios(id) — саме тому, що таблиць двадцять сім:
-- видалення портфеля одним DELETE FROM portfolios стирає все, і забути
-- таблицю в переліку неможливо, бо переліку немає. Тест звіряє, що кожна
-- таблиця з portfolio_id має саме такий ключ.
--
-- ЧОМУ ПЕРШИЙ РЯДОК ВИМИКАЄ КЛЮЧІ. Шість таблиць змінюють ключ, а не лише
-- дістають колонку: snapshots (date), settings (key), payment_status
-- (isin, pay_date), import_profiles (name), brokers (UNIQUE name),
-- npf_accounts (UNIQUE name) — без portfolio_id у ключі два портфелі не
-- могли б мати знімок за той самий день, ту саму ставку чи брокера «mono».
-- Зміна ключа в SQLite — це перебудова таблиці, а brokers і npf_accounts —
-- батьки з зовнішніми ключами від сімох і двох таблиць: при увімкнених
-- ключах DROP TABLE brokers виконав би неявний DELETE і впав би на першому
-- lots.broker_id (0010 саме через це правила lots колонками, а не
-- перебудовою). Раннер (migrate.go) читає маркер і на цей файл вимикає
-- ключі, а перед COMMIT вимагає порожнього PRAGMA foreign_key_check.
-- Той самий маркер дозволяє ADD COLUMN … REFERENCES із DEFAULT 1: при
-- увімкнених ключах SQLite приймає такий стовпець лише з DEFAULT NULL.
--
-- Наявні дані стають портфелем 1 (DEFAULT 1 на кожній колонці), тож жоден
-- запит зі старого коду не змінює результату, доки портфель один.

CREATE TABLE portfolios (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    -- slug — латинський ідентифікатор для заголовка X-Portfolio і адрес:
    -- назва українською в заголовку HTTP не живе.
    slug       TEXT    NOT NULL UNIQUE,
    name       TEXT    NOT NULL,
    created_at TEXT    NOT NULL DEFAULT (datetime('now'))
) STRICT;

INSERT INTO portfolios (id, slug, name) VALUES (1, 'main', 'Мій');

-- Робочий стан джоб — не політика портфеля. nbu_refreshed_at і
-- ovdp_auctions_polled_through жили в settings лише тому, що іншої
-- key/value таблиці не було; з появою portfolio_id у settings вони
-- мусили б належати якомусь портфелю, а довідник НБУ спільний.
CREATE TABLE app_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

INSERT INTO app_state (key, value)
SELECT key, value FROM settings
 WHERE key IN ('nbu_refreshed_at', 'ovdp_auctions_polled_through');
DELETE FROM settings WHERE key IN ('nbu_refreshed_at', 'ovdp_auctions_polled_through');

-- --- 1. Колонка там, де ключ не змінюється ---

ALTER TABLE lots                ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE sales               ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE deposits            ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE conversions         ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE fund_ops            ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE term_deposits       ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE deposit_topups      ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE reserve_ops         ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE goals               ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE goal_ops            ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE debts               ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE debt_ops            ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE debt_marks          ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE npf_ops             ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE npf_nav             ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE plan_flows          ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE plan_flow_revisions ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE plan_receipts       ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE plan_actions        ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE plan_buys           ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;
ALTER TABLE decisions           ADD COLUMN portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE;

CREATE INDEX idx_lots_portfolio                ON lots(portfolio_id);
CREATE INDEX idx_sales_portfolio               ON sales(portfolio_id);
CREATE INDEX idx_deposits_portfolio            ON deposits(portfolio_id);
CREATE INDEX idx_conversions_portfolio         ON conversions(portfolio_id);
CREATE INDEX idx_fund_ops_portfolio            ON fund_ops(portfolio_id);
CREATE INDEX idx_term_deposits_portfolio       ON term_deposits(portfolio_id);
CREATE INDEX idx_deposit_topups_portfolio      ON deposit_topups(portfolio_id);
CREATE INDEX idx_reserve_ops_portfolio         ON reserve_ops(portfolio_id);
CREATE INDEX idx_goals_portfolio               ON goals(portfolio_id);
CREATE INDEX idx_goal_ops_portfolio            ON goal_ops(portfolio_id);
CREATE INDEX idx_debts_portfolio               ON debts(portfolio_id);
CREATE INDEX idx_debt_ops_portfolio            ON debt_ops(portfolio_id);
CREATE INDEX idx_debt_marks_portfolio          ON debt_marks(portfolio_id);
CREATE INDEX idx_npf_ops_portfolio             ON npf_ops(portfolio_id);
CREATE INDEX idx_npf_nav_portfolio             ON npf_nav(portfolio_id);
CREATE INDEX idx_plan_flows_portfolio          ON plan_flows(portfolio_id);
CREATE INDEX idx_plan_flow_revisions_portfolio ON plan_flow_revisions(portfolio_id);
CREATE INDEX idx_plan_receipts_portfolio       ON plan_receipts(portfolio_id);
CREATE INDEX idx_plan_actions_portfolio        ON plan_actions(portfolio_id);
CREATE INDEX idx_plan_buys_portfolio           ON plan_buys(portfolio_id);
CREATE INDEX idx_decisions_portfolio           ON decisions(portfolio_id);

-- --- 2. Перебудова там, де portfolio_id входить у ключ ---
--
-- Порядок колонок, типи й дефолти — ті самі, що зібрались за 0001…0053;
-- зміна лише в ключі. Індексів на цих шести таблицях не було, тож
-- відновлювати нічого. Коментарі до колонок import_profiles живуть у
-- 0036 і 0051 — тут їх не дублюємо, як не дублювала 0044.

CREATE TABLE brokers_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    UNIQUE (portfolio_id, name)
) STRICT;
INSERT INTO brokers_new (id, name) SELECT id, name FROM brokers;
DROP TABLE brokers;
ALTER TABLE brokers_new RENAME TO brokers;

CREATE TABLE npf_accounts_new (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    portfolio_id       INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE,
    name               TEXT    NOT NULL,
    administrator      TEXT    NOT NULL DEFAULT '',
    currency           TEXT    NOT NULL DEFAULT 'UAH',
    nav_e6             INTEGER NOT NULL DEFAULT 0,
    nav_date           TEXT    NOT NULL DEFAULT '',
    expected_yield_bp  INTEGER NOT NULL DEFAULT 0,
    yield_simple_years INTEGER NOT NULL DEFAULT 0,
    access_date        TEXT    NOT NULL DEFAULT '',
    income_tax_bp      INTEGER NOT NULL DEFAULT 0,
    credit_rate_bp     INTEGER NOT NULL DEFAULT 0,
    contrib_day        INTEGER NOT NULL DEFAULT 0,
    note               TEXT    NOT NULL DEFAULT '',
    payout_years       INTEGER NOT NULL DEFAULT 10,
    payout_freq        TEXT    NOT NULL DEFAULT 'month',
    UNIQUE (portfolio_id, name)
) STRICT;
INSERT INTO npf_accounts_new (id, name, administrator, currency, nav_e6, nav_date,
    expected_yield_bp, yield_simple_years, access_date, income_tax_bp, credit_rate_bp,
    contrib_day, note, payout_years, payout_freq)
SELECT id, name, administrator, currency, nav_e6, nav_date,
    expected_yield_bp, yield_simple_years, access_date, income_tax_bp, credit_rate_bp,
    contrib_day, note, payout_years, payout_freq
  FROM npf_accounts;
DROP TABLE npf_accounts;
ALTER TABLE npf_accounts_new RENAME TO npf_accounts;

CREATE TABLE snapshots_new (
    portfolio_id     INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE,
    date             TEXT    NOT NULL,
    invested_uah     INTEGER NOT NULL,
    nominal_uah_eq   INTEGER NOT NULL,
    usd_share_bp     INTEGER NOT NULL,
    uninvested_uah   INTEGER NOT NULL,
    month_target_uah INTEGER NOT NULL DEFAULT 0,
    account_uah      INTEGER NOT NULL DEFAULT 0,
    funds_uah        INTEGER NOT NULL DEFAULT 0,
    deposits_uah     INTEGER NOT NULL DEFAULT 0,
    funds_cost_uah   INTEGER NOT NULL DEFAULT 0,
    reserve_uah      INTEGER NOT NULL DEFAULT 0,
    npf_uah          INTEGER NOT NULL DEFAULT 0,
    npf_cost_uah     INTEGER NOT NULL DEFAULT 0,
    goals_uah        INTEGER NOT NULL DEFAULT 0,
    net_worth_uah    INTEGER NOT NULL DEFAULT 0,
    idle_uah         INTEGER NOT NULL DEFAULT -1,
    PRIMARY KEY (portfolio_id, date)
) STRICT;
INSERT INTO snapshots_new (date, invested_uah, nominal_uah_eq, usd_share_bp, uninvested_uah,
    month_target_uah, account_uah, funds_uah, deposits_uah, funds_cost_uah, reserve_uah,
    npf_uah, npf_cost_uah, goals_uah, net_worth_uah, idle_uah)
SELECT date, invested_uah, nominal_uah_eq, usd_share_bp, uninvested_uah,
    month_target_uah, account_uah, funds_uah, deposits_uah, funds_cost_uah, reserve_uah,
    npf_uah, npf_cost_uah, goals_uah, net_worth_uah, idle_uah
  FROM snapshots;
DROP TABLE snapshots;
ALTER TABLE snapshots_new RENAME TO snapshots;

CREATE TABLE settings_new (
    portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE,
    key          TEXT    NOT NULL,
    value        TEXT    NOT NULL,
    PRIMARY KEY (portfolio_id, key)
) STRICT;
INSERT INTO settings_new (key, value) SELECT key, value FROM settings;
DROP TABLE settings;
ALTER TABLE settings_new RENAME TO settings;

CREATE TABLE payment_status_new (
    portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE,
    isin         TEXT    NOT NULL,
    pay_date     TEXT    NOT NULL,
    status       TEXT    NOT NULL CHECK (status IN ('received')),
    marked_at    TEXT    NOT NULL,
    PRIMARY KEY (portfolio_id, isin, pay_date)
) STRICT;
INSERT INTO payment_status_new (isin, pay_date, status, marked_at)
SELECT isin, pay_date, status, marked_at FROM payment_status;
DROP TABLE payment_status;
ALTER TABLE payment_status_new RENAME TO payment_status;

CREATE TABLE import_profiles_new (
    portfolio_id INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    format       TEXT    NOT NULL DEFAULT 'xlsx',
    header       INTEGER NOT NULL DEFAULT 1,
    col_date     INTEGER NOT NULL DEFAULT 0,
    col_op       INTEGER NOT NULL DEFAULT 1,
    col_ref      INTEGER NOT NULL DEFAULT -1,
    col_qty      INTEGER NOT NULL DEFAULT -1,
    col_debit    INTEGER NOT NULL DEFAULT -1,
    col_credit   INTEGER NOT NULL DEFAULT -1,
    ops          TEXT    NOT NULL DEFAULT '',
    note         TEXT    NOT NULL DEFAULT '',
    col_balance  INTEGER NOT NULL DEFAULT -1,
    col_mcc      INTEGER NOT NULL DEFAULT -1,
    debt_id      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (portfolio_id, name)
) STRICT;
INSERT INTO import_profiles_new (name, format, header, col_date, col_op, col_ref, col_qty,
    col_debit, col_credit, ops, note, col_balance, col_mcc, debt_id)
SELECT name, format, header, col_date, col_op, col_ref, col_qty,
    col_debit, col_credit, ops, note, col_balance, col_mcc, debt_id
  FROM import_profiles;
DROP TABLE import_profiles;
ALTER TABLE import_profiles_new RENAME TO import_profiles;
