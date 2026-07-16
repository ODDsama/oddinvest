-- Поповнення грошового рахунку (гаманця), мінорні одиниці UAH. Баланс
-- рахунку рахується як Σ поповнень + Σ отриманих виплат − Σ вартості лотів
-- (усе в грн-екв.); ця таблиця тримає лише ручні поповнення/зняття.
CREATE TABLE deposits (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    date    TEXT    NOT NULL,
    amount  INTEGER NOT NULL,           -- + поповнення / − зняття, UAH-мінорні
    note    TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_deposits_date ON deposits(date);
