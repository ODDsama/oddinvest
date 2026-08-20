// Операції з сертифікатами фондів: журнал, виписка, видалення.
//
// Модуль спільний, а не частина розділу: його читають і «Портфель», і
// «Гроші». Доти журнал лежав у views/portfolio.js, і «Гроші» імпортували
// його звідти — тобто розділ був сховищем даних для сусіднього розділу, і
// те, який відрендериться першим, вирішувало, що побачить інший.

import { esc, curSym, cur2 as fmtCur } from "./format.js";
import { FUND_KIND } from "./constants.js";
import {
  money as moneyField, num as numField, date as dateField,
  note as noteField, selectOf,
} from "./fields.js";
import { refSelect, refValue } from "./refs.js";
import { wireCrud } from "./crud.js";
import { opsGrid, actionsCol } from "./grid.js";


// Журнал операцій із фондами на час одного рендеру: таблиці позицій,
// лотів, продажів і дивідендів усі читають той самий список, і тягнути
// його чотири рази заради цього не варто.
//
// Розділ «Гроші» теж будує з нього таблиці виписки, тому список і
// живе тут, а не всередині сторінки: заповнює його той, хто
// першим прийшов, а читають обидва.
let fundOps = [];
export function setFundOps(ops) { fundOps = ops || []; }


// --- сертифікати фондів ---
// Розкладено так само, як облігації: позиції, під ними лоти (купівлі),
// далі продажі й дивіденди — кожне своєю таблицею з власною формою.
// Спільного хронологічного журналу свідомо немає: у портфелі питання не
// «що відбувалось», а «що в мене є і з чого воно склалось».
//
// Форма кожного розділу працює і на «додати», і на «виправити»:
// прихований id перемикає POST на PUT, і другого набору полів не треба.
// Спільні будівельники таблиць операцій фонду: «Портфель» показує
// позиції й лоти, «Імпорт» — продажі й дивіденди. Одна реалізація на
// двох, бо дві копії однієї таблиці рано чи пізно розходяться.
// filter — необов'язковий: «Портфель» показує лоти ОДНОГО фонду в рядку
// його позиції, «Імпорт» — усі.
//
// cols — СЕРЕДНІ колонки, ті, що різняться від виду до виду: у купівлі це
// кількість, ціна й сплачено, у дивіденда — нараховано, податок і чистими.
// Обрамлення (номер, фонд, дата, брокер, нотатка, дії) однакове в усіх
// трьох, тож і описане тут один раз. Доти виклик передавав шматок
// <thead> рядком і окремо функцію клітинок, і тримати їх у злагоді мусив
// сам викликач — розійшлись би вони тихо, зсувом на одну колонку.
export function fundTable(ctx, kind, cols, empty, filter) {
  const ops = fundOps || [];
  const money = (m) => (m ? fmtCur(m.amount, curSym(m.currency)) : "—");
  // Найновіші зверху: дивишся майже завжди на щойно імпортоване.
  const rows = ops.filter((o) => o.kind === kind && (!filter || filter(o)))
    .sort((a, b) => (a.date < b.date ? 1 : a.date > b.date ? -1 : b.id - a.id));
  // Набір форматерів для клітинок, які в кожної таблиці свої.
  const cellFmt = {
    money,
    price: (o) => (o.qty > 0 && o.amount ? (Number(o.amount.amount) / o.qty).toFixed(4) : ""),
    tax: (o) => (o.tax && Number(o.tax.amount) > 0 ? money(o.tax) : ""),
  };
  return opsGrid({
    cols: [
      { key: "id", label: "ID", num: true, prio: 3, cell: (o) => String(o.id) },
      { key: "fund", label: "Фонд", cell: (o) => esc(o.fund) },
      ...cols.map((c) => ({ ...c, cell: (o) => c.cell(o, cellFmt) })),
      { key: "date", label: "Дата", cell: (o) => esc(o.date) },
      { key: "broker", label: "Брокер", prio: 3, cell: (o) => esc(o.broker || "") },
      { key: "note", label: "Нотатка", cls: "muted", prio: 3, cell: (o) => esc(o.note || "") },
      actionsCol("funds", {
        label: (o) => `${FUND_KIND[o.kind] || o.kind} ${o.fund} від ${o.date}`,
      }),
    ],
    rows,
    caption: `Операції з сертифікатами (${FUND_KIND[kind] || kind}): фонд, дата, суми`,
    empty,
  });
}

/** Поля операції з сертифікатом — один список і для правки, і (колись)
 *  для запису руками. Форми додавання тут немає й не планується: записи
 *  приносить виписка, а другий вхід для тих самих даних розійшовся б із
 *  нею на першому ж імпорті.
 *
 *  А от ПРАВКА тепер є, і ось із чим її треба знати. Повторний імпорт
 *  розпізнає вже заведений рядок за четвіркою (дата, фонд, вид, кількість,
 *  сума) — store.FundOpExists. Виправив будь-що з цієї четвірки — і
 *  наступний імпорт тієї самої виписки принесе початковий рядок ще раз,
 *  уже як окремий запис. Податок, брокер і нотатка в розпізнавання не
 *  входять, тож правляться без наслідків, і саме вони найчастіше й
 *  бувають неправильні у виписці.
 *
 *  Тому попередження стоїть У САМІЙ модалці, а не лише тут: читає його
 *  той, хто править, а не той, хто читає код. */
export const fundOpFields = (ctx, row = null) => [
  refSelect(ctx, { name: "fund", ref: "fund", value: row ? row.fund : "" }),
  selectOf("kind", "Вид", Object.entries(FUND_KIND), row ? row.kind : "buy"),
  dateField("date", "Дата", row ? { value: row.date } : {}),
  numField("qty", "Кількість", { min: 0, value: row ? row.qty : "" }),
  moneyField("amount", "Сума", { required: true, value: row ? row.amount.amount : "" }),
  moneyField("tax", "Податок", { value: row && row.tax ? row.tax.amount : "" }),
  refSelect(ctx, { name: "broker", ref: "broker", value: row ? row.broker || "" : "" }),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
  `<div class="note">Дата, фонд, вид, кількість і сума — те, за чим повторний
    імпорт упізнає вже заведений рядок. Виправиш щось із них — і та сама виписка
    наступного разу принесе початковий запис ще раз, окремим рядком. Податок,
    брокер і нотатка на це не впливають.</div>`,
];

export const fundOpBody = (f) => ({
  fund: refValue(f, "fund"),
  kind: f.kind.value,
  date: f.date.value,
  qty: parseInt(f.qty.value, 10) || 0,
  amount: f.amount.value.trim(),
  tax: f.tax.value.trim(),
  broker: refValue(f, "broker"),
  note: f.note.value.trim(),
});

// Те, що принесла виписка: продажі й дивіденди. Живе на вкладці
// «Імпорт», бо саме туди йдеш перевіряти, чи все зайшло правильно.
export function fundStatementHTML(ctx) {
  if (!(fundOps || []).length) return "";
  return `<div class="card"><h2>Продажі сертифікатів</h2>
      <div class="note">Що принесла виписка. Тут же ✎ і ✕, якщо принесла не те.</div>
      ${fundTable(ctx, "sell", [
    { key: "qty", label: "К-сть", num: true, cell: (o) => String(o.qty) },
    { key: "price", label: "Ціна", num: true, cell: (o, c) => c.price(o) },
    { key: "amount", label: "Отримано", num: true, cell: (o, c) => c.money(o.amount) },
    { key: "tax", label: "Податок", num: true, cell: (o, c) => c.tax(o) },
  ], "Продажів ще не було.")}
    </div>
    <div class="card"><h2>Дивіденди фондів</h2>
      ${fundTable(ctx, "dividend", [
    { key: "amount", label: "Нараховано", num: true, cell: (o, c) => c.money(o.amount) },
    { key: "tax", label: "Податок", num: true, cell: (o, c) => c.tax(o) },
    { key: "net", label: "Чистими", num: true,
      cell: (o) => fmtCur(Number(o.amount.amount) - Number((o.tax || {}).amount || 0),
        curSym(o.amount.currency)) },
  ], "Дивідендів ще не було.")}
    </div>`;
}

// Правка операції фонду З'ЯВИЛАСЬ, і ось чому попереднє рішення
// («тільки видалення, бо записи приходять з виписки») більше не тримає.
//
// Аргумент був про два джерела правди. Але ✕ у цій же таблиці вже його
// порушував: видалити рядок виписки — теж розійтися з нею, просто
// грубіше. І найчастіша помилка виписки — податок, якого вона або не
// показує, або показує не в тій колонці; лікувати це видаленням усього
// рядка означало втратити ще й купівлю.
//
// Наслідок повторного імпорту названий у самій модалці (fundOpFields), бо
// читати його має той, хто править.
export function wireFundOps(ctx, main) {
  wireCrud(ctx, main, {
    resource: "funds", title: "Запис", rows: fundOps || [],
    fields: fundOpFields, body: fundOpBody,
    // У питанні називаємо саму операцію, а не її номер: «продаж Inzhur
    // REIT від 12-05» перевіряється поглядом, «запис #37» — ні.
    confirm: (o) => `Видалити ${FUND_KIND[o.kind] || o.kind} ${o.fund} від ${o.date}?`
      + " Позиція й ціна перерахуються.",
    msg: { edit: "Запис виправлено", del: "Запис видалено" },
  });
}
