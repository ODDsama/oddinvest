// Строкові вклади: форма, поповнення, закриття, архів.
//
// «Установа», а не «Банк»: сюди ж лягає роздрібна корпоративна облігація
// (NovaPay), і емітент у неї не банк. Довід — у шапці domain/deposit.go.

import { esc, plural, money as fmtMoney } from "../format.js";
import { onSubmit } from "../forms.js";
import {
  money as moneyField, pct as pctField, date as dateField,
  note as noteField, check as checkField, selectOf, formHTML,
} from "../fields.js";
import { refSelect, refValue, wireRefs } from "../refs.js";
import { wireCrud } from "../crud.js";
import { opsGrid, actionsCol } from "../grid.js";
import { disclosure } from "../disclosure.js";
import { PAYOUT_LABEL } from "../constants.js";


// Виплата відсотків — скінченний список, і підписи до нього вже живуть у
// constants.js: та сама мапа підписує вклад у єдиній таблиці позицій.
// Доти цей список був вписаний у форму окремо, і два джерела одних і тих
// самих трьох слів розійшлись би на першій же зміні формулювання.
const PAYOUTS = Object.entries(PAYOUT_LABEL);

/** Поля вкладу — один список і для відкриття, і для правки.
 *
 *  Правки вкладу доти не було: PUT існував, але кликали його рівно двома
 *  вузькими латками — перемикачем «поповнюваний» і закриттям. Одруківку в
 *  ставці чи даті погашення виправити було нічим, а вклад — саме той
 *  запис, де одруківка тиха: 16.5 замість 15.6 не виглядає помилкою ніде,
 *  крім самої виписки банку. */
export const depositFields = (ctx, row = null) => [
  refSelect(ctx, { name: "bank", ref: "broker", label: "Установа", value: row ? row.bank : "" }),
  refSelect(ctx, {
    name: "currency", ref: "currency",
    value: row ? row.principal.currency : "UAH",
  }),
  moneyField("principal", "Тіло", {
    ph: "100000.00", required: true, value: row ? row.principal.amount : "",
  }),
  pctField("rate_pct", "Ставка, %", {
    ph: "16.5", required: true, value: row ? row.rate_pct : "",
  }),
  dateField("open_date", "Відкрито", row ? { value: row.open_date } : {}),
  dateField("maturity_date", "Погашення", {
    required: true, value: row ? row.maturity_date : "",
  }),
  selectOf("payout", "Виплата відсотків", PAYOUTS, row ? row.payout : "end"),
  checkField("capitalized", "Капіталізація", { checked: row ? !!row.capitalized : false }),
  checkField("replenishable", "Поповнюваний", { checked: row ? !!row.replenishable : false }),
  // Дві властивості ДОГОВОРУ, а не строку, і саме тому вони тут, а не
  // виводяться з дат. «Резервний» переносить тіло з вкладів у подушку —
  // тобто зі знаменника видів і зі списку «Що купити». «Відкличний» каже,
  // що гроші можна забрати достроково: за ЦКУ строковий вклад фізособи
  // безвідкличний, доки в договорі не написано інакше, тож типове
  // значення — не поставлено.
  checkField("is_reserve", "Це подушка (резерв)", { checked: row ? !!row.is_reserve : false }),
  checkField("revocable", "Відкличний (можна забрати достроково)",
    { checked: row ? !!row.revocable : false }),
  pctField("tax_pct", "Податок, %", {
    ph: "23 (за замовч.)", value: row ? row.tax_pct : "",
  }),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

/** Тіло вкладу. PUT шле вклад ЦІЛКОМ, тож поля закриття мусять їхати
 *  разом із рештою — інакше правка ставки мовчки «відкрила» б назад
 *  розірваний вклад.
 *
 *  closed — те, чого у формі немає: при відкритті його ще не існує, а при
 *  правці воно лежить у самому записі. Доти цю перекладку робили двічі
 *  (окремо перемикач «поповнюваний», окремо закриття), і забутий у другій
 *  копії replenishable свого часу мовчки скидав прапорець. */
export const depositBody = (f, closed = {}) => ({
  bank: refValue(f, "bank"),
  currency: refValue(f, "currency"),
  principal: f.principal.value.trim(),
  rate_pct: f.rate_pct.value.trim(),
  open_date: f.open_date.value,
  maturity_date: f.maturity_date.value,
  payout: f.payout.value,
  capitalized: f.capitalized.checked,
  replenishable: f.replenishable.checked,
  is_reserve: f.is_reserve.checked,
  revocable: f.revocable.checked,
  tax_pct: f.tax_pct.value.trim(),
  note: f.note.value.trim(),
  closed_date: closed.date || "",
  closed_amount: closed.amount || "",
});

// Те саме тіло, зібране не з форми, а з уже завантаженого запису: ним
// користуються вузькі латки, які міняють ОДНЕ поле, — перемикач у рядку й
// форма розірвання.
function bodyFromRow(d, patch) {
  return {
    bank: d.bank, currency: d.principal.currency,
    principal: d.principal.amount, rate_pct: String(d.rate_pct),
    open_date: d.open_date, maturity_date: d.maturity_date,
    payout: d.payout, capitalized: !!d.capitalized,
    replenishable: !!d.replenishable,
    is_reserve: !!d.is_reserve, revocable: !!d.revocable,
    tax_pct: String(d.tax_pct), note: d.note || "",
    closed_date: d.closed_date || "", closed_amount: (d.closed_amount || {}).amount || "",
    ...patch,
  };
}

export function depositFormHTML(ctx) {
  return formHTML({ id: "termDepForm", fields: depositFields(ctx), submit: "Додати" });
}

/** Поля поповнення. Валюти серед них немає навмисно: її задає вклад, і
 *  питати означало б дозволити відповісти неправильно. */
export const topupFields = (ctx, row = null, dep = null) => [
  dateField("date", "Дата поповнення", row ? { value: row.date } : {}),
  moneyField("amount", "Сума", {
    required: true,
    value: row ? row.amount.amount : (dep ? dep.principal.amount : ""),
  }),
];

export const topupBody = (f) => ({
  date: f.date.value,
  amount: f.amount.value.trim(),
});

/** Форма поповнення одного вкладу. Атрибут із номером вкладу — на самій
 *  формі: форм на сторінці стільки ж, скільки поповнюваних вкладів, і
 *  розрізняти їх треба до того, як хоч одну буде підв'язано. */
export function topupFormHTML(ctx, d) {
  return formHTML({
    fields: topupFields(ctx, null, d), submit: "Поповнити",
    attrs: { "data-topup-form": d.id },
  });
}

/** Поля дострокового розірвання. Банк перерахує відсотки сам за штрафною
 *  ставкою — ми лише вводимо те, що реально прийшло на рахунок. */
export const closeFields = (ctx, d) => [
  dateField("closed_date", "Дата розірвання", { required: true }),
  moneyField("closed_amount", "Отримано (тіло + відсотки)", {
    ph: d.balance.amount, required: true,
  }),
];

export function closeFormHTML(ctx, d) {
  return formHTML({
    fields: closeFields(ctx, d), submit: "Підтвердити розірвання",
    attrs: { "data-close-form": d.id },
  });
}

// Розірвані вклади — вже не позиція, а історія: згорнуто.
export function closedDepositsHTML(ctx, deposits) {
  const closed = deposits.filter((d) => d.closed_date);
  if (!closed.length) return "";
  return `<div class="card">${disclosure("closeddep", "Закриті достроково", opsGrid({
    cols: [
      { key: "bank", label: "Установа", cell: (d) => esc(d.bank || "—") },
      { key: "principal", label: "Тіло", num: true, cell: (d) => fmtMoney(d.principal) },
      { key: "closed_date", label: "Розірвано", cell: (d) => esc(d.closed_date) },
      { key: "closed_amount", label: "Отримано", num: true, cell: (d) => fmtMoney(d.closed_amount) },
      actionsCol("term-deposits", {
        edit: false, label: (d) => "закритий вклад #" + d.id,
      }),
    ],
    rows: closed,
    caption: "Закриті достроково вклади: банк, тіло, дата розірвання, отримано",
  }), `${closed.length} ${plural(closed.length, "вклад", "вклади", "вкладів")}`)}</div>`;
}


export function wireDeposits(ctx, main, deposits = []) {
  const byId = new Map((deposits || []).map((d) => [String(d.id), d]));

  wireCrud(ctx, main, {
    resource: "term-deposits", form: "#termDepForm", title: "Вклад", rows: deposits,
    fields: depositFields,
    // Поля закриття беруться з САМОГО запису, бо у формі їх немає: при
    // відкритті вкладу їх ще не існує, а при правці вони вже лежать у
    // рядку. Без цього збереження ставки надіслало б порожні closed_* —
    // тобто мовчки «відкрило» б назад розірваний вклад.
    body: (f, row) => depositBody(f, row
      ? { date: row.closed_date, amount: (row.closed_amount || {}).amount }
      : {}),
    // Відкриття вкладу замикає гроші так само, як покупка паперу їх
    // витрачає, тож і питання про нестачу те саме.
    funded: (f) => ({
      check: "term-deposits/check", date: f.open_date.value, what: "відкриття вкладу",
    }),
    msg: { add: "Вклад додано", edit: "Вклад виправлено", del: "Вклад видалено" },
  });

  // Перемикач «поповнюваний» просто на рядку: це властивість вкладу, яку
  // дізнаєшся вже після відкриття, і відкривати заради неї модалку було б
  // надміру.
  main.querySelectorAll("[data-repl]").forEach((cb) =>
    cb.addEventListener("change", async () => {
      const d = byId.get(cb.dataset.repl);
      if (!d) return;
      try {
        await ctx.api("PUT", "term-deposits/" + d.id,
          bodyFromRow(d, { replenishable: cb.checked }));
        ctx.toast(cb.checked ? "Вклад поповнюваний" : "Вклад не поповнюваний");
        ctx.reload();
      } catch (err) {
        // Галочку повертаємо назад: інакше на екрані лишився б стан, якого
        // на сервері немає.
        ctx.toast(String(err.message || err), false);
        cb.checked = !cb.checked;
      }
    }));

  // Поповнення — вкладений ресурс, тож ім'я в розмітці несе ще й номер
  // вкладу: інакше проводка першого вкладу підв'язала б кнопки всіх.
  (deposits || []).forEach((d) => {
    wireCrud(ctx, main, {
      resource: "topups:" + d.id, title: "Поповнення", rows: d.topups || [],
      path: (id) => "term-deposits/" + d.id + "/topups/" + id,
      createPath: "term-deposits/" + d.id + "/topups",
      form: `[data-topup-form="${d.id}"]`,
      fields: (c, row) => topupFields(c, row, d),
      body: topupBody,
      funded: (f) => ({
        check: "term-deposits/" + d.id + "/topups/check",
        date: f.date.value, what: "поповнення вкладу",
      }),
      msg: {
        add: "Поповнення додано", edit: "Поповнення виправлено",
        del: "Поповнення видалено",
      },
    });
  });

  // Розірвання = PUT усього вкладу з проставленими closed_*: банк
  // перерахує відсотки сам за штрафною ставкою, ми лише вводимо отриману
  // суму.
  main.querySelectorAll("[data-close-form]").forEach((f) =>
    onSubmit(ctx, f, () => {
      const d = byId.get(f.dataset.closeForm);
      if (!d) return null;
      return {
        method: "PUT", path: "term-deposits/" + d.id,
        body: bodyFromRow(d, {
          closed_date: f.closed_date.value,
          closed_amount: f.closed_amount.value.trim(),
        }),
        msg: "Вклад закрито",
      };
    }));

  wireRefs(main);
}
