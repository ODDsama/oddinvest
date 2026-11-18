package store

import (
	"context"
	"encoding/json"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// TestRestoreReadsPre0070NPFDump — дамп, зроблений ДО спільного довідника,
// відновлюється тим самим кодом.
//
// Формат файла 0070 не змінювала навмисно: атрибути фонду й далі їдуть у
// записі рахунку, точки — за id рахунку. Старий дамп відрізняється лише
// тим, що точки в ньому несуть власний id, якого більше ніхто не читає.
// Літерал, а не ExportAll: інакше тест перевіряв би сьогоднішній формат
// сьогоднішнім же кодом.
func TestRestoreReadsPre0070NPFDump(t *testing.T) {
	ctx := context.Background()
	st := openTest(t)
	const old = `{"schema":1,
		"npf_accounts":[{"id":7,"name":"Династія","administrator":"АЦПО","currency":"UAH",
			"nav_e6":3400000,"nav_date":"2026-06-30","expected_yield_bp":1200,
			"access_date":"2040-01-01","contrib_day":5,"payout_years":10,"payout_freq":"month"}],
		"npf_ops":[{"id":4,"npf_id":7,"date":"2026-07-05","units_e6":288005492,"amount":100000}],
		"npf_nav":[{"id":3,"npf_id":7,"date":"2025-12-31","nav_e6":3300000},
			{"id":9,"npf_id":7,"date":"2026-06-30","nav_e6":3400000}],
		"settings":{},"payment_status":[],"snapshots":[]}`
	var b Backup
	if err := json.Unmarshal([]byte(old), &b); err != nil {
		t.Fatal(err)
	}
	if err := st.ImportAll(ctx, &b); err != nil {
		t.Fatalf("відновлення старого дампа: %v", err)
	}
	a := soleNPF(t, st)
	if a.ID != 7 || a.Administrator != "АЦПО" || a.Nav != 3400000 || a.ExpectedYieldBP != 1200 ||
		a.AccessDate != "2040-01-01" || a.ContribDay != 5 {
		t.Errorf("рахунок зі старого дампа: %+v", a)
	}
	navs, _ := st.ListNPFNav(ctx)
	if len(navs) != 2 || navs[0].NPFID != 7 || navs[0].Nav != 3300000 {
		t.Errorf("історія зі старого дампа: %+v", navs)
	}
	if ops, _ := st.ListNPFOps(ctx); len(ops) != 1 || ops[0].NPFID != 7 {
		t.Errorf("внесок зі старого дампа: %+v", ops)
	}
}

// TestRestoreKeepsNPFFundHeldByNeighbour — відновлення одного портфеля не
// забирає фонд та історію ЧВОПА в сусіда, а з останнім учасником — забирає.
// Той самий контракт, що в каталозі фондів (TestRestoreKeepsFundsWithMarks).
func TestRestoreKeepsNPFFundHeldByNeighbour(t *testing.T) {
	ctx := context.Background()
	st := openTest(t)
	wid, err := st.AddPortfolio(ctx, "wife", "Дружина")
	if err != nil {
		t.Fatal(err)
	}
	w := st.For(wid)
	mine, err := st.AddNPFAccount(ctx, domain.NPFAccount{Name: "Династія", Administrator: "АЦПО",
		Currency: money.UAH, Nav: 3400000, NavDate: "2026-06-30"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddNPFAccount(ctx, domain.NPFAccount{Name: "Династія", Currency: money.UAH}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddNPFNavPoints(ctx, mine, []domain.NPFNav{{Date: "2025-12-31", Nav: 3300000}}); err != nil {
		t.Fatal(err)
	}
	empty := func() *Backup { return &Backup{Schema: BackupSchema, Settings: map[string]string{}} }

	// Порожній бекап у головний: його рахунок зникає, фонд лишається сусідові.
	if err := st.ImportAll(ctx, empty()); err != nil {
		t.Fatal(err)
	}
	if accs, _ := st.ListNPFAccounts(ctx); len(accs) != 0 {
		t.Fatalf("порожній бекап лишив рахунки: %+v", accs)
	}
	wa := soleNPF(t, w)
	if wa.Administrator != "АЦПО" || wa.Nav != 3400000 {
		t.Errorf("сусід утратив атрибути фонду: %+v", wa)
	}
	if wn, _ := w.ListNPFNav(ctx); len(wn) != 1 || wn[0].Nav != 3300000 {
		t.Errorf("сусід утратив історію ЧВОПА: %+v", wn)
	}

	// Порожній бекап і в сусіда: тепер фонд нічий — і зникає.
	if err := w.ImportAll(ctx, empty()); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM npf_funds`); n != 0 {
		t.Errorf("фонд без рахунків пережив відновлення (%d)", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM npf_fund_nav`); n != 0 {
		t.Errorf("історія без фонду пережила відновлення (%d)", n)
	}
}

// TestNPFDumpSurvivesItsOwnRestore — дамп єдиного учасника фонду після
// відновлення дорівнює собі.
//
// Саме це й ламалось би, якби точки несли id: фонд єдиного учасника restore
// пересоздає, точки дістають нові id — і файл, відновлений і знятий знову,
// відрізнявся б від себе, хоч дані ті самі.
func TestNPFDumpSurvivesItsOwnRestore(t *testing.T) {
	ctx := context.Background()
	st := openTest(t)
	id, err := st.AddNPFAccount(ctx, domain.NPFAccount{Name: "Династія", Administrator: "АЦПО",
		Currency: money.UAH, Nav: 3400000, NavDate: "2026-06-30", ExpectedYieldBP: 1200, AccessDate: "2040-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddNPFNavPoints(ctx, id, []domain.NPFNav{
		{Date: "2025-12-31", Nav: 3300000}, {Date: "2026-06-30", Nav: 3400000}}); err != nil {
		t.Fatal(err)
	}
	// Точку видалено й додано знову: id у базі тепер «з діркою», і дамп не
	// має цього помічати.
	navs, _ := st.ListNPFNav(ctx)
	if err := st.DeleteNPFNavPoint(ctx, navs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddNPFNavPoints(ctx, id, []domain.NPFNav{{Date: "2025-12-31", Nav: 3300000}}); err != nil {
		t.Fatal(err)
	}

	dump, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(dump)
	if err := st.ImportAll(ctx, dump); err != nil {
		t.Fatal(err)
	}
	again, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(again)
	if string(before) != string(after) {
		t.Errorf("дамп після власного відновлення змінився:\n до: %s\n після: %s", before, after)
	}
}
