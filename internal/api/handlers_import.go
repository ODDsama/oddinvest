// Імпорт виписки Inzhur: попередній перегляд і застосування.

package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/imports"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

type outRow struct {
	Date   string `json:"date"`
	Kind   string `json:"kind"`
	Fund   string `json:"fund,omitempty"`
	Qty    int64  `json:"qty,omitempty"`
	Amount string `json:"amount"`
	Tax    string `json:"tax,omitempty"`
	Exists bool   `json:"exists"`
	// Conflict — та сама сума вже лежить у гаманці ручним рухом.
	// Поки обліку фондів не було, купівлі й продажі сертифікатів
	// доводилось записувати як поповнення/зняття; тепер операція фонду
	// теж рухає гаманець, тож стара пара стала б подвійним рахунком.
	Conflict string `json:"conflict,omitempty"`
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// handleImportInzhur приймає xlsx-виписку.
//
// Два режими одним ендпойнтом: ?dry=1 лише показує, що буде зроблено, без
// запису. Стан між викликами не зберігаємо — файл лежить у користувача,
// і повторно надіслати його дешевше, ніж тримати серверну сесію, яку
// потім треба протухати.
//
// Дедуплікація обов'язкова: щомісячна виписка містить і старі рядки, тож
// без неї другий імпорт подвоїв би позицію.
func (s *Server) handleImportInzhur(w http.ResponseWriter, r *http.Request) {
	dry := r.URL.Query().Get("dry") == "1"
	broker := strings.TrimSpace(r.URL.Query().Get("broker"))
	if broker == "" {
		broker = "inzhur"
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("очікували файл у полі file: %w", err))
		return
	}
	defer file.Close()
	buf, err := io.ReadAll(io.LimitReader(file, 16<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sheet, err := imports.ReadXLSX(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	res, err := imports.ParseInzhur(sheet)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	ctx := r.Context()
	// Помилку тут ковтати не можна: порожній набір означав би «нічого не
	// бачив раніше», і вся виписка лягла б дублями поверх наявного.
	deps, err := s.st.ListDeposits(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	lots, err := s.st.ListLots(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Лот вважаємо тим самим за папером, датою й кількістю. Ціну в ключ
	// не беремо: та сама купівля, внесена вручну, могла бути округлена
	// інакше, і розбіжність у копійці не робить її іншою купівлею.
	lotSeen := map[string]bool{}
	for _, l := range lots {
		lotSeen[fmt.Sprintf("%s|%s|%d", l.ISIN, l.BuyDate, l.Qty)] = true
	}
	depSeen := map[string]bool{}
	for _, d := range deps {
		depSeen[fmt.Sprintf("%s|%d|%s|%s", d.Date, d.Amount, d.Currency, d.Broker)] = true
	}

	// Водяний знак: усе, що старше за нього, не розглядаємо взагалі.
	// Виписка щомісяця приносить повну історію, і покладатись лише на
	// дедуплікацію більше не можна — після ручного підчищення журналу
	// старі рядки почали проситися назад.
	since, err := s.st.GetSetting(ctx, "import_since")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := struct {
		Rows     []outRow          `json:"rows"`
		Skipped  []imports.Skipped `json:"skipped"`
		Imported int               `json:"imported"`
		New      int               `json:"new"`
		// Since — від якої дати враховуємо, Before — скільки рядків
		// відсіяно як старіші. Списком їх не віддаємо: у файлі їх сотні,
		// і перегляд перетворився б на портянку.
		Since  string `json:"since,omitempty"`
		Before int    `json:"before,omitempty"`
	}{Rows: []outRow{}, Skipped: res.Skipped, Since: since}

	for _, row := range res.Rows {
		if since != "" && string(row.Date) < since {
			out.Before++
			continue
		}
		cur := money.UAH
		var exists bool
		switch row.Kind {
		case "fund_buy", "fund_sell", "dividend":
			kind := domain.FundBuy
			if row.Kind == "fund_sell" {
				kind = domain.FundSell
			} else if row.Kind == "dividend" {
				kind = domain.FundDividend
			}
			op := domain.FundOp{Date: row.Date, Fund: row.Fund, Kind: kind, Qty: row.Qty,
				Amount: row.Amount, Tax: row.Tax, Currency: cur, Broker: broker, Note: "виписка"}
			var eerr error
			if exists, eerr = s.st.FundOpExists(ctx, op); eerr != nil {
				writeErr(w, http.StatusInternalServerError, eerr)
				return
			}
			if !exists && !dry {
				if _, aerr := s.st.AddFundOp(ctx, op); aerr != nil {
					writeErr(w, http.StatusInternalServerError, aerr)
					return
				}
			}
		case "bond_buy":
			key := fmt.Sprintf("%s|%s|%d", row.Fund, row.Date, row.Qty)
			exists = lotSeen[key]
			if !exists {
				lotSeen[key] = true
				if !dry {
					// Ціна за папір — сума ділена на кількість; залишок від
					// ділення кладемо в комісію, щоб сумарна вартість лота
					// збіглася з випискою до копійки. Зазвичай там і справді
					// сидить комісія брокера, вшита в ціну.
					per := row.Amount / row.Qty
					fee := row.Amount - per*row.Qty
					cur := money.UAH
					if b, berr := s.st.GetBond(ctx, row.Fund); berr == nil && b != nil {
						cur = b.Nominal.Currency().Code
					}
					var feeM *money.Money
					if fee > 0 {
						feeM = money.New(fee, cur)
					}
					if _, aerr := s.st.AddLot(ctx, domain.Lot{ISIN: row.Fund, Qty: row.Qty,
						PricePerBond: money.New(per, cur), Fee: feeM, BuyDate: row.Date,
						Channel: broker, Note: "виписка"}); aerr != nil {
						writeErr(w, http.StatusInternalServerError, aerr)
						return
					}
				}
			}

		case "deposit", "withdrawal":
			amt := row.Amount
			if row.Kind == "withdrawal" {
				amt = -amt
			}
			key := fmt.Sprintf("%s|%d|%s|%s", row.Date, amt, cur, broker)
			exists = depSeen[key]
			if !exists {
				depSeen[key] = true // не задвоїти в межах одного файлу
				if !dry {
					if _, aerr := s.st.AddDeposit(ctx, store.Deposit{Date: row.Date, Amount: amt,
						Currency: cur, Broker: broker, Note: "виписка"}); aerr != nil {
						writeErr(w, http.StatusInternalServerError, aerr)
						return
					}
				}
			}
		}
		if !exists {
			out.New++
			if !dry {
				out.Imported++
			}
		}
		// Шукаємо ручний рух, який СТОЇТЬ ЗАМІСТЬ цієї операції.
		//
		// Напрямок вирішальний. Купівля списує гроші, тож її ручним
		// відповідником було б ЗНЯТТЯ; продаж і дивіденд зараховують —
		// отже ПОПОВНЕННЯ. Порівняння за модулем, як було спершу, ловило
		// й цілком нормальну пару «поповнив 8 051,74 і того ж дня купив
		// на 8 051,74»: гроші прийшли й пішли, ніякого подвоєння немає.
		// Хибні тривоги тут дорого коштують — на них перестають зважати
		// саме тоді, коли трапляється справжня.
		var conflict string
		switch row.Kind {
		case "fund_buy", "fund_sell", "dividend", "bond_buy":
			want := row.Amount
			if row.Kind == "dividend" {
				want = row.Amount - row.Tax
			}
			outflow := row.Kind == "fund_buy" || row.Kind == "bond_buy"
			for _, d := range deps {
				if (d.Amount < 0) != outflow {
					continue // рух у той самий бік, що й операція, — не заміна їй
				}
				if abs64(d.Amount) != want && abs64(d.Amount) != row.Amount {
					continue
				}
				if n := domain.DaysBetween(d.Date, row.Date); n < -2 || n > 2 {
					continue
				}
				conflict = fmt.Sprintf("та сама сума вже є ручним рухом від %s (%s) — видали його, інакше гроші порахуються двічі",
					d.Date, d.Broker)
				break
			}
		}
		out.Rows = append(out.Rows, outRow{Date: string(row.Date), Kind: row.Kind,
			Fund: row.Fund, Qty: row.Qty,
			Amount: money.New(row.Amount, cur).Display(),
			Tax:    money.New(row.Tax, cur).Display(), Exists: exists, Conflict: conflict})
	}
	// Водяний знак рухаємо на ДЕНЬ ЗАПУСКУ, а не на найпізніший рядок
	// файлу: файл міг закінчитись тижнем раніше, ніж його завантажили, і
	// брати дату з даних означало б щоразу знову перебирати той тиждень.
	//
	// Рухаємо навіть коли нічого не імпортовано: «переглянув і нічого
	// нового не було» — теж відповідь, і наступного разу перебирати ті
	// самі рядки ні до чого. Але не при перегляді: dry нічого не змінює,
	// інакше кнопка «Переглянути» тихо з'їдала б період.
	if !dry {
		today := string(domain.NewDate(time.Now()))
		if serr := s.st.SetSetting(ctx, "import_since", today); serr != nil {
			writeErr(w, http.StatusInternalServerError, serr)
			return
		}
		out.Since = today
		if out.Imported > 0 {
			s.publishAsync()
		}
	}
	writeJSON(w, http.StatusOK, out)
}
