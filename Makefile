# Робочий процес однією командою.
#
# Досі він жив у прозі README і чотирьох ad-hoc викликах `sh`, тож людина
# і CI робили те саме різними способами — а найдорожчою була церемонія
# синхронізації UI: правка одного символу в CSS вимагала regen манiфесту
# тут, коміт, пуш, а тоді ще синк і коміт у сусідньому репозиторії.
# Церемонії більше немає разом із панеллю: поверхня одна, і цілей
# `manifest`/`ui` тут теж немає — правка в web/ нікуди далі не їде.
#
# Тести: internal/api і internal/store потребують CGO (драйвер SQLite),
# тож без gcc локально бігають лише чисті пакети — `make test-pure`.

GO ?= go

.PHONY: help fmt lint vet test test-pure build check

help:
	@echo 'fmt        — gofmt -w .'
	@echo 'lint       — golangci-lint run'
	@echo 'test       — усі тести під -race (потрібен CGO)'
	@echo 'test-pure  — лише пакети без CGO (працює без gcc)'
	@echo 'build      — зібрати oddinvestd'
	@echo 'check      — те, що ганяє CI'

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
