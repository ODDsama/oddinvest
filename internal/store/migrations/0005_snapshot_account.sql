-- Баланс грошового рахунку на день знімка (мінорні UAH) — щоб малювати
-- окрему лінію «Рахунок» у Динаміці. Старі знімки лишаються з 0.
ALTER TABLE snapshots ADD COLUMN account_uah INTEGER NOT NULL DEFAULT 0;
