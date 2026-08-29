-- payment_status: прибрати мертвий статус із CHECK.
--
-- 0017 названа «drop_reinvested_status», але її тіло — один UPDATE:
-- значення прибрали з ДАНИХ і забули в СХЕМІ. Обмеження з 0001 усі ці
-- міграції дозволяло писати 'reinvested' далі, і найкоротший шлях, яким
-- воно могло повернутись, — POST /api/restore зі старого бекапу: дамп
-- тримає статуси сирими, а перевірити їх нікому.
--
-- Наслідок не косметичний. Домен розрізняє два стани виплати, а база
-- дозволяла три, і третій читався б застосунком як «не отримано» —
-- тобто гроші, що вже прийшли, знову опинялись би в календарі майбутніх.
--
-- Перебудова тут ЗАКОННА, і це рідкісний випадок. Заборона з 0010
-- («DROP TABLE усередині транзакції при увімкнених FK виконає неявний
-- DELETE і забере дітей каскадом») стосується таблиць, на які хтось
-- посилається. На payment_status не посилається ніхто: її ключ —
-- (isin, pay_date), і зв'язок із виплатою тримається значеннями, а не
-- зовнішнім ключем, бо самої виплати може вже не бути в довіднику НБУ.
-- Коментарів у CREATE TABLE вона теж не несе, тож перебудова тут нічого
-- не втрачає — на відміну від plan_buys поруч (0043).
--
-- Порядок сходиться навмисно: спершу дані (їх 0017 уже виправила, але
-- бекап міг повернути старі), потім схема.

UPDATE payment_status SET status = 'received' WHERE status <> 'received';

CREATE TABLE payment_status_new (
    isin       TEXT NOT NULL,
    pay_date   TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('received')),
    marked_at  TEXT NOT NULL,
    PRIMARY KEY (isin, pay_date)
) STRICT;

INSERT INTO payment_status_new (isin, pay_date, status, marked_at)
SELECT isin, pay_date, status, marked_at FROM payment_status;

DROP TABLE payment_status;
ALTER TABLE payment_status_new RENAME TO payment_status;
