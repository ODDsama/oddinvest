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
    // Нуль означає, що рамка ще не в розкладці (згорнута секція,
    // display:none). Малювати нема під що — домалюємо, коли розгорнуть.
    if (!w) return;
    mounted.set(box, rec);
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

/** Згруповані пари стовпчиків (факт vs ціль). groups: [{label, a, b}] */
export function svgGrouped(groups, { W = W0, H = H0 } = {}) {
  const Pl = 6, Pr = 6, Pt = 14, Pb = 30;
  const iw = W - Pl - Pr, ih = H - Pt - Pb, n = Math.max(1, groups.length);
  const max = Math.max(1, ...groups.flatMap((g) => [g.a, g.b]));
  const gap = iw / n, bw = Math.min(22, gap * 0.28);
  let out = "";
  groups.forEach((g, i) => {
    const cx = Pl + gap * i + gap / 2;
    [[g.a, "var(--oi-series-invested)", -bw - 2], [g.b, "var(--oi-series-neutral)", 2]].forEach(([v, col, dx]) => {
      const h = (v / max) * ih, x = cx + dx, y = Pt + ih - h;
      out += `<rect x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${bw}" height="${Math.max(0, h).toFixed(1)}"`
        + ` fill="${col}"><title>${v.toFixed(1)}%</title></rect>`;
    });
    out += `<text x="${cx.toFixed(1)}" y="${H - 10}" text-anchor="middle" font-size="${FS}" fill="${AXIS}">${esc(g.label)}</text>`;
  });
  return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet">${out}</svg>`;
}

/** Лінійний графік із кількома серіями по спільних x-мітках.
 *  series: [{color, values}] */
export function svgLine(xlabels, series, { W = W0, H = H0 } = {}) {
  const Pl = 8, Pr = 8, Pt = 14, Pb = 28;
  const iw = W - Pl - Pr, ih = H - Pt - Pb, n = Math.max(1, xlabels.length);
  const max = Math.max(1, ...series.flatMap((s) => s.values));
  const X = (i) => Pl + (n <= 1 ? iw / 2 : (iw * i) / (n - 1));
  const Y = (v) => Pt + ih - (v / max) * ih;
  const lines = series.map((s) =>
    `<polyline points="${s.values.map((v, i) => `${X(i).toFixed(1)},${Y(v).toFixed(1)}`).join(" ")}"`
    + ` fill="none" stroke="${s.color}" stroke-width="1.5" stroke-linejoin="round"/>`).join("");
  const xl = xlabels.map((l, i) =>
    `<text x="${X(i).toFixed(1)}" y="${H - 10}" text-anchor="middle" font-size="${FS}" fill="${AXIS}">${esc(l)}</text>`).join("");
  return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet">${lines}${xl}</svg>`;
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
  const Pl = 8, Pr = 8, Pt = 14, Pb = 28;
  const iw = W - Pl - Pr, ih = H - Pt - Pb;
  const n = Math.max(1, xlabels.length);
  // Ціль входить у масштаб: інакше лінія цілі вилітала б за полотно, і
  // «не дотягуємо» виглядало б як «дотягуємо».
  const all = lines.flatMap((s) => s.values).concat(bands.hi || [], [goal || 0]);
  const max = Math.max(1, ...all);
  const X = (i) => Pl + (n <= 1 ? iw / 2 : (iw * i) / (n - 1));
  const Y = (v) => Pt + ih - (v / max) * ih;
  let out = "";
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
    ? `<text x="${X(i).toFixed(1)}" y="${H - 10}" text-anchor="middle" font-size="${FS}" fill="${AXIS}">${esc(l)}</text>`
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
  const Pl = 46, Pr = 10, Pt = 12, Pb = 26;
  const iw = W - Pl - Pr, ih = H - Pt - Pb;
  const X = (iso) => Pl + ((day(iso) - d0) / (d1 - d0)) * iw;

  // Масштаб охоплює і плюс, і мінус: витратний потік малюється вниз, і
  // нуль мусить стояти там, де він справді нуль, а не внизу полотна.
  let hi = 0, lo = 0;
  for (const p of pts) {
    let up = 0, dn = 0;
    p.values.forEach((v) => { if (v >= 0) up += v; else dn += v; });
    hi = Math.max(hi, up, p.net);
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

  const net = `<polyline points="${pts.map((p) => `${X(p.date).toFixed(1)},${Y(p.net).toFixed(1)}`).join(" ")}"
    fill="none" stroke="var(--oi-series-invested)" stroke-width="1.5" stroke-linejoin="round"/>`;

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

  return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" role="img"
    aria-label="Профіль надходжень плану, гривень на місяць">`
    + `${yTicks}${xlabels}${areas}${net}${marks}${acts}</svg>`;
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

/** «Приємна» верхня межа осі: 1 / 2 / 2.5 / 5 × 10^k.
 *
 *  Доти було просто max × 1.1, поділене на чотири, — і підписи виходили
 *  на кшталт «247к». Такі числа нічого не якорять: щоб прикинути висоту
 *  точки, доводиться рахувати в голові. */
function niceMax(v) {
  if (!(v > 0)) return 1;
  const pow = Math.pow(10, Math.floor(Math.log10(v)));
  const norm = v / pow;
  const step = norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 2.5 ? 2.5 : norm <= 5 ? 5 : 10;
  return step * pow;
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

  let ymax = 0;
  stackTop.forEach((v) => { if (v > ymax) ymax = v; });
  lines.forEach((s) => s.values.forEach((v) => { if (v > ymax) ymax = v; }));
  ymax = niceMax(ymax);

  const x = (i) => P.l + (n <= 1 ? iw / 2 : (iw * i) / (n - 1));
  const y = (v) => P.t + ih - (ih * v) / ymax;

  let grid = "", ylabels = "";
  for (let r = 0; r <= 4; r++) {
    const gv = (ymax * r) / 4, gy = y(gv);
    // Базова лінія (r=0, нуль осі) — межа полотна, тож лишається на
    // --oi-border; чотири проміжні лінії над нею — рядок від рядка, тобто
    // --oi-rule-hair, найтихіший рівень.
    grid += `<line x1="${P.l}" y1="${gy.toFixed(1)}" x2="${width - P.r}" y2="${gy.toFixed(1)}" stroke="${r === 0 ? "var(--oi-border)" : GRID}" stroke-width="1"/>`;
    ylabels += `<text x="${P.l - 8}" y="${(gy + 4).toFixed(1)}" text-anchor="end" font-size="11" fill="${AXIS}">${compact(gv)}</text>`;
  }

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
    xlabels += `<text x="${x(i).toFixed(1)}" y="${height - 14}" text-anchor="middle" font-size="11" fill="${AXIS}">${esc(dates[i].slice(5))}</text>`;
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
    + `${grid}${ylabels}${xlabels}${bands}${paths}${hits}</svg>`;
  return { svg, legend };
}
