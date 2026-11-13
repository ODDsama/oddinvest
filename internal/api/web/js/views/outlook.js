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
  esc, uah0, signedUAH, compact, humanMonths, monthYear, monthShort, pct, plural, outsideUAH,
  TODAY_FX,
} from "../format.js";
import { sym } from "../currency.js";
import { infoBtn } from "../info.js";
import { tile, legend, empty } from "../components.js";
import {
  fluid, fitCharts, svgBandLine, moveBandMark, svgPaceBars, wireChartTips,
} from "../charts.js";
import { range } from "../fields.js";
import { pref, setPref } from "../uistate.js";
import { CONTRIB } from "../contrib.js";
import { routeFor } from "../routes.js";

const KEY = "oi.outlook.months";
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
let rootEl = null; // розділ у DOM — щоб рух повзунка знав, що оновлювати
let incomeGoal = 0; // цільовий місячний дохід (independence.target_uah) — лінія на графіку доходу

const hasPace = () => !!data && !data.why;
const spanOf = (m) => SPANS.find((s) => s >= m) || SPANS[SPANS.length - 1];
const maxMonth = () => data.points.length - 1;
const span = (m) => (m % 12 === 0 ? `${m / 12} р` : `${m} міс`);

/** Числа вибраного місяця. Перемальовується на кожен рух повзунка. */
function readoutHTML() {
  const p = data.points[month];
  const when = `Через ${humanMonths(p.month)} · ${monthYear(p.date)}`;
  if (!hasPace()) {
    return `<div class="tiles flush">${tile(`${when} · за планом`, uah0(p.plan),
      `<div class="sub-xs">${TODAY_FX}</div>`, { hero: true })}</div>`;
  }
  // «На рахунку буде» — лише в гривні: у валюті звітності сервер це число
  // обнуляє (майбутня гривня за сьогоднішнім курсом — число, якого немає).
  const nominal = p.nominal > 0 ? ` · на рахунку буде ≈ ${uah0(p.nominal)}` : "";
  return `<div class="tiles flush">
    ${tile(when, uah0(p.amount), `<div class="sub-xs">${TODAY_FX}${nominal}</div>`, { hero: true })}
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
  if (!hasPace()) return head + tipRow(C_PLAN, "за планом", p.plan);
  return head + tipRow(C_ACTUAL, "за темпом", p.amount)
    + tipRow(C_PLAN, "за планом", p.plan) + tipRow(C_PUT, "внесено", p.contributed);
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
  ])}`;
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
  const s = ctx.summary || {};
  const outside = outsideUAH(s);
  const gy = data.glide_years || 0;
  const rate = data.rate_pct > 0
    ? (data.rate_terminal_pct > 0 && gy > 0 && Math.abs(data.rate_terminal_pct - data.rate_pct) > 0.05
      ? `${pct(data.rate_pct)} номінальних сьогодні → ${pct(data.rate_terminal_pct)} за ${humanMonths(Math.round(gy * 12))}`
      : `${pct(data.rate_pct)} номінальних`)
    : "";
  const note = `Старт — ${uah0(data.start)}${outside > 0
    ? `, <b>без резерву й цілей накопичення</b> (${uah0(outside)} не інвестуються, тож і не ростуть)` : ""}.${
    rate ? ` Ставка ${rate}; знецінення гривні ${pct(data.devaluation_pct)} на рік уже відняте.` : ""}`;

  return `<div class="card" data-outlook>
    <h2 class="h-row"><span>Прогноз капіталу ${infoBtn("outlook")}</span></h2>
    ${hasPace() ? "" : `<div class="note">${esc(data.why)} Нижче — крива за планом.</div>`}
    <div data-outlook-read>${readoutHTML()}</div>
    ${range("outlook_months", "Термін", { min: 1, max: maxMonth(), value: month })}
    <div class="chart-wrap">${fluid(drawChart, { cls: "tall", onMount: mountChart })}<div class="chart-tip"></div></div>
    ${legend([
    hasPace() && { color: C_ACTUAL, label: "за нинішнім темпом" },
    { color: C_PLAN, label: "за планом" },
    hasPace() && { color: C_PUT, label: "внесено" },
    hasPace() && { color: C_ACTUAL, label: "коридор ринку", faint: true },
  ])}
    <div class="sub">${note} Це припущення, не гарантія.</div>
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
  incomeGoal = Number(((ctx.summary || {}).independence || {}).target_uah) || 0;
  const hasIncome = data.points.some((p) => p.income > 0 || p.income_plan > 0);
  return {
    forecast: forecastCardHTML(ctx),
    income: hasIncome ? incomeBlockHTML() : "",
    pace: paceCardHTML(),
  };
}

/** Проводка повзунків. Кличеться після вставки розмітки. */
export function wireOutlook(main) {
  rootEl = main;
  main.querySelectorAll('input[name="outlook_months"]').forEach((slider) =>
    slider.addEventListener("input", () => setMonth(+slider.value)));
}
