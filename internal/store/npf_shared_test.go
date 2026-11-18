package store

import (
	"context"
	"errors"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Спільний довідник НПФ (0070): фонд і ЧВОПА одні на всі портфелі, участь —
// у кожного своя.
//
// Тут стоїть сама причина міграції: власник і дружина тримають один фонд,
// і число, вписане в одному портфелі, мусить бути видне в другому. І друга
// половина — щоб спільність не протекла далі, ніж треба: особисті поля
// лишаються особистими, а портфель без рахунку у фонді до його історії не
// дістається.
func TestNPFFundIsSharedAcrossPortfolios(t *testing.T) {
	ctx := context.Background()
	st := openTest(t)
	wid, err := st.AddPortfolio(ctx, "wife", "Дружина")
	if err != nil {
		t.Fatal(err)
	}
	w := st.For(wid)

	mine, err := st.AddNPFAccount(ctx, domain.NPFAccount{Name: "Династія", Administrator: "АЦПО",
		Currency: money.UAH, Nav: 3400000, NavDate: "2026-06-30", ExpectedYieldBP: 1200,
		AccessDate: "2040-01-01", Note: "мій"})
	if err != nil {
		t.Fatal(err)
	}
	// Другий портфель називає той самий фонд — і підхоплює його, а не
	// заводить двійника. Те, що форма принесла про ФОНД, не діє: інакше
	// «додати рахунок» мовчки переписав би сусідові адміністратора й ЧВОПА.
	hers, err := w.AddNPFAccount(ctx, domain.NPFAccount{Name: "Династія", Administrator: "навздогад",
		Currency: money.UAH, Nav: 1000000, NavDate: "2020-01-01",
		AccessDate: "2042-05-05", Note: "її"})
	if err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM npf_funds`); n != 1 {
		t.Fatalf("фондів %d, хочемо 1: другий портфель мав підхопити наявний", n)
	}
	wAcc := soleNPF(t, w)
	if wAcc.ID != hers || wAcc.Administrator != "АЦПО" || wAcc.Nav != 3400000 || wAcc.NavDate != "2026-06-30" ||
		wAcc.ExpectedYieldBP != 1200 {
		t.Errorf("другий портфель бачить не той фонд: %+v", wAcc)
	}
	if wAcc.AccessDate != "2042-05-05" || wAcc.Note != "її" {
		t.Errorf("особисте другого портфеля загубилось: %+v", wAcc)
	}

	// --- точка історії, вписана в одному, видна в другому — під СВОЇМ рахунком ---
	if _, err := w.AddNPFNavPoints(ctx, hers, []domain.NPFNav{{Date: "2026-07-31", Nav: 3450000}}); err != nil {
		t.Fatal(err)
	}
	navs, err := st.ListNPFNav(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(navs) != 1 || navs[0].NPFID != mine || navs[0].Nav != 3450000 {
		t.Fatalf("головний не бачить точки дружини під своїм рахунком: %+v", navs)
	}
	point := navs[0].ID
	if wn, _ := w.ListNPFNav(ctx); len(wn) != 1 || wn[0].NPFID != hers || wn[0].ID != point {
		t.Errorf("дружина бачить точку не під своїм рахунком: %+v", wn)
	}

	// --- остання ЧВОПА й обіцянка — теж спільні; особисте — ні ---
	if err := w.SetNPFNav(ctx, hers, 3500000, "2026-09-30"); err != nil {
		t.Fatal(err)
	}
	wAcc = soleNPF(t, w)
	wAcc.ExpectedYieldBP = 1500
	wAcc.AccessDate = "2043-01-01"
	if err := w.UpdateNPFAccount(ctx, wAcc); err != nil {
		t.Fatal(err)
	}
	m := soleNPF(t, st)
	if m.Nav != 3500000 || m.NavDate != "2026-09-30" || m.ExpectedYieldBP != 1500 {
		t.Errorf("головний не бачить правок фонду з другого портфеля: %+v", m)
	}
	if m.AccessDate != "2040-01-01" || m.Note != "мій" {
		t.Errorf("особисте головного змінилось правкою з другого портфеля: %+v", m)
	}

	// --- чужий рахунок — не ключ до фонду ---
	if err := st.SetNPFNav(ctx, hers, 1, "2026-10-01"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ЧВОПА через чужий рахунок: %v, хочемо ErrNotFound", err)
	}
	if _, err := st.AddNPFNavPoints(ctx, hers, []domain.NPFNav{{Date: "2026-10-01", Nav: 1}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("точка через чужий рахунок: %v, хочемо ErrNotFound", err)
	}

	// --- портфель БЕЗ рахунку у фонді до його історії не дістається ---
	xid, err := st.AddPortfolio(ctx, "x", "Сторонній")
	if err != nil {
		t.Fatal(err)
	}
	x := st.For(xid)
	if xn, _ := x.ListNPFNav(ctx); len(xn) != 0 {
		t.Errorf("сторонній портфель бачить чужу історію: %+v", xn)
	}
	if err := x.UpdateNPFNavPoint(ctx, domain.NPFNav{ID: point, Date: "2026-07-31", Nav: 1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("правка чужої точки: %v, хочемо ErrNotFound", err)
	}
	if err := x.DeleteNPFNavPoint(ctx, point); !errors.Is(err, ErrNotFound) {
		t.Errorf("видалення чужої точки: %v, хочемо ErrNotFound", err)
	}
	// А учасник фонду править спільну точку вільно.
	if err := st.UpdateNPFNavPoint(ctx, domain.NPFNav{ID: point, Date: "2026-07-31", Nav: 3460000}); err != nil {
		t.Fatal(err)
	}
	if wn, _ := w.ListNPFNav(ctx); len(wn) != 1 || wn[0].Nav != 3460000 {
		t.Errorf("правка точки з головного не видна дружині: %+v", wn)
	}

	// --- конфлікти мають своє імʼя, а не текст SQLite ---
	if _, err := w.AddNPFAccount(ctx, domain.NPFAccount{Name: "Династія", Currency: money.UAH}); !errors.Is(err, ErrConflict) {
		t.Errorf("другий рахунок у тому самому фонді: %v, хочемо ErrConflict", err)
	}
	if _, err := st.AddNPFAccount(ctx, domain.NPFAccount{Name: "Емерит", Currency: money.UAH}); err != nil {
		t.Fatal(err)
	}
	wAcc = soleNPF(t, w)
	wAcc.Name = "Емерит"
	if err := w.UpdateNPFAccount(ctx, wAcc); !errors.Is(err, ErrConflict) {
		t.Errorf("перейменування на назву іншого фонду: %v, хочемо ErrConflict", err)
	}
	if soleNPF(t, w).Name != "Династія" {
		t.Error("відхилене перейменування таки змінило назву")
	}
}

// TestNPFFundOutlivesAccountUntilLastHolder — фонд та історія ЧВОПА живуть,
// доки у фонді є бодай один рахунок у бодай одному портфелі.
//
// Обидва боки важать. Зникне зарано — дружина втратить криву, бо власник
// закрив свій рахунок. Не зникне взагалі — лишиться невидимий сирота із
// зайнятою назвою, і наступний рахунок «Династія» мовчки успадкує стару
// ЧВОПА.
func TestNPFFundOutlivesAccountUntilLastHolder(t *testing.T) {
	ctx := context.Background()
	st := openTest(t)
	wid, err := st.AddPortfolio(ctx, "wife", "Дружина")
	if err != nil {
		t.Fatal(err)
	}
	w := st.For(wid)
	mine, err := st.AddNPFAccount(ctx, domain.NPFAccount{Name: "Династія", Currency: money.UAH})
	if err != nil {
		t.Fatal(err)
	}
	hers, err := w.AddNPFAccount(ctx, domain.NPFAccount{Name: "Династія", Currency: money.UAH})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddNPFNavPoints(ctx, mine, []domain.NPFNav{{Date: "2026-06-30", Nav: 3472156}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddNPFOp(ctx, domain.NPFOp{NPFID: mine, Date: "2026-07-05", Units: 288005492, Amount: 100000}); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteNPFAccount(ctx, mine); err != nil {
		t.Fatal(err)
	}
	if wn, _ := w.ListNPFNav(ctx); len(wn) != 1 || wn[0].NPFID != hers {
		t.Fatalf("історія зникла разом із чужим рахунком: %+v", wn)
	}
	if ops, _ := st.ListNPFOps(ctx); len(ops) != 0 {
		t.Errorf("внески видаленого рахунку лишились: %+v", ops)
	}

	if err := w.DeleteNPFAccount(ctx, hers); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM npf_funds`); n != 0 {
		t.Errorf("фонд без жодного рахунку лишився сиротою (%d)", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM npf_fund_nav`); n != 0 {
		t.Errorf("історія фонду без рахунків лишилась (%d точок)", n)
	}

	// Каскад портфеля забирає рахунки сам, а фонд каскаду не має — його
	// прибирає DeletePortfolio.
	if _, err := w.AddNPFAccount(ctx, domain.NPFAccount{Name: "Самотній", Currency: money.UAH,
		Nav: 1100000, NavDate: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePortfolio(ctx, wid); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM npf_funds`); n != 0 {
		t.Errorf("видалення портфеля лишило фонд-сироту (%d)", n)
	}
}

func soleNPF(t *testing.T, s *Store) domain.NPFAccount {
	t.Helper()
	accs, err := s.ListNPFAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 {
		t.Fatalf("рахунків НПФ %d, хочемо 1: %+v", len(accs), accs)
	}
	return accs[0]
}

func countRows(t *testing.T, s *Store, q string) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}
