# Робочий процес однією командою.
#
# Досі він жив у прозі README і чотирьох ad-hoc викликах `sh`, тож людина
# і CI робили те саме різними способами — а найдорожчою була церемонія
# синхронізації UI: правка одного символу в CSS вимагала regen манiфесту
# тут, коміт, пуш, а тоді ще синк і коміт у сусідньому репозиторії.
# Церемонії більше немає разом із панеллю: поверхня одна, цілі `manifest`
# тут немає — правка в web/ нікуди далі не їде.
#
# Ціль `ui` лишилась, але означає геть інше, ніж колись: не синхронізацію
# з сусіднім репозиторієм, а чотири перевірки фронтенду. Збірки в UI немає
# навмисно, тож бандлера, який ловив би описки, теж немає — і відколи
# панель прибрана, ці перевірки ЄДИНА сітка під web/. Доти вони жили лише
# в .github/workflows/ci.yml, тобто про поламане дізнавались після пушу.
#
# Тести: internal/api і internal/store потребують CGO (драйвер SQLite),
# тож без gcc локально бігають лише чисті пакети — `make test-pure`.
#
# `check` навмисно не тягне `ui`: Go і Node — різні набори інструментів, у
# CI це теж дві окремі джоби, і людині без node має лишатись робочий
# `make check`.

GO ?= go

.PHONY: help fmt lint vet test test-pure build check ui

help:
	@echo 'fmt        — gofmt -w .'
	@echo 'lint       — golangci-lint run'
	@echo 'test       — усі тести під -race (потрібен CGO)'
	@echo 'test-pure  — лише пакети без CGO (працює без gcc)'
	@echo 'build      — зібрати oddinvestd'
	@echo 'check      — те, що ганяє CI по Go'
	@echo 'ui         — те, що ганяє CI по web/ (потрібен node)'

fmt:
	$(GO) fmt ./...

lint:
	golangci-lint run ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -race -count=1

# Ті самі пакети, що збираються без C-компілятора.
test-pure:
	$(GO) test ./internal/domain/... ./internal/state/... ./internal/fx/... \
		./internal/nbu/... ./internal/imports/... -count=1

build:
	$(GO) build -o oddinvestd ./cmd/oddinvestd

# Дзеркало джоби `ui` з ci.yml, крок у крок. Порядок від дешевого до
# дорогого: синтаксис кожного модуля, потім чи резолвиться кожен імпорт,
# потім чи визначене кожне вжите імʼя, потім шар токенів CSS.
#
# eslint тут CI-only: ставиться через npx і в застосунок не потрапляє —
# правило «жодних залежностей у web/» не порушене.
ui:
	@cd internal/api/web && for f in $$(find js -name '*.js'); do \
		node --check "$$f" || exit 1; \
	done
	@node web-imports-check.mjs >/dev/null || { node web-imports-check.mjs | grep FAIL; exit 1; }
	npx --yes eslint@9 internal/api/web/js
	node css-tokens-check.mjs

# Те саме, що в .github/workflows/ci.yml, щоб не дізнаватись про поламане
# вже після пушу.
check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo 'запусти: make fmt'; exit 1; }
	$(GO) vet ./...
	golangci-lint run ./...
	@$(MAKE) --no-print-directory fx-boundary
	@$(MAKE) --no-print-directory sources-boundary
	@$(MAKE) --no-print-directory sleeve-state

# fx — ЄДИНА точка конвертації, і масштаб курсу ×10⁴ не має витікати за
# її межі. Витікав: курс ділили на RateScale вручну в шести місцях, а в
# бенчмарку гривню переводили в долар цілочисельним діленням, яке
# систематично применшувало результат. Для тих випадків тепер є
# fx.FromUAH і fx.RateMajor.
.PHONY: fx-boundary
fx-boundary:
	@! grep -rn 'fx\.RateScale' --include='*.go' . \
		|| { echo 'RateScale поза internal/fx: візьми fx.FromUAH або fx.RateMajor'; exit 1; }

# buildState читає сховище ЛИШЕ через sources (state_sources.go) і
# зводить факти ЛИШЕ через Holdings (domain/holdings.go). Доти читання
# були розсипані по всій функції, і ListDeposits через це викликався
# двічі за п'ятсот рядків один від одного — обидва місця були певні, що
# вони єдині. Так само двічі будувалось зведення фондів, і саме тому
# нікого не турбувало, що будівник дописує PayoutDay у першу мапу:
# друга була свіжа. Правило дешеве, поки воно механічне; щойно воно стає
# домовленістю, наступний запит просто дописують поруч.
.PHONY: sources-boundary
sources-boundary:
	@! grep -n 's\.st\.' internal/api/state_builder.go \
		|| { echo 'buildState читає сховище повз sources: додай поле в state_sources.go'; exit 1; }
	@! grep -nE 'domain\.(FundPositions|RemainingQtyNow)\(' internal/api/state_builder.go \
		|| { echo 'buildState зводить факти повз Holdings: візьми hold.Funds / hold.Lots'; exit 1; }

# Шість симуляцій рукавів (проєкція, крива, місяць до цілі, місяць до
# доходу, потрібний внесок, декумуляція) мусять збирати стан ОДНИМ
# конструктором — Sleeve.newState. Доти кожна писала projState літералом
# у себе, і новий кошик капіталу довелось би дописувати в шість місць.
# Забутий в одному не впав би тестом: симуляція просто відповіла б інакше
# на те саме питання, і розійшлись би сусідні картки, а не збірка.
.PHONY: sleeve-state
sleeve-state:
	@! grep -nE 'projState\{|\.step\(' internal/domain/sleeves.go internal/domain/drawdown.go \
		|| { echo 'симуляція рукава чіпає стан повз newState/stepSleeve (projection.go): накопичувальні позиції не виростуть'; exit 1; }
