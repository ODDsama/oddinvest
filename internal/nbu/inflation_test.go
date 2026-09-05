package nbu

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func inflationServer(t *testing.T, body []byte, gotQuery *string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotQuery != nil {
			*gotQuery = r.URL.RawQuery
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestInflationSendsNextMonthAndReturnsAskedMonth — ПІН ЗСУВУ.
//
// НБУ адресує ці дані датою «станом на 1-ше», тож за грудень 2024 треба
// питати date=202501. Помилка тут тиха: ряд лишається правдоподібним,
// просто кожне число стоїть не на своєму місяці, і всі реальні
// дохідності стають хибними приблизно на відсотковий пункт.
func TestInflationSendsNextMonthAndReturnsAskedMonth(t *testing.T) {
	var query string
	c := inflationServer(t, fixture(t, "inflation_202501.json"), &query)

	p, err := c.Inflation(context.Background(), "2024-12")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "date=202501") {
		t.Fatalf("за грудень 2024 треба питати date=202501, пішло %q", query)
	}
	if !strings.Contains(query, "period=m") {
		t.Fatalf("період не місячний: %q", query)
	}
	if p.Period != "2024-12" {
		t.Fatalf("назовні мусить повертатись ЗВІТНИЙ місяць, маємо %q", p.Period)
	}
	// Грудень 2024, опубліковано: м/м 1.4%, р/р 12.0%.
	if p.MoMBP != 140 || p.YoYBP != 1200 {
		t.Fatalf("м/м=%d р/р=%d, хочемо 140 і 1200", p.MoMBP, p.YoYBP)
	}
}

// TestInflationPicksNationalHeadlineRow — у відповіді ~780 рядків, і три
// з них схожі на потрібний до нерозрізненності: регіон із тим самим
// розділом, розділ COICOP із тим самим регіоном і БАЗОВИЙ ІСЦ, у якого
// збігається все, крім id_api.
func TestInflationPicksNationalHeadlineRow(t *testing.T) {
	c := inflationServer(t, fixture(t, "inflation_202501.json"), nil)

	p, err := c.Inflation(context.Background(), "2024-12")
	if err != nil {
		t.Fatal(err)
	}
	if p.MoMBP == 110 {
		t.Fatal("узято базовий ІСЦ замість загального")
	}
	if p.MoMBP == 160 {
		t.Fatal("узято регіональний рядок")
	}
	if p.MoMBP == 240 {
		t.Fatal("узято окремий розділ COICOP")
	}
	if p.MoMBP == 310 {
		t.Fatal("узято індекс цін виробників")
	}
}

func TestInflationFailsWhenHeadlineRowMissing(t *testing.T) {
	// Усе те саме, але національного рядка немає — лишились приманки.
	body := []byte(`[
	  {"id_api":"prices_price_cpi_","mcrd081":"Total","ku":"26","tzep":"PCPM_","value":1.6},
	  {"id_api":"prices_price_ci_","mcrd081":"Total","ku":null,"tzep":"PCPM_","value":1.1}
	]`)
	c := inflationServer(t, body, nil)

	_, err := c.Inflation(context.Background(), "2024-12")
	if err == nil {
		t.Fatal("відсутній рядок мусить падати, а не давати нуль")
	}
	if !strings.Contains(err.Error(), "форма відповіді змінилась") {
		t.Fatalf("помилка не називає причини: %v", err)
	}
}

func TestInflationRejectsAmbiguousMatch(t *testing.T) {
	body := []byte(`[
	  {"id_api":"prices_price_cpi_","mcrd081":"Total","ku":null,"tzep":"PCPM_","value":1.4},
	  {"id_api":"prices_price_cpi_","mcrd081":"Total","ku":null,"tzep":"PCPM_","value":1.7},
	  {"id_api":"prices_price_cpi_","mcrd081":"Total","ku":null,"tzep":"PCCM_","value":12.0}
	]`)
	c := inflationServer(t, body, nil)

	_, err := c.Inflation(context.Background(), "2024-12")
	if err == nil {
		t.Fatal("два кандидати — це зміна форми відповіді, а не привід узяти перший")
	}
	if !strings.Contains(err.Error(), "2 збігів") {
		t.Fatalf("помилка не каже, скільки збігів: %v", err)
	}
}

// TestInflationEmptyMonthIsNotAnError — перші 8-10 днів місяця ІСЦ за
// попередній ще не опублікований, і порожній масив тут звичайна
// відповідь. Джоба на ній зупиняється, а не падає.
func TestInflationEmptyMonthIsNotAnError(t *testing.T) {
	c := inflationServer(t, []byte(`[]`), nil)

	_, err := c.Inflation(context.Background(), "2026-08")
	if !errors.Is(err, ErrCPINotPublished) {
		t.Fatalf("порожній місяць мусить давати ErrCPINotPublished, маємо %v", err)
	}
}

func TestInflationRejectsBadMonth(t *testing.T) {
	c := inflationServer(t, []byte(`[]`), nil)
	if _, err := c.Inflation(context.Background(), "2024-13"); err == nil {
		t.Fatal("неіснуючий місяць мусить падати до запиту")
	}
}
