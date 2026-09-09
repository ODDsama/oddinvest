package domain

import (
	"math"
	"testing"
)

// Ефективна ставка вкладу проти простої договірної.
//
// Заради чого це існує: проста ставка стоїть в одній сортованій колонці з
// YTM облігації, а YTM — це IRR. Два різні означення «річної ставки» в
// колонці, яка їх порівнює, і похибка йде в обидва боки.

func mkDeposit(months int, rateBP, taxBP int64, payout DepositPayout, cap bool) Deposit {
	open := Date("2026-01-01")
	return Deposit{
		Currency: "UAH", Principal: 100_000_00, RateBP: rateBP,
		OpenDate: open, MaturityDate: open.AddMonths(months),
		Payout: payout, Capitalized: cap, TaxBP: taxBP,
	}
}

// TestEffectiveRateOneYearEqualsSimple — річний вклад із виплатою в кінці:
// ефективна ставка ДОРІВНЮЄ простій.
//
// Сторож проти тихого зсуву всього наявного: банки котирують річну
// номінальну, і саме на річному строку два означення сходяться. Якщо тут
// зʼявиться різниця — зламалась сама конвенція, а не окремий випадок.
func TestEffectiveRateOneYearEqualsSimple(t *testing.T) {
	d := mkDeposit(12, 1600, 2300, PayoutEnd, false)
	simple := NetRate(d.RateBP, d.TaxBP)
	got := d.EffectiveNetRate()
	if math.Abs(got-simple) > 0.0005 {
		t.Errorf("річний вклад: ефективна %.4f проти простої %.4f — вони мусять збігатися",
			got, simple)
	}
}

// TestEffectiveRateThreeYearEndPayoutIsLower — тризначний вклад із
// виплатою в кінці й БЕЗ капіталізації нараховує ПРОСТІ відсотки за весь
// строк, тож його ефективна ставка нижча за договірну.
//
// Найдорожчий із двох випадків: саме він ЗАВИЩУВАВ вклад у рейтингу.
// 16% × 3 роки = 48% сумарних, тобто ×1.48, тобто 13.9% річних складних —
// а показувалось 16%.
func TestEffectiveRateThreeYearEndPayoutIsLower(t *testing.T) {
	d := mkDeposit(36, 1600, 0, PayoutEnd, false)
	got := d.EffectiveNetRate()
	// Без податку рахунок видно очима: (1 + 0.16×3)^(1/3) − 1.
	want := math.Pow(1+0.16*3, 1.0/3) - 1
	if math.Abs(got-want) > 0.002 {
		t.Errorf("ефективна %.4f, а проста за весь строк дає %.4f", got, want)
	}
	if got >= 0.16 {
		t.Errorf("ефективна %.4f не нижча за договірні 16%% — прості відсотки за строк "+
			"загубились, і вклад завищений у рейтингу", got)
	}
}

// TestEffectiveRateMonthlyPayoutIsHigher — вклад із щомісячною виплатою
// дає БІЛЬШЕ за договірну ставку: гроші приходять раніше.
//
// Другий бік тієї самої вади, протилежного знаку: такий вклад
// ЗАНИЖУВАВСЯ. Порівнюємо з (1 + r/12)^12 − 1 — саме стільки й означає
// «щомісячна виплата» в термінах IRR.
func TestEffectiveRateMonthlyPayoutIsHigher(t *testing.T) {
	d := mkDeposit(12, 1600, 0, PayoutMonthly, false)
	got := d.EffectiveNetRate()
	want := math.Pow(1+0.16/12, 12) - 1
	if math.Abs(got-want) > 0.003 {
		t.Errorf("ефективна %.4f, а щомісячний компаунд дає %.4f", got, want)
	}
	if got <= 0.16 {
		t.Errorf("ефективна %.4f не вища за договірні 16%% — раннє надходження "+
			"грошей загубилось, і вклад занижений у рейтингу", got)
	}
}

// TestEffectiveRateTakesTax — податок лишається врахованим.
//
// Окремо, бо в двох тестах вище він нульовий заради читабельності
// арифметики, а найлегше загубити його саме при переході на IRR: у
// потоках стоїть Net(), і одна описка перетворила б чисту ставку на
// валову мовчки.
func TestEffectiveRateTakesTax(t *testing.T) {
	gross := mkDeposit(12, 1600, 0, PayoutEnd, false).EffectiveNetRate()
	net := mkDeposit(12, 1600, 2300, PayoutEnd, false).EffectiveNetRate()
	if math.Abs(gross-0.16) > 0.001 {
		t.Errorf("без податку ефективна %.4f, чекали 0.16", gross)
	}
	if math.Abs(net-0.16*0.77) > 0.001 {
		t.Errorf("з податком 23%% ефективна %.4f, чекали %.4f", net, 0.16*0.77)
	}
}

// TestEffectiveRateIgnoresTopups — поповнення ставки не міняють.
//
// Ставка описує ДОГОВІР: докладене йде під ті самі умови. Інакше два
// однакові договори стояли б у колонці, яка їх порівнює, з різними
// числами — залежно від того, скільки грошей туди встигли докласти.
func TestEffectiveRateIgnoresTopups(t *testing.T) {
	d := mkDeposit(24, 1600, 2300, PayoutEnd, false)
	bare := d.EffectiveNetRate()
	d.Topups = []DepositTopup{{Date: Date("2026-06-01"), Amount: 50_000_00}}
	if got := d.EffectiveNetRate(); math.Abs(got-bare) > 1e-9 {
		t.Errorf("поповнення зрушило ставку: %.6f проти %.6f", got, bare)
	}
}

// TestEffectiveRateFallsBackOnBrokenTerm — битий строк не валить рядок, а
// відкочується на просту ставку.
//
// Нуль тут читався б як «вклад нічого не дає» й тягнув би зважену
// дохідність униз; краще показати договірне число, ніж вигадане.
func TestEffectiveRateFallsBackOnBrokenTerm(t *testing.T) {
	d := mkDeposit(0, 1600, 2300, PayoutEnd, false)
	d.MaturityDate = d.OpenDate
	if got, want := d.EffectiveNetRate(), NetRate(1600, 2300); math.Abs(got-want) > 1e-9 {
		t.Errorf("на битому строку %.6f замість простої %.6f", got, want)
	}
}
