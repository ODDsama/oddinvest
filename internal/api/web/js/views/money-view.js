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
import { setFundOps, wireFundOps, fundStatementHTML } from "../fund-ops.js";
import { money as moneyField, date as dateField, note as noteField, formHTML } from "../fields.js";
import { refSelect, refValue, wireRefs } from "../refs.js";
import { wireCrud } from "../crud.js";
import {
  walletHTML, brokerBalancesHTML, fxWindowHTML, movesHTML, flowHTML, taxHTML, taxYear,
  importHTML, wireImport, importProfilesHTML, wireImportProfiles,
  reconcileHTML, wireReconcile,
} from "./money-cards.js";

/** Поля руху рахунку — ОДИН список і для форми додавання, і для модалки
 *  правки. row === null означає «додаємо».
 *
 *  Валюта й брокер тут поля-посилання (refs.js), а не рукописні випадайки:
 *  доти валютна трійка була вписана в цьому файлі локальним curOpts(), і
 *  таких локальних копій по застосунку набралось сім. */
export const cashFields = (ctx, row = null) => [
  moneyField("amount", "Сума (+ / −)", {
    value: row ? row.amount.amount : "", ph: "5000.00", required: true,
  }),
  refSelect(ctx, { name: "currency", ref: "currency", value: row ? row.amount.currency : "UAH" }),
  refSelect(ctx, { name: "broker", ref: "broker", value: row ? row.broker : "" }),
  dateField("date", "Дата", row ? { value: row.date } : {}),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

export const cashBody = (f) => ({
  amount: f.amount.value.trim(),
  currency: refValue(f, "currency"),
  broker: refValue(f, "broker"),
  date: f.date.value,
  note: f.note.value.trim(),
});

export const convFields = (ctx, row = null) => [
  moneyField("from_amount", "Віддав", {
    value: row ? row.from.amount : "", ph: "40000.00", required: true,
  }),
  refSelect(ctx, { name: "from_currency", ref: "currency", value: row ? row.from.currency : "UAH" }),
  moneyField("to_amount", "Отримав", {
    value: row ? row.to.amount : "", ph: "1000.00", required: true,
  }),
  refSelect(ctx, { name: "to_currency", ref: "currency", value: row ? row.to.currency : "USD" }),
  refSelect(ctx, { name: "broker", ref: "broker", value: row ? row.broker : "" }),
  dateField("date", "Дата", row ? { value: row.date } : {}),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

export const convBody = (f) => ({
  from_amount: f.from_amount.value.trim(),
  from_currency: refValue(f, "from_currency"),
  to_amount: f.to_amount.value.trim(),
  to_currency: refValue(f, "to_currency"),
  broker: refValue(f, "broker"),
  date: f.date.value,
  note: f.note.value.trim(),
});

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
    ${fxWindowHTML(ctx)}
    ${convertFormHTML(ctx)}`;
  wireCash(ctx, main);
}

function cashFormHTML(ctx) {
  return `<div class="card">
    <h2>Додати рух</h2>
    <div class="note">Поповнення (+) / зняття (−) у своїй валюті. Купівля лота й купони рухають рахунок автоматично.</div>
    ${formHTML({ id: "cashForm", fields: cashFields(ctx), submit: "Записати" })}
  </div>`;
}

function convertFormHTML(ctx) {
  return `<div class="card">
    <h2>Конвертація валют</h2>
    <div class="note">Віддав → отримав (курс рахується сам із сум — те, що реально сталося на Monobank).</div>
    ${formHTML({ id: "convForm", fields: convFields(ctx), submit: "Записати" })}
  </div>`;
}

// Обидва рухи — звичайний КРУД, тож проводка одна на обидва й описує лише
// те, чим вони різняться. Кнопок правки на цій сторінці немає (журнал живе
// на сусідній), і wireCrud це не заважає: він підв'язує те, що знайшов.
function wireCash(ctx, main) {
  wireCrud(ctx, main, {
    resource: "deposits", form: "#cashForm", title: "Рух",
    fields: cashFields, body: cashBody,
    msg: { add: "Рух записано", edit: "Рух виправлено", del: "Рух видалено" },
  });
  wireCrud(ctx, main, {
    resource: "conversions", form: "#convForm", title: "Конвертація",
    fields: convFields, body: convBody,
    msg: {
      add: "Конвертацію записано", edit: "Конвертацію виправлено",
      del: "Конвертацію видалено",
    },
  });
  wireRefs(main);
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
  // Журнал — те саме, що й форми на сусідній сторінці, лише з іншого боку:
  // ті самі два ресурси, ті самі поля. Саме звідси й з'явилась правка, якої
  // доти не було: PUT /deposits/{id} і PUT /conversions/{id} існували на
  // сервері від початку, і єдиним способом виправити одруківку в сумі було
  // видалити рядок і набрати заново.
  wireCrud(ctx, main, {
    resource: "deposits", title: "Рух", rows: deposits,
    fields: cashFields, body: cashBody,
    msg: { edit: "Рух виправлено", del: "Рух видалено" },
  });
  wireCrud(ctx, main, {
    resource: "conversions", title: "Конвертація", rows: conversions,
    fields: convFields, body: convBody,
    msg: { edit: "Конвертацію виправлено", del: "Конвертацію видалено" },
  });
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
  // Профілі тягнемо мʼяко: маршрут може бути новішим за бекенд, а сторінка
  // з самим лише Inzhur краща за порожню — той самий прийом, що в
  // «Порівнянні».
  const [ops, profiles] = await Promise.all([
    ctx.soft("funds", []),
    ctx.soft("import/profiles", []),
  ]);
  setFundOps(ops);
  main.innerHTML = `
    ${importHTML(ctx, profiles)}
    ${importProfilesHTML(ctx, profiles)}
    ${fundStatementHTML(ctx)}`;
  wireImport(ctx, main);
  wireImportProfiles(ctx, main);
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
