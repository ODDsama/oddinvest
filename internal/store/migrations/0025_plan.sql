-- Джерела доходу й планові дії — фаза 9 «План».
--
-- Уся майбутність застосунку доти трималась на ОДНОМУ числі:
-- Sleeve.ContribUAH, постійний місячний внесок, виведений із цілі й
-- дедлайну або виміряний фактом за півроку. Сказати «зарплата з вересня»,
-- «премія в грудні», «ремонт −250 тис. у березні 2027» не було чим.
--
-- Дві таблиці, а не одна з полем kind='flow'|'action': рядки не схожі за
-- формою (потік — сума на період, дія — подія на дату), і спільна
-- таблиця означала б NULL-поля одне під одного.
--
-- Широка таблиця замість params TEXT (JSON) — навмисно. Типів дії лише
-- два, решта бази теж STRICT-типізована колонка за колонкою, а JSON у
-- TEXT не грепається, не валідується схемою СУБД і мовчки ковтає описку
-- в імені поля.

-- plan_flows — регулярні або разові гроші: дохід чи витрата, з датою й
-- періодичністю. amount ЗАВЖДИ додатне; напрям задає kind, а не знак —
-- та сама причина, що в term_deposits: одна колонка не повинна відповідати
-- одразу на два питання.
CREATE TABLE plan_flows (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    kind       TEXT    NOT NULL,               -- income | expense
    amount     INTEGER NOT NULL,               -- мінорні, > 0
    currency   TEXT    NOT NULL DEFAULT 'UAH',
    cadence    TEXT    NOT NULL,               -- once | month | quarter | year
    from_date  TEXT    NOT NULL,
    until_date TEXT    NOT NULL DEFAULT '',    -- '' = безстроково
    -- growth_bp — індексація, %/рік × 100. Застосовується лише до
    -- регулярних потоків (cadence != once): зарплата з часом росте,
    -- разова виплата — ні, бо в неї немає «наступного разу», де зростання
    -- мало б сенс.
    growth_bp  INTEGER NOT NULL DEFAULT 0,
    -- invest_bp — яка частка ЦЬОГО потоку доходить до портфеля, %×100.
    -- За замовчуванням 100%: типовий випадок — «уся зарплата йде в
    -- інвестиції» — не має вимагати від людини вписувати очевидне число.
    -- НЕ плутати з monthly_expenses_uah у налаштуваннях: те поле рахує
    -- достатність резерву й декумуляцію, а не наповнення плану, і задавати
    -- обидва про той самий кошик означало б відняти витрату двічі.
    invest_bp  INTEGER NOT NULL DEFAULT 10000,
    note       TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_plan_flows_from ON plan_flows(from_date);

-- plan_actions — точкові рішення на дату, у термінах ВАЛЮТНОГО РУКАВА, а
-- не позиції: проєкція не знає ISIN, вона знає лише валюту. Двох типів
-- досить, щоб описати і «змінити цільові частки з дати», і «покласти суму
-- під ставку на строк» (вклад чи накопичувальний фонд однаково —
-- проєкції байдуже, як називається інструмент, який просто лежить і
-- платить за графіком).
--
-- usd_bp/eur_bp за замовчуванням -1 ("не задано"), а не 0: нуль тут
-- легальне значення ("долара в портфелі не лишається зовсім"), і
-- NOT NULL DEFAULT 0 тихо означав би протилежне тому, що малось на увазі.
CREATE TABLE plan_actions (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    date     TEXT    NOT NULL,
    type     TEXT    NOT NULL,                 -- set_shares | lock
    usd_bp   INTEGER NOT NULL DEFAULT -1,
    eur_bp   INTEGER NOT NULL DEFAULT -1,
    amount   INTEGER NOT NULL DEFAULT 0,       -- lock: сума, мінорні
    currency TEXT    NOT NULL DEFAULT 'UAH',
    rate_bp  INTEGER NOT NULL DEFAULT 0,       -- lock: ставка, %×100
    months   INTEGER NOT NULL DEFAULT 0,       -- lock: строк; 0 = безстроково
    name     TEXT    NOT NULL DEFAULT '',      -- підпис для стрічки
    note     TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_plan_actions_date ON plan_actions(date);
