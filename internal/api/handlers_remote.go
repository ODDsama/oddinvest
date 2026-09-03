package api

// Доступ ззовні: стан тунелю й дві дії — підключити, відключити.
//
// Сам механізм у internal/tunnel; тут лише двері до нього. Ручки під
// замком нарівні з рештою /api/*, і цього мало: підключати тунель, доки
// пароля немає, не можна взагалі — це буквально відчинити двері без
// замка. Ту саму умову тримав скрипт, який ці ручки замінили (він
// відмовлявся працювати без пароля в env), і вона переїхала сюди разом
// із роллю.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ODDsama/oddinvest/internal/tunnel"
)

func (s *Server) handleRemoteStatus(w http.ResponseWriter, r *http.Request) {
	if s.tun == nil {
		// Менеджера немає лише в тестах сервера: сторінка тоді покаже
		// «не налаштовано», і це чесно.
		writeJSON(w, http.StatusOK, tunnel.Status{CloudflaredFound: tunnel.CloudflaredPath() != ""})
		return
	}
	st, err := s.tun.Status(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleRemoteConnect(w http.ResponseWriter, r *http.Request) {
	if s.tun == nil {
		writeErr(w, http.StatusNotImplemented, errors.New("тунель у цій збірці недоступний"))
		return
	}
	if !s.authEnabled() {
		writeErr(w, http.StatusConflict,
			errors.New("спершу задай пароль: двері назовні без замка не відчиняємо"))
		return
	}
	var in struct {
		APIToken string `json:"api_token"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.tun.Connect(r.Context(), in.APIToken, in.Hostname); err != nil {
		// 400, а не 500: це майже завжди відповідь Cloudflare про токен,
		// права або домен — тобто те, що виправляє людина, а не сервіс.
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Публічна адреса стала налаштуванням — стан для HA треба перепублікувати.
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/remote/cert — перевипустити сертифікат для локального доступу.
//
// Окремою ручкою, хоч видача й запускається сама після «Підключити»: коли
// вона не вдалась (ліміт, права токена, зона), людині потрібен спосіб
// спробувати ще раз, не перепідключаючи тунель.
//
// Чекаємо на відповідь, а не відповідаємо одразу: видача триває секунди,
// зрідка хвилини, і сторінці є що показати — або строк, або дослівну
// причину.
func (s *Server) handleRemoteCert(w http.ResponseWriter, r *http.Request) {
	if s.tun == nil {
		writeErr(w, http.StatusNotImplemented, errors.New("тунель у цій збірці недоступний"))
		return
	}
	if err := s.tun.EnsureCert(r.Context(), true); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoteDisconnect(w http.ResponseWriter, r *http.Request) {
	if s.tun == nil {
		writeErr(w, http.StatusNotImplemented, errors.New("тунель у цій збірці недоступний"))
		return
	}
	if err := s.tun.Disconnect(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}
