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

/** Великий графік історії портфеля («Як росте»).
 *  series: [{name, color, values, dash?}], dates — ISO-рядки.
 *  Повертає {svg, legend} окремо: веб малює їх у два вже наявні
 *  контейнери, панель складає в одну картку. */
export function seriesChart(dates, series, { width = 760, height = 300, minWidth = 520 } = {}) {
  const P = { l: 66, r: 14, t: 14, b: 40 };
  const iw = width - P.l - P.r, ih = height - P.t - P.b, n = dates.length;
  let ymax = 0;
  series.forEach((s) => s.values.forEach((v) => { if (v > ymax) ymax = v; }));
  ymax = ymax > 0 ? ymax * 1.1 : 1;
  const x = (i) => P.l + (n <= 1 ? iw / 2 : (iw * i) / (n - 1));
  const y = (v) => P.t + ih - (ih * v) / ymax;

  let grid = "", ylabels = "";
  for (let r = 0; r <= 4; r++) {
    const gv = (ymax * r) / 4, gy = y(gv);
    grid += `<line x1="${P.l}" y1="${gy.toFixed(1)}" x2="${width - P.r}" y2="${gy.toFixed(1)}" stroke="${GRID}" stroke-width="1"/>`;
    ylabels += `<text x="${P.l - 8}" y="${(gy + 4).toFixed(1)}" text-anchor="end" font-size="11" fill="${AXIS}">${compact(gv)}</text>`;
  }
  let xlabels = "";
  const step = Math.max(1, Math.floor(n / 5));
  for (let i = 0; i < n; i += step) {
    xlabels += `<text x="${x(i).toFixed(1)}" y="${height - 14}" text-anchor="middle" font-size="11" fill="${AXIS}">${esc(dates[i].slice(5))}</text>`;
  }
  const lines = series.map((s) => {
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

  const legend = series.map((s) =>
    `<span style="display:inline-flex;align-items:center;gap:6px;margin-right:16px;font-size:13px">`
    + `<span style="width:16px;height:3px;background:${s.color};display:inline-block"></span>${esc(s.name)}</span>`).join("");

  const svg = `<svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="xMidYMid meet"`
    + ` style="width:100%;min-width:${minWidth}px;height:auto">${grid}${ylabels}${xlabels}${lines}</svg>`;
  return { svg, legend };
}
