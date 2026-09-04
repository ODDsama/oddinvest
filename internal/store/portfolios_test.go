package store

import (
	"context"
	"errors"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func TestPortfoliosCRUD(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	// Свіжа база має рівно головний портфель.
	list, err := s.ListPortfolios(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != MainPortfolio || list[0].Slug != "main" {
		t.Fatalf("свіжа база: %+v", list)
	}

	id, err := s.AddPortfolio(ctx, "wife", "Дружина")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPortfolio(ctx, "wife", "Ще раз"); !errors.Is(err, ErrConflict) {
		t.Errorf("повторний slug: %v, хочемо ErrConflict", err)
	}
	for _, bad := range []string{"", "Дружина", "wife portfolio", "-x", "a-very-long-slug-that-exceeds-thirty-two-chars"} {
		if _, err := s.AddPortfolio(ctx, bad, "x"); err == nil {
			t.Errorf("slug %q прийнято", bad)
		}
	}
	if _, err := s.AddPortfolio(ctx, "kid", "  "); err == nil {
		t.Error("порожня назва прийнята")
	}

	p, err := s.PortfolioBySlug(ctx, "wife")
	if err != nil || p == nil || p.ID != id || p.Name != "Дружина" {
		t.Fatalf("за slug: %+v, %v", p, err)
	}
	if p, err := s.PortfolioBySlug(ctx, "nobody"); err != nil || p != nil {
		t.Errorf("невідомий slug: %+v, %v", p, err)
	}

	if err := s.RenamePortfolio(ctx, id, "Олена"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenamePortfolio(ctx, 99, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("перейменування неіснуючого: %v", err)
	}
	p, _ = s.PortfolioBySlug(ctx, "wife")
	if p.Name != "Олена" {
		t.Errorf("назва не змінилась: %q", p.Name)
	}

	// Вміст стирається разом із портфелем, чужий — ні.
	w := s.For(id)
	if _, err := w.AddLot(ctx, domain.Lot{ISIN: "UA1", Qty: 1,
		PricePerBond: money.New(100000, money.UAH), BuyDate: "2026-09-01", Channel: "mono"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddLot(ctx, domain.Lot{ISIN: "UA1", Qty: 2,
		PricePerBond: money.New(100000, money.UAH), BuyDate: "2026-09-01", Channel: "mono"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePortfolio(ctx, MainPortfolio); err == nil {
		t.Error("головний портфель стерся")
	}
	if err := s.DeletePortfolio(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePortfolio(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("повторне видалення: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM lots WHERE portfolio_id=?`, id).Scan(&n); err != nil || n != 0 {
		t.Errorf("лоти стертого портфеля лишились: %d, %v", n, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM brokers WHERE portfolio_id=?`, id).Scan(&n); err != nil || n != 0 {
		t.Errorf("брокери стертого портфеля лишились: %d, %v", n, err)
	}
	mine, err := s.ListLots(ctx)
	if err != nil || len(mine) != 1 || mine[0].Qty != 2 {
		t.Errorf("каскад зачепив головний: %+v, %v", mine, err)
	}
}
