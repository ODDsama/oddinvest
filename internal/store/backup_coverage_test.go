// Механічна межа бекапу.
//
// У пакеті є перелік таблиць, який ведеться РУКАМИ (importAllTables), і
// його розходження зі схемою не ловиться ні компілятором, ні жодним іншим
// тестом — воно виявляється в день відновлення, коли рятувати вже нема
// чим.
//
// Та сама механіка, що в Makefile (fx-boundary, sources-boundary,
// sleeve-state, whatif-boundary, ui-kit-boundary), лише замість grep тут
// sqlite_master: правило, яке ніхто не перевіряє, живе до наступного
// поспіху.
package store

import (
	"database/sql"
	"testing"
)

// derivedTables — таблиці, яких у бекапі НЕМАЄ навмисно, бо вони
// відновлюються з НБУ командою «Оновити НБУ» (шапка backup.go). Список
// явний саме тому, що «немає в бекапі» мусить бути рішенням, а не
// недоглядом: щоб нова таблиця сюди потрапила, її треба назвати.
var derivedTables = map[string]bool{
	"bonds":             true, // довідник ЦП НБУ
	"payments":          true, // графіки виплат звідти ж
	"fx_rates":          true, // курси НБУ, добираються бекфілом
	"ovdp_auctions":     true, // аукціони Мінфіну
	"schema_migrations": true, // журнал раннера, не дані
	"sqlite_sequence":   true, // службова таблиця AUTOINCREMENT
	"sqlite_stat1":      true, // статистика PRAGMA optimize
	"sqlite_stat4":      true,
}

// TestBackupCoversEveryUserTable — кожна таблиця схеми або в бекапі, або
// названа похідною.
//
// Ловить рівно той сценарій, через який бекап і потрібен: міграція
// заводить таблицю, автор дописує до неї КРУД і забуває про ExportAll —
// а дізнається про це той, хто відновлюється.
func TestBackupCoversEveryUserTable(t *testing.T) {
	s := openTest(t)
	schema := tableNames(t, s.db)

	backed := map[string]bool{}
	for _, n := range importAllTables {
		backed[n] = true
	}

	var missing []string
	for _, n := range schema {
		if !backed[n] && !derivedTables[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		t.Errorf("таблиці поза бекапом: %v\n"+
			"впиши в importAllTables (і в Backup/ExportAll/ImportAll) або, якщо вона\n"+
			"справді відновлювана — назви її в derivedTables із доводом", missing)
	}

	// Друга половина тієї ж межі: перелік ImportAll не має посилатись на
	// таблицю, якої в схемі вже немає — інакше відновлення падає на
	// DELETE FROM неіснуючого.
	inSchema := map[string]bool{}
	for _, n := range schema {
		inSchema[n] = true
	}
	for _, n := range importAllTables {
		if !inSchema[n] {
			t.Errorf("importAllTables згадує таблицю %q, якої немає в схемі", n)
		}
	}
}

// tableNames — усі таблиці схеми.
func tableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
