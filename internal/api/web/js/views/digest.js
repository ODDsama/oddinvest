// Що змінилось — і ЧОМУ саме.
//
// Питання щоденне, а відповіді доти не було ніде. «Як росте» показує
// криву за весь час, «Підсумок місяця» — один закритий місяць; між ними
// зяяло рівно те, з чого починається день: капітал зрушив, а від чого?
//
// СТОРІНКА, А НЕ СПОВІЩЕННЯ — рішення власника. Push вимагав би порогу
// «що варте того, щоб дзенькнути», тобто судження, якого застосунок не
// виносить ніде.
//
// НІЧОГО НЕ РАХУЄТЬСЯ ТУТ: усі числа приходять із /api/digest готовими,
// разом із причинами, чому якогось розділу немає. Таблицю «було → стало»
// малює той самий structureHTML, що й «Підсумок місяця» — обидві
// сторінки показують ті самі знімки, і друга розмітка тих самих статей
// рано чи пізно розійшлася б підписами.

import { esc, uah2 as fmtUAH, pct, plural , signedUAH2 as signed } from "../format.js";
import { infoBtn } from "../info.js";
import { tile, empty } from "../components.js";
import { opsGrid } from "../grid.js";
import { structureHTML } from "./period.js";

const WINDOWS = [
  { v: 1, t: "день" },
  { v: 7, t: "тиждень" },
  { v: 30, t: "30 днів" },
];

const KEY = "oi.digest.window";

function chosenWindow() {
  try {
    const v = Number(localStorage.getItem(KEY));
    return WINDOWS.some((w) => w.v === v) ? v : 7;
  } catch (_) { return 7; }
}

/** Скільки днів МІЖ ЗНІМКАМИ насправді.
 *
 *  Не те саме, що обране вікно, і саме тому показується окремо: демон міг
 *  лежати, знімка за потрібний день може не бути, і тоді «день» на кнопці
 *  накриває три доби. Дати поруч це вже кажуть, але число позбавляє
 *  необхідності віднімати їх очима. */
function spanDays(d) {
  const a = Date.parse(d.from_date), b = Date.parse(d.to_date);
  if (!a || !b) return d.days;
  return Math.max(1, Math.round((b - a) / 86400000));
}


function headHTML(days) {
  const btn = (w) => `<button data-digwin="${w.v}" aria-pressed="${days === w.v}">${w.t}</button>`;
  return `<h2 class="card-head">
    <span>Що змінилось ${infoBtn("digest")}</span>
    <span class="seg">${WINDOWS.map(btn).join("")}</span></h2>`;
}

/** Причини — таблиця, а не плитки: їх чотири, вони одного роду й
 *  СКЛАДАЮТЬСЯ в різницю. Плитки читались би як чотири незалежні числа. */
function causesHTML(d) {
  return `<div class="card">
    ${headHTML(d.days)}
    <div class="tiles flush mb">
      ${tile("Зміна капіталу", signed(d.delta_uah),
    `<div class="sub">${esc(d.from_date || "")} → ${esc(d.to_date || "")} ·
      ${spanDays(d)} ${plural(spanDays(d), "день", "дні", "днів")}${
  d.delta_pct ? ` · ${pct(d.delta_pct)}` : ""}</div>`)}
      ${tile("Було", fmtUAH(d.from_uah || 0))}
      ${tile("Стало", fmtUAH(d.to_uah || 0))}
    </div>
    ${opsGrid({
    cols: [
      { key: "label", label: "Від чого", cell: (c) => esc(c.label) },
      { key: "uah", label: "Скільки", num: true, cell: (c) => signed(c.uah) },
      { key: "why", label: "Звідки число", cls: "muted sub-xs",
        cell: (c) => `${c.measured ? "виміряно" : "обчислено"} — ${esc(c.why || "")}` },
    ],
    rows: d.causes || [],
    caption: "Причини зміни капіталу: стаття, сума, звідки число",
    foot: [
      { cell: "Разом" },
      { cell: signed(d.delta_uah), num: true },
      { cell: "" },
    ],
  })}
    <div class="sub">Перші дві статті <b>виміряні</b>: вони проходять журналом, і в
      кожної є дата. Курс <b>обчислений</b> за нинішнім валютним обсягом — обсягу на
      початок вікна застосунок не зберігає. «Решта» так і зветься решткою: подобового
      джерела під цінами фондів і ЧВОПА немає, і розкладати її означало б вигадувати.</div>
  </div>`;
}

export async function digest(ctx, main) {
  const days = chosenWindow();
  const d = await ctx.soft(`digest?window=${days}`, null);
  if (!d) {
    main.innerHTML = `<div class="card"><h2>Що змінилось</h2>
      ${empty("", "бекенд не віддав розкладу")}</div>`;
    return;
  }
  main.innerHTML = d.structure
    ? `${causesHTML(d)}${structureHTML(d)}`
    : `<div class="card">${headHTML(d.days)}
        ${empty("", esc(d.why || "порівнювати немає з чим"))}</div>`;
  main.querySelectorAll("[data-digwin]").forEach((b) =>
    b.addEventListener("click", () => {
      try { localStorage.setItem(KEY, b.dataset.digwin); } catch (_) { /* приватне вікно */ }
      ctx.reload();
    }));
}
