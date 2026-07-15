# ODD Invest

Домашній сервіс обліку інвестиційного портфеля: власний веб-UI, REST API,
довідник паперів з відкритого API НБУ і публікація стану портфеля в
MQTT для інтеграції з Home Assistant
(окремий репозиторій — `ha-oddinvest`, кастомна інтеграція).

Наразі підтримується клас ОВДП; архітектура розрахована на розширення
на інші інструменти.

Концепція і зафіксовані архітектурні рішення — `ODD-Invest_концепція.md`.

## Можливості (MVP v0.1)

- Портфель покупок (лоти) + продажі на вторинному ринку з валідацією
  залишків, оцінкою НКД (ACT/ACT) і реалізованим результатом.
- Кеш повного довідника ЦП НБУ з графіками виплат; автокомпліт по ISIN.
- Календар майбутніх виплат з урахуванням продажів
  (виплата в день продажу — продавцю; строго після — покупцю).
- Драбина погашень за роками, окремо UAH/USD.
- Добовий знімок агрегатів (основа майбутнього графіка «факт vs модель»).
- Retained-стан у MQTT: `{prefix}/state` + LWT `{prefix}/availability`.

## Грошова модель

- `Rhymond/go-money`: суми — цілі мінорні одиниці + валюта; операції
  між різними валютами падають помилкою, а не рахуються тихо.
- `internal/fx` — єдина точка конвертації: курси цілими ×10⁴,
  `big.Rat`, банківське заокруглення (half-to-even).
- JSON НБУ парситься через `json.Number` → `big.Rat` → мінорні
  одиниці. `float64` з'являється лише на межі відображення
  (документ `oddinvest/state`, суми в мажорних одиницях).

## Збірка

```sh
go mod tidy   # перший раз: підтягне залежності і згенерує go.sum
go test ./...
go build -o oddinvestd ./cmd/oddinvestd
```

Потрібен CGO (SQLite-драйвер `mattn/go-sqlite3`): `gcc` має бути в
системі. Якщо колись знадобиться CGO-free збірка — заміна на
`modernc.org/sqlite` це один імпорт у `internal/store/store.go`.

## Конфігурація (env)

| Змінна | Типово | Опис |
|---|---|---|
| `ODDINVEST_HTTP_ADDR` | `:8080` | адреса HTTP |
| `ODDINVEST_DB_PATH` | `/var/lib/oddinvestd/oddinvest.db` | шлях до SQLite |
| `ODDINVEST_MQTT_ADDR` | — | `tcp://host:1883`; порожньо = MQTT вимкнено |
| `ODDINVEST_MQTT_USER` / `ODDINVEST_MQTT_PASS` | — | облікові дані брокера |
| `ODDINVEST_MQTT_PREFIX` | `oddinvest` | префікс топіків |
| `ODDINVEST_NBU_BASE` | `https://bank.gov.ua` | база API НБУ (для тестів) |

## Деплой (LXC + systemd)

```sh
# у контейнері
install -o root -g root -m 755 oddinvestd /usr/local/bin/
useradd -r oddinvestd
install -m 644 deploy/systemd/oddinvestd.service /etc/systemd/system/
systemctl enable --now oddinvestd
```

MQTT-креденшели розкоментувати в unit-файлі. `StateDirectory=oddinvestd`
створить `/var/lib/oddinvestd` автоматично.

## Контракт з інтеграцією HA

- `contract/oddinvest-state.schema.json` — JSON Schema (draft 2020-12)
  retained-повідомлення `{prefix}/state`.
- `contract/fixtures/*.json` — фікстури, згенеровані кодом
  (`go test ./internal/state -update`); тест `TestFixtureUpToDate`
  не дає їм розійтися з кодом.
- Еволюція: тільки додавання полів; зміна семантики поля = інкремент
  `schema`. CI репозиторію `ha-oddinvest` ганяє свої парсери проти цих фікстур.

## Що перевірити на живому API НБУ

Фікстура `internal/nbu/testdata/securities_sample.json` складена за
документацією НБУ; перед першим продовим запуском варто зняти реальну
відповідь і звірити:

```sh
curl 'https://bank.gov.ua/NBUStatService/v1/statdirectory/securities?json' | head -c 2000
```

Парсер дат приймає ISO / `DD.MM.YYYY` / `YYYYMMDD` (з `T…`-суфіксом
включно), але якщо формат полів інший — правиться в одному місці,
`internal/nbu/client.go: parseNBUDate`.

## Структура

```
cmd/oddinvestd/          — wiring, graceful shutdown
internal/domain/    — чиста доменна логіка + тести (календар, НКД,
                      результат продажу, драбина, позиції)
internal/fx/        — конвертація валют (єдина точка)
internal/nbu/       — клієнт API НБУ (json.Number, без float64)
internal/store/     — SQLite: STRICT-таблиці, вбудовані міграції
internal/state/     — збірка документа oddinvest/state (контракт)
internal/api/       — REST + вбудований веб-UI (go:embed)
internal/mqtt/      — paho-паблішер з LWT
internal/jobs/      — добове оновлення НБУ, знімки, публікація
contract/           — JSON Schema + фікстури для репо ha-oddinvest
deploy/             — systemd unit
```

## Роадмап

- **v0.2** (зроблено): поле `calendar` у контракті — повний горизонт
  майбутніх виплат для calendar-платформи HA; статуси виплат і
  blueprints — на боці ha-oddinvest.
- **v1.0** (зроблено): XIRR по валютах (бісекція, ACT/365, залишок за
  номіналом, не рахується до 30 днів історії) — `GET /api/xirr` і поле
  `xirr` у контракті; `GET /api/snapshots`; `GET /api/export/csv?year=`
  (';' + BOM під україномовний Excel); вкладка «Динаміка» в UI.
