-- Брокер (mono / inzhur / …) на кожному русі грошей.
--
-- Досі гаманець був спільним: баланси рахувались лише по валютах, тож
-- застосунок вважав, що гривня в одного брокера може купити папір в
-- іншого. Насправді рахунки роздільні, і «чи вистачає на папір» треба
-- рахувати окремо для кожного.
--
-- У лотів брокер уже є (колонка channel). Тут додаємо його поповненням і
-- конвертаціям і засіваємо наявні записи найчастішим брокером із лотів —
-- інакше вся історія повисла б «без брокера».

ALTER TABLE deposits ADD COLUMN broker TEXT NOT NULL DEFAULT '';
ALTER TABLE conversions ADD COLUMN broker TEXT NOT NULL DEFAULT '';

UPDATE deposits SET broker = COALESCE(
    (SELECT channel FROM lots WHERE channel <> ''
     GROUP BY channel ORDER BY COUNT(*) DESC LIMIT 1), '')
WHERE broker = '';

UPDATE conversions SET broker = COALESCE(
    (SELECT channel FROM lots WHERE channel <> ''
     GROUP BY channel ORDER BY COUNT(*) DESC LIMIT 1), '')
WHERE broker = '';
