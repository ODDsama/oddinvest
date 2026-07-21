package domain

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

// Папір: номінал 1000 ₴, купон 16.55% двічі на рік, погашення через 2 роки.
func ytmBond(buy Date) []Payment {
	const isin = "UA4000227748"
	return []Payment{
		{ISIN: isin, PayDate: "2027-01-15", Type: PayCoupon, PerBond: money.New(8275, money.UAH)},
		{ISIN: isin, PayDate: "2027-07-15", Type: PayCoupon, PerBond: money.New(8275, money.UAH)},
		{ISIN: isin, PayDate: "2028-01-15", Type: PayCoupon, PerBond: money.New(8275, money.UAH)},
		{ISIN: isin, PayDate: "2028-07-15", Type: PayCoupon, PerBond: money.New(8275, money.UAH)},
		{ISIN: isin, PayDate: "2028-07-15", Type: PayRedemption, PerBond: money.New(100000, money.UAH)},
	}
}

func ytmAt(t *testing.T, priceMinor int64) float64 {
	t.Helper()
	y, err := YTM(money.New(priceMinor, money.UAH), "2026-07-15", ytmBond("2026-07-15"), "UA4000227748")
	if err != nil {
		t.Fatalf("YTM(%d): %v", priceMinor, err)
	}
	return y * 100
}

// Куплений за номіналом папір дає ЕФЕКТИВНУ ставку, вищу за купонну:
// піврічні 8.275% складаються в 17.24% річних. Саме такий вигляд ставки
// й чекає MonthlyRate, тож підміна купонної ставки на YTM тут доречна.
func TestYTMAtParExceedsCouponRate(t *testing.T) {
	got := ytmAt(t, 100000)
	if got < 17.0 || got > 17.5 {
		t.Errorf("за номіналом очікували ~17.24%%, маємо %.2f%%", got)
	}
}

// Головне, заради чого YTM і потрібен: ціна купівлі має впливати.
// «Купон ÷ номінал» цього не бачив узагалі й давав 16.55% на будь-якій ціні.
func TestYTMMovesWithPrice(t *testing.T) {
	discount := ytmAt(t, 95000)  // −5% до номіналу
	par := ytmAt(t, 100000)      //
	premium := ytmAt(t, 105000)  // +5% до номіналу
	if !(discount > par && par > premium) {
		t.Errorf("дохідність має спадати зі зростанням ціни: %.2f %.2f %.2f",
			discount, par, premium)
	}
	// дисконт 5% на дворічному папері — це помітно більше за пів пункта
	if discount-par < 2 {
		t.Errorf("дисконт 5%% мав дати щонайменше +2 п.п., маємо +%.2f", discount-par)
	}
	// і навпаки: «купон ÷ номінал» дав би 16.55% у всіх трьох випадках
	for _, v := range []float64{discount, par, premium} {
		if v > 16.5 && v < 16.6 {
			t.Errorf("%.2f%% підозріло схоже на купонну ставку — YTM не спрацював", v)
		}
	}
}

func TestYTMRejectsUnusable(t *testing.T) {
	if _, err := YTM(money.New(0, money.UAH), "2026-07-15", ytmBond("2026-07-15"), "UA4000227748"); err == nil {
		t.Error("нульова ціна мала дати помилку")
	}
	// куплено після останньої виплати — рахувати нема з чого
	if _, err := YTM(money.New(100000, money.UAH), "2029-01-01", ytmBond("2026-07-15"), "UA4000227748"); err == nil {
		t.Error("папір без майбутніх виплат мав дати помилку")
	}
}

// Вага — вкладені гроші, тож більший за сумою лот тягне середню до себе.
func TestWeightedYTMWeighsByMoney(t *testing.T) {
	pays := ytmBond("2026-07-15")
	small := YTMLot{CostPerBond: money.New(95000, money.UAH), Qty: 1, BuyDate: "2026-07-15", ISIN: "UA4000227748"}
	big := YTMLot{CostPerBond: money.New(105000, money.UAH), Qty: 20, BuyDate: "2026-07-15", ISIN: "UA4000227748"}

	avg, ok := WeightedYTM([]YTMLot{small, big}, pays)
	if !ok {
		t.Fatal("середня не порахувалась")
	}
	lo, hi := ytmAt(t, 105000), ytmAt(t, 95000)
	if avg < lo || avg > hi {
		t.Errorf("середня %.2f має лежати між %.2f і %.2f", avg, lo, hi)
	}
	// великий лот дорожчий і дає нижчу дохідність — середня має тяжіти вниз
	if avg > (lo+hi)/2 {
		t.Errorf("середня %.2f мала схилитись до більшого лота (%.2f)", avg, lo)
	}

	// битий набір не має ламати розрахунок — просто не бере участі
	broken := YTMLot{CostPerBond: nil, Qty: 5, BuyDate: "2026-07-15", ISIN: "UA4000227748"}
	if _, ok := WeightedYTM([]YTMLot{broken}, pays); ok {
		t.Error("самі лише биті позиції мали дати ok=false")
	}
}
