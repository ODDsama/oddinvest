package domain

import "testing"

// Операція, датована після asOf, не входить ні в потоки, ні в термінал.
//
// Потоки обрізались на asOf, а термінальна вартість бралась із позиції по
// ВСЬОМУ журналу: купівля на завтра не віднімалась як потік, але вже
// сиділа в терміналі — прибуток завищувався на всю її суму.
func TestXIRRTerminalIgnoresFutureOps(t *testing.T) {
	ops := []FundOp{
		{ID: 1, Date: "2026-01-10", Fund: "Ocean", Kind: FundBuy, Qty: 10, Amount: 1_000_00, Currency: "UAH"},
		{ID: 2, Date: "2026-09-25", Fund: "Ocean", Kind: FundBuy, Qty: 10, Amount: 1_000_00, Currency: "UAH"},
	}
	var terminal int64
	for _, f := range FundFlows(ops, nil, "UAH", "2026-09-24") {
		if f.Date == "2026-09-24" {
			terminal += f.Amount
		}
	}
	if terminal != 1_000_00 {
		t.Errorf("термінал фондів %d, чекали 1 000,00 — лише те, що куплено до asOf", terminal)
	}

	acc := []NPFAccount{{ID: 1, Name: "Династія", Currency: "UAH", Nav: 2_000_000, NavDate: "2026-09-01"}}
	nops := []NPFOp{
		{NPFID: 1, Date: "2026-02-01", Amount: 1_000_00, Units: 500_000_000},
		{NPFID: 1, Date: "2026-10-01", Amount: 1_000_00, Units: 500_000_000},
	}
	p := NPFPositions(acc, nops)[1]
	var nterm int64
	for _, f := range NPFFlows(*p, nops, "2026-09-24") {
		if f.Date == "2026-09-24" {
			nterm += f.Amount
		}
	}
	if nterm != 1_000_00 {
		t.Errorf("термінал НПФ %d, чекали 1 000,00 — без одиниць майбутнього внеску", nterm)
	}
}
