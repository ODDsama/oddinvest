package state

// IdleCash — простій: вільні гроші брокера, на які квиток уже є, і з якого
// дня вони лежать (адитивне поле; означення й довід — у шапці
// internal/api/state_idle.go).
//
// Порожнє (nil), коли жодна пара брокер × валюта не дотягує до квитка:
// простою немає, і поле мовчить, а не показує нулі.
//
// ЦІНИ ТУТ НЕМАЄ НАВМИСНО — вона в IdleCost. Простій — факт про гаманець
// і рахується з журналу подій у buildState; ціна — порада про сьогоднішній
// світ (береться з рейтингу «Що купити») і живе там само, де черга задач:
// у /api/summary і в MQTT, але не в гіпотетичному «після» кошика, де вона
// радила б проти щойно купленого. Тому й два поля, а не одне.
type IdleCash struct {
	// InvestableUAH — Σ по парах цілих квитків, грн-екв.
	InvestableUAH float64 `json:"investable_uah"`
	// Since — дата найстарішого надходження, яке досі лежить; Days — днів
	// від неї; AgeDays — зважений за сумою вік, у днях.
	Since   string     `json:"since,omitempty"`
	Days    int        `json:"days,omitempty"`
	AgeDays float64    `json:"age_days,omitempty"`
	ByPair  []IdlePair `json:"by_pair,omitempty"`
}

// IdlePair — простій однієї пари брокер × валюта.
type IdlePair struct {
	Broker   string `json:"broker"`
	Currency string `json:"currency"`
	// Investable — цілі квитки, нативно; InvestableUAH — те саме в грн-екв.
	Investable    float64 `json:"investable"`
	InvestableUAH float64 `json:"investable_uah"`
	Since         string  `json:"since,omitempty"`
	Days          int     `json:"days,omitempty"`
	AgeDays       float64 `json:"age_days,omitempty"`
}

// IdleCost — що коштує простій за СЬОГОДНІШНЬОЮ порадою (адитивне поле).
// Порожнє без простою або без поради, яку за ці гроші можна взяти в цього
// брокера. Заповнюється лише там, де є поради (buildStateTasked).
type IdleCost struct {
	// CostMonthUAH — скільки простій коштує на місяць; CostSoFarUAH —
	// скільки вже недоотримано від дат надходжень, ACT/365.
	CostMonthUAH float64 `json:"cost_month_uah"`
	CostSoFarUAH float64 `json:"cost_so_far_uah,omitempty"`
	// RatePct — реальна ставка, за якою це рахувалось (зважена по парах);
	// RateLabel — порада, звідки вона взята (для найбільшої пари).
	RatePct   float64        `json:"rate_pct"`
	RateLabel string         `json:"rate_label"`
	ByPair    []IdleCostPair `json:"by_pair,omitempty"`
}

// IdleCostPair — ціна простою однієї пари; пари без поради сюди не входять.
type IdleCostPair struct {
	Broker       string  `json:"broker"`
	Currency     string  `json:"currency"`
	CostMonthUAH float64 `json:"cost_month_uah"`
	CostSoFarUAH float64 `json:"cost_so_far_uah,omitempty"`
	RatePct      float64 `json:"rate_pct"`
	RateLabel    string  `json:"rate_label"`
}
