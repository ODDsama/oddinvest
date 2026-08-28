// Превʼю політики: що названі цілі означають для портфеля, який уже є.
//
// Питання приходить із «Політики»: набір налаштувань ставить пʼятнадцять
// чисел одним натиском, і доти єдине, що можна було побачити до запису, —
// різницю в САМИХ НАЛАШТУВАННЯХ. Скільки це гривень, чи вписується в ціль
// найдешевший папір, що станеться з видом, у якому вже лежать гроші, —
// з'ясовувалось лише після застосування.
//
// Власної арифметики тут немає жодної, і це головне в цьому файлі. Цілі в
// гривнях, дефіцит, здійсненність, транзит і щільність рахує buildRebalance
// (state_rebalance.go) — та сама функція, що малює «Ризик» і живить
// помічника реінвесту. Превʼю лише підмінює налаштування й переказує її
// відповідь.
//
// ЧОМУ ОКРЕМИЙ МАРШРУТ, А НЕ ПОЛЕ В POST /api/whatif. Обидва питають «що
// станеться, якщо», і спокуса злити їх в одне велика. Але whatif за
// замовчуванням бере ЗБЕРЕЖЕНИЙ план купівель (`saved` без значення = так),
// тобто відповідь мовчки враховувала б папери, яких ще не купили, — а тут
// питання про портфель, який є СЬОГОДНІ. До того ж whatif будує стан двічі,
// «до» і «після», бо йому потрібен винуватець нестачі грошей; тут потрібне
// лише «після».
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ODDsama/oddinvest/internal/state"
)

// policyPreviewReq — та сама мапа ключ→значення, що йде в PUT /api/settings.
//
// Саме мапа, а не структура з пʼятнадцятьма полями: набір пише рівно ті
// ключі, які пише форма «Політики», і перелік у них один. Структура тут
// стала б п'ятнадцятим місцем, де ключ треба не забути.
type policyPreviewReq struct {
	Settings map[string]string `json:"settings"`
}

// policyPreviewResp — два розділи гіпотетичного стану, і обидва потрібні.
//
// Rebalance відповідає на «чого бракує до цілі» — у гривнях, з дефіцитом і
// здійсненністю. Concentration — на «де зібрано надто щільно» вже за НОВИМИ
// лімітами: набір, який ставить стелю на один папір нижче, ніж папір уже
// займає, кричатиме з першої секунди, і побачити це до запису дешевше.
//
// Решти документа тут немає навмисно. Превʼю відповідає на питання
// «що ці цілі означають», а не «а покажіть-но весь стан»: віддати доку
// цілком означало б, що наступний споживач почне читати з нього дюрацію,
// і маршрут тихо стане другим /api/summary — тільки з вигаданою політикою.
type policyPreviewResp struct {
	Rebalance     []state.RebalanceRow     `json:"rebalance"`
	Concentration []state.ConcentrationRow `json:"concentration"`
}

// handlePolicyPreview — POST /api/policy/preview.
func (s *Server) handlePolicyPreview(w http.ResponseWriter, r *http.Request) {
	var req policyPreviewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Та сама перевірка, що й у запису. Превʼю, яке приймає те, чого не
	// прийме PUT, показувало б числа, яких не буде.
	if err := validateSettings(req.Settings); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Порожнє тіло — законний запит, і відповідь на нього чесна: це стан за
	// ЧИННОЇ політики. Окремої гілки він не потребує, бо порожня накладка
	// нічого не підміняє (hypothetical.empty).
	doc, err := s.buildStateWith(r.Context(), time.Now(), hypothetical{settings: req.Settings})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, policyPreviewResp{
		Rebalance:     doc.Rebalance,
		Concentration: doc.Concentration,
	})
}
