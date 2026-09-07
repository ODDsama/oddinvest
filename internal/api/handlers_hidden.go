// Приховані рядки майстер-списку — те, що власник відмітив «не показувати».
//
// Сестра handlers_navorder.go, і майже все її обґрунтування чинне тут
// дослівно: БЕКЕНД НЕ ЗНАЄ, ЩО ОЗНАЧАЮТЬ ЦІ ІДЕНТИФІКАТОРИ. Перевіряється
// ФОРМА — масив коротких рядків із межами за розміром, — а не значення. Щоб
// перевірити значення, у Go довелося б завести другу копію дерева з
// web/js/master.js; вона розійшлася б із першою мовчки, і найтихішим
// наслідком був би відкинутий 400 на папір, який щойно купили. Зводить
// збережене з наявним фронтенд: невідомий рядок він просто не показує.
//
// ДВІ РОЗБІЖНОСТІ З navorder, обидві навмисні.
//
// Перша — СХОВИЩЕ: власна таблиця (0060), а не app_state. Порядок вкладок
// належить інсталяції й не має перетасовуватись від перемикання портфеля;
// «fund:Inzhur Ocean» же осмислений лише всередині свого портфеля, і спільний
// ключ сховав би однойменний фонд у сусідньому. Довід цілком — у міграції.
//
// Друга — АЛФАВІТ: тут він ширший. navOrderIDOK знає лише латиницю, цифри й
// чотири розділові знаки, бо ідентифікатори вкладок такі й є. Рядок списку ж
// адресується назвою там, де числового id немає: «fund:Inzhur MilTech»,
// «npf:ВПФ Династія» — пробіли й кирилиця. Позичити той валідатор означало б
// 400 рівно на тих рядках, заради яких усе це й пишеться.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"unicode"
	"unicode/utf8"
)

// Межі. Не про безпеку пам'яті, а про те, щоб у робочому стані застосунку не
// оселився чужий блоб: рядків у найдовшому списку — десятки, а назва фонду
// довша за сотню байтів не буває.
const (
	hiddenMaxIDs = 200
	hiddenMaxLen = 128
)

func (s *Server) handleGetHidden(w http.ResponseWriter, r *http.Request) {
	ids, err := s.st.HiddenRows(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, ids)
}

func (s *Server) handlePutHidden(w http.ResponseWriter, r *http.Request) {
	var req []string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Перевірка ВСЬОГО тіла до запису — той самий довід, що в
	// handlers_settings.go: половина набору, якої ніхто не просив, гірша за
	// відмову цілком.
	if err := validateHidden(req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.SetHiddenRows(r.Context(), req); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// publishAsync() тут немає навмисно, як і в navorder: документ стану для
	// Home Assistant не має жодного стосунку до того, які рядки власник
	// прибрав із лівого списку. Числа портфеля від цього не міняються — і в
	// цьому вся суть позначки.
	w.WriteHeader(http.StatusNoContent)
}

// validateHidden — форма й межі. Що ці рядки означають, див. шапку файла.
func validateHidden(ids []string) error {
	if len(ids) > hiddenMaxIDs {
		return fmt.Errorf("забагато прихованих рядків: %d", len(ids))
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if err := hiddenIDOK(id); err != nil {
			return fmt.Errorf("рядок %q: %w", id, err)
		}
		// Повтор — не дрібниця сама по собі (ключ таблиці його все одно не
		// пустить), а знак, що клієнт зібрав список не тим способом, яким
		// думає. Мовчки злитий дублікат сховав би цю помилку до дня, коли
		// вона зіпсує щось дорожче.
		if seen[id] {
			return fmt.Errorf("рядок %q двічі", id)
		}
		seen[id] = true
	}
	return nil
}

func hiddenIDOK(s string) error {
	if s == "" {
		return fmt.Errorf("порожній")
	}
	if len(s) > hiddenMaxLen {
		return fmt.Errorf("довший за %d байтів", hiddenMaxLen)
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("не UTF-8")
	}
	// Керівні символи — єдине, що тут справді заборонено. Не з міркувань
	// безпеки (у HTML усе екранує format.js, у SQL — плейсхолдери), а тому
	// що рядок із переводом каретки всередині не може бути ідентифікатором
	// рядка списку: такого master.js не збирає, і його поява означає, що
	// тіло прийшло не звідти.
	for _, c := range s {
		if unicode.IsControl(c) {
			return fmt.Errorf("керівний символ")
		}
	}
	return nil
}
