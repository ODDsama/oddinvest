// Рік у цифрах — підсумок місяця, розтягнутий на рік і доповнений тим,
// чого місяць не має.
//
// НІЧОГО НЕ РАХУЄТЬСЯ ТУТ, і сторінка складена з ЧОТИРЬОХ готових
// відповідей: /api/year (гроші, дні, місяці, «було → стало», рішення),
// /api/tax (річний податок із курсом на дату події), /api/progress
// (віхи, датовані цим роком, і серія проти долара). Складати їх на
// клієнті — законно: це композиція, а не арифметика (CLAUDE.md §5).
// Рахувати податок чи віхи вдруге в бекенді заради однієї сторінки
// означало б другий примірник тих самих чисел.
//
// Картки «було → стало» й «рішення» — ті самі функції, що на «Підсумку
// місяця»: обидві сторінки показують ті самі рядки за різні вікна, і
// друга розмітка розійшлась би підписами.

import { esc, uah2 as fmtUAH, uah0, signedUAH, pct, monthYear, plural } from "../format.js";
import { infoBtn } from "../info.js";
import { tile, empty } from "../components.js";
import { opsGrid } from "../grid.js";
import { structureHTML, decisionsHTML } from "./period.js";

const YEAR_KEY = "oi.year";

function chosenYear() {
  try { return localStorage.getItem(YEAR_KEY) || ""; } catch (_) { return ""; }
}

/** Гроші зі знаком; рівний нуль — прочерк (довід у period.js). */
const signed = (v) => (!v ? "—" : signedUAH(v));

function headHTML(y) {
  const btn = (v) =>
    `<button data-year="${v}" aria-pressed="${y.year === v}">${v}</button>`;
  return `<h2 class="card-head">
    <span>${y.year}${y.partial ? " · поки що" : ""} ${infoBtn("year")}</span>
    <span class="seg">${(y.years || [y.year]).map(btn).join("")}</span></h2>`;
}

function tilesHTML(y) {
  const cap = (y.structure && y.structure.rows || []).find((r) => r.key === "capital");
  const best = y.best_month;
  return `<div class="tiles flush">
    ${cap
    ? tile("Капітал на кінець", fmtUAH(cap.after),
      `<div class="sub">${signed(cap.delta)} за рік</div>`)
    : tile("Капітал на кінець", "—",
      `<div class="sub">${esc(y.structure_note || "знімків за цей рік немає")}</div>`)}
    ${tile("Зароблено", fmtUAH(y.earned_uah),
    y.principal_uah > 0
      ? `<div class="sub">і ${fmtUAH(y.principal_uah)} повернуто тіла</div>`
      : `<div class="sub">купони, дивіденди, відсотки</div>`)}
    ${tile("Внесено своїх", fmtUAH(y.money.own_uah != null ? y.money.own_uah : y.money.contributed_uah),
    (y.money.outside_uah
      ? `<div class="sub">з них ${signed(y.money.outside_uah)} у подушку й цілі</div>` : "")
    + (y.idle_uah > 0
      ? `<div class="sub">${fmtUAH(y.idle_uah)} доходу не пішло в діло</div>` : ""))}
    ${best
    ? tile("Найкращий місяць", esc(monthYear(best.month + "-01")),
      `<div class="sub">внесено ${fmtUAH(best.contributed_uah)}</div>`)
    : tile("Найкращий місяць", "—", `<div class="sub">закритих місяців ще немає</div>`)}
  </div>`;
}

/** Хітмап днів: тиждень — колонка, понеділок зверху. Порожні клітинки до
 *  першого дня року невидимі, але місце тримають. Експортується для
 *  «Звички», де стоїть за поточний рік. */
export function heatmapHTML(y) {
  const byDay = new Map((y.days || []).map((d) => [d.date, d]));
  const iso = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${
    String(d.getDate()).padStart(2, "0")}`;
  const start = new Date(y.from + "T00:00:00");
  const end = new Date(y.to + "T00:00:00");
  const cells = [];
  for (let i = (start.getDay() + 6) % 7; i > 0; i--) cells.push(`<span class="heat-c pad"></span>`);
  for (const d = new Date(start); d <= end; d.setDate(d.getDate() + 1)) {
    const k = iso(d);
    const v = byDay.get(k);
    const what = v ? [
      v.contributed_uah ? `внесено ${signedUAH(v.contributed_uah)}` : "",
      v.income_uah ? `дохід ${uah0(v.income_uah)}` : "",
      v.purchased_uah ? `покупки ${uah0(v.purchased_uah)}` : "",
    ].filter(Boolean).join(", ") : "без руху";
    cells.push(`<span class="heat-c" data-lvl="${v ? v.lvl : 0}" title="${esc(`${k} · ${what}`)}"></span>`);
  }
  const n = (y.days || []).length;
  return `<div class="heat" role="presentation">${cells.join("")}</div>
    <div class="strip-x"><span>${esc(y.from)}</span><span>${esc(y.to)}</span></div>
    <div class="sub-xs">${n} ${plural(n, "день", "дні", "днів")} з рухом грошей; насиченість — за квартилями серед них</div>`;
}

function monthsHTML(y) {
  if (!y.months || !y.months.length) {
    return `<div class="card"><h2>Місяць за місяцем</h2>
      ${empty("", "Закритих місяців із ціллю цього року ще немає.")}</div>`;
  }
  const hit = y.months.filter((m) => m.known && m.hit).length;
  const known = y.months.filter((m) => m.known).length;
  return `<div class="card">
    <h2 class="card-head"><span>Місяць за місяцем</span>
      <span class="sub-xs">${known ? `${hit} з ${known} за планом` : "цілей не було"}</span></h2>
    ${opsGrid({
    cols: [
      { key: "month", label: "Місяць", cell: (m) => esc(monthYear(m.month + "-01")) },
      { key: "contrib", label: "Внесено", num: true, cell: (m) => fmtUAH(m.contributed_uah) },
      { key: "target", label: "Ціль", num: true, prio: 3,
        cell: (m) => (m.known ? fmtUAH(m.target_uah) : "—") },
      { key: "income", label: "Дохід", num: true, prio: 3, cell: (m) => fmtUAH(m.income_uah) },
      { key: "hit", label: "За планом", cell: (m) => (!m.known ? "невідомо" : m.hit ? "так" : "повз") },
    ],
    rows: y.months,
    caption: "Місяці року: внесено, ціль того місяця, дохід, чи виконано план",
  })}</div>`;
}

/** Річний податок — із /api/tax, готовими рядками. */
function taxHTML(tax) {
  if (!tax) return "";
  const rows = tax.by_kind || [];
  if (!rows.length) {
    return `<div class="card"><h2>Податок за рік</h2>
      ${empty("", "Оподатковуваного доходу цього року не було.")}</div>`;
  }
  return `<div class="card">
    <h2 class="card-head"><span>Податок за рік</span>
      <span class="sub-xs">${pct(tax.rate_pct)} з ${fmtUAH(tax.gross_uah)}</span></h2>
    ${opsGrid({
    cols: [
      { key: "label", label: "Що", cell: (r) => esc(r.label) },
      { key: "gross", label: "Брутто", num: true, cell: (r) => fmtUAH(r.gross_uah) },
      { key: "tax", label: "Податок", num: true, cell: (r) => fmtUAH(r.tax_uah) },
      { key: "net", label: "Чистими", num: true, prio: 3, cell: (r) => fmtUAH(r.net_uah) },
    ],
    rows,
    caption: "Податок за рік по видах доходу: брутто, податок, чистими",
  })}
    <div class="sub-xs">${esc(tax.fx_basis || "")}${tax.note ? ` · ${esc(tax.note)}` : ""}</div>
  </div>`;
}

/** Віхи, датовані цим роком, і серія проти долара — зі «Шляху». */
function pathHTML(pr, year) {
  if (!pr) return "";
  const y = String(year);
  const done = (pr.milestones || []).filter((m) => m.earned && m.earned_on && m.earned_on.startsWith(y))
    .sort((a, b) => (a.earned_on < b.earned_on ? -1 : 1));
  const vs = pr.vs_usd;
  const marks = vs ? (vs.marks || []).filter((m) => m.month.startsWith(y)) : [];
  const ahead = marks.filter((m) => m.ahead).length;
  const vsLine = marks.length
    ? `<div class="sub">Попереду «просто доларів» ${ahead} з ${marks.length} ${
      plural(marks.length, "місяця", "місяців", "місяців")}</div>` : "";
  return `<div class="card">
    <h2 class="card-head"><span>Віхи року</span><span class="sub-xs">${done.length}</span></h2>
    ${done.length ? `<div class="tl">${done.map((m) => `<div class="tl-row">
        <span class="tl-d">${esc(m.earned_on)}</span><span class="tl-n">${esc(m.title)}</span>
      </div>`).join("")}</div>`
    : `<div class="sub">Датованих віх цього року немає.</div>`}
    ${vsLine}
  </div>`;
}

export async function year(ctx, main) {
  const chosen = chosenYear();
  const y = await ctx.soft("year" + (chosen ? `?year=${chosen}` : ""), null);
  if (!y) {
    main.innerHTML = `<div class="card"><h2>Рік у цифрах</h2>
      ${empty("", "Бекенд не віддав рік. Спробуй оновити сторінку.")}</div>`;
    return;
  }
  const [tax, pr] = await Promise.all([
    ctx.soft(`tax?year=${y.year}`, null),
    ctx.soft("progress", null),
  ]);
  main.innerHTML = `<div class="card">${headHTML(y)}${tilesHTML(y)}</div>
    <div class="card"><h2>Дні з рухом грошей</h2>${heatmapHTML(y)}</div>
    ${monthsHTML(y)}
    ${structureHTML({ structure: y.structure, structure_note: y.structure_note })}
    ${taxHTML(tax)}
    ${pathHTML(pr, y.year)}
    ${decisionsHTML({ decisions: y.decisions }, "Рішення року")}`;
  main.querySelectorAll("[data-year]").forEach((b) =>
    b.addEventListener("click", () => {
      try { localStorage.setItem(YEAR_KEY, b.dataset.year); } catch (_) {}
      ctx.reload();
    }));
}
