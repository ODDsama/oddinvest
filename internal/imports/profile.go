package imports

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Розбір виписки за ПРОФІЛЕМ: людина каже, у якій колонці що лежить, і як
// її брокер називає операції.
//
// ЧОМУ ЦЕ НЕ ЗАМІНИЛО РОЗБІР INZHUR. Довгий аргумент лежить у шапці
// міграції 0036, коротко: у виписці Inzhur кількість сертифікатів
// сидить УСЕРЕДИНІ тексту операції, податок стоїть окремим рядком і має
// прилипнути до найближчої оподатковуваної події того самого фонду, а
// порядок задає серійний номер Excel, бо в даті немає часу. Виразити це
// профілем можна лише вбудувавши в нього три гачки, потрібні рівно
// одному брокерові. Ціна такого рішення названа там же: два шляхи
// розбору замість одного.
//
// ЩО СПІЛЬНЕ — те, що справді спільне: рядок результату (Row), пропуски з
// причиною (Skipped), розбір грошей і дат. Усе, що йде ПІСЛЯ розбору —
// перегляд, дедуплікація, водяний знак, виявлення конфліктів із ручними
// рухами — формату не знає взагалі й лишилось одне на обидва шляхи.
//
// ЧОГО ТУТ СВІДОМО НЕМАЄ.
//
// Автовизначення колонок за назвами шапки. Спокусливо й ненадійно:
// «Сума» буває і дебетом, і кредитом, а «Дата» — і датою операції, і
// датою розрахунку. Помилка тут тиха — виписка заходить, числа виглядають
// правдоподібно й лягають не туди. Профіль задають руками ОДИН раз, і
// далі він працює щомісяця.
//
// Податку окремим рядком. У Inzhur він є, тут — ні: щоб прилипнути до
// своєї події, податок мусить знати, ЯКА подія його, а без спільного
// ключа (фонд + доба) це вгадування. Брокер, у якого податок лежить своєю
// колонкою, опише її як звичайну колонку суми; брокер, у якого він
// окремим рядком, отримає пропуск із причиною — тобто побачить проблему,
// а не тихо втратить гроші.
//
// Конвертацій між фондами. Той самий випадок, що в Inzhur: у рядку немає
// кількості сертифікатів, тож позицію з нього не відновити.

// Profile — як читати чужу виписку.
//
// Індекси 0-based; -1 означає «колонки немає». Kinds — словник
// «як брокер називає операцію» → «чим вона є у нас».
type Profile struct {
	Name   string
	Header int
	Date   int
	Op     int
	Ref    int
	Qty    int
	Debit  int
	Credit int
	// Balance/MCC — колонки виписки КАРТКИ: залишок після операції (зі
	// знаком, як у файлі) і код категорії. -1 у брокерської виписки.
	Balance int
	MCC     int
	// Card — це виписка картки (профіль привʼязаний до картки). Явна
	// ознака, а не «є колонка залишку»: нульове значення індексу — теж
	// колонка, і профіль-літерал без залишку читався б як картковий.
	Card  bool
	Kinds map[string]string
}

// Види операцій, які профіль уміє називати. Той самий словник, що в
// Row.Kind: третій набір слів довелось би перекладати на кожному кроці.
//
// Три карткові види (card_*) описують ВИПИСКУ КАРТКИ, а не брокера:
// card_in — надходження на картку (зарплата, переказ) → платіж по
// картці; card_cash — зняття готівки чи переказ із картки → готівка з
// ліміту, під відсотком з першого дня; card_out — покупка: у журнал не
// пишеться (покупки вже сидять у балансі звірки), лише сумується як
// витрати.
var profileKinds = map[string]bool{
	"fund_buy": true, "fund_sell": true, "dividend": true,
	"deposit": true, "withdrawal": true, "bond_buy": true,
	"card_in": true, "card_cash": true, "card_out": true,
}

// KindsList — перелік видів для повідомлень і довідки, у сталому порядку.
const KindsList = "fund_buy, fund_sell, dividend, deposit, withdrawal, bond_buy, card_in, card_cash, card_out"

// IsCardKind — чи вид належить виписці картки.
func IsCardKind(kind string) bool {
	return kind == "card_in" || kind == "card_cash" || kind == "card_out"
}

// IsCard — профіль читає виписку картки.
func (p Profile) IsCard() bool { return p.Card }

// cashMCC — коди категорій, за якими рух із картки є готівкою або
// переказом, тобто під відсотком з першого дня. 6010 — видача готівки в
// касі, 6011 — банкомат.
var cashMCC = map[string]bool{"6010": true, "6011": true}

// cardKindBySign — вид рядка виписки картки зі знаку суми й MCC.
func cardKindBySign(debit, credit int64, mcc string) string {
	signed := debit
	if signed == 0 {
		signed = credit
	}
	switch {
	case signed > 0:
		return "card_in"
	case signed < 0 && cashMCC[strings.TrimSpace(mcc)]:
		return "card_cash"
	case signed < 0:
		return "card_out"
	}
	return ""
}

// ParseOps розбирає словник операцій із того вигляду, у якому його пише
// людина: рядки «фраза = kind», порожні й закоментовані пропускаються.
//
// Порівняння фраз — за ПРЕФІКСОМ і без огляду на регістр: брокери
// дописують у кінець номер договору, назву паперу й дату, а початок
// рядка сталий. Точний збіг вимагав би від людини вгадати весь хвіст.
func ParseOps(text string) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		phrase, kind, ok := strings.Cut(line, "=")
		phrase, kind = strings.TrimSpace(phrase), strings.TrimSpace(kind)
		if !ok || phrase == "" || kind == "" {
			return nil, fmt.Errorf("рядок %d: очікували «фраза = вид», маємо %q", i+1, line)
		}
		if !profileKinds[kind] {
			return nil, fmt.Errorf("рядок %d: невідомий вид %q — буває %s", i+1, kind, KindsList)
		}
		out[strings.ToLower(phrase)] = kind
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("словник операцій порожній — без нього жоден рядок виписки не впізнати")
	}
	return out, nil
}

// Parse розбирає аркуш за профілем.
//
// Порядок рядків беремо ТОЙ, ЯКИЙ У ФАЙЛІ, і лише перевертаємо його, якщо
// файл іде від новішого до старішого. Сортувати за датою не можна: у даті
// немає часу, а дві операції одного дня (продаж і купівля за ті самі
// гроші) мусять лишитись у своєму порядку — інакше дедуплікація й
// виявлення конфліктів побачать іншу картину, ніж була насправді.
func Parse(rows [][]string, p Profile) (Result, error) {
	var res Result
	if p.Date < 0 || p.Op < 0 {
		return res, fmt.Errorf("профіль %q: колонки дати й операції обовʼязкові", p.Name)
	}
	if len(p.Kinds) == 0 {
		return res, fmt.Errorf("профіль %q: словник операцій порожній", p.Name)
	}
	if p.Header < 0 || p.Header >= len(rows) {
		return res, fmt.Errorf("порожня виписка: рядків %d, шапка %d", len(rows), p.Header)
	}
	body := rows[p.Header:]

	type parsed struct {
		date domain.Date
		cell func(int) string
	}
	items := make([]parsed, 0, len(body))
	for _, r := range body {
		row := r
		cell := func(i int) string {
			if i < 0 || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		if cell(p.Op) == "" && cell(p.Date) == "" {
			continue // порожній рядок-роздільник, яких у виписках вистачає
		}
		date, err := excelDate(cell(p.Date))
		if err != nil {
			res.Skipped = append(res.Skipped, Skipped{cell(p.Date), cell(p.Op), "не розпізнав дату"})
			continue
		}
		items = append(items, parsed{date, cell})
	}
	if len(items) > 1 && items[0].date.After(items[len(items)-1].date) {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}

	for _, it := range items {
		cell, date := it.cell, it.date
		op := cell(p.Op)
		kind := p.match(op)
		debit, credit := money(cell(p.Debit)), money(cell(p.Credit))
		if kind == "" && p.IsCard() {
			// ВИПИСКА КАРТКИ: опис операції — назва крамниці, і словник
			// фраз їх не перелічить. Вид виводиться зі ЗНАКУ й MCC:
			// плюс — надходження на картку; 6010/6011 — готівка (під
			// відсотком з першого дня); решта мінусів — покупка. Словник
			// лишається старшим: «Переказ на картку = card_cash» назве
			// переказ готівкою там, де за MCC він був би покупкою.
			kind = cardKindBySign(debit, credit, cell(p.MCC))
		}
		if kind == "" {
			res.Skipped = append(res.Skipped, Skipped{string(date), op, "невідомий тип операції"})
			continue
		}
		// Сума — та з двох колонок, у якій щось є. Напрямок задає ВИД
		// операції, а не знак: брокери пишуть списання то в кредит, то
		// зі знаком мінус, і довіритись знаку означало б отримати
		// поповнення там, де була купівля.
		amount := debit
		if amount == 0 {
			amount = credit
		}
		if amount < 0 {
			amount = -amount
		}
		if amount == 0 {
			res.Skipped = append(res.Skipped, Skipped{string(date), op, "не знайшов суми"})
			continue
		}

		row := Row{Date: date, Kind: kind, Amount: amount, Note: op}
		switch kind {
		case "deposit", "withdrawal":
			// Рух грошей паперу не має — і не мусить: колонка ref у такому
			// рядку зазвичай порожня або тримає призначення платежу.
		case "card_in", "card_cash", "card_out":
			// Виписка картки: залишок після операції ЗІ ЗНАКОМ, як у файлі
			// (що означає знак — вирішує людина в превʼю, не розбирач), і
			// MCC текстом. Обидва необовʼязкові.
			row.Balance, row.HasBalance = money(cell(p.Balance)), p.Balance >= 0 && cell(p.Balance) != ""
			row.MCC = cell(p.MCC)
		case "bond_buy":
			isin := isinRe.FindString(cell(p.Ref))
			if isin == "" {
				res.Skipped = append(res.Skipped, Skipped{string(date), op, "не знайшов ISIN у назві паперу"})
				continue
			}
			row.Fund = isin
			if row.Qty = qtyOf(cell(p.Qty)); row.Qty <= 0 {
				res.Skipped = append(res.Skipped, Skipped{string(date), op, "не розпізнав кількість облігацій"})
				continue
			}
		default: // fund_buy, fund_sell, dividend
			if row.Fund = cell(p.Ref); row.Fund == "" {
				res.Skipped = append(res.Skipped, Skipped{string(date), op, "не знайшов назви фонду"})
				continue
			}
			// Дивіденд кількості не має — він приходить грошима на наявні
			// сертифікати. Вимагати її означало б відкинути цілий вид.
			if kind != "dividend" {
				if row.Qty = qtyOf(cell(p.Qty)); row.Qty <= 0 {
					res.Skipped = append(res.Skipped, Skipped{string(date), op, "не розпізнав кількість сертифікатів"})
					continue
				}
			}
		}
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}

// match — знайти вид за початком рядка операції.
//
// Найдовший збіг, а не перший-ліпший: «Купівля» і «Купівля облігацій»
// цілком можуть стояти в одному словнику, і коротший префікс не має
// перехоплювати те, що людина описала точніше.
func (p Profile) match(op string) string {
	low := strings.ToLower(strings.TrimSpace(op))
	best, bestLen := "", 0
	for phrase, kind := range p.Kinds {
		if len(phrase) > bestLen && strings.HasPrefix(low, phrase) {
			best, bestLen = kind, len(phrase)
		}
	}
	return best
}

// qtyOf — кількість із колонки. Дробову частину відкидаємо: сертифікати й
// облігації купують штуками, і «12,00» у виписці означає дванадцять, а не
// привід для помилки.
func qtyOf(s string) int64 {
	s = strings.NewReplacer(" ", "", " ", "", ",", ".").Replace(strings.TrimSpace(s))
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(f)
}
