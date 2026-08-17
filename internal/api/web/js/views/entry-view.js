// Розділ «Записати» — чим внести операцію. Вісім сторінок у двох ярусах.
//
// Питання «чим це записати» одне, а місць для нього було чотири: купівля й
// продаж жили в «Портфелі», готівка, конвертація, виписка й звірка — у
// «Грошах», потоки й дії — у «Плані». Тепер форми зібрані сюди, а журнали
// лишились там, де на них дивляться.
//
// ЯРУСІВ ДВА, і другий названий механізмом навмисно.
//
// Перші шість сторінок — ручні форми, по одній сутності на сторінку.
// Останні дві — пакетні входи, які заводять кілька сутностей одразу:
// виписка Inzhur розкладається на операції з сертифікатами, лот ОВДП і рух
// грошей (три гілки в handleImportInzhur), а звірка дописує коригування
// рахунку. Посутнісного імені в них немає й бути не може, тож у меню вони
// відділені рискою.
//
// Сертифікатів фондів тут немає власною сторінкою, і це не пропуск: форми
// для них не існує взагалі — fund-ops.js уміє лише видаляти, бо два
// джерела правди (виписка й рука) розійшлися б, і розійшлися б тихо.
// Єдиний їхній вхід — «Виписка».
//
// НПФ, навпаки, сторінку має, але форма в нього не власна: внесок мусить
// цілитись у конкретний рахунок, тож форми живуть у блоці рахунку
// (npfDetailHTML) і в «Активах», і тут. Одна реалізація на два місця —
// окрема форма з випадайкою рахунків була б другою.

import { esc } from "../format.js";
import { infoBtn } from "../info.js";
import { empty } from "../components.js";
import { onSubmit } from "../forms.js";
import { wireDisclosures } from "../disclosure.js";
import { routeFor } from "../routes.js";
import { setFundOps, wireFundOps, fundStatementHTML } from "../fund-ops.js";
import { setNPF, npfDetailHTML, wireNPF } from "../npf.js";
import { bondBuyFormHTML, bondSaleFormHTML, wireBonds } from "./bonds.js";
import { depositFormHTML, wireDeposits } from "./deposits.js";
import {
  reserveFormHTML, importHTML, wireImport, reconcileHTML, wireReconcile,
} from "./money-cards.js";

const curOpts = (sel) => ["UAH", "USD", "EUR"]
  .map((c) => `<option${c === sel ? " selected" : ""}>${c}</option>`).join("");

/** ОВДП: купівля й продаж на вторинному ринку.
 *
 *  Дві форми на одній сторінці — єдиний випадок у розділі, і виправданий:
 *  це одна сутність із двома напрямками, а не дві сутності. Обидві
 *  потребують списку лотів, тож і запит один. */
export async function bond(ctx, main) {
  const lots = await ctx.api("GET", "lots");
  main.innerHTML = `
    <div class="card"><h2>Нова покупка ОВДП</h2>${bondBuyFormHTML(ctx, lots)}</div>
    <div class="card"><h2>Продаж на вторинному ринку</h2>${bondSaleFormHTML(ctx, lots)}</div>`;
  wireBonds(ctx, main);
  wireDisclosures(main);
}

/** Вклади: відкриття нового. Поповнення чинного живе в рядку самого
 *  вкладу («Активи → Вклади»), бо мусить цілитись у конкретний вклад. */
export async function deposit(ctx, main) {
  const deposits = await ctx.soft("term-deposits", []);
  main.innerHTML = `
    <div class="card"><h2 class="h-row">Новий вклад ${infoBtn("deposit")}</h2>
      ${depositFormHTML(ctx)}</div>
    <div class="card"><div class="sub">Поповнити чинний вклад або розірвати його достроково
      можна в його ж рядку — розгорни позицію в «Активи → Вклади».</div></div>`;
  wireDeposits(ctx, main, deposits);
  wireDisclosures(main);
}

/** НПФ: внески й ЧВОПА по кожному рахунку. */
export async function npf(ctx, main) {
  const [accounts, ops, nav] = await Promise.all([
    ctx.soft("npf-accounts", []),
    ctx.soft("npf", []),
    ctx.soft("npf-nav", []),
  ]);
  setNPF({ accounts, ops, nav });
  const rows = (ctx.summary || {}).npf || [];
  main.innerHTML = rows.length
    ? rows.map((n) => `<div class="card"><h2>${esc(n.name)}</h2>
        ${npfDetailHTML(ctx, n)}</div>`).join("")
    : `<div class="card"><h2>НПФ</h2>${empty(
      "Рахунків ще немає",
      "Пенсійний рахунок заводиться в довідниках — після цього тут з'являться форми внесків і ЧВОПА.",
      { href: routeFor("settings/refs"), label: "Довідники" })}</div>`;
  wireNPF(ctx, main);
  wireDisclosures(main);
}

/** Резерв: відкласти або взяти. Плитки й журнал — в «Активах». */
export async function reserve(ctx, main) {
  main.innerHTML = reserveFormHTML();
  onSubmit(ctx, main.querySelector("#resForm"), (f) => ({
    path: "reserve",
    body: {
      amount: f.amount.value.trim(), currency: f.currency.value,
      place: f.place.value.trim(), date: f.date.value, note: f.note.value.trim(),
    },
    msg: "Рух резерву записано",
  }));
}

/** Готівка: поповнення (+) і зняття (−) рахунку. */
export async function cash(ctx, main) {
  main.innerHTML = `<div class="card">
    <h2>Додати рух</h2>
    <div class="note">Поповнення (+) / зняття (−) у своїй валюті. Купівля лота й купони рухають рахунок автоматично.</div>
    <form id="cashForm">
      <label>Сума (+ / −)<input name="amount" inputmode="decimal" placeholder="5000.00" required></label>
      <label>Валюта<select name="currency">${curOpts("UAH")}</select></label>
      <label>Брокер<select name="broker">${ctx.brokerOptions()}</select></label>
      <label>Дата<input name="date" type="date" value="${new Date().toISOString().slice(0, 10)}"></label>
      <label>Нотатка<input name="note"></label>
      <div class="form-actions"><button type="submit">Записати</button></div>
    </form>
  </div>`;
  onSubmit(ctx, main.querySelector("#cashForm"), (f) => ({
    path: "deposits",
    body: {
      amount: f.amount.value.trim(), currency: f.currency.value, broker: f.broker.value,
      date: f.date.value, note: f.note.value.trim(),
    },
    msg: "Рух записано",
  }));
}

/** Конвертація валют. Окремо від готівки: це не рух рахунку, а обмін
 *  однієї валюти на іншу, і курс тут не задають — він рахується із сум,
 *  тобто з того, що реально сталося в банку. */
export async function convert(ctx, main) {
  main.innerHTML = `<div class="card">
    <h2>Конвертація валют</h2>
    <div class="note">Віддав → отримав (курс рахується сам із сум — те, що реально сталося на Monobank).</div>
    <form id="convForm">
      <label>Віддав<input name="from_amount" inputmode="decimal" placeholder="40000.00" required></label>
      <label>Валюта<select name="from_currency">${curOpts("UAH")}</select></label>
      <label>Отримав<input name="to_amount" inputmode="decimal" placeholder="1000.00" required></label>
      <label>Валюта<select name="to_currency">${curOpts("USD")}</select></label>
      <label>Брокер<select name="broker">${ctx.brokerOptions()}</select></label>
      <label>Дата<input name="date" type="date" value="${new Date().toISOString().slice(0, 10)}"></label>
      <label>Нотатка<input name="note"></label>
      <div class="form-actions"><button type="submit">Записати</button></div>
    </form>
  </div>`;
  onSubmit(ctx, main.querySelector("#convForm"), (f) => ({
    path: "conversions",
    body: {
      from_amount: f.from_amount.value.trim(), from_currency: f.from_currency.value,
      to_amount: f.to_amount.value.trim(), to_currency: f.to_currency.value,
      broker: f.broker.value, date: f.date.value, note: f.note.value.trim(),
    },
    msg: "Конвертацію записано",
  }));
}

/** Виписка: пакетний вхід. Два кроки навмисно — спершу показати, що буде
 *  зроблено, і лише потім писати. Ціна помилки тут подвоєний баланс, а він
 *  знаходиться не одразу. Продажі й дивіденди фондів показані тут-таки: це
 *  єдине місце, де вони взагалі заводяться. */
export async function importStatement(ctx, main) {
  const ops = await ctx.soft("funds", []);
  setFundOps(ops);
  main.innerHTML = `
    ${importHTML(ctx)}
    ${fundStatementHTML(ctx)}`;
  wireImport(ctx, main);
  wireFundOps(ctx, main);
  wireDisclosures(main);
}

/** Звірка рахунку: що каже брокер проти того, що каже облік.
 *
 *  Коригування — звичайне поповнення з поміткою, а не окрема сутність:
 *  так розбіжність лишається видимою в історії рухів, а не ховається. */
export async function reconcile(ctx, main) {
  main.innerHTML = reconcileHTML(ctx)
    || `<div class="card"><h2>Звірка рахунку</h2>${empty(
      "Звіряти ще нема з чим",
      "Рядок з'явиться, коли на рахунку брокера будуть гроші.",
      { href: routeFor("deposit"), label: "Додати рух" })}</div>`;
  wireReconcile(ctx, main);
}
