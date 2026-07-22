-- Довідники замість вільного тексту + конвертація між фондами.
--
-- Що було не так:
--   * брокер лежав рядком у чотирьох таблицях (lots.channel,
--     deposits.broker, conversions.broker, fund_ops.broker), а САМ СПИСОК
--     брокерів — CSV-рядком у settings('channels'). Кілька значень в
--     одній комірці — це порушення навіть 1NF, і перейменувати брокера
--     означало руками пройтись по чотирьох таблицях;
--   * назва фонду так само була рядком у кожній операції: друкарська
--     помилка мовчки розщеплювала позицію надвоє;
--   * fund_ops.currency залежала від ФОНДУ, а не від операції — саме
--     транзитивна залежність, тобто порушення 3NF. Дві операції одного
--     фонду могли суперечити одна одній, і ніщо цього не ловило.
--
-- Чого тут свідомо НЕ чіпаємо: lots.currency і sales.currency. Виглядає
-- як та сама транзитивна залежність від bonds.currency, але bonds — це
-- КЕШ довідника НБУ, і папір може з нього зникнути (погашений випуск не
-- вічно лежить у реєстрі). Лот, що втратив валюту разом із записом
-- довідника, не порахувався б узагалі, тож копія тут — не надлишок, а
-- страховка.
--
-- Чому lots/deposits/conversions правляться через ADD/DROP COLUMN, а не
-- перебудовою таблиці: міграції виконуються В ТРАНЗАКЦІЇ, а PRAGMA
-- foreign_keys усередині транзакції не діє — вимкнути перевірку ключів
-- на час перебудови неможливо. При увімкнених ключах DROP TABLE lots
-- виконав би неявний DELETE FROM lots і каскадом (sales.lot_id ...
-- ON DELETE CASCADE) стер би всю історію продажів. fund_ops натомість
-- перебудовуємо чесно — на нього ніхто не посилається.

CREATE TABLE brokers (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE
) STRICT;

CREATE TABLE funds (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    name     TEXT NOT NULL UNIQUE,
    currency TEXT NOT NULL DEFAULT 'UAH'
) STRICT;

-- --- 1. Заселяємо довідник брокерів усім, що вже зустрічалось ---

INSERT OR IGNORE INTO brokers(name)
SELECT DISTINCT TRIM(channel) FROM lots        WHERE TRIM(channel) <> ''
UNION SELECT DISTINCT TRIM(broker) FROM deposits    WHERE TRIM(broker) <> ''
UNION SELECT DISTINCT TRIM(broker) FROM conversions WHERE TRIM(broker) <> ''
UNION SELECT DISTINCT TRIM(broker) FROM fund_ops    WHERE TRIM(broker) <> '';

-- CSV із settings('channels'): у SQLite немає split, тож розбираємо
-- рекурсивним CTE. Кома в кінці — щоб останній елемент теж мав роздільник.
WITH RECURSIVE csv(head, rest) AS (
    SELECT '', COALESCE((SELECT value FROM settings WHERE key = 'channels'), '') || ','
    UNION ALL
    SELECT TRIM(SUBSTR(rest, 1, INSTR(rest, ',') - 1)),
           SUBSTR(rest, INSTR(rest, ',') + 1)
    FROM csv WHERE INSTR(rest, ',') > 0
)
INSERT OR IGNORE INTO brokers(name) SELECT head FROM csv WHERE head <> '';

-- --- 2. Довідник фондів; валюта — з останньої операції фонду ---

INSERT OR IGNORE INTO funds(name, currency)
SELECT d.fund,
       (SELECT o.currency FROM fund_ops o
         WHERE o.fund = d.fund ORDER BY o.date DESC, o.id DESC LIMIT 1)
  FROM (SELECT DISTINCT fund FROM fund_ops WHERE TRIM(fund) <> '') d;

-- --- 3. lots.channel -> lots.broker_id ---

ALTER TABLE lots ADD COLUMN broker_id INTEGER REFERENCES brokers(id);
UPDATE lots SET broker_id = (SELECT b.id FROM brokers b WHERE b.name = TRIM(lots.channel));
ALTER TABLE lots DROP COLUMN channel;
CREATE INDEX idx_lots_broker ON lots(broker_id);

-- --- 4. deposits/conversions.broker -> broker_id ---

ALTER TABLE deposits ADD COLUMN broker_id INTEGER REFERENCES brokers(id);
UPDATE deposits SET broker_id = (SELECT b.id FROM brokers b WHERE b.name = TRIM(deposits.broker));
ALTER TABLE deposits DROP COLUMN broker;

ALTER TABLE conversions ADD COLUMN broker_id INTEGER REFERENCES brokers(id);
UPDATE conversions SET broker_id = (SELECT b.id FROM brokers b WHERE b.name = TRIM(conversions.broker));
ALTER TABLE conversions DROP COLUMN broker;

-- --- 5. fund_ops: перебудова під fund_id/broker_id + пара конвертації ---
--
-- pair_id зв'язує дві операції в один переказ між фондами. Досі
-- конвертацію не було чим виразити: пара «продаж + купівля» без зв'язку
-- двічі рухала гаманець і нараховувала неіснуючий результат продажу.
-- Саме через це імпорт виписки Inzhur пропускав рядки «Конвертація», а
-- позиції закритих фондів лишались висіти як живі.

CREATE TABLE fund_ops_new (
    id        INTEGER PRIMARY KEY,
    date      TEXT    NOT NULL,
    fund_id   INTEGER NOT NULL REFERENCES funds(id),
    kind      TEXT    NOT NULL CHECK (kind IN ('buy','sell','dividend')),
    qty       INTEGER NOT NULL DEFAULT 0,
    amount    INTEGER NOT NULL,
    tax       INTEGER NOT NULL DEFAULT 0,
    broker_id INTEGER REFERENCES brokers(id),
    pair_id   INTEGER REFERENCES fund_ops_new(id),
    note      TEXT    NOT NULL DEFAULT ''
) STRICT;

INSERT INTO fund_ops_new (id, date, fund_id, kind, qty, amount, tax, broker_id, note)
SELECT o.id, o.date,
       (SELECT f.id FROM funds f WHERE f.name = o.fund),
       o.kind, o.qty, o.amount, o.tax,
       (SELECT b.id FROM brokers b WHERE b.name = TRIM(o.broker)),
       o.note
  FROM fund_ops o;

DROP INDEX IF EXISTS idx_fund_ops_fund_date;
DROP TABLE fund_ops;
ALTER TABLE fund_ops_new RENAME TO fund_ops;

CREATE INDEX idx_fund_ops_fund_date ON fund_ops(fund_id, date);
CREATE INDEX idx_fund_ops_pair ON fund_ops(pair_id);

-- --- 6. CSV-список брокерів більше не потрібен ---

DELETE FROM settings WHERE key = 'channels';
