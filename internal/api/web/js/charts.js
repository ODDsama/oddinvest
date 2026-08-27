// Маленькі SVG-графіки без бібліотек.
//
// Малюємо руками з двох причин: усередині shadow DOM більшість чартових
// бібліотек ламається на стилях, а сам застосунок ставиться в LXC без
// збірки — тягнути пакет заради п'яти стовпчиків невигідно.
//
// Кольори — ТІЛЬКИ токени. Колись та сама svgBars мала fill="#8b949e" в
// одній копії і fill="var(--secondary-text-color)" в другій: один код,
// дві палітри, і на світлому тлі половина кольорів була б невидима.
// Копій більше немає, а правило лишається — колір вирішує тема, не
// функція, і хекс тут читається як помилка.

import { esc, compact } from "./format.js";

/** Палітра категорій без власного значення (сегменти кільця брокерів):
 *  тут потрібна саме відрізнюваність, а не сенс. */
export const CAT_COLORS = [
  "var(--oi-cat-1)", "var(--oi-cat-2)", "var(--oi-cat-3)", "var(--oi-cat-4)",
  "var(--oi-cat-5)", "var(--oi-cat-6)", "var(--oi-cat-7)",
];

/** Кольори повернень тіла за видом інструмента. Експортуються, бо той самий
 *  колір мусить збігтися між маркером на графіку й рядком у списку під ним:
 *  порізно вони роз'їхались би при першій же правці токена. */
export const EVENT_COLORS = {
  bond: "var(--oi-kind-bond)", deposit: "var(--oi-kind-deposit)", fund: "var(--oi-kind-fund)",
  npf: "var(--oi-kind-npf)",
};

// Підписи — найтихіший рівень тексту: вони супроводжують число чи стовпчик,
// а не несуть власний зміст, тож --oi-text-faint (3.6:1) тут доречний рівно
// за визначенням цього токена в темі. Було --oi-muted — рівень для тексту,
// що читають САМ, а вісь ніхто не читає окремо від того, що на ній стоїть.
const AXIS = "var(--oi-text-faint)";
// Сітка й розділові лінії — рядок від рядка всередині одного полотна,
// тобто та сама роль, що в таблиць: --oi-rule-hair, а не --oi-border. Базова
// лінія осі (нуль) лишається на --oi-border — вона межа полотна, а не
// проміжна позначка.
const GRID = "var(--oi-rule-hair)";

// Полотно малих графіків.
//
// Було 320×170, і в цьому й полягала вада, яку довго приймали за
// «графіки не гумові». Гумовими вони були завжди (style="width:100%"),
// біда протилежна: viewBox масштабується ЦІЛКОМ, разом із текстом. У
// картці на 1000px полотно шириною 320 розтягується втричі — і підпис
// font-size="10" приїжджає на екран розміром 31px, більшим за заголовок
// картки. На телефоні той самий графік малює ті самі 10px.
//
// Ширше полотно зменшує коефіцієнт розтягу: у півширинній картці
// десктопа він стає ~0.8 замість ~1.6. Разом із більшим базовим
// кеглем (13 замість 10) підписи приземляються на 10-11px СКРІЗЬ.
//
// Викликач може задати своє W/H — об'єкт опцій необов'язковий, тож усі
// наявні виклики працюють без змін.
const W0 = 640, H0 = 220;
// Кегль підписів. Тепер це справжні пікселі на екрані, а не значення в
// довільно розтягнутій системі координат, — тож він мусить збігатися з
// рядом типографіки: --oi-fs-xs у tokens.css описаний рівно як «підписи
// осей». Різниці між підписом осі й числом над стовпчиком не робимо:
// далі 11px іде вже нечитабельне.
const FS = 11;
const FS_SM = 11;

// ---------- відкладене малювання ----------
//
// Фіксований viewBox не може бути правильним двічі. Картка буває
// шириною 351px на телефоні й 1030px на десктопі, а між ними ще
// півширинна на ~500 — і той самий viewBox дає коефіцієнт розтягу від
// 0.55 до 1.6. Текст усередині SVG розтягується РАЗОМ із полотном, тож
// один і той самий підпис приїжджає то 7px, то 21px. Жодне значення W
// цього не лікує: лікує лише збіг viewBox із фактичною шириною, коли
// коефіцієнт стає рівно 1.
//
// Тому графік малюється у два проходи. Розділ вставляє порожню рамку
// (розмітка це рядок, ширини на той момент ще не існує), а після
// вставки fitCharts() міряє рамку й кличе саму функцію малювання вже зі
// справжніми розмірами. Заразом це дає перемальовування при зміні
// розміру вікна — доти графіки просто розтягувались.

let seq = 0;
const queued = new Map();       // id -> draw, живе до першої вставки
const mounted = new WeakMap();  // рамка -> draw, щоб перемалювати на resize

/** Місце під графік. draw(w, h) поверне SVG, коли стануть відомі розміри.
 *
 *  onMount(box) — те, що треба зробити ПІСЛЯ появи SVG: підказки під
 *  курсором інакше не було б до чого чіпляти, бо на момент звичайної
 *  проводки розділу малюнка ще не існує.
 *  cls — додатковий клас рамки (наприклад «вищий графік»). */
export function fluid(draw, { onMount = null, cls = "" } = {}) {
  const id = `oi-c${++seq}`;
  queued.set(id, { draw, onMount });
  return `<div class="chart-frame${cls ? " " + cls : ""}" data-chart="${id}"></div>`;
}

/** Домалювати всі рамки в root. Кличеться після вставки розмітки і на
 *  зміну розміру вікна. */
export function fitCharts(root) {
  if (!root) return;
  root.querySelectorAll("[data-chart]").forEach((box) => {
    const rec = queued.get(box.dataset.chart) || mounted.get(box);
    if (!rec) return;
    const b = box.getBoundingClientRect();
    const w = Math.round(b.width);
    // Запис переїжджає в mounted ПЕРШИМ, ще до перевірки ширини, і це не
    // перестановка заради охайності.
    //
    // Нуль означає, що рамка ще не в розкладці (згорнута секція,
    // display:none) — малювати нема під що, домалюємо, коли розгорнуть.
    // Але queued.clear() наприкінці циклу відпрацьовував БЕЗУМОВНО, тож
    // вихід звідси до mounted.set() викидав функцію малювання назовсім:
    // у queued її вже нема, у mounted ще нема, і слухач toggle, який мав
    // домалювати рамку в мить розкриття, не знаходив нічого. Тобто
    // «домалюємо, коли розгорнуть» не працювало жодного разу — графік у
    // згорнутій за замовчуванням секції лишався порожньою рамкою назавжди,
    // і побачити це можна було в «Плані», де таких було чотири підряд.
    mounted.set(box, rec);
    if (!w) return;
    box.innerHTML = rec.draw(w, Math.round(b.height) || Math.round(w / 2.6));
    if (rec.onMount) rec.onMount(box);
  });
  queued.clear();
}

/** Легенда серій. Окремо від seriesChart, бо рамка малює лише SVG, а
 *  легенда стоїть під нею й ширини не потребує. */
export function seriesLegend(series) {
  return series.map((s) =>
    `<span><i style="--oi-c:${s.color}"></i>${esc(s.name)}</span>`).join("");
}

/** Стовпчики. items: [{label, value, color?}]. showVals — підпис суми зверху. */
export function svgBars(items, { showVals = false, W = W0, H = H0 } = {}) {
  const Pl = 6, Pr = 6, Pt = 18, Pb = 30;
  const iw = W - Pl - Pr, ih = H - Pt - Pb, n = Math.max(1, items.length);
  const max = Math.max(1, ...items.map((i) => i.value));
  const gap = iw / n, bw = Math.min(46, gap * 0.62);
  let out = "";
  items.forEach((it, i) => {
    const h = (it.value / max) * ih, x = Pl + gap * i + (gap - bw) / 2, y = Pt + ih - h;
    out += `<rect x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${bw.toFixed(1)}" height="${Math.max(0, h).toFixed(1)}"`
      + ` fill="${it.color || "var(--oi-series-invested)"}">`
      + `<title>${esc(it.label)}: ${Math.round(it.value).toLocaleString("uk")} ₴</title></rect>`;
    out += `<text x="${(x + bw / 2).toFixed(1)}" y="${H - 10}" text-anchor="middle" font-size="${FS}" fill="${AXIS}">${esc(it.label)}</text>`;
    if (showVals && it.value > 0) {
      out += `<text x="${(x + bw / 2).toFixed(1)}" y="${(y - 4).toFixed(1)}" text-anchor="middle" font-size="${FS_SM}" fill="${AXIS}">${compact(it.value)}</text>`;
    }
  });
  return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet">${out}</svg>`;
}

/** Згруповані стовпчики. groups: [{label, values: [..], derived: [bool]}]
 *  або, як було, [{label, a, b}] — пара читається як values з двох чисел.
 *
 *  derived позначає стовпчики, чиє число ВИВЕДЕНЕ, а не записане: такі
 *  малюються контуром.
 *
 *  keys/colors/fmt лишаються з типовими значеннями «факт vs ціль у
 *  відсотках»: обидва старі виклики (валютні частки, крива аукціонів) від
 *  узагальнення не змінились. Третій ряд знадобився «Плану проти факту»,
 *  де стовпчиків три й вони в гривнях, а не у відсотках. */
export function svgGrouped(groups, {
  W = W0, H = H0,
  colors = ["var(--oi-series-invested)", "var(--oi-series-neutral)"],
  fmt = (v) => `${v.toFixed(1)}%`,
  hits = false,
} = {}) {
  const Pl = 6, Pr = 6, Pt = 14, Pb = 30;
  const iw = W - Pl - Pr, ih = H - Pt - Pb, n = Math.max(1, groups.length);
  const vals = (g) => (g.values || [g.a, g.b]).map((v) => v || 0);
  const max = Math.max(1, ...groups.flatMap((g) => vals(g)));
  const k = Math.max(1, ...groups.map((g) => vals(g).length));
  const gap = iw / n, bw = Math.min(22, (gap * 0.72) / k);
  let out = "", hitRects = "";
  groups.forEach((g, i) => {
    const cx = Pl + gap * i + gap / 2;
    vals(g).forEach((v, j) => {
      // Стовпчики розходяться від центру групи: при двох це та сама
      // картинка, що й доти (−bw−2 / +2), при трьох середній стоїть на осі.
      const h = (v / max) * ih;
      const x = cx + (j - k / 2) * (bw + 2) + 1, y = Pt + ih - h;
      // derived — стовпчик малюється контуром: це число не записане, а
      // виведене. Контур замість <pattern>: патерн у тіньовому дереві
      // вимагає власного <defs> з унікальним id на кожен графік, а різниця
      // «залите vs порожнє» читається й без нього.
      const col = colors[j % colors.length];
      const hollow = (g.derived || [])[j];
      out += `<rect x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${bw.toFixed(1)}"`
        + ` height="${Math.max(0, h).toFixed(1)}" fill="${col}"`
        + (hollow ? ` fill-opacity="0.3" stroke="${col}" stroke-width="1"` : "")
        + `><title>${esc(fmt(v))}</title></rect>`;
    });
    out += `<text x="${cx.toFixed(1)}" y="${H - 10}" text-anchor="middle" font-size="${FS}" fill="${AXIS}">${esc(g.label)}</text>`;
    if (hits) {
      hitRects += `<rect class="chart-hit" data-i="${i}" tabindex="0" role="button"
        aria-label="${esc(g.label)}" x="${(Pl + gap * i).toFixed(1)}" y="${Pt}"
        width="${gap.toFixed(1)}" height="${ih}" fill="transparent"/>`;
    }
  });
  return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet">${out}${hitRects}</svg>`;
}

/** Лінійний графік із кількома серіями по спільних x-мітках.
 *  series: [{color, values}], xlabels — підпис КОЖНОЇ точки (проріджує
 *  функція сама, див. нижче).
 *
 *  zero — чи мусить вісь Y починатися з нуля. Типово НІ, і це головна
 *  зміна: доти шкала завжди росла від нуля, тож крива ціни сертифіката
 *  (~1000 ₴) лягала пласкою рискою під стелею — уся річна зміна не
 *  дотягувала й до відсотка висоти полотна. Проєкція капіталу передає
 *  zero: true, бо вона справді росте з нуля.
 *
 *  fmt — форматувальник підписів осі. Типове compact розписує суми
 *  («247к»), але ціну сертифіката воно звело б до «1,0к» на всіх поділках
 *  одразу; викликач, що показує не суму, передає своє.
 *
 *  Точки розставлені за ІНДЕКСОМ, а не за датою: пропуск у три місяці
 *  малюється тією ж шириною, що й день. Так було завжди, і тут це
 *  свідомо не чіпається — рівномірна шкала часу зачепила б і підписи, і
 *  смуги влучання, тобто це окреме рішення, а не подробиця цього. */
export function svgLine(xlabels, series, {
  W = W0, H = H0, zero = false, fmt = compact, label = "",
} = {}) {
  const scale = niceScale(series.flatMap((s) => s.values), { zero });
  // Pl був 8 — рівно стільки, скільки треба обведенню лінії. Тепер ліворуч
  // стоять числа осі, і місце під них рахується з них самих: 56 різало
  // «1000.0000», а під ЧВОПА з шістьма знаками не вистачило б і 66.
  const Pl = axisWidth(scale.ticks, fmt), Pr = 12, Pt = 14, Pb = 28;
  const iw = W - Pl - Pr, ih = H - Pt - Pb, n = Math.max(1, xlabels.length);
  const X = (i) => Pl + (n <= 1 ? iw / 2 : (iw * i) / (n - 1));
  const Y = (v) => Pt + ih - ((v - scale.lo) / (scale.hi - scale.lo)) * ih;
  const lines = series.map((s) =>
    `<polyline points="${s.values.map((v, i) => `${X(i).toFixed(1)},${Y(v).toFixed(1)}`).join(" ")}"`
    + ` fill="none" stroke="${s.color}" stroke-width="1.5" stroke-linejoin="round"/>`).join("");

  // Крок підписів за шириною полотна — те саме правило, що в seriesChart:
  // на вузькому екрані дванадцять дат злипаються в сіру смугу, а п'ять
  // читаються. Доти проріджували самі викликачі, кожен своєю копією того
  // самого рядка; тут це знає той, хто єдиний знає ширину.
  const step = Math.max(1, Math.floor(n / (W < 560 ? 3 : 5)));
  const shown = new Set();
  for (let i = 0; i < n; i += step) shown.add(i);
  shown.add(n - 1);
  const xl = [...shown].sort((a, b) => a - b).map((i) => (xlabels[i]
    ? `<text x="${X(i).toFixed(1)}" y="${H - 10}" text-anchor="${xAnchor(X(i), Pl, W - Pr)}"`
      + ` font-size="${FS}" fill="${AXIS}">${esc(xlabels[i])}</text>`
    : "")).join("");

  // Смуги влучання на всю висоту, по одній на точку — як у seriesChart.
  // Розмітку підказки будує вже той, хто знає дані (wireChartTips), а
  // tabindex/role/aria-label дають до неї дійти з клавіатури: без них
  // крива читалась лише мишею.
  let hits = "";
  if (n > 1) {
    const bw = iw / (n - 1);
    for (let i = 0; i < n; i++) {
      const hx = Math.max(Pl, X(i) - bw / 2);
      hits += `<rect class="chart-hit" data-i="${i}" tabindex="0" role="button"`
        + ` aria-label="${esc(xlabels[i] || "")}" x="${hx.toFixed(1)}" y="${Pt}"`
        + ` width="${Math.min(bw, W - Pr - hx).toFixed(1)}" height="${ih}" fill="transparent"/>`;
    }
  }

  const name = label || "Крива";
  return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" role="img"`
    + ` aria-label="${esc(name)}"><title>${esc(name)}</title>`
    + `${axisY(scale, Pl, W - Pr, Y, fmt)}${xl}${lines}${hits}</svg>`;
}

/** Крива прогнозу: коридор між двома серіями, лінії всередині нього і
 *  горизонтальна лінія цілі.
 *
 *  Окремо від svgLine навмисно. Той малює рівноправні серії по спільних
 *  мітках; тут же в серій РІЗНІ ролі — дві задають межі коридору, третя
 *  йде посередині, четверта показує відхилення від неї, а ціль це взагалі
 *  не серія, а орієнтир. Запхати ролі в svgLine означало б додати йому
 *  чотири прапорці й зламати два наявні виклики.
 *
 *  bands: {lo, hi} — значення нижньої й верхньої межі коридору (може
 *  бути порожньо). lines: [{color, values, dash}]. goal — число або 0.
 *  xlabels — підписи, порожній рядок = мітки немає. */
export function svgBandLine(xlabels, bands, lines, goal, { W = W0, H = H0 } = {}) {
  const n = Math.max(1, xlabels.length);
  // Ціль входить у масштаб: інакше лінія цілі вилітала б за полотно, і
  // «не дотягуємо» виглядало б як «дотягуємо».
  const all = lines.flatMap((s) => s.values).concat(bands.hi || [], [goal || 0]);
  const scale = niceScale(all, { zero: true });
  // Місце під числа осі — з них самих, як у svgLine.
  const Pl = axisWidth(scale.ticks, compact), Pr = 12, Pt = 14, Pb = 28;
  const iw = W - Pl - Pr, ih = H - Pt - Pb;
  const X = (i) => Pl + (n <= 1 ? iw / 2 : (iw * i) / (n - 1));
  const Y = (v) => Pt + ih - ((v - scale.lo) / (scale.hi - scale.lo)) * ih;
  let out = axisY(scale, Pl, W - Pr, Y, compact);

  if ((bands.lo || []).length && (bands.hi || []).length) {
    const up = bands.hi.map((v, i) => `${X(i).toFixed(1)},${Y(v).toFixed(1)}`);
    const down = bands.lo.map((v, i) => `${X(i).toFixed(1)},${Y(v).toFixed(1)}`).reverse();
    out += `<polygon points="${up.concat(down).join(" ")}" fill="var(--oi-series-invested)" opacity="0.09"/>`;
  }
  if (goal > 0) {
    const y = Y(goal).toFixed(1);
    out += `<line x1="${Pl}" y1="${y}" x2="${W - Pr}" y2="${y}" stroke="${GRID}"`
      + ` stroke-width="1" stroke-dasharray="2 4"/>`;
  }
  out += lines.map((s) =>
    `<polyline points="${s.values.map((v, i) => `${X(i).toFixed(1)},${Y(v).toFixed(1)}`).join(" ")}"`
    + ` fill="none" stroke="${s.color}" stroke-width="1.5" stroke-linejoin="round"`
    + `${s.dash ? ` stroke-dasharray="${s.dash}"` : ""}/>`).join("");
  out += xlabels.map((l, i) => l
    ? `<text x="${X(i).toFixed(1)}" y="${H - 10}" text-anchor="${xAnchor(X(i), Pl, W - Pr)}" font-size="${FS}" fill="${AXIS}">${esc(l)}</text>`
    : "").join("");
  return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet">${out}</svg>`;
}

/** Профіль надходжень: скільки ₴/міс заходить у кожен місяць горизонту.
 *
 *  Замінив Гант зі смугами «з…до». Причина не в смаку: Гант малював рівно
 *  те, що вже стоїть у таблиці колонками «З» і «До», — дві смуги на всю
 *  ширину й жодного числа. Питання, на яке таблиця відповісти не може, —
 *  ФОРМА плану в часі: коли надходження підскочить, коли просяде, де його
 *  зжере разова витрата. Саме її ця картинка й малює.
 *
 *  Складені площі по потоках (доходи вгору від нуля, витрати вниз), лінія
 *  net поверх — те саме число, що плитка «План дає», але в часі. Вісь Y у
 *  ₴/міс із підписами: у попередньої стрічки чисел не було взагалі.
 *
 *  profile — {step_months, series, points} із GET /api/plan. */
export function svgInflowProfile(profile, actions = [], milestones = [], { W = W0, H = 260 } = {}) {
  const pts = (profile && profile.points) || [];
  const series = (profile && profile.series) || [];
  if (pts.length < 2) return "";

  const day = (iso) => Date.parse(iso + "T00:00:00Z") / 86400000;
  const d0 = day(pts[0].date), d1 = Math.max(day(pts[pts.length - 1].date), d0 + 1);
  // Ліве поле під підписи осі Y: без нього «122к» вилазило б за полотно.
  // Нижнє поле ширше, коли є повернення тіла: маркер росте вниз від нуля, а
  // на 26px він або наліз би на підписи років, або лишився б пилинкою.
  const hasEvents = ((profile && profile.events) || []).length > 0;
  const Pl = 46, Pr = 10, Pt = 12, Pb = hasEvents ? 32 : 26;
  const iw = W - Pl - Pr, ih = H - Pt - Pb;
  const X = (iso) => Pl + ((day(iso) - d0) / (d1 - d0)) * iw;

  // Масштаб охоплює і плюс, і мінус: витратний потік малюється вниз, і
  // нуль мусить стояти там, де він справді нуль, а не внизу полотна.
  // Дохід портфеля лягає ПОВЕРХ додатного стосу, тож у стелю входить
  // разом із ним.
  const up = (p) => p.values.reduce((a, v) => a + (v >= 0 ? v : 0), 0);
  let hi = 0, lo = 0;
  for (const p of pts) {
    const dn = p.values.reduce((a, v) => a + (v < 0 ? v : 0), 0);
    hi = Math.max(hi, up(p) + (p.income || 0), p.net);
    lo = Math.min(lo, dn, p.net);
  }
  if (hi === 0 && lo === 0) return "";
  const span = (hi - lo) || 1;
  const Y = (v) => Pt + ih - ((v - lo) / span) * ih;
  const zero = Y(0);

  // Складання: кожен ряд лежить на сумі попередніх ТОГО САМОГО знака, тож
  // доходи ростуть угору від нуля, а витрати вниз, і вони не з'їдають
  // одне одного візуально.
  const areas = series.map((s, i) => {
    const top = [], bot = [];
    for (const p of pts) {
      const v = p.values[i] || 0;
      let base = 0;
      for (let k = 0; k < i; k++) {
        const o = p.values[k] || 0;
        if ((o >= 0) === (v >= 0)) base += o;
      }
      top.push(`${X(p.date).toFixed(1)},${Y(base + v).toFixed(1)}`);
      bot.push(`${X(p.date).toFixed(1)},${Y(base).toFixed(1)}`);
    }
    const color = s.kind === "expense" ? "var(--oi-warn)" : CAT_COLORS[i % CAT_COLORS.length];
    return `<polygon points="${top.concat(bot.reverse()).join(" ")}" fill="${color}"
      opacity="0.55"><title>${esc(s.name)}</title></polygon>`;
  }).join("");

  // Дохід портфеля — ОКРЕМИЙ шар поверх плану, а не ще один ряд у стосі.
  //
  // Різниця принципова: план це гроші, які ти вносиш, а це — які портфель
  // заробляє сам. Змішавши їх, довелось би або зламати рівність лінії
  // «план» із плиткою «План дає», або вдавати, що купон — твій внесок.
  const hasIncome = pts.some((p) => (p.income || 0) > 0);
  let incomeArea = "";
  if (hasIncome) {
    const top = [], bot = [];
    for (const p of pts) {
      const base = up(p);
      top.push(`${X(p.date).toFixed(1)},${Y(base + (p.income || 0)).toFixed(1)}`);
      bot.push(`${X(p.date).toFixed(1)},${Y(base).toFixed(1)}`);
    }
    incomeArea = `<polygon points="${top.concat(bot.reverse()).join(" ")}"
      fill="var(--oi-series-nominal)" opacity="0.35"><title>дохід портфеля</title></polygon>`;
  }

  const net = `<polyline points="${pts.map((p) => `${X(p.date).toFixed(1)},${Y(p.net).toFixed(1)}`).join(" ")}"
    fill="none" stroke="var(--oi-series-invested)" stroke-width="1.5" stroke-linejoin="round"/>`;

  // «Усе разом» — план плюс дохід портфеля. Пунктиром і лише коли дохід
  // справді є: інакше це була б друга лінія поверх першої.
  const total = hasIncome
    ? `<polyline points="${pts.map((p) =>
      `${X(p.date).toFixed(1)},${Y(p.net + (p.income || 0)).toFixed(1)}`).join(" ")}"
      fill="none" stroke="var(--oi-series-neutral)" stroke-width="1.5"
      stroke-dasharray="5 3" stroke-linejoin="round"/>`
    : "";

  // Вісь Y: нуль плюс дві межі. Більше підписів на 260px злиплись би, а
  // менше — і масштаб знову став би невідомим.
  const yTicks = [hi, 0, lo].filter((v, i, a) => a.indexOf(v) === i && (v !== 0 || true))
    .map((v) => `<line x1="${Pl}" y1="${Y(v).toFixed(1)}" x2="${(Pl + iw).toFixed(1)}"
        y2="${Y(v).toFixed(1)}" stroke="${v === 0 ? "var(--oi-border)" : GRID}" stroke-width="1"/>
      <text x="${Pl - 6}" y="${(Y(v) + 3).toFixed(1)}" text-anchor="end" font-size="${FS}"
        fill="${AXIS}">${esc(compact(v))}</text>`).join("");

  // Вісь X — роки, ПІД полотном і всередині viewBox. Попередня стрічка
  // малювала їх на curveTop+curveH+16 при фіксованій висоті рамки, і при
  // десятьох рядках смуг вони опинялись за межами viewBox: вісь була, її
  // просто не було видно.
  let xlabels = "";
  const y0 = +pts[0].date.slice(0, 4), y1 = +pts[pts.length - 1].date.slice(0, 4);
  for (let yr = y0; yr <= y1; yr++) {
    const iso = `${yr}-01-01`;
    if (day(iso) < d0 || day(iso) > d1) continue;
    const x = X(iso);
    xlabels += `<line x1="${x.toFixed(1)}" y1="${Pt}" x2="${x.toFixed(1)}" y2="${(Pt + ih).toFixed(1)}"
        stroke="${GRID}" stroke-width="1"/>
      <text x="${x.toFixed(1)}" y="${(H - 8).toFixed(1)}" text-anchor="middle"
        font-size="${FS}" fill="${AXIS}">${yr}</text>`;
  }

  // Віхи — вертикалі з підписом угорі; крайні тримаємо в межах полотна,
  // інакше «сьогодні» на лівому краї обрізалось наполовину.
  const marks = (milestones || []).map((m) => {
    const x = X(m.date);
    if (x < Pl - 1 || x > Pl + iw + 1) return "";
    const anchor = x < Pl + 30 ? "start" : x > Pl + iw - 30 ? "end" : "middle";
    return `<line x1="${x.toFixed(1)}" y1="${Pt}" x2="${x.toFixed(1)}" y2="${(Pt + ih).toFixed(1)}"
        stroke="${GRID}" stroke-width="1" stroke-dasharray="2 4"/>
      <text x="${x.toFixed(1)}" y="${(Pt - 3).toFixed(1)}" text-anchor="${anchor}"
        font-size="${FS}" fill="${AXIS}">${esc(m.label)}</text>`;
  }).join("");

  // Дії — ромби на нульовій лінії: вони не сума на місяць, а подія.
  const acts = (actions || []).map((a) => {
    const x = X(a.date);
    if (x < Pl - 1 || x > Pl + iw + 1) return "";
    const color = a.type === "lock" ? "var(--oi-series-invested)" : "var(--oi-info)";
    const title = a.name || (a.type === "lock" ? "замок" : "зміна часток");
    const r = 4;
    return `<polygon points="${x.toFixed(1)},${(zero - r).toFixed(1)} ${(x + r).toFixed(1)},${zero.toFixed(1)}`
      + ` ${x.toFixed(1)},${(zero + r).toFixed(1)} ${(x - r).toFixed(1)},${zero.toFixed(1)}"`
      + ` fill="${color}"><title>${esc(title)}</title></polygon>`;
  }).join("");

  // Повернення тіла — трикутники ПІД нульовою лінією, щоб їх не плутати з
  // ромбами дій плану. Це не дохід і не внесок: твої ж гроші повертаються
  // з паперу на рахунок, і питання, яке вони ставлять, інше — «що з ними
  // робити далі».
  //
  // Розмір маркера НЕ пропорційний сумі, і це навмисно: погашення на 1 000 ₴
  // проти осі в 42 000 дало б смужку в два пікселі, тобто «події немає».
  // Питання тут не «скільки», а «коли» — на «скільки» відповідає підказка й
  // список під графіком. Стебло від нульової лінії робить подію засічкою на
  // осі: доти чотирипіксельний трикутник під віссю читався як пилинка.
  const events = ((profile && profile.events) || []).map((e) => {
    const x = X(e.date);
    if (x < Pl - 1 || x > Pl + iw + 1) return "";
    const r = 5, y = zero + 5, color = EVENT_COLORS[e.kind] || AXIS;
    const title = `<title>${esc(e.label)}: повертається ${
      Math.round(e.amount_uah).toLocaleString("uk-UA")} ₴</title>`;
    return `<line x1="${x.toFixed(1)}" y1="${zero.toFixed(1)}" x2="${x.toFixed(1)}"
        y2="${y.toFixed(1)}" stroke="${color}" stroke-width="1.5">${title}</line>
      <polygon points="${x.toFixed(1)},${(y + r).toFixed(1)} ${(x + r).toFixed(1)},${y.toFixed(1)}`
      + ` ${(x - r).toFixed(1)},${y.toFixed(1)}" fill="${color}">${title}</polygon>`;
  }).join("");

  // Смуги влучання — по одній на точку, на всю висоту полотна: розмітку
  // підказки будує той, хто знає дані (див. wireChartTips), а тут лише
  // геометрія. Межі — середини між сусідніми точками, а не рівна сітка:
  // крок по датах нерівномірний (місяці різної довжини), і рівна сітка
  // поволі з'їжджала б із точок під кінець горизонту.
  const hits = pts.map((p, i) => {
    const cx = X(p.date);
    const lo = i === 0 ? Pl : (X(pts[i - 1].date) + cx) / 2;
    const hi2 = i === pts.length - 1 ? Pl + iw : (cx + X(pts[i + 1].date)) / 2;
    return `<rect class="chart-hit" data-i="${i}" tabindex="0" role="button"
      aria-label="${esc(p.date)}" x="${lo.toFixed(1)}" y="${Pt}"
      width="${Math.max(1, hi2 - lo).toFixed(1)}" height="${ih}" fill="transparent"/>`;
  }).join("");

  return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" role="img"
    aria-label="Профіль надходжень плану, гривень на місяць">`
    + `${yTicks}${xlabels}${areas}${incomeArea}${net}${total}${marks}${acts}${events}${hits}</svg>`;
}

/** Кільце часток. parts: [{label, value}] — уже відсортовані.
 *  Повертає {svg, colors}: легенду малює той, хто кличе, бо підпис
 *  частки в різних місцях різний (сума, відсоток, і те й те). */
export function svgDonut(parts) {
  // Тонше кільце: доти товщина штриха (22) майже дорівнювала радіусу (60),
  // і форма читалась ближче до товстого бублика, ніж до лінії частки.
  // Ширший радіус (64) заразом лишає більше повітря всередині — там, де
  // в редизайні стоїть легенда.
  const R = 64, WIDTH = 12, C = 2 * Math.PI * R;
  const total = parts.reduce((s, p) => s + p.value, 0) || 1;
  const colors = parts.map((_, i) => CAT_COLORS[i % CAT_COLORS.length]);
  let acc = 0;
  const arcs = parts.map((p, i) => {
    const len = (p.value / total) * C;
    const arc = `<circle cx="80" cy="80" r="${R}" fill="none" stroke="${colors[i]}" stroke-width="${WIDTH}"`
      + ` stroke-dasharray="${len.toFixed(2)} ${(C - len).toFixed(2)}" stroke-dashoffset="${(-acc).toFixed(2)}"/>`;
    acc += len;
    return arc;
  }).join("");
  const svg = `<svg class="donut" viewBox="0 0 160 160" width="140" height="140">${arcs}</svg>`;
  return { svg, colors };
}

/** Підказка під курсором для графіків, намальованих seriesChart.
 *
 *  wrap — контейнер, у якому лежать <svg> і <div class="chart-tip">.
 *  buildHTML(i) повертає вміст підказки для дати з індексом i: сам
 *  charts.js даних не знає й знати не мусить, його справа — влучання й
 *  позиціювання.
 *
 *  Перший інтерактивний елемент у графіках застосунку. Нативного <title>
 *  тут замало: він показує один рядок, зʼявляється з секундною затримкою
 *  і не вміє показати ВСІ серії за день — а питання до кривої саме таке. */
export function wireChartTips(wrap, buildHTML) {
  if (!wrap) return;
  const tip = wrap.querySelector(".chart-tip");
  const svg = wrap.querySelector("svg");
  if (!tip || !svg) return;

  const hide = () => tip.classList.remove("show");
  wrap.addEventListener("mouseleave", hide);
  // Прокрутка зсуває графік під курсором, а підказка лишалась би висіти
  // біля чужої дати.
  wrap.addEventListener("scroll", hide);

  const show = (hit) => {
    const html = buildHTML(+hit.dataset.i);
    if (!html) return;
    tip.innerHTML = html;
    tip.classList.add("show");
    // Рахуємо від контейнера, а не від viewBox: SVG масштабується під
    // ширину картки, тож координати всередині нього до пікселів сторінки
    // не сходяться.
    const hb = hit.getBoundingClientRect(), wb = wrap.getBoundingClientRect();
    const cx = hb.left - wb.left + hb.width / 2 + wrap.scrollLeft;
    const half = tip.offsetWidth / 2;
    const max = wrap.scrollWidth - tip.offsetWidth - 4;
    tip.style.left = Math.max(4, Math.min(cx - half, max)) + "px";
  };

  wrap.querySelectorAll(".chart-hit").forEach((hit) => {
    hit.addEventListener("mouseenter", () => show(hit));
    // Клавіатура теж. Смуги влучання були mouseenter-only, тобто крива —
    // єдиний блок застосунку, який без миші не читався взагалі: підказка
    // й є той спосіб дізнатися числа за день.
    hit.addEventListener("focus", () => show(hit));
    hit.addEventListener("blur", hide);
  });
}

/** «Приємне» число з ряду 1 / 2 / 2.5 / 5 × 10^k, не менше за v.
 *
 *  Доти було просто max × 1.1, поділене на чотири, — і підписи виходили
 *  на кшталт «247к». Такі числа нічого не якорять: щоб прикинути висоту
 *  точки, доводиться рахувати в голові.
 *
 *  Служить одразу двом: верхньою межею осі, що росте від нуля, і кроком
 *  поділки на осі, що від нуля не росте. Арифметика та сама, тож другої
 *  копії їй не заводимо — це та сама функція під двома питаннями. */
function niceStep(v) {
  if (!(v > 0)) return 1;
  const pow = Math.pow(10, Math.floor(Math.log10(v)));
  const norm = v / pow;
  const step = norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 2.5 ? 2.5 : norm <= 5 ? 5 : 10;
  return step * pow;
}

/** Межі осі Y разом із поділками: {lo, hi, ticks}.
 *
 *  zero: true — вісь росте від нуля, як було завжди. Так читаються СУМИ:
 *  стовпчик росте з підлоги, і «наскільки він високий» має сенс лише
 *  відносно нуля.
 *
 *  zero: false — вісь накриває ДАНІ. Без цього крива ціни не читалась
 *  зовсім: сертифікат ходить біля 1000 ₴, і вся зміна за два тижні — це
 *  0.9 % висоти полотна, тобто пласка лінія, притиснута до стелі. Питання
 *  до ціни завжди «як вона рухалась», а не «чи далеко вона від нуля».
 *
 *  Ціна цього рішення чесно велика: вісь, що не починається з нуля,
 *  розтягує рух 0.9 % на все полотно, і крива виглядає крутішою, ніж є.
 *  Саме тому axisY нижче не опційна — з підписами поділок шкала себе
 *  називає, без них вона бреше. Друге страхування — підпис зі зміною у
 *  відсотках поруч із полотном (див. dateCurve).
 *
 *  Межі й крок повертаються разом, бо рахуються з одного числа: якби
 *  викликач добирав поділки сам, вони розʼїхались би з межами на першому
 *  ж заокругленні. */
function niceScale(values, { zero = false } = {}) {
  const nums = values.filter((v) => v != null && isFinite(v));
  let lo = nums.length ? Math.min(...nums) : 0;
  let hi = nums.length ? Math.max(...nums) : 1;
  if (zero) {
    // Рівно те, що робила niceMax: нуль унизу, «приємний» максимум угорі,
    // чотири однакові проміжки між ними.
    hi = niceStep(Math.max(hi, 0));
    const s = hi / 4;
    return { lo: 0, hi, ticks: [0, s, s * 2, s * 3, hi] };
  }
  // Рівна серія — не привід ділити на нуль: розсуваємо межі на відсоток
  // від самого значення, і лінія лягає рівно посередині полотна.
  if (hi <= lo) {
    const pad = Math.abs(hi) * 0.01 || 1;
    lo -= pad;
    hi += pad;
  }
  const step = niceStep((hi - lo) / 4);
  lo = Math.floor(lo / step) * step;
  hi = Math.ceil(hi / step) * step;
  const ticks = [];
  for (let i = 0, k = Math.round((hi - lo) / step); i <= k; i++) {
    // Множення, а не накопичення: lo + step + step + … набирає похибку, і
    // поділка «1002.4999999999998» приїхала б у підпис рівно такою.
    ticks.push(lo + step * i);
  }
  return { lo, hi, ticks };
}

/** Прив'язка підпису осі X. Крайні мітки центрувати не можна: підпис на
 *  краю полотна наполовину лежить за viewBox, а <svg> типово ріже все, що
 *  виходить за межі. Видно було на кривій ціни, де перша дата читалась як
 *  «-08-13», а остання — як «2026-0».
 *
 *  Логіка жила в svgInflowProfile, де на цю межу натрапили першою. */
function xAnchor(x, x0, x1, pad = 18) {
  return x < x0 + pad ? "start" : x > x1 - pad ? "end" : "middle";
}

/** Ширина числової колонки ліворуч — з найдовшого підпису поділки.
 *
 *  Константою це бути не може. 56 вистачало на «250к» і різало
 *  «1000.0000» рівно на 12 пікселів (знайдено вживу, на кривій ціни), а
 *  ЧВОПА друкує ще й шість знаків після коми. Запас під найдовший
 *  можливий підпис зʼїдав би полотно там, де підписи короткі.
 *
 *  Виміряти текст усередині рядка SVG нема чим — розмітка будується до
 *  того, як її побачить браузер, — тож ширина цифри береться оцінкою:
 *  в Inter на 11px вона ~0.56em. Кілька зайвих пікселів дешевші за
 *  обрізаний підпис, тож оцінка навмисно з запасом. */
function axisWidth(ticks, fmt) {
  const chars = Math.max(...ticks.map((v) => String(fmt(v)).length));
  return Math.round(chars * FS * 0.56) + 14;
}

/** Сітка й підписи осі Y — спільна розмітка для всіх кривих.
 *
 *  Стояла лише в seriesChart. svgLine і svgBandLine осі Y не мали взагалі,
 *  тобто малювали лінію в порожній рамці без жодного числа — і саме тому
 *  графік ціни читався як «нічого не показує»: показувати не було чим. */
function axisY(scale, x0, x1, Y, fmt) {
  let grid = "", labels = "";
  scale.ticks.forEach((v, i) => {
    const gy = Y(v);
    // Нижня лінія — межа полотна, тож --oi-border; проміжні над нею —
    // рядок від рядка, тобто --oi-rule-hair, найтихіший рівень.
    grid += `<line x1="${x0}" y1="${gy.toFixed(1)}" x2="${x1}" y2="${gy.toFixed(1)}"`
      + ` stroke="${i === 0 ? "var(--oi-border)" : GRID}" stroke-width="1"/>`;
    labels += `<text x="${x0 - 8}" y="${(gy + 4).toFixed(1)}" text-anchor="end"`
      + ` font-size="${FS}" fill="${AXIS}">${esc(fmt(v))}</text>`;
  });
  return grid + labels;
}

/** Великий графік історії портфеля.
 *
 *  series: [{name, color, values, dash?, area?}], dates — ISO-рядки.
 *  area: true — серія лягає СМУГОЮ поверх попередніх таких (накопичені
 *  області); решта малюється звичайними лініями в тому самому масштабі.
 *
 *  Навіщо смуги: лініями з нуля не прочитати ані суми, ані появи нового
 *  інструмента — фонди, куплені в середині історії, виглядали як лінія,
 *  що зʼявилась у повітрі. У смузі це просто новий шар, що росте від нуля,
 *  а верхня межа стосу — увесь капітал.
 *
 *  Повертає {svg, legend} окремо, а не одним рядком: підпис і полотно
 *  лягають у різні контейнери картки, і склеювати їх тут означало б
 *  вирішувати за викликача, як вони розташовані. */
export function seriesChart(dates, series, { width = 760, height = 300, minWidth = 520, label = "" } = {}) {
  const P = { l: 66, r: 14, t: 14, b: 40 };
  const iw = width - P.l - P.r, ih = height - P.t - P.b, n = dates.length;
  const areas = series.filter((s) => s.area);
  const lines = series.filter((s) => !s.area);

  // Стос рахуємо один раз: він потрібен і для масштабу, і для полігонів.
  // null у смузі — це нульова товщина, тобто шару в той день просто немає.
  const tops = [];
  areas.forEach((s, k) => {
    tops[k] = s.values.map((v, i) => (k ? tops[k - 1][i] : 0) + (v || 0));
  });
  const stackTop = tops.length ? tops[tops.length - 1] : [];

  // Масштаб і вісь — спільні (niceScale/axisY). Раніше та сама розмітка
  // стояла тут одна на весь застосунок, і решта кривих лишалась без осі
  // взагалі; тепер це один механізм, а zero: true каже, що капітал
  // справді росте від нуля.
  const scale = niceScale(
    stackTop.concat(...lines.map((s) => s.values)), { zero: true });

  const x = (i) => P.l + (n <= 1 ? iw / 2 : (iw * i) / (n - 1));
  const y = (v) => P.t + ih - (ih * (v - scale.lo)) / (scale.hi - scale.lo);

  const yaxis = axisY(scale, P.l, width - P.r, y, compact);

  // Останню дату підписуємо завжди: без неї з графіка не прочитати, станом
  // на коли він узагалі намальований.
  // Крок підписів залежить від ширини полотна: на вузькому екрані
  // дванадцять дат злипаються в сіру смугу, а п'ять читаються.
  let xlabels = "";
  const step = Math.max(1, Math.floor(n / (width < 560 ? 3 : 5)));
  const marks = new Set();
  for (let i = 0; i < n; i += step) marks.add(i);
  if (n) marks.add(n - 1);
  [...marks].sort((a, b) => a - b).forEach((i) => {
    xlabels += `<text x="${x(i).toFixed(1)}" y="${height - 14}" text-anchor="${xAnchor(x(i), P.l, width - P.r)}" font-size="11" fill="${AXIS}">${esc(dates[i].slice(5))}</text>`;
  });

  // Смуги: верх — накопичена сума до цього шару включно, низ — без нього.
  // Заливка кольором серії з прозорістю, щоб не заводити другий набір
  // токенів під кожен інструмент.
  const bands = areas.map((s, k) => {
    const top = tops[k].map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`);
    const bottom = (k ? tops[k - 1] : s.values.map(() => 0))
      .map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).reverse();
    return `<polygon points="${top.concat(bottom).join(" ")}" fill="${s.color}"`
      + ` fill-opacity="0.16" stroke="${s.color}" stroke-width="1.25"/>`;
  }).join("");

  const paths = lines.map((s) => {
    // null у значенні — це «тоді не рахували», а не нуль. Точку не
    // малюємо зовсім: намальований нуль читався б як «грошей не було», і
    // лінія злітала б угору в той день, коли з'явилась КОЛОНКА, а не
    // самі гроші. Так серія просто починається там, де є що показувати.
    const pts = s.values
      .map((v, i) => (v == null ? null : `${x(i).toFixed(1)},${y(v).toFixed(1)}`))
      .filter(Boolean).join(" ");
    return `<polyline points="${pts}" fill="none" stroke="${s.color}" stroke-width="1.5"`
      + `${s.dash ? ' stroke-dasharray="6 5"' : ""} stroke-linejoin="round"/>`;
  }).join("");

  // Зони наведення: по одному прозорому прямокутнику на дату, на всю
  // висоту. Влучити в них легко навіть там, де лінії злиплись, а розмітку
  // підказки будує вже той, хто знає дані (див. wireChartTips).
  let hits = "";
  if (n > 1) {
    const bw = iw / (n - 1);
    for (let i = 0; i < n; i++) {
      const hx = Math.max(P.l, x(i) - bw / 2);
      // tabindex — щоб до підказки можна було дійти з клавіатури; role і
      // aria-label — щоб дійшовши, було що почути.
      hits += `<rect class="chart-hit" data-i="${i}" tabindex="0" role="button"`
        + ` aria-label="${esc(dates[i])}" x="${hx.toFixed(1)}" y="${P.t}"`
        + ` width="${Math.min(bw, width - P.r - hx).toFixed(1)}" height="${ih}" fill="transparent"/>`;
    }
  }

  const legend = seriesLegend(series);

  // viewBox збігається з фактичною шириною рамки (її передає fitCharts),
  // тож коефіцієнт розтягу дорівнює одиниці й підписи мають рівно той
  // кегль, який тут написаний. minWidth лишається як запобіжник для
  // прямих викликів повз рамку.
  // Ширину й висоту малює CSS (.chart-frame > svg). Звідси приходить лише
  // ЗАПАСНИЙ поріг мінімальної ширини — він залежить від кількості точок,
  // тобто відомий тільки тут; --oi-chart-min поверх нього ставить
  // медіазапит, коли крива має вміститись у телефон цілком.
  const svg = `<svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="xMidYMid meet" role="img"`
    + ` aria-label="${esc(label || "Історія портфеля")}"`
    + ` style="--oi-chart-w:${minWidth}px">`
    + `<title>${esc(label || "Історія портфеля")}</title>`
    + `${yaxis}${xlabels}${bands}${paths}${hits}</svg>`;
  return { svg, legend };
}

/** Крива за датами: полотно, підказки й підпис під ним — одним викликом.
 *
 *  Ціна сертифіката фонду й ЧВОПА пенсійного рахунку — той самий обʼєкт:
 *  список пар «дата → число», який треба показати кривою й підписати
 *  діапазоном. Обидва викликачі писали це нарізно й однаково — те саме
 *  проріджування підписів, те саме «менше двох точок», той самий рядок
 *  діапазону. Другий випадок і є привід винести спільне.
 *
 *  points: [[isoДата, число], …] у порядку зростання дати.
 *  fmt(v) — як показувати число: у ціни чотири знаки, у ЧВОПА шість.
 *  empty — що написати, коли кривої ще немає.
 *  caption — готовий рядок під діапазоном (зміна у відсотках приходить
 *  порахованою з БЕКЕНДА; тут її не рахують — CLAUDE.md §5).
 *
 *  fluid(), а не голий svgLine. Доти обидва викликачі віддавали SVG
 *  напряму, бо «панель живе у згорнутому <details>, а fitCharts міряє
 *  згорнуте як нуль». Це давно неправда: fitCharts переносить запис у
 *  mounted ДО виходу по нульовій ширині (саме заради цього випадку), а
 *  app.js вішає toggle на кожен <details> і домальовує при розкритті.
 *  Ціна старого обходу була висока: viewBox лишався 640 при фактичній
 *  ширині картки, тобто полотно розтягувалось у півтора раза РАЗОМ із
 *  текстом — підписи приїжджали ~17px, а рамка виростала до ~255px. */
export function dateCurve(points, {
  color, fmt, unit = "", label, empty, caption = "",
}) {
  if (points.length < 2) return `<div class="sub muted">${empty}</div>`;
  const series = [{ name: label, color, values: points.map((p) => p[1]) }];
  // Рік на осі не потрібен і не влазить: він стоїть у рядку діапазону
  // нижче, а на самій осі важать день і місяць.
  const labels = points.map((p) => p[0].slice(5));
  const val = (v) => `${fmt(v)}${unit ? " " + unit : ""}`;
  const first = points[0], last = points[points.length - 1];
  const frame = fluid(
    (w, h) => svgLine(labels, series, { W: w, H: h, fmt, label }),
    {
      onMount: (box) => wireChartTips(box.closest(".chart-wrap"), (i) => points[i]
        && `<b>${esc(points[i][0])}</b><div class="r"><span><i style="--oi-c:${color}"></i>${
          esc(label)}</span><span>${esc(val(points[i][1]))}</span></div>`),
    });
  return `<div class="chart-wrap">${frame}<div class="chart-tip"></div></div>
    <div class="sub-xs muted">${esc(first[0])} → ${esc(last[0])} · ${esc(fmt(first[1]))} → ${
      esc(val(last[1]))} · точок: ${points.length}${caption}</div>`;
}
