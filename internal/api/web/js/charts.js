// Маленькі SVG-графіки без бібліотек.
//
// Малюємо руками з двох причин: усередині shadow DOM панелі більшість
// чартових бібліотек ламається на стилях, а сам застосунок ставиться в
// LXC без збірки — тягнути пакет заради п'яти стовпчиків невигідно.
//
// Кольори — ТІЛЬКИ токени. Раніше та сама функція svgBars у вебі мала
// fill="#8b949e", а в панелі fill="var(--secondary-text-color)": один
// код, дві палітри, і на світлій темі HA веб-версія кольорів була б
// невидимою. Тепер колір вирішує тема, а не функція.

import { esc, compact } from "./format.js";

/** Палітра категорій без власного значення (сегменти кільця брокерів):
 *  тут потрібна саме відрізнюваність, а не сенс. */
export const CAT_COLORS = [
  "var(--oi-cat-1)", "var(--oi-cat-2)", "var(--oi-cat-3)", "var(--oi-cat-4)",
  "var(--oi-cat-5)", "var(--oi-cat-6)", "var(--oi-cat-7)",
];

const AXIS = "var(--oi-muted)";
const GRID = "var(--oi-border)";

/** Стовпчики. items: [{label, value, color?}]. showVals — підпис суми зверху. */
export function svgBars(items, { showVals = false } = {}) {
  const W = 320, H = 170, Pl = 6, Pr = 6, Pt = 18, Pb = 30;
  const iw = W - Pl - Pr, ih = H - Pt - Pb, n = Math.max(1, items.length);
  const max = Math.max(1, ...items.map((i) => i.value));
  const gap = iw / n, bw = Math.min(46, gap * 0.62);
  let out = "";
  items.forEach((it, i) => {
    const h = (it.value / max) * ih, x = Pl + gap * i + (gap - bw) / 2, y = Pt + ih - h;
    out += `<rect x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${bw.toFixed(1)}" height="${Math.max(0, h).toFixed(1)}"`
      + ` rx="2" fill="${it.color || "var(--oi-series-invested)"}">`
      + `<title>${esc(it.label)}: ${Math.round(it.value).toLocaleString("uk")} ₴</title></rect>`;
    out += `<text x="${(x + bw / 2).toFixed(1)}" y="${H - 10}" text-anchor="middle" font-size="10" fill="${AXIS}">${esc(it.label)}</text>`;
    if (showVals && it.value > 0) {
      out += `<text x="${(x + bw / 2).toFixed(1)}" y="${(y - 4).toFixed(1)}" text-anchor="middle" font-size="9" fill="${AXIS}">${compact(it.value)}</text>`;
    }
  });
  return `<svg viewBox="0 0 ${W} ${H}" style="width:100%;height:auto">${out}</svg>`;
}

/** Згруповані пари стовпчиків (факт vs ціль). groups: [{label, a, b}] */
export function svgGrouped(groups) {
  const W = 320, H = 170, Pl = 6, Pr = 6, Pt = 14, Pb = 30;
  const iw = W - Pl - Pr, ih = H - Pt - Pb, n = Math.max(1, groups.length);
  const max = Math.max(1, ...groups.flatMap((g) => [g.a, g.b]));
  const gap = iw / n, bw = Math.min(22, gap * 0.28);
  let out = "";
  groups.forEach((g, i) => {
    const cx = Pl + gap * i + gap / 2;
    [[g.a, "var(--oi-series-invested)", -bw - 2], [g.b, "var(--oi-series-neutral)", 2]].forEach(([v, col, dx]) => {
      const h = (v / max) * ih, x = cx + dx, y = Pt + ih - h;
      out += `<rect x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${bw}" height="${Math.max(0, h).toFixed(1)}"`
        + ` rx="2" fill="${col}"><title>${v.toFixed(1)}%</title></rect>`;
    });
    out += `<text x="${cx.toFixed(1)}" y="${H - 10}" text-anchor="middle" font-size="10" fill="${AXIS}">${esc(g.label)}</text>`;
  });
  return `<svg viewBox="0 0 ${W} ${H}" style="width:100%;height:auto">${out}</svg>`;
}

/** Лінійний графік із кількома серіями по спільних x-мітках.
 *  series: [{color, values}] */
export function svgLine(xlabels, series) {
  const W = 320, H = 170, Pl = 8, Pr = 8, Pt = 14, Pb = 28;
  const iw = W - Pl - Pr, ih = H - Pt - Pb, n = Math.max(1, xlabels.length);
  const max = Math.max(1, ...series.flatMap((s) => s.values));
  const X = (i) => Pl + (n <= 1 ? iw / 2 : (iw * i) / (n - 1));
  const Y = (v) => Pt + ih - (v / max) * ih;
  const lines = series.map((s) =>
    `<polyline points="${s.values.map((v, i) => `${X(i).toFixed(1)},${Y(v).toFixed(1)}`).join(" ")}"`
    + ` fill="none" stroke="${s.color}" stroke-width="2.5" stroke-linejoin="round"/>`).join("");
  const xl = xlabels.map((l, i) =>
    `<text x="${X(i).toFixed(1)}" y="${H - 10}" text-anchor="middle" font-size="10" fill="${AXIS}">${esc(l)}</text>`).join("");
  return `<svg viewBox="0 0 ${W} ${H}" style="width:100%;height:auto">${lines}${xl}</svg>`;
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
export function svgBandLine(xlabels, bands, lines, goal) {
  const W = 320, H = 170, Pl = 8, Pr = 8, Pt = 14, Pb = 28;
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
    out += `<polygon points="${up.concat(down).join(" ")}" fill="var(--oi-series-invested)" opacity="0.12"/>`;
  }
  if (goal > 0) {
    const y = Y(goal).toFixed(1);
    out += `<line x1="${Pl}" y1="${y}" x2="${W - Pr}" y2="${y}" stroke="${AXIS}"`
      + ` stroke-width="1" stroke-dasharray="4 3"/>`;
  }
  out += lines.map((s) =>
    `<polyline points="${s.values.map((v, i) => `${X(i).toFixed(1)},${Y(v).toFixed(1)}`).join(" ")}"`
    + ` fill="none" stroke="${s.color}" stroke-width="2.5" stroke-linejoin="round"`
    + `${s.dash ? ` stroke-dasharray="${s.dash}"` : ""}/>`).join("");
  out += xlabels.map((l, i) => l
    ? `<text x="${X(i).toFixed(1)}" y="${H - 10}" text-anchor="middle" font-size="10" fill="${AXIS}">${esc(l)}</text>`
    : "").join("");
  return `<svg viewBox="0 0 ${W} ${H}" style="width:100%;height:auto">${out}</svg>`;
}

/** Кільце часток. parts: [{label, value}] — уже відсортовані.
 *  Повертає {svg, colors}: легенду малює той, хто кличе, бо підпис
 *  частки в різних місцях різний (сума, відсоток, і те й те). */
export function svgDonut(parts) {
  const R = 60, WIDTH = 22, C = 2 * Math.PI * R;
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
  const svg = `<svg viewBox="0 0 160 160" width="140" height="140" style="transform:rotate(-90deg);flex:0 0 auto">${arcs}</svg>`;
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

  wrap.querySelectorAll(".chart-hit").forEach((hit) => {
    hit.addEventListener("mouseenter", () => {
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
    });
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
 *  Повертає {svg, legend} окремо: веб малює їх у два вже наявні
 *  контейнери, панель складає в одну картку. */
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
    grid += `<line x1="${P.l}" y1="${gy.toFixed(1)}" x2="${width - P.r}" y2="${gy.toFixed(1)}" stroke="${GRID}" stroke-width="1"/>`;
    ylabels += `<text x="${P.l - 8}" y="${(gy + 4).toFixed(1)}" text-anchor="end" font-size="11" fill="${AXIS}">${compact(gv)}</text>`;
  }

  // Останню дату підписуємо завжди: без неї з графіка не прочитати, станом
  // на коли він узагалі намальований.
  let xlabels = "";
  const step = Math.max(1, Math.floor(n / 5));
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
      + ` fill-opacity="0.55" stroke="${s.color}" stroke-width="1"/>`;
  }).join("");

  const paths = lines.map((s) => {
    // null у значенні — це «тоді не рахували», а не нуль. Точку не
    // малюємо зовсім: намальований нуль читався б як «грошей не було», і
    // лінія злітала б угору в той день, коли з'явилась КОЛОНКА, а не
    // самі гроші. Так серія просто починається там, де є що показувати.
    const pts = s.values
      .map((v, i) => (v == null ? null : `${x(i).toFixed(1)},${y(v).toFixed(1)}`))
      .filter(Boolean).join(" ");
    return `<polyline points="${pts}" fill="none" stroke="${s.color}" stroke-width="2.5"`
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
      hits += `<rect class="chart-hit" data-i="${i}" x="${hx.toFixed(1)}" y="${P.t}"`
        + ` width="${Math.min(bw, width - P.r - hx).toFixed(1)}" height="${ih}" fill="transparent"/>`;
    }
  }

  const legend = series.map((s) =>
    `<span><i style="background:${s.color}"></i>${esc(s.name)}</span>`).join("");

  const svg = `<svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="xMidYMid meet" role="img"`
    + ` style="width:100%;min-width:${minWidth}px;height:auto">`
    + `<title>${esc(label || "Історія портфеля")}</title>`
    + `${grid}${ylabels}${xlabels}${bands}${paths}${hits}</svg>`;
  return { svg, legend };
}
