// Добовий знімок і реєстр його колонок.
//
// Колонку доводилось вписувати у ВІСІМ місць, і десять із них ламались
// тихо. Найтихіше — перелік `ON CONFLICT DO UPDATE SET`: пропустиш там
// колонку, і вставка працює, а повторний запис того самого дня її не
// оновлює НІКОЛИ. Жодної помилки, жодного тесту; помітно стає на кривій
// за півроку. Далі за тихістю йдуть позиційні `Scan` і `VALUES(?,…)`, де
// сусідні int64 можна поміняти місцями без наслідків для компілятора.
//
// Тепер колонка описується один раз, а SQL і аргументи будуються з опису.

package store

import (
	"strings"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Snapshot — рядок добового знімка для графіка «факт vs модель».
//
// Він же формат знімка в бекапі: json-теги тут саме для цього. Доти поруч
// жила окрема BackupSnapshot із тими самими полями й тими самими іменами —
// друга структура, яку треба було не забути оновити.
type Snapshot struct {
	Date         domain.Date `json:"date"`
	InvestedUAH  int64       `json:"invested_uah"`
	NominalUAHEq int64       `json:"nominal_uah_eq"`
	USDShareBP   int64       `json:"usd_share_bp"`
	// Поля нижче додавались пізніше за сам знімок, і всі вони
	// `omitempty`: бекап, зроблений до появи колонки, читається
	// по-старому — там, де поля немає, лишається нуль, тобто те саме
	// «тоді не рахували», що й у самій колонці.
	UninvestedUAH  int64 `json:"uninvested_uah"`
	MonthTargetUAH int64 `json:"month_target_uah,omitempty"`
	AccountUAH     int64 `json:"account_uah,omitempty"`
	// FundsUAH — сертифікати фондів у грн-екв. DepositsUAH — тіло
	// банківських вкладів. Нуль у старих рядках в обох означає «тоді не
	// рахували», а не «не було»: колонки з'явились пізніше за самі
	// інструменти (міграції 0012 і 0016).
	FundsUAH    int64 `json:"funds_uah,omitempty"`
	DepositsUAH int64 `json:"deposits_uah,omitempty"`
	// FundsCostUAH — за скільки ці сертифікати куплені (0018). Разом із
	// InvestedUAH і DepositsUAH дає повну собівартість портфеля, без якої
	// прибуток на кривій не намалюєш: InvestedUAH — це лише облігації.
	FundsCostUAH int64 `json:"funds_cost_uah,omitempty"`
	// ReserveUAH — резерв («матрац») у грн-екв. (0021).
	ReserveUAH int64 `json:"reserve_uah,omitempty"`
	// NPFUAH — пенсійні активи в грн-екв., NPFCostUAH — сума внесків
	// (0029). Пара, а не одне число, і друге не косметичне: прибуток на
	// кривій рахується як капітал мінус собівартість, тож без внесків у
	// собівартості він завищився б рівно на весь пенсійний баланс.
	NPFUAH     int64 `json:"npf_uah,omitempty"`
	NPFCostUAH int64 `json:"npf_cost_uah,omitempty"`
}

// snapshotCol — одна колонка знімка. Ptr дає доступ до поля структури,
// тож із одного опису будуються і аргументи запису, і цілі для Scan.
type snapshotCol struct {
	Name string
	Ptr  func(*Snapshot) *int64
}

// snapshotCols — УСІ числові колонки знімка, крім ключа `date`.
//
// Порядок тут довільний, але мусить бути сталий: із нього виводяться і
// SELECT, і Scan, тож він однаково діє на обидва боки й розійтись вони не
// можуть.
//
// Додаючи колонку: міграція, рядок сюди, і поле в структурі вище. Решта —
// SaveSnapshot, ListSnapshots, бекап і /api/snapshots — підхопить сама.
var snapshotCols = []snapshotCol{
	{"invested_uah", func(s *Snapshot) *int64 { return &s.InvestedUAH }},
	{"nominal_uah_eq", func(s *Snapshot) *int64 { return &s.NominalUAHEq }},
	{"usd_share_bp", func(s *Snapshot) *int64 { return &s.USDShareBP }},
	{"uninvested_uah", func(s *Snapshot) *int64 { return &s.UninvestedUAH }},
	{"month_target_uah", func(s *Snapshot) *int64 { return &s.MonthTargetUAH }},
	{"account_uah", func(s *Snapshot) *int64 { return &s.AccountUAH }},
	{"funds_uah", func(s *Snapshot) *int64 { return &s.FundsUAH }},
	{"deposits_uah", func(s *Snapshot) *int64 { return &s.DepositsUAH }},
	{"funds_cost_uah", func(s *Snapshot) *int64 { return &s.FundsCostUAH }},
	{"reserve_uah", func(s *Snapshot) *int64 { return &s.ReserveUAH }},
	{"npf_uah", func(s *Snapshot) *int64 { return &s.NPFUAH }},
	{"npf_cost_uah", func(s *Snapshot) *int64 { return &s.NPFCostUAH }},
}

// SnapshotColumns — імена числових колонок знімка, для споживачів поза
// пакетом (наприклад, щоб віддати їх у JSON, не переписуючи список).
func SnapshotColumns() []string {
	out := make([]string, 0, len(snapshotCols))
	for _, c := range snapshotCols {
		out = append(out, c.Name)
	}
	return out
}

// SnapshotValue — значення колонки за іменем. Разом із SnapshotColumns
// дає повний обхід знімка без знання про його поля.
func SnapshotValue(sn *Snapshot, col string) int64 {
	for _, c := range snapshotCols {
		if c.Name == col {
			return *c.Ptr(sn)
		}
	}
	return 0
}

// snapshotColumnList — "date, invested_uah, …" для SELECT та INSERT.
func snapshotColumnList() string {
	return "date, " + strings.Join(SnapshotColumns(), ", ")
}

// snapshotPlaceholders — "?,?,…" рівно під snapshotColumnList.
func snapshotPlaceholders() string {
	return strings.TrimSuffix(strings.Repeat("?,", len(snapshotCols)+1), ",")
}

// snapshotUpsertSet — "col=excluded.col, …" для ON CONFLICT. Ключ `date`
// сюди не входить: він і є те, за чим конфлікт.
func snapshotUpsertSet() string {
	parts := make([]string, 0, len(snapshotCols))
	for _, c := range snapshotCols {
		parts = append(parts, c.Name+"=excluded."+c.Name)
	}
	return strings.Join(parts, ", ")
}

// snapshotArgs — значення в порядку snapshotColumnList.
func snapshotArgs(sn *Snapshot) []any {
	out := make([]any, 0, len(snapshotCols)+1)
	out = append(out, string(sn.Date))
	for _, c := range snapshotCols {
		out = append(out, *c.Ptr(sn))
	}
	return out
}

// snapshotScanTargets — куди читати, у тому самому порядку. date окремо:
// у БД це TEXT, а в структурі domain.Date.
func snapshotScanTargets(sn *Snapshot, date *string) []any {
	out := make([]any, 0, len(snapshotCols)+1)
	out = append(out, date)
	for _, c := range snapshotCols {
		out = append(out, c.Ptr(sn))
	}
	return out
}
