// Презентер — валюта звітності на виході.
//
// Документ і відповіді обробників будуються в гривні (у ній облік), а
// показуються у валюті з settings.report_currency. Сам переклад робить
// internal/present за типом state.Money; тут — те, чого той пакет не знає:
// звідки валюта, звідки курси й що казати, коли курсу немає.
//
// ТРИ МІСЦЯ ЗАСТОСУВАННЯ, І САМЕ ТРИ: /api/summary, публікація в MQTT
// (jobs.Runner.PublishState через PresentDoc) і обробники з власними
// відповідями. Добовий знімок презентера не бачить — він читає сирий
// документ, і саме так гривня в базі лишається гривнею (довід у шапці
// internal/present).
//
// КУРСИ — ОДНИМ ЧИТАННЯМ на запит: уся історія валюти звітності
// (помісячно 10 років бекфілу плюс щодня відколи працює демон — сотні
// точок), відсортована раз, далі бінарний пошук на кожну дату. Не
// asOfRates: той робить запит на кожну унікальну дату, а тут дат стільки,
// скільки рядків у знімках.
package api

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/present"
	"github.com/ODDsama/oddinvest/internal/state"
)

const reportCurrencyKey = "report_currency"

// reportCurrency — ЕФЕКТИВНА валюта звітності: та, що в налаштуваннях, або
// гривня, коли курсу для неї ще немає. Одне означення на всіх: презентер
// перекладає числа, будівник за тим самим кодом вибирає лінійку
// (state_sources.go) — і розійтись їм нема де.
func (s *Server) reportCurrency(ctx context.Context) (string, error) {
	raw, err := s.st.GetSetting(ctx, reportCurrencyKey)
	if err != nil {
		return "", err
	}
	report := strings.TrimSpace(raw)
	if report == "" || report == money.UAH {
		return money.UAH, nil
	}
	e4, err := s.st.LatestRate(ctx, report)
	if err != nil {
		return "", err
	}
	if e4 <= 0 {
		return money.UAH, nil
	}
	return report, nil
}

type presenter struct {
	// report — ЕФЕКТИВНА валюта: та, що просили, або гривня, коли курсу на
	// сьогодні ще немає; note тоді каже чому.
	report string
	note   string
	quotes domain.Quotes // за датою, зростає
	today  domain.Date
}

// presenter — валюта з налаштувань і курси з бази, один раз на запит.
func (s *Server) presenter(ctx context.Context, today domain.Date) (*presenter, error) {
	p := &presenter{report: money.UAH, today: today}
	raw, err := s.st.GetSetting(ctx, reportCurrencyKey)
	if err != nil {
		return nil, err
	}
	report := strings.TrimSpace(raw)
	if report == "" || report == money.UAH {
		return p, nil
	}
	q, err := s.quotesSince(ctx, report, "")
	if err != nil {
		return nil, err
	}
	sort.SliceStable(q, func(i, j int) bool { return q[i].On < q[j].On })
	p.quotes = q
	// «Сьогодні» для курсу — найновіша відома точка, навіть якщо вона
	// датована завтра: НБУ вдень публікує курс на наступний день, і саме
	// його документ віддає в rates (LatestRate). Брати тут «останню не
	// пізніше сьогодні» означало б перекладати капітал старішим курсом, ніж
	// той, що стоїть поруч у тому самому документі, — і 49 209 ₴ ставали б
	// 1 105 $ при курсі, за яким це 1 104,52.
	p.today = latestAsOf(q, today)
	if _, ok := p.At(p.today); !ok {
		// Свіжа база до першого оновлення НБУ. Не помилка й не нулі: людина
		// попросила долар, а показати його нема чим — сказати це прямо й
		// лишитись у гривні, поки курс не приїде.
		p.note = fmt.Sprintf("курсу НБУ для %s ще немає — показано в гривні", report)
		return p, nil
	}
	p.report = report
	return p, nil
}

// latestAsOf — «сьогодні» очима курсів: сама дата, або найновіша точка,
// коли вона вже датована пізніше (завтрашній курс НБУ). Одне правило для
// презентера й для будівника (дельта капіталу), щоб вони брали ту саму
// точку, що й rates у документі.
func latestAsOf(q domain.Quotes, today domain.Date) domain.Date {
	if n := len(q); n > 0 && q[n-1].On > today {
		return q[n-1].On
	}
	return today
}

// At — курс на дату: остання точка НЕ ПІЗНІША за неї (та сама умова, що в
// domain.Quotes.AsOf, лише без сортування на кожен виклик).
func (p *presenter) At(on domain.Date) (float64, bool) {
	i := sort.Search(len(p.quotes), func(i int) bool { return p.quotes[i].On > on })
	if i == 0 {
		return 0, false
	}
	return p.quotes[i-1].V, true
}

func (p *presenter) apply(v any) error {
	return present.Apply(v, present.Opts{Book: money.UAH, Report: p.report, Rates: p, Today: p.today})
}

// doc — документ стану: переклад плюс примітка, чому валюта не та.
func (p *presenter) doc(d *state.Doc) error {
	if err := p.apply(d); err != nil {
		return err
	}
	d.CurrencyNote = p.note
	return nil
}

// present — обробникам: перекласти відповідь перед writeJSON.
func (s *Server) present(ctx context.Context, v any) error {
	p, err := s.presenter(ctx, domain.NewDate(time.Now()))
	if err != nil {
		return err
	}
	if d, ok := v.(*state.Doc); ok {
		return p.doc(d)
	}
	return p.apply(v)
}

// PresentDoc — для публікації в MQTT: той самий шлях, що /api/summary, щоб
// Home Assistant бачив рівно те, що бачить застосунок.
func (s *Server) PresentDoc(ctx context.Context, doc *state.Doc) error {
	return s.present(ctx, doc)
}

// quotesSince — історія курсу валюти одним читанням, у мажорних одиницях.
//
// Точка ПЕРЕД початком потрібна окремо: історія курсів помісячна, і без
// неї ряд мовчав би на всіх днях до першого числа наступного місяця.
// Порожній from — уся історія.
func (s *Server) quotesSince(ctx context.Context, code string, from domain.Date) (domain.Quotes, error) {
	pts, err := s.st.RatesSince(ctx, code, from)
	if err != nil {
		return nil, err
	}
	if from != "" {
		if p, err := s.st.RatePointOnOrBefore(ctx, code, from); err == nil && p.RateE4 > 0 {
			pts = append(pts, p)
		}
	}
	q := make(domain.Quotes, 0, len(pts))
	for _, p := range pts {
		q = append(q, domain.Quote{On: p.Date, V: fx.Major(p.RateE4)})
	}
	return q, nil
}
