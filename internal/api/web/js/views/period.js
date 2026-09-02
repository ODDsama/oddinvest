// Підсумок місяця — єдина сторінка, що дивиться назад як на ціле.
//
// Розділ «Портфель» відповідав на «що я маю» п'ятьма кутами одного
// погляду, і всі вони — про СЬОГОДНІ. «Як росте» здається винятком, але
// вона про криву за весь час: питання «чим липень відрізнявся від червня»
// на ній треба вимірювати очима між двома точками.
//
// Тому сторінка стоїть одразу після «Як росте»: обидві дивляться назад,
// але крива — це весь час, а підсумок — один ЗАКРИТИЙ період.
//
// НІЧОГО НЕ РАХУЄТЬСЯ ТУТ. Усі числа приходять із /api/period готовими,
// разом із причинами, чому якогось розділу немає. Знак і абсолютне
// значення — оформлення напряму, як у виписці поруч.
//
// «Рух грошей» малює flowHTML із money-cards.js — той самий рендер, що на
// сторінці «Гроші → Рухи», і це навмисно: обидві сторінки показують ті
// самі п'ять сум за той самий місяць (бекенд рахує їх однією
// summarizeCash), тож і виглядати вони мусять однаково. Друга розмітка
// тих самих статей рано чи пізно розійшлася б підписами, і читач вирішив
// би, що розійшлись числа.

import { esc, uah2 as fmtUAH, pct, pp, monthYear, plural } from "../format.js";
import { infoBtn } from "../info.js";
import { tile, empty, kindPill } from "../components.js";
import { opsGrid } from "../grid.js";
import { flowHTML } from "./money-cards.js";

const MONTH_KEY = "oi.period.month";

/** Обраний місяць — «YYYY-MM» або порожньо (типово: минулий закритий).
 *
 *  У localStorage з тієї ж причини, що й вікно кривої поруч: вибір
 *  переживає перезавантаження сторінки, бо на неї повертаються дивитись
 *  той самий місяць. */
function chosenMonth() {
  try { return localStorage.getItem(MONTH_KEY) || ""; } catch (_) { return ""; }
}

/** Останні N закритих місяців, найновіший першим. */
function recentMonths(n) {
  const out = [];
  const d = new Date();
  for (let i = 1; i <= n; i++) {
    const m = new Date(d.getFullYear(), d.getMonth() - i, 1);
    out.push(`${m.getFullYear()}-${String(m.getMonth() + 1).padStart(2, "0")}`);
  }
  return out;
}

/** Гроші зі знаком. Рівний нуль — прочерк: «+0,00 ₴» стверджує зміну,
 *  якої не було, а рядок стоїть у таблиці саме через інші свої колонки. */
const signed = (v) => (!v ? "—" : (v > 0 ? "+" : "−") + fmtUAH(Math.abs(v)));

// Шапка називає САМ ПЕРІОД, а не сторінку: підпис «Підсумок місяця» вже
// стоїть над нею від оболонки, і другий такий заголовок читався б як два
// різні блоки з однією назвою.
function headHTML(month) {
  const months = recentMonths(6);
  const active = month || months[0];
  const btn = (v) =>
    `<button data-month="${v}" aria-pressed="${active === v}">${esc(monthYear(v + "-01"))}</button>`;
  return `<h2 class="card-head">
    <span>${esc(monthYear(active + "-01"))} ${infoBtn("period")}</span>
    <span class="seg">${months.map(btn).join("")}</span></h2>`;
}

/** Плитки: чим місяць закінчився і що його зрушило. */
function tilesHTML(p) {
  const cap = (p.structure && p.structure.rows || []).find((r) => r.key === "capital");
  const idle = p.idle_uah > 0
    ? `<div class="sub">${fmtUAH(p.idle_uah)} з доходу до кінця місяця не пішло в діло</div>` : "";
  return `<div class="tiles flush">
    ${cap
    ? tile("Капітал на кінець", fmtUAH(cap.after), `<div class="sub">${signed(cap.delta)} за місяць</div>`)
    : tile("Капітал на кінець", "—", "<div class=\"sub\">знімків за цей місяць немає</div>")}
    ${tile("Надійшло доходу", fmtUAH(p.money.income_uah), idle)}
    ${tile("Внесено своїх", fmtUAH(p.money.own_uah != null ? p.money.own_uah : p.money.contributed_uah),
    (p.plan ? `<div class="sub">${pct(p.plan.done_pct, 0)} від цілі ${fmtUAH(p.plan.target_uah)}</div>`
      : `<div class="sub">${esc(p.plan_note || "")}</div>`)
    // Подушка й цілі — тим самим рядком, що на «Огляді»: без нього місяць,
    // у якому гроші пішли в матрац повз гаманець, читався б як зрив.
    + (p.money.outside_uah
      ? `<div class="sub">з них ${signed(p.money.outside_uah)} у подушку й цілі, ${
        fmtUAH(p.money.contributed_uah)} на рахунки</div>` : ""))}
  </div>`;
}

/** «Було → стало» по видах.
 *
 *  Дати в підписі — СПРАВЖНІ дати знімків, а не межі місяця: демон міг
 *  лежати, і читач мусить бачити, між якими днями насправді міряно. */
export function structureHTML(p) {
  if (!p.structure) {
    return `<div class="card"><h2>Було → стало</h2>
      ${empty("", esc(p.structure_note || "порівнювати немає з чим"))}</div>`;
  }
  const s = p.structure;
  const rows = s.rows.slice();
  return `<div class="card">
    <h2>Було → стало</h2>
    <div class="note">${esc(s.from_date)} → ${esc(s.to_date)}${
  s.usd_share_from !== s.usd_share_to
    ? ` · частка USD ${pct(s.usd_share_from)} → ${pct(s.usd_share_to)}`
    : ` · частка USD ${pct(s.usd_share_to)}`}</div>
    ${opsGrid({
    cols: [
      { key: "label", label: "Що", cell: (r) => esc(r.label) },
      { key: "before", label: "Було", num: true, cell: (r) => fmtUAH(r.before) },
      { key: "after", label: "Стало", num: true, cell: (r) => fmtUAH(r.after) },
      { key: "delta", label: "Зміна", num: true, cell: (r) => signed(r.delta) },
    ],
    rows,
    caption: `Склад портфеля ${esc(s.from_date)} проти ${esc(s.to_date)}: що, було, стало, зміна`,
  })}</div>`;
}

/** Рішення періоду: що куплено й чи це були верхні рядки помічника.
 *  Заголовок приходить ззовні: «Рік у цифрах» малює ту саму картку за
 *  рік, і другий примірник цієї розмітки розійшовся б із першим. */
export function decisionsHTML(p, title = "Рішення місяця") {
  const d = p.decisions || {};
  if (!d.count) {
    return `<div class="card"><h2>${esc(title)}</h2>
      ${empty("", esc(d.note || "цього місяця нічого не куплено"))}</div>`;
  }
  // Речення про верхні рядки — переказ, а не оцінка: скільки разів обране
  // стояло першим і на скільки п.п. розійшлось решта. Слова «правильно» чи
  // «дарма» тут немає навмисно — у режимі «під план» верхнім законно стоїть
  // менш дохідний рядок.
  // Крапка ставиться в кожній гілці окремо: pp() віддає «п.п.», і крапка
  // речення поверх неї дала б «п.п..».
  const note = `${d.followed} з ${d.count} ${
    plural(d.count, "покупки", "покупок", "покупок")} — за верхнім рядком помічника`
    + (d.vs_top_pp_avg
      ? `; решта розійшлась із ним у середньому на ${pp(d.vs_top_pp_avg, 2)}` : ".")
    // Подушка окремим реченням, а не в тому самому знаменнику: верхнім
    // рядком рейтингу вона не стоїть ніколи, і в частці «за верхнім» кожен
    // її рух читався б як порушення дисципліни. Слова «втрачено» тут немає
    // навмисно — резерв тримають не заради дохідності.
    + (d.reserve_count
      ? ` Плюс ${d.reserve_count} ${plural(d.reserve_count, "рух", "рухи", "рухів")}
        у подушку — доступне тоді давало ${pp(d.reserve_forgone_pct_avg, 2)}.` : "")
    // Цілі — ТРЕТІМ реченням, а не разом із подушкою: обидві повз рейтинг,
    // але доля різна. Подушку тримають, щоб не витратити, ціль — щоб
    // витратити, і злиті в одне число вони приховали б саме цю різницю.
    + (d.goal_count
      ? ` І ${d.goal_count} ${plural(d.goal_count, "рух", "рухи", "рухів")}
        у цілі накопичення — доступне тоді давало ${pp(d.goal_forgone_pct_avg, 2)}.` : "");
  return `<div class="card">
    <h2>${esc(title)}</h2>
    <div class="note">${note}</div>
    ${opsGrid({
    cols: [
      { key: "made_on", label: "Коли", cls: "muted", cell: (r) => esc(r.made_on) },
      { key: "kind", label: "Вид", cell: (r) => kindPill(r.kind === "bond" ? "bond" : r.kind) },
      { key: "ref", label: "Що", cell: (r) => esc(r.ref) },
      { key: "amount", label: "Сума", num: true,
        cell: (r) => fmtUAH(Number((r.amount || {}).amount || 0)) },
      { key: "promised", label: "Обіцяло", num: true, prio: 3,
        cell: (r) => pct(r.promised_pct) },
      { key: "rank", label: "Місце", num: true, prio: 3,
        cell: (r) => (r.rank_pos ? `${r.rank_pos}-й` : "—") },
      { key: "vs", label: "Проти верхнього", num: true, prio: 3,
        cell: (r) => (r.top_label ? pp(r.vs_top_pp, 2) : "—") },
    ],
    rows: d.rows || [],
    caption: "Рішення місяця: коли, вид, що, сума, обіцяна дохідність, місце в рейтингу",
  })}</div>`;
}

/** Сторінка. */
export async function period(ctx, main) {
  const month = chosenMonth();
  const p = await ctx.soft("period" + (month ? `?month=${month}` : ""), null);
  if (!p) {
    main.innerHTML = `<div class="card">${headHTML(month)}
      ${empty("", "Бекенд не віддав підсумок. Спробуй оновити сторінку.")}</div>`;
    wirePeriod(ctx, main);
    return;
  }
  main.innerHTML = `<div class="card">${headHTML(month)}
      ${tilesHTML(p)}</div>
    ${flowHTML({ ...p.money, from: p.from, to: p.to })}
    ${structureHTML(p)}
    ${decisionsHTML(p)}`;
  wirePeriod(ctx, main);
}

function wirePeriod(ctx, main) {
  main.querySelectorAll("[data-month]").forEach((b) =>
    b.addEventListener("click", () => {
      try { localStorage.setItem(MONTH_KEY, b.dataset.month); } catch (_) {}
      ctx.reload();
    }));
}
