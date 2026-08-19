// Розділ «Гроші» — де вони лежать, куди рухались і скільки забрала держава.
//
// П'ять сторінок у двох ярусах: баланси з формами руху, журнал, податок —
// і, за розділювачем, два ПАКЕТНІ входи.
//
// Форми готівки й конвертації повернулись сюди з розпущеного «Записати», і
// це не відкат до старого. Старе було іншим: «Гроші» несли і журнал, і
// чотири форми, і імпорт — тобто відповідали і на «де мої гроші», і на
// «чим записати ЩО завгодно». Тепер тут лише те, що є рухом РАХУНКУ, а
// сторінка саме про рахунок; форми інструментів пішли у свої воронки.
//
// «Виписка» і «Звірка» лишились названими МЕХАНІЗМОМ, і причина чинна без
// змін: обидві заводять кілька сутностей одразу. Виписка Inzhur
// розкладається на операції з сертифікатами, лот ОВДП і рух грошей — три
// гілки в handleImportInzhur. Посутнісного імені в них немає й бути не
// може, тож у меню вони відділені рискою.
//
// Резерв тут не живе: він частина капіталу, а не рух рахунку, і питання
// «на скільки місяців вистачить» стоїть у своїй воронці.

import { empty } from "../components.js";
import { routeFor } from "../routes.js";
import { wireDisclosures } from "../disclosure.js";
import { onSubmit, onDelete } from "../forms.js";
import { setFundOps, wireFundOps, fundStatementHTML } from "../fund-ops.js";
import {
  walletHTML, brokerBalancesHTML, movesHTML, flowHTML, taxHTML, taxYear,
  importHTML, wireImport, reconcileHTML, wireReconcile,
} from "./money-cards.js";

const curOpts = (sel) => ["UAH", "USD", "EUR"]
  .map((c) => `<option${c === sel ? " selected" : ""}>${c}</option>`).join("");

/** Скільки і де — і чим це змінити.
 *
 *  Дві форми на одній сторінці, і саме тому в ANCHORS з'явились cash і
 *  convert: routes.js каже, що якорі потрібні рівно там, де форм більше
 *  однієї, і це той випадок.
 *
 *  Конвертація стоїть окремою формою від готівки навмисно: це не рух
 *  рахунку, а обмін однієї валюти на іншу, і курс тут не задають — він
 *  рахується із сум, тобто з того, що реально сталося в банку. */
export async function balances(ctx, main) {
  main.innerHTML = `
    ${walletHTML(ctx)}
    ${brokerBalancesHTML(ctx)}
    ${cashFormHTML(ctx)}
    ${convertFormHTML(ctx)}`;
  wireCash(ctx, main);
}

function cashFormHTML(ctx) {
  return `<div class="card">
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
}

function convertFormHTML(ctx) {
  return `<div class="card">
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
}

function wireCash(ctx, main) {
  onSubmit(ctx, main.querySelector("#cashForm"), (f) => ({
    path: "deposits",
    body: {
      amount: f.amount.value.trim(), currency: f.currency.value, broker: f.broker.value,
      date: f.date.value, note: f.note.value.trim(),
    },
    msg: "Рух записано",
  }));
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

/** Куди воно рухалось: потік за період і журнал рухів. */
export async function flows(ctx, main) {
  const [deposits, conversions, flow] = await Promise.all([
    ctx.soft("deposits", []),
    ctx.soft("conversions", []),
    ctx.soft("cashflow", null),
  ]);
  main.innerHTML = `
    ${flowHTML(flow)}
    ${movesHTML(deposits, conversions)}`;
  onDelete(ctx, main, "[data-deldep]", (b) => ({
    path: "deposits/" + b.dataset.deldep, msg: "Рух видалено",
  }));
  onDelete(ctx, main, "[data-delconv]", (b) => ({
    path: "conversions/" + b.dataset.delconv, msg: "Конвертацію видалено",
  }));
  wireDisclosures(main);
}

/** Скільки з доходу забрала держава — грошима, а не ставкою.
 *
 *  Власна сторінка, а не картка в хвості рухів: сюди приходять раз на рік
 *  і цілеспрямовано, з декларацією перед очима. */
export async function tax(ctx, main) {
  const x = await ctx.soft("tax?year=" + taxYear(), null);
  main.innerHTML = taxHTML(x);

  main.querySelector("[data-tax-year]")?.addEventListener("change", (e) => {
    try { localStorage.setItem("oddinvest.taxYear", e.target.value); } catch (_) { /* приватний режим */ }
    ctx.reload();
  });
  // Вивантаження — тим самим шляхом, що й бекап у «Налаштуваннях»:
  // сирий запит через транспорт, далі blob у файл.
  main.querySelector("[data-tax-csv]")?.addEventListener("click", async (e) => {
    const y = e.currentTarget.dataset.taxCsv;
    try {
      const resp = await ctx.store.raw("export/csv?year=" + y);
      if (!resp.ok) throw new Error(await resp.text());
      const url = URL.createObjectURL(await resp.blob());
      const a = document.createElement("a");
      a.href = url;
      a.download = `oddinvest-${y}.csv`;
      a.click();
      URL.revokeObjectURL(url);
      ctx.toast(`Звіт за ${y} завантажено`);
    } catch (err) { ctx.toast(String(err.message || err), false); }
  });
  wireDisclosures(main);
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
