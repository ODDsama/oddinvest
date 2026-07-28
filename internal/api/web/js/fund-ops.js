// Операції з сертифікатами фондів: журнал, виписка, видалення.
//
// Модуль спільний, а не частина розділу: його читають і «Портфель», і
// «Гроші». Доти журнал лежав у views/portfolio.js, і «Гроші» імпортували
// його звідти — тобто розділ був сховищем даних для сусіднього розділу, і
// те, який відрендериться першим, вирішувало, що побачить інший.

import { esc, curSym, cur2 as fmtCur } from "./format.js";
import { FUND_KIND } from "./constants.js";
import { onDelete } from "./forms.js";


// Журнал операцій із фондами на час одного рендеру: таблиці позицій,
// лотів, продажів і дивідендів усі читають той самий список, і тягнути
// його чотири рази заради цього не варто.
//
// Розділ «Гроші» теж будує з нього таблиці виписки, тому список і
// живе тут, а не всередині renderPortfolio: заповнює його той, хто
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
// його позиції, «Імпорт» — усі. Без нього поводиться як раніше.
export function fundTable(ctx, kind, head, cells, empty, filter) {
  const ops = fundOps || [];
  const money = (m) => m ? fmtCur(m.amount, curSym(m.currency)) : "—";
  // Найновіші зверху: дивишся майже завжди на щойно імпортоване.
  const rows = ops.filter((o) => o.kind === kind && (!filter || filter(o)))
    .sort((a, b) => a.date < b.date ? 1 : a.date > b.date ? -1 : b.id - a.id);
  if (!rows.length) return `<div class="muted">${empty}</div>`;
  // Набір форматерів для клітинок, які в кожної таблиці свої. Раніше
  // звався ctx — тепер це ім'я зайняте контекстом розділу.
  const cellFmt = {
    money,
    price: (o) => o.qty > 0 && o.amount ? (Number(o.amount.amount) / o.qty).toFixed(4) : "",
    tax: (o) => o.tax && Number(o.tax.amount) > 0 ? money(o.tax) : "",
  };
  // Тільки видалення: записи приходять з виписки, і руками їх не
  // правлять — два джерела правди неминуче розійшлись би. ✕ лишається
  // як аварійний вихід, бо імпорт уже одного разу приніс зайве.
  return `<table><thead><tr>${head}<th>Дата</th><th>Брокер</th><th>Нотатка</th><th></th></tr></thead><tbody>
    ${rows.map((o) => `<tr><td class="num">${o.id}</td><td>${esc(o.fund)}</td>${cells(o, cellFmt)}
      <td>${esc(o.date)}</td><td>${esc(o.broker || "")}</td><td class="muted">${esc(o.note || "")}</td>
      <td class="row-actions"><button class="sm warn" data-delfund="${o.id}">✕</button></td></tr>`).join("")}
    </tbody></table>`;
}

// Те, що принесла виписка: продажі й дивіденди. Живе на вкладці
// «Імпорт», бо саме туди йдеш перевіряти, чи все зайшло правильно.
export function fundStatementHTML(ctx) {
  if (!(fundOps || []).length) return "";
  return `<div class="card"><h2>Продажі сертифікатів</h2>
      <div class="muted" style="margin-bottom:10px">Що принесла виписка. Тут же ✕, якщо принесла зайве.</div>
      ${fundTable(ctx, "sell",
        `<th class="num">ID</th><th>Фонд</th><th class="num">К-сть</th><th class="num">Ціна</th><th class="num">Отримано</th><th class="num">Податок</th>`,
        (o, c) => `<td class="num">${o.qty}</td><td class="num">${c.price(o)}</td>
          <td class="num">${c.money(o.amount)}</td><td class="num">${c.tax(o)}</td>`,
        "Продажів ще не було.")}
    </div>
    <div class="card"><h2>Дивіденди фондів</h2>
      ${fundTable(ctx, "dividend",
        `<th class="num">ID</th><th>Фонд</th><th class="num">Нараховано</th><th class="num">Податок</th><th class="num">Чистими</th>`,
        (o, c) => `<td class="num">${c.money(o.amount)}</td><td class="num">${c.tax(o)}</td>
          <td class="num">${fmtCur(Number(o.amount.amount) - Number((o.tax || {}).amount || 0), curSym(o.amount.currency))}</td>`,
        "Дивідендів ще не було.")}
    </div>`;
}

// ---------- єдина таблиця позицій ----------


// Правка веде в ту форму, якій операція належить: купівлю правиш там,
// де купуєш. «Скасувати» видно лише в режимі правки — поки її не
// натиснули, форма пам'ятає, що вона змінює, а не додає.
// Лишилось саме видалення: форм більше немає, правити записи виписки
// руками не дають.
export function wireFundOps(ctx, main) {
  onDelete(ctx, main, "[data-delfund]", (b) => {
    // У питанні називаємо саму операцію, а не її номер: «продаж Inzhur REIT
    // від 12-05» перевіряється поглядом, «запис #37» — ні.
    const o = (fundOps || []).find((x) => x.id === +b.dataset.delfund);
    const what = o ? `${FUND_KIND[o.kind] || o.kind} ${o.fund} від ${o.date}` : "запис #" + b.dataset.delfund;
    return {
      path: "funds/" + b.dataset.delfund,
      confirm: `Видалити ${what}? Позиція й ціна перерахуються.`,
      msg: "Запис видалено",
    };
  });
}


