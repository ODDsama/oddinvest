package store

import (
	"context"
	"testing"
)

func TestCPIRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	pts := []CPIPoint{
		{Period: "2024-11", MoMBP: 190, YoYBP: 1120},
		{Period: "2024-12", MoMBP: 140, YoYBP: 1200},
		{Period: "2025-01", MoMBP: 120, YoYBP: 1230},
	}
	for _, p := range pts {
		if err := s.SaveCPI(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.CPISince(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Period != "2024-11" || got[2].Period != "2025-01" {
		t.Fatalf("ряд не за зростанням або неповний: %+v", got)
	}
	if got[1].MoMBP != 140 || got[1].YoYBP != 1200 {
		t.Fatalf("грудень 2024 читається не тим, чим записаний: %+v", got[1])
	}

	from, err := s.CPISince(ctx, "2024-12")
	if err != nil {
		t.Fatal(err)
	}
	if len(from) != 2 || from[0].Period != "2024-12" {
		t.Fatalf("відсікання за місяцем не спрацювало: %+v", from)
	}

	newest, err := s.NewestCPI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if newest.Period != "2025-01" {
		t.Fatalf("найсвіжіша точка = %q", newest.Period)
	}

	// Ідемпотентність: бекфіл і добова джоба перекриваються на межі, і
	// повторний запис мусить ОНОВИТИ, а не подвоїти.
	if err := s.SaveCPI(ctx, CPIPoint{Period: "2024-12", MoMBP: 150, YoYBP: 1210}); err != nil {
		t.Fatal(err)
	}
	n, err := s.CPIMonthCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("повторний запис подвоїв місяць: %d", n)
	}
	again, err := s.CPISince(ctx, "2024-12")
	if err != nil {
		t.Fatal(err)
	}
	if again[0].MoMBP != 150 {
		t.Fatalf("повторний запис не оновив число: %+v", again[0])
	}
}

// TestCPIEmptySeriesIsNotAnError — порожній ряд це стан першого запуску, а
// не збій: джоба з нього починає бекфіл, картка на ньому мовчить.
func TestCPIEmptySeriesIsNotAnError(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	p, err := s.NewestCPI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Period != "" {
		t.Fatalf("на порожній базі найсвіжіша точка = %q", p.Period)
	}
	n, err := s.CPIMonthCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("на порожній базі місяців %d", n)
	}
}
