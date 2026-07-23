-- Банківські строкові вклади — третій інструмент поряд з ОВДП і фондами.
--
-- Не плутати з таблицею `deposits`: та тримає ПОПОВНЕННЯ рахунку (рух
-- готівки), а ця — сам ІНСТРУМЕНТ (вклад під відсоток на строк).
-- Українською різниця чітка: поповнення vs вклад; у коді top-up лишається
-- `deposits`/`store.Deposit`, вклад — `term_deposits`/`domain.Deposit`.
--
-- Чому власна таблиця, а не lots чи fund_ops: у вкладу немає ні ISIN і
-- номіналу-за-штуку (це разова сума, не N паперів), ні ринкової ціни
-- (немає вторинного ринку). Натомість є фіксована ставка, відомий графік
-- і податок на відсотки — тому й окрема форма.
--
-- payout: коли платять відсотки
--   end        усі в кінці строку
--   monthly    щомісяця на рахунок
--   quarterly  щокварталу на рахунок
-- capitalized: відсотки додаються до тіла (сенс лише при payout=end)
-- rate_bp / tax_bp: ставка та податок × 100 (16.5% = 1650, 19.5% = 1950)
-- closed_date / closed_amount: дострокове розірвання — банк перерахував за
--   штрафною ставкою й видав суму, яку користувач і вводить; NULL = діє.

-- bank — це рахунок (broker), а не вільний текст: розміщення вкладу
-- дебетує його баланс так само, як купівля лота дебетує брокера, тож банк
-- живе в тій самій таблиці brokers і перейменування підхоплюють усі
-- записи разом. NULL = не прив'язано до рахунку.
CREATE TABLE term_deposits (
    id            INTEGER PRIMARY KEY,
    broker_id     INTEGER REFERENCES brokers(id),
    currency      TEXT    NOT NULL DEFAULT 'UAH',
    principal     INTEGER NOT NULL,
    rate_bp       INTEGER NOT NULL,
    open_date     TEXT    NOT NULL,
    maturity_date TEXT    NOT NULL,
    payout        TEXT    NOT NULL DEFAULT 'end',
    capitalized   INTEGER NOT NULL DEFAULT 0,
    tax_bp        INTEGER NOT NULL DEFAULT 1950,
    closed_date   TEXT    NOT NULL DEFAULT '',
    closed_amount INTEGER NOT NULL DEFAULT 0,
    note          TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_term_deposits_maturity ON term_deposits(maturity_date);
