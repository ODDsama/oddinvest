package nbu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Індекс споживчих цін — місячна статистика НБУ.
//
// inflationURI бере date=YYYYMM, тобто ТРЕТІЙ формат дати в цьому
// клієнті: exchange хоче YYYYMMDD, аукціони — DD.MM.YYYY (довід при
// auctionsURI), а тут місяць без дня. Розходження в межах одного API вже
// не двостороннє, а тристороннє, і спільного хелпера дат тут не буде й
// поготів.
const inflationURI = "/NBUStatService/v1/statdirectory/inflation?period=m&date=%s&json"

// ErrCPINotPublished — місяць ще не вийшов.
//
// Держстат публікує ІСЦ близько 8-10 числа наступного місяця, тож перші
// дні кожного місяця цей ендпойнт законно віддає порожній масив. Це
// звичайна відповідь, а не збій мережі, і джоба на ній зупиняється,
// лишаючи водяний знак на місці.
var ErrCPINotPublished = errors.New("НБУ ІСЦ: місяць ще не опубліковано")

// CPIPoint — опублікована пара за звітний місяць.
type CPIPoint struct {
	Period string // 'YYYY-MM', звітний місяць
	MoMBP  int64  // % до попереднього місяця × 100
	YoYBP  int64  // % до того самого місяця торік × 100
}

// rawInflation — один рядок відповіді. Полів у ній більше (txt, txten,
// leveli, parent, freq, mcrk110), але жодне з них не бере участі у
// відборі, тож тут їх немає: перелічене поле читається як таке, що на
// щось впливає.
type rawInflation struct {
	IDAPI    string      `json:"id_api"`
	Division string      `json:"mcrd081"`
	KU       *string     `json:"ku"`
	TZEP     string      `json:"tzep"`
	Value    json.Number `json:"value"`
}

const (
	// Ключі національного рядка ІСЦ. Відповідь на один місяць — це ~780
	// рядків: 25 регіонів × 12 розділів COICOP, плюс індекс цін
	// виробників і базовий ІСЦ (обидва з ІНШИМ id_api).
	cpiIDAPI    = "prices_price_cpi_"
	cpiDivision = "Total" // усі розділи разом, а не окремий розділ COICOP
	cpiMoM      = "PCPM_" // % до попереднього місяця
	cpiYoY      = "PCCM_" // % до того самого місяця торік
)

// Inflation — ІСЦ за ЗВІТНИЙ місяць month у форматі 'YYYY-MM'.
//
// ЗСУВ НА МІСЯЦЬ — найдорожча пастка цього ендпойнта, і тому він
// знімається тут, на межі клієнта: назовні його не існує взагалі.
// Параметр date= НБУ розуміє як «станом на 1-ше», тобто відповідь містить
// дані за ПОПЕРЕДНІЙ місяць. Перевірено на трьох контрольних точках:
//
//	date=202501 → грудень 2024: м/м 1.4, р/р 12.0
//	date=202412 → листопад 2024: м/м 1.9, р/р 11.2
//	date=202210 → вересень 2022: м/м 1.9, р/р 24.6
//
// Усі три збігаються з опублікованими Держстатом числами. Помилка знака
// тут тиха за побудовою: ряд лишається правдоподібним, просто кожне число
// стоїть не на своєму місяці — тому під ним стоїть ДРУГА, незалежна
// сітка, domain.CPIYoYDrift.
//
// ВІДБІР — четвірка (id_api, mcrd081, ku=null, tzep), і на кожне
// перетворення мусить знайтись РІВНО ОДИН рядок. Нуль або кілька — це
// зміна форми відповіді, і вона мусить впасти голосно: мовчазне «нічого
// не збіглось → нуль» дало б нульову інфляцію, яка виглядає як
// вимірювання.
func (c *Client) Inflation(ctx context.Context, month string) (CPIPoint, error) {
	asOf, err := nextMonthParam(month)
	if err != nil {
		return CPIPoint{}, err
	}
	url := c.base + fmt.Sprintf(inflationURI, asOf)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return CPIPoint{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return CPIPoint{}, fmt.Errorf("НБУ ІСЦ %s: %w", month, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CPIPoint{}, fmt.Errorf("НБУ ІСЦ %s: HTTP %d", month, resp.StatusCode)
	}
	var raw []rawInflation
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return CPIPoint{}, fmt.Errorf("НБУ ІСЦ %s: декодування: %w", month, err)
	}
	if len(raw) == 0 {
		return CPIPoint{}, ErrCPINotPublished
	}
	mom, err := pickNational(raw, cpiMoM, month)
	if err != nil {
		return CPIPoint{}, err
	}
	yoy, err := pickNational(raw, cpiYoY, month)
	if err != nil {
		return CPIPoint{}, err
	}
	return CPIPoint{Period: month, MoMBP: mom, YoYBP: yoy}, nil
}

// nextMonthParam: 'YYYY-MM' -> 'YYYYMM' наступного місяця. Один рядок, у
// якому й живе весь зсув.
func nextMonthParam(month string) (string, error) {
	d, err := domain.ParseDate(month + "-01")
	if err != nil {
		return "", fmt.Errorf("НБУ ІСЦ: місяць %q: %w", month, err)
	}
	next := string(d.AddMonths(1))
	return next[0:4] + next[5:7], nil
}

func pickNational(rows []rawInflation, tzep, month string) (int64, error) {
	var hits []rawInflation
	for _, r := range rows {
		if r.IDAPI == cpiIDAPI && r.Division == cpiDivision && r.KU == nil && r.TZEP == tzep {
			hits = append(hits, r)
		}
	}
	if len(hits) != 1 {
		return 0, fmt.Errorf(
			"НБУ ІСЦ %s: національний рядок %s не знайдено або неоднозначний (%d збігів, форма відповіді змінилась)",
			month, tzep, len(hits))
	}
	return parseRateBP(hits[0].Value.String())
}
