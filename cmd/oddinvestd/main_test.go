package main

import (
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Годинник процесу — київський. Тест сторожить не присвоєння як таке, а його
// наслідок: який день застосунок називає сьогоднішнім о 21:57 UTC першого
// вересня. Саме на цій хвилині верхній екран показував серпневий місячний
// план — 210% виконання при нулі внесеного в новому місяці.
func TestSetKyivLocal(t *testing.T) {
	setKyivLocal()

	if got := time.Local.String(); got != "Europe/Kyiv" {
		t.Fatalf("зона процесу %q, а має бути Europe/Kyiv", got)
	}
	// 2026-08-31T21:57:22Z — це 1 вересня 00:57 за Києвом. Дата береться так
	// само, як її бере buildStateWith: domain.NewDate поверх часу процесу.
	utc := time.Date(2026, 8, 31, 21, 57, 22, 0, time.UTC)
	if got := domain.NewDate(utc.In(time.Local)); got != "2026-09-01" {
		t.Errorf("о 00:57 за Києвом сьогодні %q, а не 2026-09-01", got)
	}
	// І місяць — той, у якому живе людина, а не сервер.
	if got := utc.In(time.Local).Format("2006-01"); got != "2026-09" {
		t.Errorf("місяць %q, а не 2026-09", got)
	}
}

// Зона має бути в бінарнику, а не в контейнері: на хості без
// /usr/share/zoneinfo LoadLocation мовчки лишав би зону серверною. Тримає її
// там сліпий імпорт time/tzdata — і саме те, що він у main.go, а не в
// налаштуваннях розгортання, перевіряє цей тест.
func TestZoneDatabaseIsEmbedded(t *testing.T) {
	if _, err := time.LoadLocation("Europe/Kyiv"); err != nil {
		t.Fatalf("Europe/Kyiv недоступна: %v", err)
	}
}
