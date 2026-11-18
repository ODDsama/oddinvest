package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"
)

// guard — паніка у фоновій задачі лишається записом у журналі, а не
// смертю демона. Кличеться через defer; err — іменований результат
// задачі або nil, якщо задача нічого не повертає.
//
// Навіщо: net/http ловить паніку обробника сам, а горутини добового
// прогону, бекфілів і публікації — ні. Арифметика грошей панікує свідомо
// (state.Money на різних валютах), і одна така помилка в документі клала
// б увесь процес; а оскільки старт наздоганяє пропущений прогін, systemd
// піднімав би його рівно в ту саму паніку — по колу.
func guard(log *slog.Logger, task string, err *error) {
	p := recover()
	if p == nil {
		return
	}
	log.Error("паніка у фоновій задачі", "task", task, "panic", p, "stack", string(debug.Stack()))
	if err != nil && *err == nil {
		*err = fmt.Errorf("%s: паніка: %v", task, p)
	}
}

// sleepCtx — витримка між запитами до чужого сервісу, що переривається
// скасуванням: зупинка демона не має чекати на паузу кожного циклу.
func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
