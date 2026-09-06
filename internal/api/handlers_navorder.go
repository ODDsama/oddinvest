// Порядок рядків майстер-списку — той, який поставив собі власник.
//
// Дерево навігації (які взагалі є вкладки й рядки) живе в web/js/nav.js і
// задає порядок ПИТАНЬ: борг перший, бо він з'їдає гроші місяця раніше за
// все інше. Це правильний порядок, щоб пояснити застосунок, і не
// обов'язково той, у якому в нього заходять щодня. Тут зберігається друге.
//
// БЕКЕНД НЕ ЗНАЄ, ЩО ОЗНАЧАЮТЬ ЦІ ІДЕНТИФІКАТОРИ, і це рішення, а не
// недогляд. Перевіряється ФОРМА — об'єкт, у ньому масиви коротких рядків,
// із межами за розміром, — а не значення. Щоб перевірити значення, у Go
// довелося б завести другу копію дерева з nav.js; вона розійшлася б із
// першою мовчки, і найтихішим наслідком був би відкинутий 400 на рядок,
// який щойно з'явився в UI. Зводить збережене з наявним фронтенд
// (web/js/navorder.js): незнайомий рядок він ігнорує, новий — дописує в
// кінець.
//
// СХОВИЩЕ — app_state, а не settings. Розкладка списку належить
// інсталяції, а не портфелю: перемикання портфеля не має перетасовувати
// список. До того ж settings — це фінансова політика за суворим білим
// списком (settings_registry.go), і ключ про UI серед ставок і лімітів
// стояв би не на своєму місці.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Ключ в app_state. Один на всі вкладки: порядок читається цілком на
// кожному завантаженні сторінки, і ключ на вкладку означав би шість
// читань замість одного.
const navOrderKey = "nav_order"

// Межі. Не про безпеку пам'яті, а про те, щоб у робочому стані
// застосунку не оселився чужий блоб: справжніх вкладок сім, а рядків у
// найдовшій — сім.
const (
	navOrderMaxTabs = 20
	navOrderMaxIDs  = 200
	navOrderMaxLen  = 64
)

func (s *Server) handleGetNavOrder(w http.ResponseWriter, r *http.Request) {
	raw, err := s.st.GetAppState(r.Context(), navOrderKey)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := map[string][]string{}
	// Зіпсований JSON — порожній порядок, а не 500. Це вподобання: його
	// втрата коштує одного «Скинути», а 500 тут погасив би застосунок
	// цілком, бо порядок читається до першого малювання списку.
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			out = map[string][]string{}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutNavOrder(w http.ResponseWriter, r *http.Request) {
	var req map[string][]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Перевірка ВСЬОГО тіла до запису — той самий довід, що в
	// handlers_settings.go: половина набору, якої ніхто не просив, гірша
	// за відмову цілком. Тут воно ще й дешевше, бо запис один.
	if err := validateNavOrder(req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	blob, err := json.Marshal(req)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.st.SetAppState(r.Context(), navOrderKey, string(blob)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// publishAsync() тут немає навмисно: документ стану для Home Assistant
	// не має жодного стосунку до того, у якому порядку намальований лівий
	// список.
	w.WriteHeader(http.StatusNoContent)
}

// validateNavOrder — форма й межі. Що ці рядки означають, див. шапку файла.
func validateNavOrder(m map[string][]string) error {
	if len(m) > navOrderMaxTabs {
		return fmt.Errorf("забагато вкладок у порядку: %d", len(m))
	}
	for tab, ids := range m {
		if !navOrderIDOK(tab) {
			return fmt.Errorf("недопустима назва вкладки: %q", tab)
		}
		if len(ids) > navOrderMaxIDs {
			return fmt.Errorf("забагато рядків на вкладці %q: %d", tab, len(ids))
		}
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if !navOrderIDOK(id) {
				return fmt.Errorf("недопустимий рядок %q на вкладці %q", id, tab)
			}
			// Повтор — не дрібниця: порядок із двома «Боргами» означає, що
			// один рядок списку зник, і зник він мовчки.
			if seen[id] {
				return fmt.Errorf("рядок %q на вкладці %q двічі", id, tab)
			}
			seen[id] = true
		}
	}
	return nil
}

func navOrderIDOK(s string) bool {
	if s == "" || len(s) > navOrderMaxLen {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '_' || c == '-' || c == ':' || c == '.':
		default:
			return false
		}
	}
	return true
}
