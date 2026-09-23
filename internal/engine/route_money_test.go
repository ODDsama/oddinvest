package engine

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/state"
)

// Позика в подушки, повернена ПОВНІСТЮ, гасне, і надбавка до цілі зникає
// разом із нею.
//
// Доти відсоток нараховувався дробом копійки (1234.57 × 13.7% / 12 =
// 14.0947…), а нога подушки платить цілими копійками — і після виплати
// рівно того, що людина бачить боргом, лишалось 0.0047 ₴. loanOwed == 0 не
// спрацьовувало ніколи, і маршрут до кінця горизонту вимагав відсоток за
// позику, якої вже немає.
func TestRouteLoanClosesOnFullRepayment(t *testing.T) {
	c := &routeCarry{loanOwed: 1234.57, loanRate: 13.7, gapUAH: 5000, fillNow: 5000}
	c.accrueLoan()
	owed := state.Major(c.loanOwed, money.UAH) // так його бачить людина
	gapBefore := c.gapUAH
	c.apply(allocPlan{Reserve: &allocReserve{AmountUAH: owed}})
	if c.loanOwed != 0 || c.loanInterest != 0 {
		t.Fatalf("після виплати %.2f лишилось винні %.6f, надбавка %.6f", owed.Major(), c.loanOwed, c.loanInterest)
	}
	if c.gapUAH >= gapBefore-owed.Major() {
		t.Errorf("розрив %.4f мав упасти ще й на нараховане (був %.4f, сплачено %.2f)",
			c.gapUAH, gapBefore, owed.Major())
	}
}

// «Поза частками» на маршруті — не витрачене й не залишок.
//
// Частки видів дають 60%, і вид уже на своїй частці: з 10 000 ₴ у папери
// йдуть 6 000, 4 000 лишаються людині. Доти маршрут рахував ці 4 000
// «витраченими» (горщик їх губив, а нога звітувала, що всі гроші пішли в
// діло). Тепер вони видні в нозі, горщик їх далі не везе — наступне
// надходження не розклало б їх за частками замість людини, — і нога з
// одними лише вільними грошима не називає надходжень «витраченими».
func TestRouteFreeMoneyIsNeitherSpentNorCarried(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 60, 66000)}, nil)
	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH,
			routeFlow("2026-09-10", 10000, "UA0001"),
			routeFlow("2026-10-10", 10000, "UA0002")),
		routePlans(0), nil, allocRates, nil, nil, routeToday)
	if len(got.Legs) != 2 {
		t.Fatalf("ніг %d, чекали 2", len(got.Legs))
	}
	first := got.Legs[0]
	if first.FreeUAH.Major() <= 0 {
		t.Fatalf("перша нога: поза частками %.2f — нерозподілене мало бути видно", first.FreeUAH.Major())
	}
	var lines int64
	for _, l := range first.Lines {
		lines += l.TotalUAH.Minor()
	}
	if sum := lines + first.FreeUAH.Minor() + first.RestUAH.Minor(); sum != first.AmountUAH.Minor() {
		t.Errorf("рядки %d + поза частками %d + залишок %d ≠ горщик %d",
			lines, first.FreeUAH.Minor(), first.RestUAH.Minor(), first.AmountUAH.Minor())
	}
	if got.Legs[1].CarryInUAH.Minor() != first.RestUAH.Minor() {
		t.Errorf("у другу ногу перенесено %.2f, а залишок першої %.2f: вільні гроші горщик не везе",
			got.Legs[1].CarryInUAH.Major(), first.RestUAH.Major())
	}
}

// Перенесений залишок — уже бюджет видів, а не нові гроші, і «поза
// частками» з нього не береться вдруге.
//
// Вид понад частку (90% проти 60%), папір коштує 70 000: купити нічого, і
// 6 000 їдуть далі. Друга нога розкладає горщик 16 000 заново, і доти 40%
// від усього горщика ставали «поза частками» — 6 400 замість 4 000, бо
// перенесені 6 000 теж потрапляли під нерозподілені відсотки. Людина лишила
// собі 40% НОВИХ грошей, а не 40% кожного залишку щоразу.
func TestRouteCarryIsNotFreeAgain(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 60, 90000)}, nil)
	got := buildRoute(doc, []suggestion{bondSug("UA0001", 70000, money.UAH)},
		routeInc("mono", money.UAH,
			routeFlow("2026-09-10", 10000, "UA0001"),
			routeFlow("2026-10-10", 10000, "UA0002"),
			routeFlow("2026-11-10", 10000, "UA0003")),
		routePlans(0), nil, allocRates, nil, nil, routeToday)
	if len(got.Legs) != 3 {
		t.Fatalf("ніг %d, чекали 3", len(got.Legs))
	}
	for i, leg := range got.Legs {
		if leg.FreeUAH.Minor() != 400_000 {
			t.Errorf("нога %d: поза частками %.2f, чекали 4000 — 40%% від нових 10 000 (горщик %.2f, перенесено %.2f)",
				i, leg.FreeUAH.Major(), leg.AmountUAH.Major(), leg.CarryInUAH.Major())
		}
	}
}
