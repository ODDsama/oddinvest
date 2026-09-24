package store

import (
	"context"
	"encoding/json"
	"testing"
)

// Дамп, старший за пізні колонки, відновлюється з чесними «не знаю».
//
// Знімок: eur_share_bp (0061), idle_uah (0052), accrued_uah (0063) — «тоді
// не рахували» це −1, а відсутнє поле читалось нулем, тобто виміряним
// нулем: дельти періоду порівнювали «з купоном» проти «без».
// Вклад: ставку 19,5/23 %, заведену за замовчуванням, міграція 0064
// переписала на −1 «за законом»; старий дамп повертав її назад, і вклад
// після грудня 2024 знову рахувався за 19,5 %.
func TestRestoreOldDumpDefaults(t *testing.T) {
	var snap Snapshot
	if err := json.Unmarshal([]byte(`{"date":"2025-01-01","invested_uah":100}`), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.EURShareBP != -1 || snap.IdleUAH != -1 || snap.AccruedUAH != -1 {
		t.Errorf("пізні колонки старого знімка мали стати −1: %+v", snap)
	}
	if err := json.Unmarshal([]byte(`{"date":"2026-01-01","eur_share_bp":0,"idle_uah":0,"accrued_uah":0}`), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.EURShareBP != 0 || snap.IdleUAH != 0 || snap.AccruedUAH != 0 {
		t.Errorf("явний нуль мусить лишитись нулем: %+v", snap)
	}

	st := openTest(t)
	ctx := context.Background()
	old := &Backup{Schema: BackupSchema, App: "oddinvest", TermDeposits: []BackupTermDeposit{
		{ID: 1, Bank: "Приват", Currency: "UAH", Principal: 10_000_00, RateBP: 1500,
			OpenDate: "2024-01-10", MaturityDate: "2025-01-10", Payout: "end", TaxBP: 1950},
	}}
	if err := st.ImportAll(ctx, old); err != nil {
		t.Fatal(err)
	}
	deps, err := st.ListTermDeposits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0].TaxBP != -1 {
		t.Errorf("ставка старого дампу мала стати «за законом» (−1): %+v", deps)
	}
}
