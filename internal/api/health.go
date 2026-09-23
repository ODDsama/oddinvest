// /healthz — чи живий сервіс, і який саме.
//
// Деплой доти перевіряв 200 на «/»: це доводить, що новий процес
// піднявся (сховище й міграції відкриваються ДО ListenAndServe), але не
// каже, ЯКИЙ бінарник відповідає, і не питає базу вже під час роботи.
// Тут обидва: пінг бази з останньою застосованою міграцією й версія
// збірки, яку lxc-deploy.sh звіряє з комітом, що його щойно зібрав.
//
// Поза /api/ навмисно, тож без замка: перевірку деплою робить скрипт без
// сесії, а відповідь не несе нічого, крім версії й імені файла міграції.
package api

import "net/http"

// Version — версія збірки: коротке sha коміту, яке lxc-deploy.sh вшиває
// через -ldflags "-X …/internal/api.Version=<sha>". "dev" — локальна
// збірка без прапорця.
var Version = "dev"

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	mig, err := s.st.Health(r.Context())
	if err != nil {
		// Деталі — у журнал, не назовні: ендпойнт відкритий без замка.
		s.log.Error("healthz: база не відповідає", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "db", "version": Version, "error": "база не відповідає",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok", "version": Version, "migration": mig,
	})
}
