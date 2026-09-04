package store

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Сторож звуження до портфеля (0054).
//
// Кожен SQL-літерал у пакеті, що згадує скоуповану таблицю після FROM,
// INTO, UPDATE чи JOIN, мусить згадувати й portfolio_id — у WHERE, у
// переліку колонок INSERT або в умові JOIN. Це ЄДИНЕ, що відрізняє
// «список лотів» від «список лотів усіх портфелів», і різниці цієї не
// видно нічим, доки другий портфель порожній: тест на одному портфелі
// проходить із пропущеним WHERE так само, як і без нього.
//
// Grep у Makefile тут не годиться: літерал багаторядковий, і portfolio_id
// стоїть рядком нижче за назву таблиці. Тому go/parser — він бачить
// літерал цілком.
//
// Чого сторож НЕ бачить: таблицю, підставлену змінною ("DELETE FROM "+t
// в ImportAll). Такі місця покриває тест відновлення в чужий портфель
// (backup_test.go), а не цей.
//
// scopeGuardAllow — функції, де літерал СВІДОМО без portfolio_id, кожна з
// доводом. Нова тут — це рішення, а не спосіб пройти тест.
var scopeGuardAllow = map[string]string{
	// Каталог фондів спільний: видалити фонд можна лише тоді, коли на
	// нього не посилається НІХТО — ані операції, ані позначки ціни
	// жодного портфеля. Звужений лічильник дав би видалити фонд, у якому
	// сидить інший портфель.
	"DeleteFund": "лічить операції фондів по всіх портфелях навмисно",
	// Той самий довід із боку restore: фонд без операцій у ЖОДНОМУ
	// портфелі — сирота, і лише його можна витерти разом із позначками.
	"pruneOrphanFundsIn": "шукає вжиток фонду по всіх портфелях навмисно",
}

func TestScopedQueriesMentionPortfolio(t *testing.T) {
	re := regexp.MustCompile(`(?is)\b(FROM|INTO|UPDATE|JOIN)\s+(` +
		strings.Join(scopedTables, "|") + `)\b`)

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	seenAllow := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					lit, ok := n.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return true
					}
					s, err := strconv.Unquote(lit.Value)
					if err != nil {
						return true
					}
					m := re.FindStringSubmatch(s)
					if m == nil {
						return true
					}
					if _, allowed := scopeGuardAllow[fn.Name.Name]; allowed {
						seenAllow[fn.Name.Name] = true
						return true
					}
					if !strings.Contains(s, "portfolio_id") {
						pos := fset.Position(lit.Pos())
						bad = append(bad, fmt.Sprintf("%s:%d %s: %s %s без portfolio_id",
							pos.Filename, pos.Line, fn.Name.Name, strings.ToUpper(m[1]), m[2]))
					}
					return true
				})
			}
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
	// Дозвіл, який нічого не дозволяє, — застарілий: функція зникла або
	// сама стала звуженою, і рядок у переліку лише вводить в оману.
	for name := range scopeGuardAllow {
		if !seenAllow[name] {
			t.Errorf("scopeGuardAllow[%q] нічого не покриває — прибери", name)
		}
	}
}
