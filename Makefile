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
	@$(MAKE) --no-print-directory ui-kit-boundary

# Розмітку полів, форм і таблиць пише КИТ, а не розділ.
#
# Правило коштувало дорого, і тримати його на пам'яті не вийде. Доти
# двадцять шість форм і двадцять сім таблиць писались копіюванням
# сусідньої, і копіювалась не лише вдала розмітка: inputmode на сумі стояв
# у більшості форм і був відсутній у кількох, .table-scroll мали дванадцять
# таблиць із двадцяти семи, а трійка валют була вписана руками в семи
# місцях чотирма різними способами. Кожна розбіжність окремо нічого не
# варта; разом вони означають, що «як тут заведено» доводиться щоразу
# з'ясовувати читанням сусіда.
#
# Перевірка навмисно механічна, як fx-boundary поруч: домовленість, яку
# ніхто не перевіряє, живе рівно до наступного поспіху.
#
# Валюти шукаються саме в ЛАПКАХ. Без них перевірка ловила прозу: у
# підписі драбини погашень стоїть «окремо UAH / USD / EUR», і це текст
# для читача, а не список у коді.
#
# Винятків рівно два, і обидва названі поіменно, щоб список не ріс мовчки:
#
#   .pos-table (views/positions.js) — рукотворна картка 3x3 під конкретні сім
#     колонок портфеля; аргумент лежить у base.css при .pos-table.
#   .inline-block (views/settings-view.js) — <label> навколо вибору файла для
#     відновлення з бекапу. Це не поле форми: воно нічого не подає, а тягне
#     обробник onchange. Заводити для нього функцію кита означало б завести
#     абстракцію з одним користувачем — рівно те, від чого це правило й
#     захищає.

.PHONY: ui-kit-boundary
ui-kit-boundary:
	@! grep -nE "<(form|label|table)" internal/api/web/js/views/*.js \
		| grep -vE "pos-table|inline-block" \
		|| { echo "рукописна форма/поле/таблиця у views/: візьми fields.js, refs.js або grid.js"; exit 1; }
	@! grep -rn '"UAH"' internal/api/web/js --include="*.js" \
		| grep '"USD"' | grep -v "constants.js" \
		|| { echo "трійка валют повз CURRENCIES (constants.js)"; exit 1; }
	@! grep -rn 'list="' internal/api/web/js --include="*.js" \
		|| { echo "datalist у shadow root не працює: візьми refSelect (refs.js)"; exit 1; }

# Те саме, що в .github/workflows/ci.yml, щоб не дізнаватись про поламане
# вже після пушу.
check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo 'запусти: make fmt'; exit 1; }
	$(GO) vet ./...
	golangci-lint run ./...
	@$(MAKE) --no-print-directory fx-boundary
	@$(MAKE) --no-print-directory sources-boundary
	@$(MAKE) --no-print-directory sleeve-state
	@$(MAKE) --no-print-directory whatif-boundary

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

# Гіпотеза (покупки, яких ще немає) домішується в стан РІВНО в одному
# місці — buildStateWith, — і збирається рівно в одному — state_plan_buys.go.
# Публічний BuildStateDoc її не приймає навмисно: той документ іде в MQTT
# і щодня лягає в добовий знімок, тож щойн гіпотеза протече повз
# buildStateWith, вигадка буде опублікована як стан. Домовленість, яку
# ніхто не перевіряє, живе до наступного поспіху.
.PHONY: whatif-boundary
whatif-boundary:
	@! grep -rn 'hypothetical' internal/api/*.go \
		| grep -vE 'state_builder\.go|handlers_whatif\.go|state_plan_buys\.go|_test\.go' \
		|| { echo 'гіпотеза протікає повз buildStateWith: у MQTT і знімок іде реальний стан'; exit 1; }
