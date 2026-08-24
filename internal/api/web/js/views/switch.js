// Перекладання: за якою ціною варто продати папір і взяти те, що дають.
//
// ЧОМУ ЦЕ ЖИВЕ НА СТОРІНЦІ ОВДП, А НЕ В «ЩО КУПИТИ». «Що купити» відповідає
// на питання про ВІЛЬНІ гроші й ставить поруч чотири види інструментів. Тут
// питання інше й вужче: гроші вже в папері, і рішення стосується обміну.
// Поруч із рейтингом покупок такий рядок читався б як ще одна порада
// купити — а це порада продати, і ціна помилки в неї інша.
//
// ЧОМУ НЕМАЄ КНОПКИ «ПРОДАТИ». Форма продажу стоїть кроком нижче, на тій
// самій сторінці, і веде в неї людина, а не таблиця. Поріг — це факт про
// ціну; продаж — рішення з причинами, яких застосунок не знає (спред
// брокера, потреба в грошах, небажання світити операцію).
//
// АРИФМЕТИКИ ТУТ НЕМАЄ ЖОДНОЇ (CLAUDE.md §5). Поріг, виграш і різницю в
// п.п. рахує /api/switch; форма надсилає введене котирування й малює те,
// що повернув сервер. Спокуса порахувати «ціна × кількість» просто в
// таблиці особливо велика — і це рівно та копія арифметики, через яку
// плитка з карткою вже одного разу розійшлись.

import { esc, pct, pp, cur2 as fmtCur, curSym } from "../format.js";
import { empty } from "../components.js";
import { opsGrid } from "../grid.js";
import { money as moneyField, formHTML } from "../fields.js";
import { infoBtn } from "../info.js";
import { refSuggest, wireSuggest } from "../refs.js";

let data = { alt: null, rows: [] };

/** Пороги приїжджають окремим запитом, а не в summary.
 *
 *  Свідомо: retained-стан у MQTT — це те, що читає Home Assistant, а
 *  таблиця порогів там нікому не потрібна (сутність HA — одне число з
 *  історією, а не сітка). Той самий довід, що в /api/devaluation і
 *  /api/auctions/curve поруч. */
export async function loadSwitch(ctx) {
  try { data = await ctx.api("GET", "switch"); }
  catch (_) { data = { alt: null, rows: [] }; }
  if (!data || !Array.isArray(data.rows)) data = { alt: null, rows: [] };
}

export function switchHTML() {
  const rows = data.rows || [];
  if (!rows.length) {
    return `<div class="card">${empty("Переоцінювати поки нічого",
      "Тут з'явиться поріг для кожного паперу: та чиста ціна, за якої продати й "
      + "перекластися вигідніше, ніж дотримати до погашення.")}</div>`;
  }
  const alt = data.alt;
  const head = alt
    ? `<div class="note">Порівнюємо з тим, що помічник вважає найкращим зараз:
        <b>${esc(alt.label)}</b> — ${pct(alt.real_pct)} реальних.</div>`
    : `<div class="note">Помічник зараз нічого не пропонує, тож порівнювати нема з чим.</div>`;

  return `<div class="card"><h3 class="card-head">
      <span>За якою ціною варто продати ${infoBtn("switchprice")}</span></h3>
    ${head}
    ${opsGrid({
    cols: [
      { key: "isin", label: "Папір", cell: (r) => esc(r.isin) },
      { key: "qty", label: "Шт.", num: true, cell: (r) => String(r.qty) },
      { key: "cost", label: "Купував по", num: true, prio: 2,
        cell: (r) => amt(r.cost_per_bond) },
      { key: "hold", label: "Тримаю під", num: true,
        cell: (r) => (r.hold_real_pct ? pct(r.hold_real_pct) : "—") },
      { key: "be", label: "Поріг", num: true,
        cell: (r) => (r.reason ? `<span class="muted">${esc(r.reason)}</span>` : amt(r.break_even)) },
      { key: "bepct", label: "% номіналу", num: true, prio: 2,
        cell: (r) => (r.break_even_pct ? pct(r.break_even_pct) : "—") },
      { key: "accrued", label: "НКД", num: true, prio: 3,
        cell: (r) => amt(r.accrued) },
    ],
    rows,
    caption: "Пороги перекладання: папір, кількість, ціна купівлі, дохідність утримання, поріг, поріг у % номіналу, НКД",
  })}
    <div class="sub">Поріг — ЧИСТА ціна, як її називає брокер: НКД додається зверху.
      Комісія за продаж у поріг не входить — застосунок її не знає, тож на свій тариф
      поправ сам.</div>
  </div>
  <div class="card"><h3>Перевірити котирування</h3>
    <div class="note">Брокер назвав ціну — подивись, що вона означає.</div>
    ${formHTML({ id: "switchForm", fields: verdictFields(), submit: "Порахувати" })}
    <div id="switchOut" class="mt"></div>
  </div>`;
}

// Поле паперу — той самий refSuggest, що й у формі покупки: підказка по
// ISIN одна на застосунок.
//
// Звужувати її до паперів портфеля не стали навмисно. Технічно це
// означало б другий вид підказки в refs.js — заради одного екрана, — а
// поведінково нічого не змінило б: чужий папір сервер відхиляє окремою
// відповіддю «паперів немає в портфелі», і ця фраза пояснює більше, ніж
// відсутність рядка у випадайці.
function verdictFields() {
  return [
    refSuggest({ name: "isin", ref: "bond", label: "Папір", required: true }),
    moneyField("clean", "Чиста ціна за папір", { required: true }),
  ];
}

export function wireSwitch(ctx, main) {
  const form = main.querySelector("#switchForm");
  if (!form) return;
  // Підказку по ISIN проводить той, хто її намалював, — той самий поділ,
  // що між reinvestHTML і wireReinvest. wireRefs поруч тут не потрібен:
  // варіанта «інший…» у цій формі немає, бо папір або є в довіднику, або
  // питання про його продаж не стоїть.
  wireSuggest(ctx, main);
  const out = main.querySelector("#switchOut");
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(form);
    const isin = String(fd.get("isin") || "").trim();
    const clean = String(fd.get("clean") || "").trim();
    if (!isin || !clean) return;
    out.innerHTML = `<div class="muted">Рахую…</div>`;
    try {
      const v = await ctx.api("POST", "switch", { isin, clean });
      out.innerHTML = verdictHTML(v);
    } catch (err) {
      out.innerHTML = `<div class="t-warn">${esc(String(err && err.message || err))}</div>`;
    }
  });
}

function verdictHTML(v) {
  const verdict = v.worth
    ? `<b class="t-ok">За цією ціною перекладатися вигідно.</b>`
    : `<b class="muted">За цією ціною вигідніше дотримати.</b>`;
  return `<div>${verdict}</div>
    <div class="pv-row"><span class="muted">Тримаю під</span><span>${pct(v.hold_real_pct)}</span></div>
    <div class="pv-row"><span class="muted">Дають</span><span>${pct(v.alt_real_pct)}</span></div>
    <div class="pv-row"><span class="muted">Різниця</span>
      <span class="${v.edge_pp > 0 ? "t-ok" : "muted"}">${pp(v.edge_pp)}</span></div>
    <div class="pv-row"><span class="muted">Виграш на папір</span><span>${amt(v.gain_per_bond)}</span></div>
    <div class="pv-row"><span class="muted">На всю позицію (${v.qty} шт.)</span>
      <span>${amt(v.gain_total)}</span></div>`;
}

// Гроші приходять парою «сума + валюта» (moneyJSON), і показувати їх треба
// в тій самій валюті, у якій папір: гривневий поріг зі знаком долара — це
// не одруківка, а хибне число.
function amt(m) {
  if (!m || !m.amount) return "—";
  return fmtCur(Number(m.amount), curSym(m.currency));
}
