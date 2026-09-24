package store

import (
	"context"
	"strconv"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Посилання-ТЕКСТ перечіплюються так само, як зовнішні ключі.
//
// Відновлення в портфель, де id із бекапу вже зайняті сусідом, дає рядкам
// нові id (idMaps). Числові FK за ними йшли, а текстові — ні: призначення
// потоку «npf:<id>», ref планової купівлі НПФ і приховані рядки
// «goal:<id>»/«deposit:<id>» показували на чужий рахунок або в нікуди.
func TestRestoreRemapsTextRefs(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	npf, err := st.AddNPFAccount(ctx, domain.NPFAccount{Name: "Династія", Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	goal, err := st.AddGoal(ctx, Goal{Name: "Авто", TargetAmount: 1, Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPlanFlow(ctx, PlanFlow{Name: "Внесок", Kind: "expense", Amount: 100000,
		Currency: "UAH", Cadence: "month", FromDate: "2026-01-01", InvestBP: 10000,
		Dest: domain.NPFPlanDest(npf)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPlanBuy(ctx, PlanBuy{Kind: "npf", Ref: strconv.FormatInt(npf, 10), Amount: 100000}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetHiddenRows(ctx, []string{"goal:" + strconv.FormatInt(goal, 10), "fund:Inzhur Ocean"}); err != nil {
		t.Fatal(err)
	}
	dump, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Той самий дамп — у ДРУГИЙ портфель, поки перший тримає свої id.
	pid, err := st.AddPortfolio(ctx, "wife", "Дружина")
	if err != nil {
		t.Fatal(err)
	}
	b := st.For(pid)
	if err := b.ImportAll(ctx, dump); err != nil {
		t.Fatal(err)
	}
	accs, _ := b.ListNPFAccounts(ctx)
	goals, _ := b.ListGoals(ctx)
	if len(accs) != 1 || len(goals) != 1 || accs[0].ID == npf || goals[0].ID == goal {
		t.Fatalf("очікували нові id у другому портфелі: npf %+v, цілі %+v", accs, goals)
	}
	flows, _ := b.ListPlanFlows(ctx)
	if len(flows) != 1 || flows[0].Dest != domain.NPFPlanDest(accs[0].ID) {
		t.Errorf("призначення потоку %+v, чекали %s", flows, domain.NPFPlanDest(accs[0].ID))
	}
	buys, _ := b.ListPlanBuys(ctx)
	if len(buys) != 1 || buys[0].Ref != strconv.FormatInt(accs[0].ID, 10) {
		t.Errorf("ref планової купівлі НПФ %+v, чекали %d", buys, accs[0].ID)
	}
	hidden, _ := b.HiddenRows(ctx)
	want := map[string]bool{"goal:" + strconv.FormatInt(goals[0].ID, 10): true, "fund:Inzhur Ocean": true}
	for _, h := range hidden {
		if !want[h] {
			t.Errorf("прихований рядок %q не перечеплено (чекали %v)", h, want)
		}
	}
}
