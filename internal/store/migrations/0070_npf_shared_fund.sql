-- foreign_keys: off
-- Пенсійний фонд — спільний довідник; рахунок у ньому — портфельний.
--
-- 0054 провела межу «що воно таке» / «що я маю» і лишила спільними funds і
-- fund_prices, а НПФ скоупувала цілком: і назву, і адміністратора, і ЧВОПА
-- з її історією. Це був недогляд, а не рішення. Два портфелі (власник і
-- дружина) тримають ОДИН фонд, ціна одиниці в нього одна на всіх учасників,
-- а вводити її доводилось двічі — і крива, вклеєна в одному портфелі, в
-- другому лишалась порожньою, тобто там «фактична дохідність» мовчала, хоч
-- дані в базі лежали.
--
-- Тепер поділ такий самий, як у фондів:
--
--   npf_funds     — СПІЛЬНЕ: назва, адміністратор, валюта, остання відома
--                   ЧВОПА з датою, обіцяна дохідність (і роки простої);
--   npf_fund_nav  — СПІЛЬНЕ: історія ЧВОПА фонду;
--   npf_accounts  — портфельне: участь людини у фонді — дата доступу,
--                   податок на виплату, ставка знижки, день внеску, строк і
--                   періодичність виплати, нотатка;
--   npf_ops       — портфельне, не чіпається: внески.
--
-- ЗВЕДЕННЯ ЗА ТОЧНОЮ НАЗВОЮ, без NOCASE і без TRIM, і це не лінощі.
-- NOCASE у SQLite згортає лише ASCII: «Династія» й «династія» лишились би
-- різними, тобто він обіцяв би те, чого не робить. TRIM гірший: пара «X» і
-- «X » в ОДНОМУ портфелі сьогодні законна (UNIQUE(portfolio_id, name)), а
-- після зведення впала б на UNIQUE(portfolio_id, fund_id) — і сервіс не
-- стартував би. Точна рівність упасти не може: унікальна назва в портфелі
-- сьогодні дає унікальний фонд у портфелі завтра. Так само зводяться
-- funds (UNIQUE name, fundRef у backup.go). Ціна: назви, що різняться хоч
-- пробілом, дадуть два фонди, а дії «обʼєднати» немає — їх вирівнюють
-- руками ДО цієї міграції.
--
-- ХТО ВИГРАЄ, КОЛИ ПОРТФЕЛІ РОЗХОДЯТЬСЯ.
--   * Атрибути фонду (адміністратор, валюта, обіцянка) — від рахунку з
--     найменшим portfolio_id, тобто від головного: його заводили першим і
--     звіряли найдовше.
--   * Остання відома ЧВОПА — НАЙСВІЖІША за датою, чия б не була: це число
--     модель і так порівнює за датою (domain.NPFPositions), і старіше з
--     двох було б просто втратою.
--   * Точка історії на ту саму дату — головного портфеля. Дві різні ЧВОПА
--     одного фонду на один день — суперечність (шапка 0028), і розвʼязати
--     її можна лише вибором; друга точка зникає.
--
-- ЧОМУ ІСТОРІЯ ЛЯГАЄ В НОВУ ТАБЛИЦЮ, А npf_nav ЗНИКАЄ. Можна було
-- перебудувати npf_nav під тим самим імʼям, без portfolio_id. Але імʼя
-- npf_nav стоїть у переліку скоупованих таблиць (migrate_0054_test.go), і
-- сторож читає текст SQL: будь-який запит до npf_nav мусить нести
-- portfolio_id. Нове імʼя лишає і той перелік, і перевірку 0054 на своїй
-- миті недоторканими — зникла таблиця в переліку вже не нова річ (deposits,
-- conversions, plan_buys, import_profiles). І читач бачить з назви, що
-- це історія ФОНДУ, а не рахунку.
--
-- ЧОМУ ВИМКНЕНІ КЛЮЧІ. npf_accounts — батько npf_ops; DROP TABLE при
-- увімкнених ключах виконав би неявний DELETE і впав би на першому внеску
-- (шапка 0054). id рахунків зберігаються, тож npf_ops.npf_id і текстове
-- «npf:<id>» у plan_flows.dest показують туди ж, куди й показували; раннер
-- перед COMMIT вимагає порожнього foreign_key_check.

CREATE TABLE npf_funds (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT    NOT NULL UNIQUE,
    administrator      TEXT    NOT NULL DEFAULT '',
    currency           TEXT    NOT NULL DEFAULT 'UAH',
    nav_e6             INTEGER NOT NULL DEFAULT 0,
    nav_date           TEXT    NOT NULL DEFAULT '',
    expected_yield_bp  INTEGER NOT NULL DEFAULT 0,
    yield_simple_years INTEGER NOT NULL DEFAULT 0
) STRICT;

INSERT INTO npf_funds (name, administrator, currency, expected_yield_bp,
    yield_simple_years, nav_e6, nav_date)
SELECT a.name, a.administrator, a.currency, a.expected_yield_bp, a.yield_simple_years,
    (SELECT n.nav_e6 FROM npf_accounts n WHERE n.name = a.name
      ORDER BY (n.nav_e6 > 0) DESC, n.nav_date DESC, n.portfolio_id, n.id LIMIT 1),
    (SELECT n.nav_date FROM npf_accounts n WHERE n.name = a.name
      ORDER BY (n.nav_e6 > 0) DESC, n.nav_date DESC, n.portfolio_id, n.id LIMIT 1)
  FROM npf_accounts a
 WHERE a.id = (SELECT m.id FROM npf_accounts m WHERE m.name = a.name
                ORDER BY m.portfolio_id, m.id LIMIT 1)
 ORDER BY a.id;

CREATE TABLE npf_fund_nav (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    fund_id INTEGER NOT NULL REFERENCES npf_funds(id),
    date    TEXT    NOT NULL,
    nav_e6  INTEGER NOT NULL,
    UNIQUE (fund_id, date)
) STRICT;

INSERT INTO npf_fund_nav (id, fund_id, date, nav_e6)
SELECT n.id, f.id, n.date, n.nav_e6
  FROM npf_nav n
  JOIN npf_accounts a ON a.id = n.npf_id
  JOIN npf_funds f ON f.name = a.name
 WHERE NOT EXISTS (
        SELECT 1 FROM npf_nav n2
          JOIN npf_accounts a2 ON a2.id = n2.npf_id
         WHERE a2.name = a.name AND n2.date = n.date
           AND a2.portfolio_id < a.portfolio_id);

DROP TABLE npf_nav;

CREATE TABLE npf_accounts_new (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    portfolio_id   INTEGER NOT NULL DEFAULT 1 REFERENCES portfolios(id) ON DELETE CASCADE,
    fund_id        INTEGER NOT NULL REFERENCES npf_funds(id),
    access_date    TEXT    NOT NULL DEFAULT '',
    income_tax_bp  INTEGER NOT NULL DEFAULT 0,
    credit_rate_bp INTEGER NOT NULL DEFAULT 0,
    contrib_day    INTEGER NOT NULL DEFAULT 0,
    note           TEXT    NOT NULL DEFAULT '',
    payout_years   INTEGER NOT NULL DEFAULT 10,
    payout_freq    TEXT    NOT NULL DEFAULT 'month',
    -- Один рахунок на фонд у портфелі: те саме, що доти казав
    -- UNIQUE(portfolio_id, name), лише мовою нового ключа.
    UNIQUE (portfolio_id, fund_id)
) STRICT;
INSERT INTO npf_accounts_new (id, portfolio_id, fund_id, access_date, income_tax_bp,
    credit_rate_bp, contrib_day, note, payout_years, payout_freq)
SELECT a.id, a.portfolio_id, f.id, a.access_date, a.income_tax_bp,
    a.credit_rate_bp, a.contrib_day, a.note, a.payout_years, a.payout_freq
  FROM npf_accounts a JOIN npf_funds f ON f.name = a.name;

-- Лічильник AUTOINCREMENT переноситься явно. Вставка з готовими id лишає
-- в новій таблиці seq = найбільший ЖИВИЙ id, тобто id рахунку, видаленого
-- колись останнім, дістався б наступному новому. FK цього не помітив би, а
-- plan_flows.dest = 'npf:<id>' — текст без ключа: висячий внесок плану тихо
-- причепився б до чужого рахунку.
DELETE FROM sqlite_sequence WHERE name = 'npf_accounts_new';
INSERT INTO sqlite_sequence (name, seq)
SELECT 'npf_accounts_new', seq FROM sqlite_sequence WHERE name = 'npf_accounts';

DROP TABLE npf_accounts;
ALTER TABLE npf_accounts_new RENAME TO npf_accounts;

-- FK без індексу — повний перегляд на кожному «чи тримає фонд ще хтось»
-- (0042 ставила такі самі). Вибірку за портфелем покриває UNIQUE вище,
-- історію — UNIQUE(fund_id, date).
CREATE INDEX idx_npf_accounts_fund ON npf_accounts(fund_id);
