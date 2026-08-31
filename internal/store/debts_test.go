package store

import (
	"context"
	"strings"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func TestDebtsCRUD(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	card, err := s.AddDebt(ctx, domain.Debt{
		Name: "ПУМБ ВсеМожу", Kind: domain.DebtCard, Currency: "UAH",
		LimitAmount: 200_000_00, StatementDay: 30,
		APRBp: 4788, APROverdueBp: 6200,
		MinPaymentBp: 300, MinPaymentFloor: 100_00, LateFee: 100_00,
		OpenedDate: "2024-05-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Розстрочка ВСЕРЕДИНІ картки: її частина падає у виписку.
	inCard, err := s.AddDebt(ctx, domain.Debt{
		Name: "Холодильник частинами", Kind: domain.DebtInstallment, Currency: "UAH",
		CardID: card, Principal: 30_000_00, PaymentsTotal: 9,
		FirstPaymentDate: "2026-09-30", FeeMonthBp: 199,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Самостійна розстрочка — інший банк, свій графік.
	if _, err := s.AddDebt(ctx, domain.Debt{
		Name: "Товарна", Kind: domain.DebtInstallment, Currency: "UAH",
		Principal: 12_000_00, PaymentsTotal: 12,
		FirstPaymentDate: "2026-10-05", FeeMonthBp: 300, FeeFreeMonths: 3,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListDebts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Порядок задає СХОВИЩЕ: картка, під нею її розстрочки, далі решта.
	if len(got) != 3 || got[0].Name != "ПУМБ ВсеМожу" ||
		got[1].Name != "Холодильник частинами" || got[2].Name != "Товарна" {
		t.Fatalf("порядок боргів: %+v", got)
	}
	if got[0].CardID != 0 {
		t.Errorf("самостійна картка дістала card_id %d, чекали 0 (NULL)", got[0].CardID)
	}
	if got[1].CardID != card {
		t.Errorf("розстрочка не привʼязана до картки: %+v", got[1])
	}
	if !got[0].IsCard() || got[1].IsCard() {
		t.Errorf("вид боргу поїхав: %q / %q", got[0].Kind, got[1].Kind)
	}
	if got[0].APROverdueBp != 6200 || got[0].StatementDay != 30 {
		t.Errorf("умови картки поїхали: %+v", got[0])
	}
	if got[2].FeeFreeMonths != 3 || got[2].FeeMonthBp != 300 {
		t.Errorf("комісія товарної поїхала: %+v", got[2])
	}
	// Скільки платежів лишилось, у базі немає навмисно (див. 0045): це
	// виводиться з дати першого платежу й кількості.
	if got[1].PaymentsTotal != 9 {
		t.Errorf("кількість платежів: %d", got[1].PaymentsTotal)
	}

	upd := got[0]
	upd.LimitAmount = 250_000_00
	if err := s.UpdateDebt(ctx, upd); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListDebts(ctx)
	if got[0].LimitAmount != 250_000_00 {
		t.Errorf("ліміт не оновився: %+v", got[0])
	}

	// Картку з привʼязаною розстрочкою видалити не можна: разом із нею
	// пішов би журнал, якого немає більше ніде.
	err = s.DeleteDebt(ctx, card)
	if err == nil || !strings.Contains(err.Error(), "розстрочок") {
		t.Fatalf("видалення картки з розстрочкою: %v", err)
	}
	if err := s.DeleteDebt(ctx, inCard); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDebt(ctx, card); err != nil {
		t.Fatalf("картка без нащадків мусить видалятись: %v", err)
	}
}

func TestDebtOpsCRUD(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	card, err := s.AddDebt(ctx, domain.Debt{Name: "Картка", Kind: domain.DebtCard, Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []domain.DebtOp{
		{DebtID: card, Date: "2026-08-05", Kind: domain.DebtOpPayment, Amount: 40_000_00, Note: "зарплата"},
		{DebtID: card, Date: "2026-08-07", Kind: domain.DebtOpDraw, Amount: 3_500_00},
		{DebtID: card, Date: "2026-08-09", Kind: domain.DebtOpCash, Amount: 5_000_00},
	} {
		if _, err := s.AddDebtOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}

	ops, err := s.ListDebtOps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 3 || ops[0].Kind != domain.DebtOpPayment || ops[2].Kind != domain.DebtOpCash {
		t.Fatalf("рухи боргу: %+v", ops)
	}
	// Сума завжди додатна, напрям несе вид — інакше одна колонка
	// відповідала б на два питання (правило 0025).
	for _, op := range ops {
		if op.Amount <= 0 {
			t.Errorf("відʼємна сума руху: %+v", op)
		}
	}

	upd := ops[1]
	upd.Amount = 4_000_00
	if err := s.UpdateDebtOp(ctx, upd); err != nil {
		t.Fatal(err)
	}
	ops, _ = s.ListDebtOps(ctx)
	if ops[1].Amount != 4_000_00 {
		t.Errorf("рух не оновився: %+v", ops[1])
	}

	// Борг із рухами не видаляється.
	if err := s.DeleteDebt(ctx, card); err == nil ||
		!strings.Contains(err.Error(), "рухів") {
		t.Fatalf("видалення боргу з рухами: %v", err)
	}
	for _, op := range ops {
		if err := s.DeleteDebtOp(ctx, op.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteDebt(ctx, card); err != nil {
		t.Fatalf("борг без рухів мусить видалятись: %v", err)
	}
}

// Дві звірки на одну дату — це виправлення одруківки, а не дві правди:
// друга ПЕРЕПИСУЄ першу, як у позначок ціни фонду.
func TestDebtMarkSameDayOverwrites(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	card, err := s.AddDebt(ctx, domain.Debt{Name: "Картка", Kind: domain.DebtCard, Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddDebtMark(ctx, domain.DebtMark{
		DebtID: card, Date: "2026-08-30",
		Balance: 12_000_00, StatementDue: 18_400_00, NonGrace: 5_000_00,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddDebtMark(ctx, domain.DebtMark{
		DebtID: card, Date: "2026-08-30",
		Balance: -3_000_00, StatementDue: 18_400_00, NonGrace: 5_000_00,
		Note: "перечитав у додатку",
	}); err != nil {
		t.Fatal(err)
	}

	marks, err := s.ListDebtMarks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 1 {
		t.Fatalf("звірок на одну дату %d, чекали 1", len(marks))
	}
	// Баланс знакозмінний: мінус — використаний ліміт, плюс — власні
	// гроші на картці.
	if marks[0].Balance != -3_000_00 || marks[0].Note != "перечитав у додатку" {
		t.Errorf("повторна звірка не переписала першу: %+v", marks[0])
	}
	if marks[0].StatementDue != 18_400_00 || marks[0].NonGrace != 5_000_00 {
		t.Errorf("решта чисел звірки поїхала: %+v", marks[0])
	}

	if err := s.DeleteDebt(ctx, card); err == nil ||
		!strings.Contains(err.Error(), "звірок") {
		t.Fatalf("видалення боргу зі звірками: %v", err)
	}
	if err := s.DeleteDebtMark(ctx, marks[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDebt(ctx, card); err != nil {
		t.Fatal(err)
	}
}
