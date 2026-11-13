// Прогноз капіталу за нинішнім темпом і сам темп: дві картки на «Мета
// капіталу й прогноз».
//
// Стоять на місці таблиці «Проєкції капіталу» (1 / 3 / 5 / 10 років). Та
// відповідала на чотири питання з нескінченних: «а через сім?», «а через
// пів року?» лишались без відповіді, хоч модель уміє будь-який місяць.
// Тепер термін вибирають повзунком, а крива показує дорогу до нього.
//
// АРИФМЕТИКИ ТУТ НЕМАЄ. GET /api/outlook віддає відповідь на кожен місяць
// тридцяти років, разом із «наросло» й коридором; повзунок лише вибирає
// точку. Те саме з темпом: і стовпчики, і середнє приходять готовими.
//
// Стан — на рівні модуля, як у history.js і з тієї самої причини: графік
// малюється пізніше за розмітку (fitCharts, коли стає відома ширина), і до
// того моменту локальні змінні розділу вже не існують.

import {
  esc, uah0, cur2, curSym, signedUAH, compact, humanMonths, monthYear, monthShort, pct, plural,
  outsideUAH, TODAY_FX,
} from "../format.js";
import { KIND_GROUP } from "../constants.js";
import { rateSourceLabel } from "./forecast.js";
import { sym } from "../currency.js";
import { infoBtn } from "../info.js";
import { tile, legend, empty } from "../components.js";
import {
  fluid, fitCharts, svgBandLine, moveBandMark, svgPaceBars, wireChartTips,
} from "../charts.js";
import { range } from "../fields.js";
import { pref, setPref, wirePrefs } from "../uistate.js";
import { CONTRIB } from "../contrib.js";
import { routeFor } from "../routes.js";

const KEY = "oi.outlook.months";
const VIEW_KEY = "oi.outlook.view";
const SCOPE_KEY = "oi.outlook.scope";
const DEFAULT_MONTHS = 120;

// Вікно графіка — найменше з цих, що вміщує вибраний термін. Завжди
// тридцять років на полотні означали б, що перший рік — це три пікселі
// біля лівого краю: вибираєш «через 8 місяців» і дивишся на пряму лінію.
const SPANS = [12, 24, 36, 60, 120, 180, 240, 360];
// Крок підписів осі для кожного вікна, у місяцях: пʼять-шість міток.
const LABEL_STEP = { 12: 3, 24: 6, 36: 12, 60: 12, 120: 24, 180: 36, 240: 48, 360: 60 };

const C_ACTUAL = "var(--oi-series-invested)";
const C_PLAN = "var(--oi-series-plan)";
const C_PUT = "var(--oi-series-neutral)";

let data = null;   // відповідь /api/outlook
let month = DEFAULT_MONTHS;
let view = "total"; // розріз графіка капіталу: total | currency | kind | origin
let rootEl = null; // розділ у DOM — щоб рух повзунка знав, що оновлювати
let incomeGoal = 0; // цільовий місячний дохід (independence.target_uah) — лінія на графіку доходу

const hasPace = () => !!data && !data.why;
const spanOf = (m) => SPANS.find((s) => s >= m) || SPANS[SPANS.length - 1];
const maxMonth = () => data.points.length - 1;
const span = (m) => (m % 12 === 0 ? `${m / 12} р` : `${m} міс`);

// ---------- розрізи: та сама лінія, складена смугами ----------
//
// Три способи розкласти ОДНЕ число. Смуги приходять із сервера вже
// порахованими на кожен місяць і в сумі дають загальну лінію
// (engine/outlook_test.go) — тут їх лише вибирають і підписують.
//
// «Разом» — окремий вигляд, а не четвертий розріз: там замість смуг
// живуть коридор ринку, план і «внесено», яким у стосі місця немає.
const VIEWS = [
  { v: "total", t: "Разом" },
  { v: "currency", t: "Валюти" },
  { v: "kind", t: "Види" },
  { v: "origin", t: "Наявне й нове" },
];
const SCOPES = [
  { v: "portfolio", t: "Портфель" },
  { v: "all", t: "Усі гроші" },
];

/** Обсяг, з яким питати /api/outlook: вибір живе в браузері, а суми під
 *  нього рахує сервер — тому розділ мусить знати його ще до запиту. */
export const outlookScope = () => pref(SCOPE_KEY, SCOPES.map((s) => s.v), "portfolio");

// Підпис і колір смуги за її ключем. Види й резерв — тими самими
// кольорами, що на «Як росте»: одна сутність — один колір на обох
// графіках. Назви видів — зі спільного словника (KIND_GROUP).
const BAND = {
  UAH: { label: "Гривня", color: "var(--oi-cat-1)" },
  USD: { label: "Долар", color: "var(--oi-cat-2)" },
  EUR: { label: "Євро", color: "var(--oi-cat-3)" },
  bonds: { label: KIND_GROUP.bonds, color: "var(--oi-series-nominal)" },
  deposits: { label: KIND_GROUP.deposits, color: "var(--oi-series-deposits)" },
  funds: { label: KIND_GROUP.funds, color: "var(--oi-series-funds)" },
  npf: { label: KIND_GROUP.npf, color: "var(--oi-series-npf)" },
  held: { label: "Уже куплене", color: "var(--oi-cat-4)" },
  new: { label: "Реінвест і внески", color: C_ACTUAL },
  outside: { label: "Резерв і цілі", color: "var(--oi-series-reserve)" },
};

const bandsOf = () => (view === "currency" ? data.by_currency
  : view === "kind" ? data.by_kind
    : view === "origin" ? data.by_origin : null) || [];
const bandLook = (b) => BAND[b.key] || { label: b.key, color: C_PUT };
const sharePct = (key) => ((data.strategy || []).find((s) => s.key === key) || {}).pct;

/** Дрібний рядок під числом смуги: що саме в ній лежить. */
function bandNote(b) {
  if (b.key === "outside") return "як сьогодні: не поповнюються й не ростуть";
  if (view === "currency") {
    // Нативна сума — лише для валюти: для гривні вона та сама, що й число.
    return b.key !== "UAH" && b.native ? `${cur2(b.native[month], b.key)} у самій валюті` : "";
  }
  if (view === "origin") {
    return b.key === "held" ? "папери, вклади, фонди, НПФ — тане з погашеннями"
      : "купони й погашення, що пішли в нові папери, і внески";
  }
  if (b.key === "new") return "стратегію за видами не задано";
  const share = sharePct(b.key);
  return share > 0 ? `з нових грошей сюди ${pct(share)} — за стратегією`
    : "лише вже куплене: у стратегії частки немає";
}

/** Числа вибраного місяця. Перемальовується на кожен рух повзунка. */
function readoutHTML() {
  const p = data.points[month];
  const when = `Через ${humanMonths(p.month)} · ${monthYear(p.date)}`;
  // «На рахунку буде» — лише в гривні: у валюті звітності сервер це число
  // обнуляє (майбутня гривня за сьогоднішнім курсом — число, якого немає).
  const nominal = p.nominal > 0 ? ` · на рахунку буде ≈ ${uah0(p.nominal)}` : "";
  const hero = hasPace()
    ? tile(when, uah0(p.amount), `<div class="sub-xs">${TODAY_FX}${nominal}</div>`, { hero: true })
    : tile(`${when} · за планом`, uah0(p.plan), `<div class="sub-xs">${TODAY_FX}</div>`, { hero: true });
  if (view !== "total") {
    return `<div class="tiles flush">${hero}${bandsOf().map((b) => tile(
      `<i class="swatch" style="--oi-c:${bandLook(b).color}"></i>${esc(bandLook(b).label)}`,
      uah0(b.values[month]),
      bandNote(b) ? `<div class="sub-xs">${bandNote(b)}</div>` : "")).join("")}</div>`;
  }
  if (!hasPace()) return `<div class="tiles flush">${hero}</div>`;
  return `<div class="tiles flush">
    ${hero}
    ${tile("З них внесено", uah0(p.contributed), `<div class="sub-xs">старт і внески, без відсотків</div>`)}
    ${tile("Наросло", signedUAH(p.gain), `<div class="sub-xs">купони й реінвест понад внесене</div>`,
    { tone: p.gain < 0 ? "t-warn" : "" })}
    ${tile("Гірший … кращий ринок", `${compact(p.low)} … ${compact(p.high)} ${sym()}`,
    `<div class="sub-xs">той самий темп, інші ставка й курс</div>`)}
    ${p.plan > 0 ? tile(`${CONTRIB.plan.label}: якби за планом`, uah0(p.plan),
    `<div class="sub-xs">планові надходження замість темпу</div>`) : ""}
  </div>`;
}

/** Точки й підписи осі для вікна, що вміщує вибраний термін. Спільне для
 *  обох графіків: вони стоять один під одним на тому самому повзунку, і
 *  різні вікна читались би як різні терміни. */
function windowPts() {
  const n = Math.min(spanOf(month), maxMonth());
  const pts = data.points.slice(0, n + 1);
  const step = LABEL_STEP[spanOf(month)] || 60;
  return { pts, labels: pts.map((p) => (p.month > 0 && p.month % step === 0 ? span(p.month) : "")) };
}

function drawChart(w, h) {
  const { pts, labels } = windowPts();
  if (view !== "total") {
    // Лінія тут одна — загальна: вона ж верх стосу, і на ній стоїть маркер.
    const total = { color: "var(--oi-text)", values: pts.map((p) => (hasPace() ? p.amount : p.plan)) };
    const areas = bandsOf().map((b) => ({ color: bandLook(b).color, values: b.values.slice(0, pts.length) }));
    return svgBandLine(labels, {}, [total], 0, { W: w, H: h, hits: true, mark: month, areas });
  }
  const lines = hasPace()
    ? [{ color: C_ACTUAL, values: pts.map((p) => p.amount) },
      { color: C_PLAN, values: pts.map((p) => p.plan), dash: "5 3" },
      { color: C_PUT, values: pts.map((p) => p.contributed), dash: "2 3" }]
    : [{ color: C_PLAN, values: pts.map((p) => p.plan) }];
  const bands = hasPace()
    ? { lo: pts.map((p) => p.low), hi: pts.map((p) => p.high) } : {};
  return svgBandLine(labels, bands, lines, 0, { W: w, H: h, hits: true, mark: month });
}

const tipRow = (color, label, v) =>
  `<div class="r"><span><i style="--oi-c:${color}"></i>${esc(label)}</span><b>${uah0(v)}</b></div>`;

function tipHTML(i) {
  const p = data.points[i];
  if (!p) return "";
  const head = `<div><b>${esc(humanMonths(p.month))} · ${esc(monthYear(p.date))}</b></div>`;
  if (view !== "total") {
    return head + bandsOf().map((b) => tipRow(bandLook(b).color, bandLook(b).label, b.values[i])).join("")
      + `<div class="r tot"><span>Разом</span><b>${uah0(hasPace() ? p.amount : p.plan)}</b></div>`;
  }
  if (!hasPace()) return head + tipRow(C_PLAN, "за планом", p.plan);
  return head + tipRow(C_ACTUAL, "за темпом", p.amount)
    + tipRow(C_PLAN, "за планом", p.plan) + tipRow(C_PUT, "внесено", p.contributed);
}

function legendHTML() {
  if (view !== "total") {
    return legend(bandsOf().map((b) => ({ color: bandLook(b).color, label: bandLook(b).label })));
  }
  return legend([
    hasPace() && { color: C_ACTUAL, label: "за нинішнім темпом" },
    { color: C_PLAN, label: "за планом" },
    hasPace() && { color: C_PUT, label: "внесено" },
    hasPace() && { color: C_ACTUAL, label: "коридор ринку", faint: true },
  ]);
}

const seg = (items, on, attr) => `<span class="seg">${items.map((it) =>
  `<button type="button" ${attr} value="${it.v}" aria-pressed="${it.v === on}">${it.t}</button>`).join("")}</span>`;

/** Перемкнути розріз на місці: дані ті самі, міняється лише те, як їх
 *  показано, — тож ні запиту, ні перемальовування розділу. */
function setView(v) {
  view = v;
  setPref(VIEW_KEY, v === "total" ? "" : v);
  const card = rootEl && rootEl.querySelector("[data-outlook]");
  if (!card) return;
  card.querySelectorAll("[data-oview]").forEach((b) => b.setAttribute("aria-pressed", String(b.value === v)));
  card.querySelector("[data-outlook-read]").innerHTML = readoutHTML();
  card.querySelector("[data-outlook-legend]").innerHTML = legendHTML();
  fitCharts(card);
}

// ---------- на чому засновано ----------
//
// Власник спитав прямо: «незрозуміло, звідки такі прогнози». Абзац дрібним
// шрифтом під графіком на це не відповідав — у ньому не було жодного
// числа, яке можна звірити. Тут припущення стоять випискою, кожне своїм
// рядком і своїм значенням, і всі приходять із тієї самої відповіді, що й
// крива: рукави (sleeves) — це ті, якими її пораховано.
function basisHTML(ctx) {
  const outside = outsideUAH(ctx.summary || {});
  const all = data.scope === "all";
  const kv = (l, v) => `<div class="kv"><span class="muted fine">${l}</span><span class="fine">${v}</span></div>`;
  const gy = Math.round((data.glide_years || 0) * 12);
  const rows = [
    kv("Старт", `${uah0(data.start)} · ${all
      ? "увесь капітал, разом із резервом і цілями"
      : outside > 0 ? `без резерву й цілей (${uah0(outside)}): вони не інвестуються` : "те, що вже вкладено"}`),
    kv("Щомісяця додається", hasPace()
      ? `${uah0(data.pace.monthly)} — виміряний темп за ${data.pace.months || "—"} міс`
      : "планові надходження: темпу ще немає"),
    kv("Уже куплене", "купони, відсотки й погашення — за графіком виплат; фонди й НПФ — кожен за своєю ставкою"),
  ];
  for (const c of data.sleeves || []) {
    const src = rateSourceLabel(c);
    const drift = c.rate_terminal_pct > 0 && gy > 0 && Math.abs(c.rate_terminal_pct - c.rate_pct) > 0.05
      ? ` → ${pct(c.rate_terminal_pct)} за ${humanMonths(gy)}` : ", незмінна";
    rows.push(kv(`${curSym(c.currency)} · зараз і внесок`,
      `${cur2(c.amount, c.currency)}${c.contrib_monthly > 0 ? ` · +${uah0(c.contrib_monthly)}/міс` : ""}`));
    rows.push(kv(`${curSym(c.currency)} · ставка нових грошей`,
      `${pct(c.rate_pct)}${src ? ` (${esc(src)})` : ""}${drift}`));
  }
  rows.push(kv("Знецінення гривні", `${pct(data.devaluation_pct)} на рік — уже відняте з усіх сум`));
  if (hasPace()) {
    rows.push(kv("Гірший і кращий ринок",
      `довгострокова ставка ∓${data.rate_spread_pp} п.п., знецінення ±${data.deval_spread_pp} п.п.`));
  }
  rows.push(kv("Нові гроші за видами", (data.strategy || []).length
    ? (data.strategy.map((s) => `${esc((BAND[s.key] || {}).label || s.key)} ${pct(s.pct)}`).join(" · ")
      + " — за стратегією")
    : "частки видів у стратегії не задано"));
  return `<div class="rule-top">
    <div class="sub-xs mb-xs">На чому засновано</div>
    <div class="ledger">${rows.join("")}</div>
    <div class="sub-xs mt-xs">Це припущення, не гарантія.
      <a class="lnk" href="${routeFor("policy/assumptions/main")}">Змінити припущення</a> ·
      <a class="lnk" href="${routeFor("policy/strategy/main")}">Стратегія</a></div>
  </div>`;
}

// ---------- дохід: та сама вісь часу, інше питання ----------
//
// Капітал відповідає на «скільки накопичу», дохід — на «скільки це
// приноситиме щомісяця» і, через лінію цільового доходу, на «коли можна
// перестати». Обидва графіки ходять за одним повзунком: термін один, і
// дивитись на капітал через сім років поруч із доходом через десять
// означало б порівнювати різні дні.

/** Числа доходу вибраного місяця. */
function incomeReadHTML() {
  const p = data.points[month];
  const when = `Через ${humanMonths(p.month)} · ${monthYear(p.date)}`;
  const per = (v) => `${uah0(v)}<span class="muted fine">/міс</span>`;
  if (!hasPace()) {
    return `<div class="tiles flush">${tile(`${when} · за планом`, per(p.income_plan), "", { hero: true })}</div>`;
  }
  return `<div class="tiles flush">
    ${tile(`${when} · за нинішнім темпом`, per(p.income), "", { hero: true })}
    ${tile(`${CONTRIB.plan.label}: якби за планом`, per(p.income_plan))}
  </div>`;
}

function drawIncome(w, h) {
  const { pts, labels } = windowPts();
  const lines = hasPace()
    ? [{ color: C_ACTUAL, values: pts.map((p) => p.income) },
      { color: C_PLAN, values: pts.map((p) => p.income_plan), dash: "5 3" }]
    : [{ color: C_PLAN, values: pts.map((p) => p.income_plan) }];
  return svgBandLine(labels, {}, lines, incomeGoal, { W: w, H: h, hits: true, mark: month });
}

function incomeTipHTML(i) {
  const p = data.points[i];
  if (!p) return "";
  const head = `<div><b>${esc(humanMonths(p.month))} · ${esc(monthYear(p.date))}</b></div>`;
  return head + (hasPace() ? tipRow(C_ACTUAL, "за темпом", p.income) : "")
    + tipRow(C_PLAN, "за планом", p.income_plan);
}

/** Вміст картки «Пасивний дохід» на місце рядків «через 1 / 3 / 5 / 10
 *  років»: числа вибраного місяця, другий повзунок (той самий термін —
 *  картка стоїть нижче, і тягнутись до першого було б далеко) і крива. */
function incomeBlockHTML() {
  return `<div data-income-read>${incomeReadHTML()}</div>
    ${range("outlook_months", "Термін", { min: 1, max: maxMonth(), value: month })}
    <div class="chart-wrap">${fluid(drawIncome, { onMount: (box) => mountChart(box, incomeTipHTML) })}<div class="chart-tip"></div></div>
    ${legend([
    hasPace() && { color: C_ACTUAL, label: "за нинішнім темпом" },
    { color: C_PLAN, label: "за планом" },
    incomeGoal > 0 && { color: "var(--oi-muted)", label: "цільовий дохід" },
  ])}
    <div class="sub-xs mt-xs">На чому засновано: той самий капітал, що на графіку вище, того ж
      місяця — купони й відсотки вже купленого за графіком, решта під ставку нових грошей;
      із гривневої частини віднято знецінення ${pct(data.devaluation_pct)} на рік. Резерв і цілі
      доходу тут не дають, тож перемикачі капіталу цих чисел не міняють.</div>`;
}

/** Вибрати місяць: числа, повзунки, маркери — в обох картках. Графік
 *  перемальовується лише коли термін вийшов за його вікно — інакше досить
 *  пересунути маркер. */
function setMonth(m) {
  const next = Math.max(1, Math.min(maxMonth(), Math.round(m) || DEFAULT_MONTHS));
  const redraw = spanOf(next) !== spanOf(month);
  month = next;
  setPref(KEY, month);
  if (!rootEl) return;
  const put = (sel, html) => { const el = rootEl.querySelector(sel); if (el) el.innerHTML = html; };
  put("[data-outlook-read]", readoutHTML());
  put("[data-income-read]", incomeReadHTML());
  const sliders = rootEl.querySelectorAll('input[name="outlook_months"]');
  sliders.forEach((s) => { if (+s.value !== month) s.value = String(month); });
  sliders.forEach((s) => {
    const wrap = s.closest(".range-row").nextElementSibling;
    if (redraw || !moveBandMark(wrap.querySelector(".chart-frame svg"), month)) fitCharts(wrap);
  });
}

function mountChart(box, tip = tipHTML) {
  wireChartTips(box.closest(".chart-wrap"), tip);
  // Клік по кривій ставить повзунок у цю точку: навів, побачив, зафіксував.
  box.querySelectorAll(".chart-hit").forEach((hit) =>
    hit.addEventListener("click", () => setMonth(+hit.dataset.i)));
}

function forecastCardHTML(ctx) {
  return `<div class="card" data-outlook>
    <h2 class="card-head"><span>Прогноз капіталу ${infoBtn("outlook")}</span>
      ${seg(SCOPES, data.scope || "portfolio", `data-pref="${SCOPE_KEY}"`)}</h2>
    ${hasPace() ? "" : `<div class="note">${esc(data.why)} Нижче — крива за планом.</div>`}
    <div class="mb-sm">${seg(VIEWS, view, "data-oview")}</div>
    <div data-outlook-read>${readoutHTML()}</div>
    ${range("outlook_months", "Термін", { min: 1, max: maxMonth(), value: month })}
    <div class="chart-wrap">${fluid(drawChart, { cls: "tall", onMount: mountChart })}<div class="chart-tip"></div></div>
    <div data-outlook-legend>${legendHTML()}</div>
    ${basisHTML(ctx)}
  </div>`;
}

function paceTipHTML(i) {
  const b = data.pace.bars[i];
  if (!b) return "";
  return `<div><b>${esc(monthYear(b.month))}</b></div>
    <div class="r"><span>зайшло нетто</span><b>${signedUAH(b.amount)}</b></div>${
    b.in_window ? "" : `<div class="tip-note">поза вікном, з якого виміряно темп</div>`}`;
}

function paceCardHTML() {
  const p = data.pace;
  const bars = p.bars || [];
  if (!bars.some((b) => b.amount !== 0)) return "";
  const avg = p.monthly > 0 ? p.monthly : 0;
  const days = p.window_days || 183;
  const dayWord = `${days} ${plural(days, "день", "дні", "днів")}`;
  return `<div class="card">
    <h2 class="h-row"><span>Темп ${infoBtn("pace")}</span></h2>
    <div class="tiles flush">${tile(`${CONTRIB.actual.label} — ${CONTRIB.actual.hint}`,
    avg > 0 ? `${uah0(avg)}/міс` : "—",
    `<div class="sub-xs">${avg > 0
      ? `середнє за ${p.months || "—"} міс (вікно — останні ${dayWord})`
      : `за останні ${dayWord} нових грошей нетто не додалось`}</div>`)}</div>
    <div class="chart-wrap">${fluid(
    (w, h) => svgPaceBars(bars.map((b) => ({
      label: monthShort(b.month), value: b.amount, faint: !b.in_window,
    })), avg, { W: w, H: h }),
    { onMount: (box) => wireChartTips(box.closest(".chart-wrap"), paceTipHTML) })}<div class="chart-tip"></div></div>
    ${legend([
    { color: C_ACTUAL, label: "зайшло за місяць" },
    avg > 0 && { color: C_PLAN, label: "середній темп" },
    bars.some((b) => !b.in_window) && { color: C_ACTUAL, label: "поза вікном темпу", faint: true },
  ])}
    <div class="sub">Нові гроші в портфелі за місяць, нетто: куплене мінус виплати, які не перевкладено, разом із резервом і цілями. Саме це число — внесок у прогнозі вище.</div>
  </div>`;
}

/** Шматки розділу. d — відповідь /api/outlook або null (не доїхала).
 *
 *  → { forecast, income, pace }: картка прогнозу, ВМІСТ для картки
 *  «Пасивний дохід» (саму картку з її «зараз» і датою незалежності малює
 *  future.js) і картка темпу. Порожній рядок — шматка немає; income
 *  порожній означає, що картка доходу лишається при своїх чотирьох роках. */
export function outlookHTML(ctx, d) {
  data = d && (d.points || []).length > 1 ? d : null;
  rootEl = null;
  const none = { forecast: "", income: "", pace: "" };
  if (!data) return none;
  if (!hasPace() && !data.points.some((p) => p.plan > 0)) {
    return { ...none, forecast: `<div class="card"><h2 class="h-row"><span>Прогноз капіталу ${infoBtn("outlook")}</span></h2>
      ${empty("", "Прогноз будується з капіталу й темпу поповнень — щойно в портфелі зʼявиться перший папір, крива намалюється сама.",
      { href: routeFor("policy/strategy/main"), label: "Задати мету капіталу" })}</div>` };
  }
  month = Math.max(1, Math.min(maxMonth(), Number(pref(KEY, null, "")) || DEFAULT_MONTHS));
  view = pref(VIEW_KEY, VIEWS.map((x) => x.v), "total");
  incomeGoal = Number(((ctx.summary || {}).independence || {}).target_uah) || 0;
  const hasIncome = data.points.some((p) => p.income > 0 || p.income_plan > 0);
  return {
    forecast: forecastCardHTML(ctx),
    income: hasIncome ? incomeBlockHTML() : "",
    pace: paceCardHTML(),
  };
}

/** Проводка повзунків і перемикачів. Кличеться після вставки розмітки. */
export function wireOutlook(main, ctx) {
  rootEl = main;
  main.querySelectorAll('input[name="outlook_months"]').forEach((slider) =>
    slider.addEventListener("input", () => setMonth(+slider.value)));
  main.querySelectorAll("[data-oview]").forEach((b) =>
    b.addEventListener("click", () => setView(b.value)));
  // Обсяг — інший запит, тож через спільну проводку: запамʼятати й
  // перемалювати розділ.
  wirePrefs(main, ctx, SCOPE_KEY);
}
