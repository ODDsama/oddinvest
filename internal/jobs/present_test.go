package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/state"
)

// Знімок читає СИРИЙ документ: презентер (валюта звітності) до нього не
// доходить ні за яких налаштувань. Інакше долари лягли б у гривневі
// колонки, і побачити це можна було б лише за стрибком кривої.
func TestSnapshotNeverPresents(t *testing.T) {
	r, st := testRunner(t, "")
	r.build = func(context.Context, time.Time) (*state.Doc, error) {
		return &state.Doc{Currency: "UAH", InvestedUAH: state.UAH(497_500)}, nil
	}
	presented := false
	r.SetPresenter(func(_ context.Context, doc *state.Doc) error {
		presented = true
		doc.InvestedUAH = state.Minor(11275, "USD")
		return nil
	})
	if err := r.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if presented {
		t.Fatal("Snapshot покликав презентер — знімок мусить лишатись у гривні")
	}
	snaps, err := st.ListSnapshots(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].InvestedUAH != 497_500 {
		t.Errorf("у базі %+v, чекали 497 500 копійок гривні", snaps)
	}
}
