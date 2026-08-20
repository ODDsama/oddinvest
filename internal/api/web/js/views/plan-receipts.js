// Надходження місяця: чекліст «що з обіцяного вже прийшло».
//
// Окремо від потоків, хоч і виросло з них: потік — це ОБІЦЯНКА
// («зарплата, 40 000, щомісяця»), а надходження — факт одного місяця проти
// неї. Різні сутності з різним життям: обіцянку правлять раз на півроку,
// факт відмічають щомісяця, і тримати їх в одному файлі означало б, що
// найчастіша дія розділу лежить у найрідше змінюваному коді.
//
// amtOf і monthKeyOffset експортуються: підрядок вердикту (plan-cards.js)
// рахує з тих самих відміток і мусить читати їх однаково.

import {
  esc, money as fmtMoney, uah0 as fmtUAH, cur2, dayMonth, monthYear,
} from "../format.js";
import { infoBtn } from "../info.js";
import { empty } from "../components.js";
import { apply, onSubmit, onDelete, openEdit } from "../forms.js";
import { disclosure } from "../disclosure.js";
import {
  money as moneyField, num as numField, text as textField, formHTML,
} from "../fields.js";
import { refSelect } from "../refs.js";
import { opsGrid } from "../grid.js";

// ---------- надходження місяця ----------
//
// ЧЕКЛИСТ, а не журнал, і це головне рішення картки. Форма «заведи запис
// про надходження» вимагала б щоразу пригадувати, скільки мало прийти, і
// вписувати те, що застосунок уже знає. Тут навпаки: рядки розгортає план
// («зп 17-го — 32 000»), а від людини потрібен один тик.
//
// Три стани, і жоден не можна злити з іншим: «ще не відмічено» (кнопки),
// «прийшло стільки» і «не прийшло». Останні два — це записаний факт;
// перший — його відсутність. Саме заради цієї різниці таблиця й існує:
// нуль, який означає «зарплати не було», мусить виглядати інакше за
// місяць, до якого просто не дійшли руки.
//
// Вікно навігації збігається з тим, де відмітка щось міняє: назад — стільки
// ж, скільки показує «План проти факту», уперед — вікно, за яким
// усереднюється «План дає». Далі тицяти просто нема сенсу.

// monthSel — вибраний місяць зсувом від поточного. Модульна змінна, як
// factTip нижче: ctx.reload() перемальовує розділ, але не перезавантажує
// модуль, тож вибір переживає і збереження відмітки, і перемальовування.
// У localStorage не їде навмисно — «який місяць я гортав учора» не той
// стан, який варто пам'ятати між сеансами.
let monthSel = 0;

const RECEIPT_BACK = 12;
const RECEIPT_FWD = 12;

// Прочерк «тут нічого й не мало бути» — на відміну від нуля, який означає
// «мало, але не прийшло». Своя константа в кожного модуля, який його
// пише, а не спільний токен: по всьому застосунку це літерал (format.js,
// deposits.js, history.js), і заводити для одного знаку загальне ім'я
// означало б завести конвенцію, якої більше ніхто не дотримується.
const DASH = "—";

/** "YYYY-MM" зі зсувом n від поточного місяця.
 *
 *  Арифметикою над роком і місяцем, а не Date.setMonth: та має семантику
 *  переповнення (31 березня мінус місяць = 3 березня), і ключ місяця з неї
 *  часом виходив би не тим. Той самий розрахунок, що monthKeyAt на
 *  бекенді, — інакше браузер і сервер називали б «травнем» різні місяці. */
export function monthKeyOffset(n) {
  const d = new Date();
  const t = d.getFullYear() * 12 + d.getMonth() + n;
  return `${String(Math.floor(t / 12)).padStart(4, "0")}-${
    String((t % 12) + 1).padStart(2, "0")}`;
}

export const amtOf = (m) => parseFloat(String((m || {}).amount || "0").replace(",", ".")) || 0;

// Різниця «факт проти обіцяного» у валюті потоку. Показується лише коли
// вона є: рівно за планом — це нормальний випадок, і підпис «0» під ним
// був би шумом.
function receiptDiffHTML(e) {
  const r = e.receipt;
  if (!r) return "";
  const diff = amtOf(r.amount) - amtOf(e.amount);
  if (Math.abs(diff) < 0.005) return "";
  const cur = (e.amount || {}).currency || "UAH";
  const sign = diff > 0 ? "+" : "−";
  return ` <span class="fine ${diff > 0 ? "t-ok" : "t-warn"}">${sign}${
    esc(cur2(Math.abs(diff), cur))}</span>`;
}

function receiptStateHTML(e) {
  const r = e.receipt;
  if (!r) {
    // Дві кнопки, а не форма: у 95% випадків прийшло рівно те, що обіцяв
    // план, і питати про суму в цьому випадку означає питати даремно.
    return `<button type="button" class="sm" data-mark="${e.flow_id}"
        data-month="${esc(e.month)}" data-amt="${esc((e.amount || {}).amount || "")}"
        aria-label="Відмітити, що «${esc(e.name)}» надійшло">✓ прийшло</button>
      <button type="button" class="sm quiet" data-skip="${e.flow_id}"
        data-month="${esc(e.month)}"
        aria-label="Відмітити, що «${esc(e.name)}» не прийшло">✕ не прийшло</button>`;
  }
  return amtOf(r.amount) === 0
    ? `<span class="pill redemption">не прийшло</span>`
    : `<span class="pill coupon">${esc(fmtMoney(r.amount))}</span>${receiptDiffHTML(e)}`;
}

const receiptNoteHTML = (r) =>
  (r && r.note ? `<div class="fine-xs muted">${esc(r.note)}</div>` : "");

// Рядок таблиці. Кнопки правки й зняття стоять лише там, де є що правити:
// «скасувати відмітку» для невідміченого рядка означало б нічого.
// Очікуване й позапланове — ОДНА таблиця з одним набором колонок.
//
// Доти це були дві функції рядка, які будували по п'ять <td> кожна, і
// збігатись вони мусили на слово: розійшлися б — і колонки другої групи
// поїхали б відносно шапки. Тепер розрізняє їх одне поле (planned), а
// колонки описані раз.
//
// У позапланового немає ні дати, ні планової суми — і прочерк каже про це
// чесніше, ніж підставлений нуль.
function receiptCols() {
  return [
    { key: "name", label: "Джерело", cell: (r) => esc(r.name)
      + (r.planned ? "" : ` <span class="fine muted">позапланово</span>`)
      + receiptNoteHTML(r.planned ? r.receipt : r) },
    { key: "due", label: "Коли", num: true,
      cell: (r) => (r.planned && r.due_date ? esc(dayMonth(r.due_date)) : DASH) },
    { key: "plan", label: "План", num: true,
      cell: (r) => (r.planned ? esc(fmtMoney(r.amount)) : DASH) },
    { key: "fact", label: "Факт", cell: (r) => (r.planned
      ? receiptStateHTML(r)
      : `<span class="pill coupon">${esc(fmtMoney(r.amount))}</span>`) },
    { key: "acts", label: "", cls: "row-actions nowrap", cell: (r) => (r.planned
      ? `<button type="button" class="sm" data-editrec="${r.flow_id}"
           data-month="${esc(r.month)}" aria-label="Вписати суму для «${esc(r.name)}»">✎</button>`
        + (r.receipt ? `<button type="button" class="sm warn" data-delrec="${r.receipt.id}"
           aria-label="Зняти відмітку з «${esc(r.name)}»">✕</button>` : "")
      : `<button type="button" class="sm" data-editother="${r.id}"
           aria-label="Змінити «${esc(r.name)}»">✎</button>
         <button type="button" class="sm warn" data-delrec="${r.id}"
           aria-label="Видалити «${esc(r.name)}»">✕</button>`) },
  ];
}

// Поля правки прив'язаної відмітки: сума й причина, більше нічого. Валюта
// й джерело задані самим потоком, а місяць — рядком, у якому натиснули.
function receiptFields(values = null) {
  const v = values || {};
  const val = (k, d = "") => (v[k] != null ? String(v[k]) : d);
  return moneyField("amount", "Скільки надійшло", { required: true, value: val("amount") })
    + textField("note", "Причина", {
      ph: "вийшов з відпустки, 2 дні", value: val("note"),
    })
    + `<div class="sub-xs">Нуль означає «не прийшло». Сума валова — та, що прийшла на руки;
      скільки з неї доходить до портфеля, визначає частка самого джерела.</div>`;
}

function otherFields(values = null) {
  const v = values || {};
  const val = (k, d = "") => (v[k] != null ? String(v[k]) : d);
  return [
    textField("name", "Що це", { ph: "Премія", required: true, value: val("name") }),
    moneyField("amount", "Сума", { required: true, value: val("amount") }),
    refSelect(null, { name: "currency", ref: "currency", value: val("currency", "UAH") }),
    numField("invest_pct", "Частка в портфель, %", {
      step: "0.01", min: "0", max: "100", value: val("invest_pct", "100"),
    }),
    textField("note", "Причина", { value: val("note") }),
  ].join("");
}

const otherBody = (f, month) => ({
  flow_id: 0, month, name: f.name.value.trim(), amount: f.amount.value.trim(),
  currency: f.currency.value, invest_pct: f.invest_pct.value.trim() || "100",
  note: f.note.value.trim(),
});

export function receiptsHTML(doc) {
  const expected = doc.expected || [];
  const all = doc.receipts || [];
  if (!expected.length && !all.length) return ""; // плану доходу ще немає

  const key = monthKeyOffset(monthSel);
  const rows = expected.filter((e) => e.month === key);
  const others = all.filter((r) => r.flow_id === 0 && r.month === key);
  const future = monthSel > 0;

  const nav = `<div class="row-actions">
    <button type="button" class="sm quiet" data-monthstep="-1"
      ${monthSel <= -RECEIPT_BACK ? "disabled" : ""} aria-label="Попередній місяць">‹</button>
    <b>${esc(monthYear(key + "-01"))}</b>
    <button type="button" class="sm quiet" data-monthstep="1"
      ${monthSel >= RECEIPT_FWD ? "disabled" : ""} aria-label="Наступний місяць">›</button>
    ${monthSel !== 0
    ? `<button type="button" class="sm quiet" data-monthstep="0">сьогодні</button>` : ""}
  </div>`;

  const head = `<h2 class="card-head"><span>Надходження ${infoBtn("planReceipts")}</span>${nav}</h2>`;

  if (!rows.length && !others.length) {
    return `<div class="card">${head}${empty("",
      future
        ? `Цього місяця план нічого не обіцяє — відмічати нема чого.`
        : `Цього місяця план нічого не обіцяв. Позапланове надходження можна дописати
           нижче, у місяці, який уже настав.`)}
      ${future ? "" : otherFormHTML(key)}</div>`;
  }

  // Підсумок рахується по ТИХ САМИХ рядках, що на екрані. «Надійшло» — у
  // гривні (gives_uah), бо це єдине число, у якому можна скласти зарплату в
  // доларах із премією в гривні; планова сума в рядку лишається валютною.
  const marked = rows.filter((e) => e.receipt).length;
  const planSum = rows.reduce((a, e) => a + (e.plan_uah || 0), 0);
  const gotSum = rows.reduce((a, e) => a + (e.receipt ? e.receipt.gives_uah || 0 : 0), 0)
    + others.reduce((a, r) => a + (r.gives_uah || 0), 0);
  const left = rows.length - marked;

  const summary = `<div class="sub">Відмічено <b>${marked}</b> із ${rows.length}${
    left ? ` · лишилось ${left}` : ""} · план дає <b>${fmtUAH(planSum)}</b> · надійшло
    <b>${fmtUAH(gotSum)}</b>.</div>`;

  // Застереження про майбутнє коротке й стоїть лише в майбутньому: у
  // минулому відмітка нічого не прогнозує, і той самий текст там читався б
  // як попередження ні про що.
  const futureNote = future
    ? `<div class="sub-xs">Місяць ще не настав: відмічене тут не «сталося», а «відомо
       наперед» — і саме так воно й піде в прогноз, замістивши план цього місяця.</div>`
    : "";

  return `<div class="card">${head}
    ${opsGrid({
    cols: receiptCols(),
    rows: [
      ...rows.map((e) => ({ ...e, planned: true })),
      ...others.map((r) => ({ ...r, planned: false })),
    ],
    caption: "Надходження місяця: джерело, дата, планова сума, факт",
  })}
    ${summary}${futureNote}
    ${future ? "" : otherFormHTML(key)}</div>`;
}

// Форма позапланового — згорнута: це рідкісний випадок, і розгорнутою вона
// відтягувала б увагу від чеклиста, заради якого картка й існує.
function otherFormHTML(month) {
  return disclosure("planOtherReceipt", "Інше надходження", formHTML({
    id: "otherReceiptForm", submit: "Відмітити", fields: [otherFields()],
    attrs: { "data-month": month },
  }), "премія, подарунок, продаж — те, чого в плані немає");
}

export function wirePlanReceipts(ctx, main, doc) {
  const key = monthKeyOffset(monthSel);
  const expected = (doc.expected || []).filter((e) => e.month === key);
  const byFlow = new Map(expected.map((e) => [String(e.flow_id), e]));
  const others = new Map((doc.receipts || []).map((r) => [String(r.id), r]));

  main.querySelectorAll("[data-monthstep]").forEach((b) => b.addEventListener("click", () => {
    const step = +b.dataset.monthstep;
    monthSel = step === 0 ? 0 : monthSel + step;
    if (monthSel < -RECEIPT_BACK) monthSel = -RECEIPT_BACK;
    if (monthSel > RECEIPT_FWD) monthSel = RECEIPT_FWD;
    // Тепле перемальовування: дані вже в кеші store, тож мережі тут немає,
    // а розділ не блимає (app.js:_loadTab, warm).
    ctx.reload();
  }));

  // Один тик = планова сума. Саме валова: чеклист звіряється з випискою.
  main.querySelectorAll("[data-mark]").forEach((b) => b.addEventListener("click", () => {
    apply(ctx, {
      path: "plan/receipts",
      body: { flow_id: +b.dataset.mark, month: b.dataset.month, amount: b.dataset.amt },
    }, "Надходження відмічено");
  }));

  main.querySelectorAll("[data-skip]").forEach((b) => b.addEventListener("click", () => {
    apply(ctx, {
      path: "plan/receipts",
      body: { flow_id: +b.dataset.skip, month: b.dataset.month, amount: "0" },
    }, "Відмічено: не прийшло");
  }));

  onDelete(ctx, main, "[data-delrec]", (b) => ({
    path: "plan/receipts/" + b.dataset.delrec,
    confirm: "Зняти відмітку?",
    msg: "Відмітку знято",
  }));

  main.querySelectorAll("[data-editrec]").forEach((b) => b.addEventListener("click", () => {
    const e = byFlow.get(b.dataset.editrec);
    if (!e) return;
    const r = e.receipt;
    // Префіл — фактом, якщо він уже є, інакше планом: правка існуючої
    // відмітки має показувати те, що правиться, а перша — те, з чим
    // порівнюють.
    const values = {
      amount: (r ? (r.amount || {}).amount : (e.amount || {}).amount) || "",
      note: (r && r.note) || "",
    };
    const body = (f) => ({
      flow_id: e.flow_id, month: e.month,
      amount: f.amount.value.trim(), note: f.note.value.trim(),
    });
    openEdit(ctx, {
      title: `«${esc(e.name)}» за ${esc(monthYear(e.month + "-01"))}`,
      fields: receiptFields(values),
      submit: r ? "Зберегти" : "Відмітити",
    }, (f) => (r
      ? { method: "PUT", path: "plan/receipts/" + r.id, body: body(f), msg: "Відмітку змінено" }
      : { path: "plan/receipts", body: body(f), msg: "Надходження відмічено" }));
  }));

  onSubmit(ctx, main.querySelector("#otherReceiptForm"), (f) => ({
    path: "plan/receipts", body: otherBody(f, f.dataset.month), msg: "Надходження відмічено",
  }));

  main.querySelectorAll("[data-editother]").forEach((b) => b.addEventListener("click", () => {
    const r = others.get(b.dataset.editother);
    if (!r) return;
    openEdit(ctx, {
      title: `Правка «${esc(r.name)}»`,
      fields: otherFields({
        name: r.name, amount: (r.amount || {}).amount || "",
        currency: (r.amount || {}).currency || "UAH",
        invest_pct: r.invest_pct != null ? r.invest_pct : 100,
        note: r.note || "",
      }),
    }, (f) => ({
      method: "PUT", path: "plan/receipts/" + r.id,
      body: otherBody(f, r.month), msg: "Відмітку змінено",
    }));
  }));
}

