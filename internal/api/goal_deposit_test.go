package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// Вклад під ціль накопичення (0062) — дзеркало резервного, і перевіряється
// тим самим набором наслідків, що TestReserveDepositLeavesThePortfolio.
//
// Заради чого воно існує взагалі: доти гроші цілей лежали ЧИСТОЮ ГОТІВКОЮ
// під нуль — вкладу в них не було, — тоді як ціна самої цілі росла на
// інфляцію. Тому головна перевірка тут не «сума переїхала», а «ставка
// дійшла»: без неї потрібний темп рахувався б так, ніби вклад нічого не
// дає, і вся фіча звелась би до перекладання числа з колонки в колонку.

func addGoalWithDeposit(t *testing.T, st *store.Store, ctx context.Context,
	due domain.Date, rateBP int64) int64 {

	t.Helper()
	gid, err := st.AddGoal(ctx, store.Goal{
		Name: "Авто", TargetAmount: 1_000_000_00, Currency: money.UAH, DueDate: due,
	})
	if err != nil {
		t.Fatal(err)
	}
	today := domain.NewDate(time.Now())
	// Поповнюваний — щоб «не пропонується» не пройшло випадково через те,
	// що вклад і так не приймає поповнень. Той самий прийом, що в тесті
	// подушки.
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 200_000_00,
		RateBP: rateBP, OpenDate: today, MaturityDate: today.AddMonths(12),
		Payout: domain.PayoutEnd, TaxBP: 2300,
		Replenishable: true, GoalID: gid,
	}); err != nil {
		t.Fatal(err)
	}
	return gid
}

// TestGoalDepositLeavesThePortfolio — тіло цільового вкладу йде в цілі, а
// не у вклади портфеля, і помічник не пропонує його поповнювати.
//
// Три наслідки одного посилання, і кожен окремо тихий — рівно як у
// подушки. Найдорожчий із них третій: порада поповнити вклад, обіцяний
// авто, це порада купувати папір за гроші, яких у портфеля немає.
func TestGoalDepositLeavesThePortfolio(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	ctx := context.Background()
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: 500_000_00,
		Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	addGoalWithDeposit(t, st, ctx, domain.NewDate(time.Now()).AddMonths(24), 1600)

	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	var doc struct {
		DepositsUAH float64 `json:"deposits_uah"`
		GoalsUAH    float64 `json:"goals_uah"`
		Goals       []struct {
			ID           int64   `json:"id"`
			CollectedUAH float64 `json:"collected_uah"`
		} `json:"goals"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("розбір: %v", err)
	}
	if doc.DepositsUAH != 0 {
		t.Errorf("цільовий вклад лишився у вкладах портфеля: %.2f ₴", doc.DepositsUAH)
	}
	if doc.GoalsUAH < 200_000 {
		t.Errorf("тіло цільового вкладу не дійшло до цілей: goals_uah = %.2f ₴", doc.GoalsUAH)
	}
	// Подвійний рахунок — саме те, чого боїться buildGoals: гроші цілі
	// зводить ОДНЕ місце, і якби будівник додав їх ще й від себе, тут
	// стояло б 400 000 при 200 000 внесених.
	if doc.GoalsUAH > 200_100 {
		t.Errorf("тіло цільового вкладу порахувалось двічі: goals_uah = %.2f ₴", doc.GoalsUAH)
	}
	if len(doc.Goals) != 1 || doc.Goals[0].CollectedUAH < 200_000 {
		t.Errorf("зібране самої цілі не бачить вкладу: %+v", doc.Goals)
	}

	_, body = do(t, "GET", srv.URL+"/api/reinvest", "")
	var got []struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("розбір: %v", err)
	}
	for _, g := range got {
		if g.Kind == "deposit" && g.Label == "ПУМБ" {
			t.Error("помічник пропонує поповнити вклад ЦІЛІ — це порада купувати папір за гроші на авто")
		}
	}
}

// TestGoalDepositRateReachesTheGoal — ставка цільового вкладу доходить до
// самої цілі.
//
// Це ГОЛОВНА перевірка фази: без ставки потрібний темп проти майбутньої
// ціни вважав би, що зібране лежить під нуль, і вимагав би однакового
// внеску від цілі на вкладі під 16% і від цілі в шухляді. Тобто вклад
// заводили б заради числа, яке його не помічає.
func TestGoalDepositRateReachesTheGoal(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	ctx := context.Background()
	addGoalWithDeposit(t, st, ctx, domain.NewDate(time.Now()).AddMonths(24), 1600)

	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	var doc struct {
		Goals []struct {
			RatePct float64 `json:"rate_pct"`
		} `json:"goals"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("розбір: %v", err)
	}
	if len(doc.Goals) != 1 {
		t.Fatalf("цілей %d, чекали одну", len(doc.Goals))
	}
	// 16% мінус податок 23% = 12.32% чистих. Число зашите навмисно: воно
	// перевіряє не «ставка не нуль», а що по дорозі не загубився саме
	// податок — без нього тут стояло б 16.00.
	if got := doc.Goals[0].RatePct; got < 12.31 || got > 12.33 {
		t.Errorf("ставка цілі %.2f%%, чекали 12.32%% (16%% мінус податок 23%%)", got)
	}
}

// TestGoalDepositIsExclusiveWithReserve — вклад не буває водночас подушкою
// й ціллю.
//
// Перевірка живе в хендлері, а не CHECK-ом у схемі (довід у шапці 0062),
// тож перевіряти її треба саме через API — інакше вона мовчки зникне
// разом із рефакторингом хендлера, а БД її не підстрахує.
func TestGoalDepositIsExclusiveWithReserve(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	ctx := context.Background()
	gid, err := st.AddGoal(ctx, store.Goal{
		Name: "Ремонт", TargetAmount: 500_000_00, Currency: money.UAH,
	})
	if err != nil {
		t.Fatal(err)
	}
	today := domain.NewDate(time.Now())
	body := `{"bank":"ПУМБ","currency":"UAH","principal":"1000","rate_pct":"16",` +
		`"open_date":"` + string(today) + `","maturity_date":"` + string(today.AddMonths(12)) + `",` +
		`"payout":"end","is_reserve":true,"goal_id":"` + itoa(int(gid)) + `"}`
	resp, got := do(t, "POST", srv.URL+"/api/term-deposits", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("вклад і подушка, і ціль водночас пройшов: %d %s", resp.StatusCode, got)
	}
	if !strings.Contains(got, "подушкою") {
		t.Errorf("помилка %q не називає причину — саме заради цього перевірка тут, а не в CHECK", got)
	}
}

// TestDeleteGoalRefusesWhileDepositHangsOnIt — ціль не видаляється, доки на
// ній висить вклад.
//
// Без цієї перевірки видалення падало б сирою помилкою FK: те саме по суті,
// але незрозуміло, а головне — не сказало б, що робити (зняти ціль із
// вкладу, а не видаляти сам вклад).
func TestDeleteGoalRefusesWhileDepositHangsOnIt(t *testing.T) {
	_, st := testServer(t)
	seed(t, st)
	ctx := context.Background()
	gid := addGoalWithDeposit(t, st, ctx, "", 1600)
	err := st.DeleteGoal(ctx, gid)
	if err == nil {
		t.Fatal("ціль видалилась разом із вкладом, який на неї посилається")
	}
	if !strings.Contains(err.Error(), "вклад") {
		t.Errorf("помилка %q не називає вклади — людині нема з чого зрозуміти, що робити", err)
	}
}
