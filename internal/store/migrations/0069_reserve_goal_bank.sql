-- Установа в русі резерву й цілі: посилання на довідник brokers поруч із
-- вільним «місцем».
--
-- ДОТИ place був єдиним полем, і довід 0020 («за ним нічого не рахується —
-- це підпис для людини») тримався рівно доти, доки за ним справді нічого
-- не рахувалось. Тепер рахується: картка «В одній установі» питає «скільки я
-- втрачу, якщо ця установа зникне», і євро подушки на картці банку стоїть
-- за тим самим банком, що й строковий вклад у ньому. Вільний текст
-- «моно» / «monobank» / «mono» не зводиться в один рядок концентрації, а
-- посилання — зводиться, і перейменування брокера його не розриває.
--
-- PLACE НЕ ЗНИКАЄ. Готівка й сейф — не установа: за ними немає
-- контрагента, який міг би зникнути, і заводити для них «брокера» означало
-- б вигадати сутність. Рух без установи (broker_id NULL) так і лишається
-- «місцем», а картка концентрації показує такі гроші окремим рядком без
-- ліміту.
--
-- Дозаповнення: місце, що дослівно (з точністю до регістру латиниці —
-- NOCASE SQLite кирилицю не зводить) збігається з назвою брокера свого ж
-- портфеля, стає посиланням, а текст очищається. Решта лишається текстом;
-- перепризначити її — одна правка руху.
ALTER TABLE reserve_ops ADD COLUMN broker_id INTEGER REFERENCES brokers(id);
ALTER TABLE goal_ops    ADD COLUMN broker_id INTEGER REFERENCES brokers(id);

UPDATE reserve_ops SET
    broker_id = (SELECT b.id FROM brokers b
                 WHERE b.portfolio_id = reserve_ops.portfolio_id
                   AND b.name = trim(reserve_ops.place) COLLATE NOCASE),
    place = ''
WHERE EXISTS (SELECT 1 FROM brokers b
              WHERE b.portfolio_id = reserve_ops.portfolio_id
                AND b.name = trim(reserve_ops.place) COLLATE NOCASE);

UPDATE goal_ops SET
    broker_id = (SELECT b.id FROM brokers b
                 WHERE b.portfolio_id = goal_ops.portfolio_id
                   AND b.name = trim(goal_ops.place) COLLATE NOCASE),
    place = ''
WHERE EXISTS (SELECT 1 FROM brokers b
              WHERE b.portfolio_id = goal_ops.portfolio_id
                AND b.name = trim(goal_ops.place) COLLATE NOCASE);
