// Форми ОВДП: купівля з підказкою по ISIN і продаж на вторинному ринку.

import { esc, money as fmtMoney } from "../format.js";
import {
  money as moneyField, num as numField, date as dateField,
  note as noteField, formHTML,
} from "../fields.js";
import { refSelect, refSuggest, refValue, refAttr, wireRefs, wireSuggest } from "../refs.js";
import { wireCrud, inlineEdit } from "../crud.js";
import { CURRENCIES } from "../constants.js";


/** Поля лота — один список і для купівлі, і для правки.
 *
 *  Правки в цього розділу доти НЕ БУЛО ЗОВСІМ, попри те, що
 *  PUT /api/lots/{id} існував від початку: помилку в ціні чи даті
 *  виправляли видаленням лота й повторним набором. Для лота це найдорожчий
 *  спосіб, бо разом із ним зникали прив'язані продажі (ON DELETE CASCADE),
 *  тобто одруківка в комісії коштувала всієї історії паперу. */
export const lotFields = (ctx, row = null) => [
  refSuggest({ name: "isin", ref: "bond", value: row ? row.isin : "", required: true }),
  numField("qty", "Кількість", { min: 1, required: true, value: row ? row.qty : "" }),
  moneyField("price_per_bond", "Ціна за папір (брудна)", {
    ph: "995.00", required: true, value: row ? row.price_per_bond.amount : "",
  }),
  moneyField("fee", "Комісія (сумарно)", { value: row ? row.fee.amount : "" }),
  refSelect(ctx, {
    name: "currency", ref: "currency", blank: "авто (з довідника)",
    value: row ? row.price_per_bond.currency : "",
  }),
  dateField("buy_date", "Дата купівлі", row ? { value: row.buy_date } : {}),
  refSelect(ctx, { name: "channel", ref: "broker", value: row ? row.channel || "" : "" }),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

export const lotBody = (f) => ({
  isin: f.isin.value.trim(),
  qty: parseInt(f.qty.value, 10),
  price_per_bond: f.price_per_bond.value.trim(),
  fee: f.fee.value.trim(),
  currency: refValue(f, "currency"),
  buy_date: f.buy_date.value,
  channel: refValue(f, "channel"),
  note: f.note.value.trim(),
});

// Аргумента lots тут більше немає: брокер приходить полем-посиланням, а
// воно бере список саме там, де він і живе, — об'єднання довідника з тим,
// що вже зустрічалось у лотах (app.js:_brokerList). Доти список лотів
// передавали сюди рівно заради випадайки брокера.
export function bondBuyFormHTML(ctx) {
  return formHTML({ id: "lotForm", fields: lotFields(ctx), submit: "Додати" })
    + `<div class="muted mt-sm" id="bondInfo"></div>`;
}

/** Поля продажу. lots потрібні для випадайки лота — це та сама
 *  підстановка сутності, що й брокер, лише список приходить сторінкою, а
 *  не з ctx: лоти вантажить той, кому вони потрібні. */
export const saleFields = (ctx, row = null, lots = []) => [
  refSelect(ctx, {
    name: "lot_id", ref: "lot", items: (lots || []).filter((l) => l.remaining > 0 || (row && l.id === row.lot_id)),
    value: row ? String(row.lot_id) : "", required: true,
  }),
  dateField("sale_date", "Дата продажу", row ? { value: row.sale_date } : {}),
  numField("qty", "Кількість", { min: 1, required: true, value: row ? row.qty : "" }),
  moneyField("clean_per_bond", "Чиста ціна/папір", {
    ph: "1001.50", required: true, value: row ? row.clean_per_bond.amount : "",
  }),
  moneyField("accrued", "НКД (сумарно)", { value: row ? row.accrued.amount : "" }),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

export const saleBody = (f) => ({
  lot_id: parseInt(refValue(f, "lot_id"), 10),
  sale_date: f.sale_date.value,
  qty: parseInt(f.qty.value, 10),
  clean_per_bond: f.clean_per_bond.value.trim(),
  accrued: f.accrued.value.trim(),
  // Валюта продажу — з ЛОТА, а не з форми: продати можна лише те, що є, і
  // окреме поле валюти тут дозволяло б відповісти неправильно.
  currency: refAttr(f, "lot_id", "data-cur") || CURRENCIES[0],
  note: f.note.value.trim(),
});

export function bondSaleFormHTML(ctx, lots) {
  return formHTML({ id: "saleForm", fields: saleFields(ctx, null, lots), submit: "Записати" })
    + `<div class="sub mt">Записані продажі видно в рядку того паперу, якого вони
        стосуються — розкрий позицію стрілкою.</div>`;
}


// Продажі правляться НА МІСЦІ, без модалки, і це не непослідовність.
// Помилку в лоті видно одразу — він з'явився не з тим ISIN; у продажі не
// видно, бо на екрані стоїть уже ЗВЕДЕНИЙ результат, і розходження з
// випискою брокера на шість гривень не каже, котре з чотирьох полів набрано
// не так. Правити доводиться навпомацки, звіряючи результат, — а модалка
// ховає саме те число, за яким звіряються.
//
// Тіло збирається з усіх полів плюс lot_id і валюта з data-атрибутів:
// часткового оновлення бекенд не знає, і надіслати саму кількість означало б
// обнулити ціну.
function wireSales(ctx, main, lots, sales) {
  wireCrud(ctx, main, {
    resource: "sales", form: "#saleForm", title: "Продаж", rows: sales,
    // lots їдуть ЗАМИКАННЯМ, а не полем на ctx. Приліплене збоку поле тут
    // уже пробували (ctx._deposits свого часу) і відмовились: працює доти,
    // доки список вантажить рівно один розділ, а щойно їх двоє — котрийсь
    // мусить пам'ятати чуже правило.
    fields: (c, row) => saleFields(c, row, lots),
    body: saleBody,
    confirm: (row) => "Скасувати продаж #" + row.id
      + "? Реалізований результат і залишок лота повернуться до стану до нього.",
    msg: { add: "Продаж записано", del: "Продаж скасовано" },
  });
  inlineEdit(ctx, main, {
    rows: "[data-sale]", fields: ".sale-f",
    path: (row) => "sales/" + row.dataset.sale,
    extra: (row) => ({ lot_id: Number(row.dataset.lot), currency: row.dataset.cur }),
    msg: "Продаж виправлено",
  });
}


export function wireBonds(ctx, main, lots = [], sales = []) {
  wireCrud(ctx, main, {
    resource: "lots", form: "#lotForm", title: "Лот", rows: lots,
    fields: lotFields, body: lotBody,
    // Купівля йде через перевірку грошей: якщо на рахунку брокера не
    // вистачає, форма спершу запропонує поповнити його рівно на нестачу.
    funded: (f) => ({
      check: "lots/check", date: f.buy_date.value, what: "купівля ОВДП",
    }),
    confirm: (row) => "Видалити лот #" + row.id + " (" + esc(row.isin) + ")?"
      + (row.qty !== row.remaining ? " Продажі з нього теж зникнуть." : ""),
    msg: { add: "Лот додано", edit: "Лот виправлено", del: "Лот видалено" },
  });

  wireSales(ctx, main, lots, sales);

  // Проводка полів: «інший…» біля брокера й підказка ISIN. Обидві терплять
  // відсутність своїх цілей, тож один виклик покриває і сторінку з формою,
  // і сторінку з самою лише таблицею.
  wireRefs(main);
  wireSuggest(ctx, main);

  // Автозаповнення з довідника при виборі ISIN — далі лише коригуєш.
  // Живе тут, а не в refs.js: підказка вміє ВИБРАТИ сутність, а що робити
  // з рештою форми — питання цієї конкретної форми.
  const buyForm = main.querySelector("#lotForm");
  if (!buyForm) return;
  const isinInput = buyForm.elements.isin;
  isinInput.addEventListener("change", async () => {
    const isin = isinInput.value.trim();
    const info = main.querySelector("#bondInfo");
    if (!isin) { if (info) info.textContent = ""; return; }
    try {
      const b = await ctx.api("GET", "bonds/" + encodeURIComponent(isin));
      if (!b || !b.nominal) return;
      if (CURRENCIES.includes(b.nominal.currency)) buyForm.currency.value = b.nominal.currency;
      if (!buyForm.price_per_bond.value.trim()) buyForm.price_per_bond.value = b.nominal.amount;
      if (info) {
        info.textContent = `${esc(b.descr || "")} · ${b.rate_pct}% · погашення ${esc(b.maturity)}`
          + ` · номінал ${fmtMoney(b.nominal)}`;
      }
    } catch (_) { if (info) info.textContent = ""; }
  });
}
