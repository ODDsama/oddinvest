package api

import (
	"context"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/state"
)

// Розклад ставки — ОДНЕ місце, де реальна дохідність виводиться з
// номінальної.
//
// Доти цей ланцюжок був розписаний по дванадцятьох місцях: у кожному
// виробнику стояла пара рядків «NominalPct: x, RealPct: realYield(...)»,
// і кожен знав свій шматок правди про те, що вже відняте, а що ні. Тепер
// виробник каже, ЩО в нього за ставка (валова, чиста, у якій валюті, з
// якої основи), а як із неї виходить реальна — вирішує ця функція.
//
// Податок тут НЕ РАХУЄТЬСЯ, а лише називається. Він уже врахований до
// виклику: вклад приходить через domain.NetRate, фонд — через
// domain.NetOfTax, ОВДП звільнені від ПДФО й військового збору взагалі.
// Рахувати його вдруге означало б завести друге означення податку — те
// саме, від чого застерігає розбір у handlers_whatif.go.
type rateContext struct {
	deval float64
	cpi   float64
	cpiOK bool
}

// newRateContext збирає обидві лінійки один раз на запит. Знецінення
// приходить готовим (у buildState воно вже лежить у src.deval — одна
// точка входу, яку стереже make sources-boundary), інфляція читається
// тут, бо ряд цін ніхто інший не питає.
func (s *Server) newRateContext(ctx context.Context, deval float64) rateContext {
	cpi, ok := s.inflation(ctx)
	return rateContext{deval: deval, cpi: cpi, cpiOK: ok}
}

// breakdown — gross і net у ЧАСТКАХ (0.1655), як їх рахують виробники;
// назовні йдуть відсотки, як їх показує UI.
func (rc rateContext) breakdown(gross, net float64, cur, basis string) *state.RateBreakdown {
	b := &state.RateBreakdown{
		Currency:       cur,
		GrossPct:       round2(gross * 100),
		NetPct:         round2(net * 100),
		DevaluationPct: rc.deval,
		RealFXPct:      round2(realYield(net, cur, rc.deval) * 100),
		Basis:          basis,
	}
	if tax := (gross - net) * 100; tax > 0.005 {
		b.TaxPct = round2(tax)
	}
	if rc.cpiOK {
		infl := rc.cpi
		real := round2(realByCPI(net, cur, rc.deval, rc.cpi) * 100)
		b.InflationPct, b.RealCPIPct = &infl, &real
	}
	return b
}

// realByCPI — дохідність проти ЦІН.
//
// Для гривні це та сама дія, що realYield, лише з іншим дефлятором. Для
// валюти — ДВА кроки, і другий не є другим відрахуванням: ставка спершу
// переводиться в гривневі терміни знеціненням (гроші прийдуть у долар, а
// витрачати їх тут), і вже гривневе число дефлюється цінами.
//
// Саме тут ІСЦ і робить те, заради чого заводився: realYield для валюти
// повертає ставку НЕТОРКАНОЮ, тобто вважає, що долар купівельну
// спроможність тримає. Це припущення, і воно єдине в моделі, якого не
// було чим перевірити. Тепер поруч стоїть вимір.
func realByCPI(y float64, cur string, devalPct, cpiPct float64) float64 {
	inUAH := y
	if cur != money.UAH {
		inUAH = (1+y)*(1+devalPct/100) - 1
	}
	return (1+inUAH)/(1+cpiPct/100) - 1
}
