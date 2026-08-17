// Package jobs — фонові процеси: добове оновлення довідника НБУ і курсу,
// щоденний знімок портфеля, публікація стану в MQTT.
package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/mqtt"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

type Runner struct {
	st         *store.Store
	nbu        *nbu.Client
	pub        *mqtt.Publisher // nil = MQTT вимкнено
	build      func(ctx context.Context, now time.Time) (*state.Doc, error)
	log        *slog.Logger
	loc        *time.Location
	backupPath string // куди щодня писати JSON-дамп (порожньо = вимкнено)
	// pause — витримка між запитами до НБУ при переборі дат. Поле, а не
	// константа, лише щоб тести не чекали хвилинами на те, що вони й не
	// перевіряють; у бою вона завжди така, як її ставить New.
	pause time.Duration
}

func New(st *store.Store, nc *nbu.Client, pub *mqtt.Publisher,
	build func(ctx context.Context, now time.Time) (*state.Doc, error), log *slog.Logger, backupPath string) *Runner {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		loc = time.FixedZone("EET", 2*3600)
	}
	return &Runner{st: st, nbu: nc, pub: pub, build: build, log: log, loc: loc,
		backupPath: backupPath, pause: 250 * time.Millisecond}
}

// dumpBackup — щоденний JSON-дамп користувацьких даних поряд із БД.
// Пишемо атомарно (temp + rename), щоб бекап Proxmox не спіймав半-файл.
// Помилка не фатальна: це страховка, а не основний шлях.
func (r *Runner) dumpBackup(ctx context.Context) {
	if r.backupPath == "" {
		return
	}
	b, err := r.st.ExportAll(ctx)
	if err != nil {
		r.log.Warn("бекап: експорт не вдався", "err", err)
		return
	}
	b.ExportedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		r.log.Warn("бекап: серіалізація", "err", err)
		return
	}
	tmp := r.backupPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		r.log.Warn("бекап: запис", "err", err)
		return
	}
	if err := os.Rename(tmp, r.backupPath); err != nil {
		r.log.Warn("бекап: rename", "err", err)
		return
	}
	r.log.Info("бекап збережено", "path", r.backupPath,
		"лотів", len(b.Lots), "поповнень", len(b.Deposits))
}

// RefreshAll — довідник НБУ + курс USD + знімок + публікація.
func (r *Runner) RefreshAll(ctx context.Context) error {
	secs, err := r.nbu.Securities(ctx)
	if err != nil {
		return err
	}
	if err := r.st.ReplaceDirectory(ctx, secs, time.Now()); err != nil {
		return err
	}
	// Позначаємо час успішного оновлення: інакше несвіжість довідника
	// лишається тихою (порожній довідник ми свого часу помітили випадково).
	if err := r.st.SetSetting(ctx, "nbu_refreshed_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		r.log.Warn("не зберіг час оновлення довідника", "err", err)
	}
	r.log.Info("довідник НБУ оновлено", "паперів", len(secs))

	rateDate := domain.NewDate(time.Now().In(r.loc))
	for _, code := range []string{"USD", "EUR"} {
		rate, quoted, err := r.nbu.RateOn(ctx, code, "")
		if err != nil {
			r.log.Warn("курс недоступний", "code", code, "err", err) // не фатально: працюємо на останньому
			continue
		}
		// Пишемо під датою КОТИРУВАННЯ, а не запуску: у вихідні НБУ віддає
		// п'ятничний курс, і під суботнім числом виходила б точка, якої не
		// існувало. Доки історія читалась лише останнім рядком, це нічого
		// не важило; для вимірювання темпу — важить.
		d := rateDate
		if quoted != "" {
			d = quoted
		}
		if err := r.st.SaveRate(ctx, code, rate, d); err != nil {
			return err
		}
		r.log.Info("курс збережено", "code", code, "rate_e4", rate, "дата", string(d))
	}

	// Аукціони не фатальні, як і курс: без них не буде кривої первинного
	// ринку, але портфель рахується й публікується далі.
	if err := r.RefreshAuctions(ctx); err != nil {
		r.log.Warn("аукціони недоступні", "err", err)
	}

	if err := r.Snapshot(ctx); err != nil {
		r.log.Warn("знімок не збережено", "err", err)
	}
	r.dumpBackup(ctx)
	return r.PublishState(ctx)
}

// auctionWatermark — до якого дня аукціони вже переглянуто. Робочий стан
// джоби, а не політика портфеля, тому в реєстрі налаштувань його немає —
// так само, як nbu_refreshed_at поруч.
const auctionWatermark = "ovdp_auctions_polled_through"

// auctionCatchupCap — скільки днів максимум догоняти за один прогін.
// RefreshAll живе під п'ятихвилинним контекстом, а сервіс міг стояти
// вимкненим місяцями: без стелі перший запуск після довгої паузи з'їв би
// увесь контекст на перебір дат і не дійшов би ні до знімка, ні до
// публікації стану.
const auctionCatchupCap = 60

// RefreshAuctions — результати аукціонів за дні, яких ще немає.
//
// Усталений режим — ОДИН запит на добу, і тримається він на тому, що
// запит без дати віддає останній аукціонний день. Якщо цей день не
// новіший за той, до якого ми вже дійшли, то між ними й немає нічого:
// аукціону, свіжішого за найсвіжіший, не буває. Перебирати дати треба
// лише тоді, коли сервіс стояв і пропустив тижні.
//
// Аукціони бувають раз на тиждень, тож чотири дні з п'яти відповідь
// порожня — і це не привід ні для помилки, ні для запитів.
func (r *Runner) RefreshAuctions(ctx context.Context) error {
	today := domain.NewDate(time.Now().In(r.loc))
	latest, err := r.nbu.Auctions(ctx, "")
	if err != nil {
		return err
	}
	if err := r.st.SaveAuctions(ctx, latest); err != nil {
		return err
	}
	through, err := r.st.GetSetting(ctx, auctionWatermark)
	if err != nil {
		return err
	}
	var newest domain.Date
	for _, a := range latest {
		if a.Date > newest {
			newest = a.Date
		}
	}
	// Перший запуск історію не тягне — це справа бекфілу, який іде
	// власною горутиною й має власний, довгий таймаут. Добова джоба не
	// місце для сотні запитів.
	from, perr := time.Parse("2006-01-02", through)
	if through == "" || perr != nil || (newest != "" && string(newest) <= through) {
		return r.st.SetSetting(ctx, auctionWatermark, string(today))
	}
	end := time.Now().In(r.loc)
	if d := end.AddDate(0, 0, -auctionCatchupCap); d.After(from) {
		r.log.Info("аукціони: догін обрізано стелею",
			"від", through, "беремо з", domain.NewDate(d), "стеля_днів", auctionCatchupCap)
		from = d
	}
	var got int
	for d := from.AddDate(0, 0, 1); !d.After(end); d = d.AddDate(0, 0, 1) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		as, err := r.nbu.Auctions(ctx, domain.NewDate(d))
		if err != nil {
			// Один недоступний день не привід кидати решту: наступний
			// прогін догоняє його з того самого знака.
			r.log.Debug("аукціони: день пропущено", "date", domain.NewDate(d), "err", err)
			time.Sleep(r.pause)
			continue
		}
		if err := r.st.SaveAuctions(ctx, as); err != nil {
			return err
		}
		got += len(as)
		time.Sleep(r.pause)
	}
	r.log.Info("аукціони догнано", "рядків", got, "від", through, "до", string(today))
	return r.st.SetSetting(ctx, auctionWatermark, string(today))
}

// BackfillAuctions — разово підтягує історію аукціонів за weeks тижнів.
//
// ЩОДНЯ, а не помісячно, як BackfillRates поруч, і це не недогляд.
// Курс — рівень, який існує кожного дня, тож місячної сітки для річного
// темпу досить. Аукціон — ПОДІЯ: він або був того дня, або ні, і
// місячна сітка пропустила б їх майже всі.
//
// 52 тижні, хоч НБУ віддає історію років на десять. Жодне питання в
// застосунку не питає про первинний ринок 2015 року, а 3650 запитів —
// це рівно те зловживання чужим сервісом, від якого відмовляється
// BackfillRates. Те саме число стоїть порогом «несвіжості» в помічнику
// реінвесту: застосунок не заявляє знання глибше, ніж тягне.
func (r *Runner) BackfillAuctions(ctx context.Context, weeks int) error {
	end := time.Now().In(r.loc)
	start := end.AddDate(0, 0, -weeks*7)
	var got, failed int
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		as, err := r.nbu.Auctions(ctx, domain.NewDate(d))
		if err != nil {
			failed++
			r.log.Debug("бекфіл аукціонів: день пропущено", "date", domain.NewDate(d), "err", err)
			time.Sleep(r.pause)
			continue
		}
		if err := r.st.SaveAuctions(ctx, as); err != nil {
			return err
		}
		got += len(as)
		time.Sleep(r.pause)
	}
	r.log.Info("історію аукціонів підтягнуто", "рядків", got, "днів_пропущено", failed)
	return r.st.SetSetting(ctx, auctionWatermark, string(domain.NewDate(end)))
}

// BackfillAuctionsIfThin — бекфіл лише тоді, коли історії справді мало.
// Викликається при старті у власній горутині: мережа може лежати, і
// сервіс не має через це не піднятись.
func (r *Runner) BackfillAuctionsIfThin(ctx context.Context, weeks, minDays int) {
	have, err := r.st.CountAuctionDays(ctx)
	if err != nil {
		r.log.Warn("бекфіл аукціонів: не вдалось порахувати історію", "err", err)
		return
	}
	if have >= minDays {
		return
	}
	r.log.Info("історії аукціонів замало — тягнемо з НБУ", "днів", have, "треба", minDays)
	if err := r.BackfillAuctions(ctx, weeks); err != nil {
		r.log.Warn("бекфіл аукціонів не завершився", "err", err)
	}
}

// Snapshot зберігає добовий знімок агрегатів для майбутнього графіка
// «факт vs модель».
func (r *Runner) Snapshot(ctx context.Context) error {
	doc, err := r.build(ctx, time.Now())
	if err != nil {
		return err
	}
	today := domain.NewDate(time.Now().In(r.loc))
	return r.st.SaveSnapshot(ctx, store.Snapshot{
		Date:           today,
		InvestedUAH:    int64(doc.InvestedUAH * 100),
		NominalUAHEq:   int64(doc.NominalUAHEq * 100),
		USDShareBP:     int64(doc.USDSharePct * 100),
		UninvestedUAH:  int64(doc.UninvestedUAH * 100),
		MonthTargetUAH: int64(doc.MonthTargetUAH * 100),
		AccountUAH:     int64(doc.AccountUAH * 100),
		FundsUAH:       int64(doc.FundsUAH * 100),
		DepositsUAH:    int64(doc.DepositsUAH * 100),
		// Собівартість фондів беремо готовою з документа, а не складаємо
		// тут заново: без неї крива не може показати прибуток (InvestedUAH —
		// це лише облігації), а друга копія суми рано чи пізно розійшлася б
		// із тією, що показує плитка «Вкладено».
		FundsCostUAH: int64(doc.FundsCostUAH * 100),
		// Резерв — частина капіталу, тож без нього крива показувала б
		// портфель меншим за фактичний. Собівартості в нього немає: вона
		// дорівнює самій сумі, і в прибуток він не додає нічого.
		ReserveUAH: int64(doc.ReserveUAH * 100),
		// НПФ — обидва числа, і друге не для симетрії: собівартість тут
		// РЕАЛЬНА (внески) і відрізняється від вартості, тож без неї крива
		// малювала б прибуток, завищений на весь пенсійний баланс. Резерву
		// така пара не потрібна саме тому, що в нього вони збігаються.
		NPFUAH:     int64(doc.NPFUAH * 100),
		NPFCostUAH: int64(doc.NPFCostUAH * 100),
	})
}

func (r *Runner) PublishState(ctx context.Context) error {
	if r.pub == nil {
		return nil
	}
	doc, err := r.build(ctx, time.Now())
	if err != nil {
		return err
	}
	b, err := doc.JSON()
	if err != nil {
		return err
	}
	return r.pub.PublishState(b)
}

// BackfillRates — разово підтягує історію курсу з НБУ, по одній точці на
// місяць за years років назад.
//
// Навіщо. Знецінення гривні тепер вимірюється, а не припускається, і
// міряти його треба довгим вікном: коротке ловить або стрибок, або
// затишшя між стрибками, і число стає лотереєю. Але fx_rates наповнюється
// добовою джобою з дня встановлення, тож на свіжій базі історії немає
// взагалі, а на працюючій — рівно стільки, скільки демон прожив.
//
// Помісячно, а не щодня: для річного темпу за десять років денна
// докладність нічого не додає, а 3650 запитів до НБУ замість 120 — це
// зловживання чужим сервісом. Пауза між запитами з тієї ж причини.
//
// Ідемпотентно: SaveRate пише через ON CONFLICT, тож повторний прогін
// нічого не зіпсує. Помилка окремої дати не зупиняє решту — НБУ може не
// мати котирування на конкретний день, і це нормально.
func (r *Runner) BackfillRates(ctx context.Context, code string, years int) error {
	const pause = 250 * time.Millisecond
	now := time.Now().In(r.loc)
	var got, failed int
	for i := years * 12; i > 0; i-- {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		// Перше число кожного місяця; НБУ на вихідний віддасть курс
		// попереднього робочого дня і сам назве його дату.
		d := domain.NewDate(now.AddDate(0, -i, 0))
		day := domain.Date(string(d)[:8] + "01")
		rate, quoted, err := r.nbu.RateOn(ctx, code, day)
		if err != nil {
			failed++
			r.log.Debug("backfill: дата пропущена", "code", code, "date", string(day), "err", err)
			time.Sleep(pause)
			continue
		}
		if quoted == "" {
			quoted = day
		}
		if err := r.st.SaveRate(ctx, code, rate, quoted); err != nil {
			return err
		}
		got++
		time.Sleep(pause)
	}
	r.log.Info("історію курсу підтягнуто", "code", code, "точок", got, "пропущено", failed)
	return nil
}

// BackfillIfThin запускає backfill лише тоді, коли історії справді мало.
// Викликається при старті у власній горутині: мережа може лежати, і
// сервіс не має через це не піднятись.
func (r *Runner) BackfillIfThin(ctx context.Context, code string, years, minMonths int) {
	have, err := r.st.RateMonthCount(ctx, code)
	if err != nil {
		r.log.Warn("backfill: не вдалось порахувати історію", "err", err)
		return
	}
	if have >= minMonths {
		return
	}
	r.log.Info("історії курсу замало — тягнемо з НБУ", "code", code, "місяців", have, "треба", minMonths)
	if err := r.BackfillRates(ctx, code, years); err != nil {
		r.log.Warn("backfill не завершився", "err", err)
	}
}

// RunDaily — цикл: щодня о 06:10 Києва RefreshAll.
func (r *Runner) RunDaily(ctx context.Context) {
	for {
		now := time.Now().In(r.loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), 6, 10, 0, 0, r.loc)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		r.log.Info("наступне оновлення", "at", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			if err := r.RefreshAll(cctx); err != nil {
				r.log.Error("добове оновлення", "err", err)
			}
			cancel()
		}
	}
}
