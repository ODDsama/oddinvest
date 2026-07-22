-- Сертифікати фондів (Inzhur REIT і подібні) поруч із ОВДП.
--
-- Це принципово інший інструмент, і складати його в таблицю lots не можна:
-- у сертифіката немає ні дати погашення, ні номіналу, ні графіка купонів —
-- усе, на чому тримається наявна модель облігацій. Натомість він має
-- ринкову ціну, яка змінюється, нерегулярні дивіденди й податок на них
-- (ОВДП від податку звільнені, фонди — ні).
--
-- Один журнал операцій замість пари «лоти + продажі»: у безстрокового
-- інструмента немає сенсу тягнути зв'язок продажу з конкретною купівлею,
-- позиція — це просто сальдо, а собівартість — середньозважена.
--
-- kind:
--   buy      купівля,   qty > 0, amount = сплачено
--   sell     продаж,    qty > 0, amount = отримано, tax = податок з прибутку
--   dividend нарахування, qty = 0, amount = брутто, tax = утриманий податок
--
-- Ціна за сертифікат навмисно не зберігається: вона завжди amount/qty, і
-- окреме поле лише дало б їм розійтись.

CREATE TABLE fund_ops (
    id       INTEGER PRIMARY KEY,
    date     TEXT    NOT NULL,
    fund     TEXT    NOT NULL,
    kind     TEXT    NOT NULL,
    qty      INTEGER NOT NULL DEFAULT 0,
    amount   INTEGER NOT NULL,
    tax      INTEGER NOT NULL DEFAULT 0,
    currency TEXT    NOT NULL DEFAULT 'UAH',
    broker   TEXT    NOT NULL DEFAULT '',
    note     TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_fund_ops_fund_date ON fund_ops(fund, date);
