// Профілі імпорту — КРУД над розкладкою колонок чужої виписки.
//
// ПОВНИЙ КРУД, як вимагає CLAUDE.md §2: профіль заводять руками,
// одруківка в номері колонки цілком імовірна, і «видали й набери заново»
// не спосіб її виправити — разом із профілем зникло б і те, що людина
// вивіряла по своїй виписці півгодини.
//
// Ключ — НАЗВА, а не id. Так само, як у фондів і брокерів поруч: назва
// профілю їде в запит імпорту (?profile=…), у брокера за замовчуванням і
// в бекап, і числовий id довелось би перекладати в назву на кожному
// кроці.
//
// СТВОРЕННЯ Й ПРАВКА — один PUT, а не пара POST/PUT. Питання «чи існує
// профіль із такою назвою» тут не має наслідків: назву задає людина, і
// «зберегти» для неї означає те саме в обох випадках. Той самий прийом,
// що в PUT /api/settings.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ODDsama/oddinvest/internal/imports"
	"github.com/ODDsama/oddinvest/internal/store"
)

type importProfileJSON struct {
	Name   string `json:"name"`
	Format string `json:"format"`
	Header int    `json:"header"`
	Date   int    `json:"col_date"`
	Op     int    `json:"col_op"`
	Ref    int    `json:"col_ref"`
	Qty    int    `json:"col_qty"`
	Debit  int    `json:"col_debit"`
	Credit int    `json:"col_credit"`
	Ops    string `json:"ops"`
	Note   string `json:"note"`
}

func (s *Server) handleListImportProfiles(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListImportProfiles(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]importProfileJSON, 0, len(list))
	for _, p := range list {
		out = append(out, importProfileJSON{
			Name: p.Name, Format: p.Format, Header: p.Header,
			Date: p.Date, Op: p.Op, Ref: p.Ref, Qty: p.Qty,
			Debit: p.Debit, Credit: p.Credit, Ops: p.Ops, Note: p.Note,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSaveImportProfile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("профіль без назви"))
		return
	}
	if strings.EqualFold(name, inzhurProfile) {
		// Назва зайнята вбудованим розбором. Дозволити її означало б
		// завести профіль, який ніколи не спрацює: /api/import віддає
		// «inzhur» вбудованому шляху ще до звернення до сховища.
		writeErr(w, http.StatusConflict,
			fmt.Errorf("назва %q зайнята вбудованим розбором виписки Inzhur", inzhurProfile))
		return
	}
	var req importProfileJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p := store.ImportProfile{
		Name: name, Format: strings.ToLower(strings.TrimSpace(req.Format)),
		Header: req.Header, Date: req.Date, Op: req.Op, Ref: req.Ref,
		Qty: req.Qty, Debit: req.Debit, Credit: req.Credit,
		Ops: req.Ops, Note: req.Note,
	}
	if p.Format != "csv" {
		p.Format = "xlsx"
	}
	if err := validateImportProfile(p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.SaveImportProfile(r.Context(), p); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteImportProfile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if err := s.st.DeleteImportProfile(r.Context(), name); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validateImportProfile — перевірити профіль ДО збереження, а не під час
// імпорту.
//
// Порядок тут важить більше, ніж здається. Профіль зі зламаним словником
// операцій зберігся б мовчки, а впав би через місяць — у мить, коли
// людина принесла виписку й чекає на результат. Ловимо це в момент, коли
// вона ще дивиться саме на профіль і памʼятає, що в ньому писала.
func validateImportProfile(p store.ImportProfile) error {
	if p.Header < 0 {
		return fmt.Errorf("рядків шапки не буває менше нуля")
	}
	if p.Date < 0 || p.Op < 0 {
		return fmt.Errorf("колонки дати й операції обовʼязкові")
	}
	if p.Debit < 0 && p.Credit < 0 {
		return fmt.Errorf("потрібна щонайменше одна колонка суми — дебет або кредит")
	}
	if _, err := imports.ParseOps(p.Ops); err != nil {
		return err
	}
	return nil
}
