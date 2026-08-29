-- Індекси під зовнішні ключі.
--
-- SQLite (як і Postgres) індексує БАТЬКІВСЬКУ сторону ключа й НЕ індексує
-- дочірню. Тобто `brokers.id` шукається за індексом, а «хто на нього
-- посилається» — повним сканом кожної дочірньої таблиці, і робиться це на
-- кожному DELETE та UPDATE брокера. Шість таблиць посилаються на brokers;
-- індекс мала лише lots (0010), решта п'ять — ні.
--
-- Найчастіший споживач цього скану не кнопка «✕», а ВІДНОВЛЕННЯ:
-- ImportAll витирає всі 26 таблиць, і `DELETE FROM brokers` наприкінці
-- перевіряє кожен рядок кожної дочірньої таблиці. Плюс DeleteBroker, який
-- тепер чесно рахує всі шість.
--
-- Обсяги тут дрібні, і жодного «повільно» ніхто не бачив. Але це рівно та
-- ціна, яку індекс прибирає назавжди одним рядком, а не та, за якою
-- приходять оптимізувати згодом.

CREATE INDEX idx_deposits_broker      ON deposits(broker_id);
CREATE INDEX idx_conversions_broker   ON conversions(broker_id);
CREATE INDEX idx_term_deposits_broker ON term_deposits(broker_id);
CREATE INDEX idx_npf_ops_broker       ON npf_ops(broker_id);
CREATE INDEX idx_fund_ops_broker      ON fund_ops(broker_id);

-- goals (0039) не мала ЖОДНОГО індексу, а читається вона рівно одним
-- запитом і завжди в одному порядку — ListGoals сортує
-- `ORDER BY priority, due_date = '', due_date, id`.
--
-- Індекс повторює цей порядок ЦІЛКОМ, разом із виразом. Спокуса написати
-- просто (priority, due_date) велика й помилкова: EXPLAIN QUERY PLAN на
-- такому індексі каже «SCAN goals USING COVERING INDEX» — і одразу
-- «USE TEMP B-TREE FOR LAST 3 TERMS OF ORDER BY». Тобто індекс читається,
-- а сортування однаково будується в пам'яті: вираз `due_date = ''` стоїть
-- ДРУГИМ, і все, що за ним, індексу вже не належить.
--
-- Вираз тут не оздоба: він виштовхує цілі без строку в кінець списку, і
-- без нього порожній рядок сортувався б перед будь-якою датою, тобто
-- «колись» опинялось би поперед «до березня». Індекси на виразах є і в
-- SQLite, і в Postgres, тож портабельності це не коштує.
--
-- goal_ops індекс уже має (idx_goal_ops_goal), тож FK на goals прикритий.
CREATE INDEX idx_goals_order ON goals(priority, due_date = '', due_date, id);
