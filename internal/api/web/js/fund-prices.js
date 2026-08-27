// Позначки ціни сертифіката: крива, форма й вклеювання історії.
//
// Модуль окремий, а не всередині views/settings-view.js, з тієї ж причини,
// що fund-ops.js і npf.js: сама сторінка довідників тримає ЛИШЕ спільну
// розмітку рядка, а все специфічне для інструмента живе поруч зі своїм
// інструментом.
//
// Навіщо позначки взагалі. Ціна сертифіката доти рухалась ЛИШЕ операціями:
// купівля й продаж приносили її з собою, і для REIT, який докуповують
// щомісяця, цього справді досить. Накопичувальний фонд (Inzhur MilTech) не
// платить нічого, купують його раз — і ціна заморожувалась на ціні купівлі
// назавжди. Ринкова вартість дорівнювала собівартості, а дохідність
// виходила нулем ЗА ПОБУДОВОЮ, бо термінальна вартість бралася з тієї самої
// ціни. Тобто весь дохід паперу, який є самим змістом цього фонду, був
// невидимий.
//
// Стан модуль НЕ кешує, на відміну від npf.js. Там кеш має власне
// виправдання (списки читає і «Портфель», і «Гроші», і хто прийшов першим —
// той і заповнив); тут єдиний споживач — сторінка довідників, тож списки
// їздять параметрами. Схована залежність від порядку вкладок нікому не
// потрібна вдруге.

import { esc, curSym, plural } from "./format.js";
import { onSubmit } from "./forms.js";
import { money as moneyField, date as dateField, textarea, formHTML } from "./fields.js";
import { wireCrud } from "./crud.js";
import { opsGrid, actionsCol } from "./grid.js";
import { disclosure } from "./disclosure.js";
import { dateCurve } from "./charts.js";
import { parseDatedNumbers } from "./paste.js";

/** Ціна однієї операції, у гривнях за сертифікат. Ділення тут — не
 *  арифметика домену, а розпакування того, що бекенд уже віддав: сума
 *  операції й кількість лежать поруч у рядку журналу. Те саме робить
 *  cellFmt.price у fund-ops.js — інакше ціна в журналі й ціна на кривій
 *  розійшлись би, показуючи одну операцію двома числами.
 *
 *  БЕЗ ділення на 100: moneyJSON віддає суму в ГРИВНЯХ рядком («5000.00»),
 *  а не в копійках. Спіймано вживу — на кривій ціна купівлі вийшла 10 ₴
 *  замість 1000, і крива провалювалась ямою рівно в день покупки. Тест би
 *  цього не побачив: JS тут не тестується, а бекенд віддавав правильне. */
const opPrice = (o) => Number(o.amount.amount) / o.qty;

/** Усі відомі точки ціни фонду: позначені руками разом із виведеними з
 *  операцій, по одній на дату.
 *
 *  Дзеркалить domain.FundPricePoints, і позначка так само важить більше за
 *  виведену з операції на ту саму дату: вона точна, а виведена несе
 *  округлення суми до копійки. Друга реалізація тут неминуча — сервер
 *  віддає обидва списки сирими, бо крива малюється лише в UI, — тож правило
 *  записане в обох місцях однаково. */
function priceSeries(fundName, marks, ops) {
  const byDate = new Map();
  (ops || []).filter((o) => o.fund === fundName && o.qty > 0
    && (o.kind === "buy" || o.kind === "sell"))
    .forEach((o) => byDate.set(o.date, opPrice(o)));
  (marks || []).forEach((m) => byDate.set(m.date, m.price));
  return [...byDate.entries()].sort((a, b) => (a[0] < b[0] ? -1 : 1));
}

/** Крива ціни. Менше двох точок — не крива, і малювати з однієї означало б
 *  показати горизонтальну лінію там, де даних немає.
 *
 *  Полотно, проріджування підписів і рядок діапазону тепер робить dateCurve:
 *  усе це слово в слово повторювалось у navChartHTML для ЧВОПА. Разом із
 *  ним пішли два обходи, які тут довго стояли поясненими:
 *
 *  — голий svgLine замість fluid() «бо панель у згорнутому <details>».
 *    fitCharts уже вміє згорнуте (запис лягає в mounted до виходу по
 *    нульовій ширині), а app.js домальовує на toggle. Обхід коштував
 *    розтягнутого в півтора раза полотна разом із підписами;
 *  — діапазон текстом «бо svgLine центрує підписи, тож крайні виходять за
 *    полотно». Крайні підписи більше не центруються (xAnchor), а рядок
 *    діапазону лишився — але вже як підпис, а не як латка: він називає рік,
 *    якого на осі свідомо немає. */
function priceChartHTML(fundName, currency, marks, ops, row) {
  // Зміну за період кривої приносить БЕКЕНД окремим полем. Порахувати її
  // тут ((last/first − 1) × 100) означало б завести другу копію арифметики
  // дохідності в браузері — рівно те, від чого застерігає CLAUDE.md §5.
  //
  // І це НЕ price_return_pct із рядка нижче: те число — відсотки річних
  // складних, воно навмисно порожнє на відрізку коротшому за пів року
  // (ануалізувати два тижні означає намалювати сотні відсотків). Це —
  // проста зміна за відомий відрізок, чесна на будь-якій довжині, і саме
  // вона підписує вісь, що не починається з нуля.
  const caption = row && row.price_change_days
    ? ` · <b class="${row.price_change_pct >= 0 ? "t-ok" : "t-danger"}">${
      row.price_change_pct >= 0 ? "+" : ""}${row.price_change_pct.toFixed(2)}%</b> за ${
      row.price_change_days} ${plural(row.price_change_days, "день", "дні", "днів")}`
    : "";
  return dateCurve(priceSeries(fundName, marks, ops), {
    color: "var(--oi-series-funds)",
    fmt: (v) => v.toFixed(4),
    unit: curSym(currency),
    label: "Ціна сертифіката",
    empty: `Крива зʼявиться з двох точок. Кожна купівля приносить
      одну з собою; опубліковану фондом історію можна вклеїти нижче.`,
    caption,
  });
}

/** Заведені руками позначки — окремим списком, бо лише їх і можна виправити
 *  чи видалити: ціни з виписки живуть у журналі операцій і зникають разом
 *  із ним. */
function markListHTML(fundID, currency, marks) {
  const pts = (marks || []).filter((m) => m.fund_id === fundID)
    .sort((a, b) => (a.date < b.date ? 1 : -1));
  if (!pts.length) return "";
  return opsGrid({
    cols: [
      { key: "date", label: "Дата", cls: "muted", cell: (m) => esc(m.date) },
      { key: "price", label: "Ціна", num: true,
        cell: (m) => (m.price || 0).toFixed(4) + " " + curSym(currency) },
      actionsCol("fund-prices", { label: (m) => "позначку ціни за " + m.date }),
    ],
    rows: pts,
    caption: "Позначені руками ціни сертифіката: дата й значення",
    cls: "mt",
  });
}

/** Пара «обіцяли / фактично» — те, заради чого історія й вклеюється.
 *
 *  Обіцянка з довідника стоїть вічно й сама себе ніколи не перевіряє; поруч
 *  із виміряним зростанням ціни різницю видно з одного погляду. Це не те
 *  саме, що дохідність позиції: та про МОЇ гроші й дати моїх купівель, а це
 *  — про сам фонд. */
function promiseVsFactHTML(row) {
  if (!row) return "";
  const promise = row.expected_pct
    ? `${row.expected_pct.toFixed(2)}%`
    : `<span class="muted">не задано</span>`;
  if (!row.price_return_pct) {
    return `<div class="sub">Обіцяно фондом: <b>${promise}</b>.
      Виміряти ще нема по чому — потрібні дві точки ціни щонайменше за пів року.</div>`;
  }
  const diff = row.expected_pct ? row.price_return_pct - row.expected_pct : 0;
  const tone = diff >= 0 ? "t-ok" : "t-danger";
  const gap = row.expected_pct
    ? ` <span class="${tone}">(${diff >= 0 ? "+" : ""}${diff.toFixed(2)}%)</span>` : "";
  return `<div class="sub">Обіцяно фондом: <b>${promise}</b> ·
    ціна фактично росте на <b>${row.price_return_pct.toFixed(2)}%</b>${gap}</div>
    <div class="sub-xs muted">Зростання ціни — «як спрацював фонд», незалежно від дат моїх
      купівель. «Скільки заробили мої гроші» — інше число, воно в дохідності позиції на
      «Портфелі», і при нерівномірних купівлях вони розходяться.</div>`;
}

/** Поля однієї позначки. Фонд не поле: позначка належить фонду, у чиєму
 *  рядку вона й стоїть, а перенести її на інший фонд — це не правка, а нова
 *  позначка. */
export const priceFields = (ctx, row = null, placeholder = "") => [
  dateField("date", "Дата", row ? { value: row.date } : {}),
  moneyField("price", "Ціна за сертифікат", {
    required: true, ph: placeholder,
    value: row ? (row.price || 0).toFixed(4) : "",
  }),
];

export const priceBody = (f) => ({ date: f.date.value, price: f.price.value.trim() });

/** Панель позначок під рядком фонда в довіднику.
 *
 *  fund — рядок довідника (id, name, currency); row — рядок документа стану
 *  для цього фонда, якщо він є (фонд без операцій у документі не зʼявиться,
 *  і це нормально: позначити ціну наперед можна, а позиції ще немає).
 *
 *  Панель — СУСІД рядка довідника, а не його дитина, і це не смак. Правку
 *  на місці (inlineEdit) робить збирання всіх .cat-f усередині [data-cat];
 *  якби форма позначки лежала там, довідник почав би отримувати в PUT поля,
 *  яких він не знає. */
export function fundPricePanelHTML(ctx, fund, marks, ops, row) {
  const mine = (marks || []).filter((m) => m.fund_id === fund.id);
  const last = mine.length ? mine[mine.length - 1] : null;
  const hint = mine.length
    ? `${mine.length} позначок · остання ${last.date}`
    : "позначок немає";
  const body = `
    ${priceChartHTML(fund.name, fund.currency, mine, ops, row)}
    ${promiseVsFactHTML(row)}
    <div class="sub-xs muted">Ціна сертифіката рухається сама, а виписка приносить її лише в
      мить купівлі. Для накопичувального фонду, який нічого не платить, позначка — єдиний спосіб
      побачити дохід узагалі: без неї ринкова вартість дорівнює собівартості, а дохідність
      виходить нулем за побудовою.</div>

    <h4 class="mt">Позначити ціну</h4>
    ${formHTML({
    fields: priceFields(ctx, null, last ? (last.price || 0).toFixed(4) : ""),
    submit: "Позначити",
    attrs: { "data-fundprice-form": fund.id },
  })}

    <h4 class="mt">Вклеїти історію цін</h4>
    <div class="sub-xs muted">Опублікована фондом таблиця — по рядку на день:
      <code>2026-01-31 10.5</code>. Дата й число, розділені пробілом, комою або табуляцією.
      Історію до першої купівлі теж можна: саме вона й робить видимим track record, на якому
      стоїть обіцянка.</div>
    ${formHTML({
    fields: [textarea("points", "Рядки", { ph: "2026-01-31 10.5\n2026-02-28 10.84" })],
    submit: "Вклеїти",
    attrs: { "data-fundpricebulk-form": fund.id },
  })}
    ${markListHTML(fund.id, fund.currency, marks)}`;
  return disclosure(`fundprice.${fund.id}`, "Ціна сертифіката", body, hint);
}

/** Проводка. Ресурс правки/видалення один на всі фонди: позначка знає свій
 *  фонд сама, а список під кожним фондом уже відфільтрований. А от форми
 *  прив'язуються ПОІМЕННО з номером фонда — інакше проводка першого
 *  підв'язала б кнопки всіх, рівно як це вже було з внесками в НПФ. */
export function wireFundPrices(ctx, main, marks) {
  wireCrud(ctx, main, {
    resource: "fund-prices", title: "Позначку ціни", rows: marks || [],
    fields: (c, row) => priceFields(c, row),
    body: priceBody,
    confirm: (m) => `Видалити позначку ціни за ${m.date}?`
      + " Вартість позиції й дохідність перерахуються.",
    msg: { edit: "Ціну виправлено", del: "Позначку видалено" },
  });

  main.querySelectorAll("[data-fundprice-form]").forEach((form) => {
    const id = Number(form.dataset.fundpriceForm);
    onSubmit(ctx, form, (f) => ({
      path: "fund-prices",
      body: { fund_id: id, points: [priceBody(f)] },
      msg: "Ціну позначено",
    }));
  });

  main.querySelectorAll("[data-fundpricebulk-form]").forEach((form) => {
    const id = Number(form.dataset.fundpricebulkForm);
    onSubmit(ctx, form, (f) => {
      const { points, bad } = parseDatedNumbers(new FormData(f).get("points"));
      // Жодного рядка не приймаємо, якщо є зіпсовані: половина вклеєної
      // історії гірша за жодну — на ній порахувалась би дохідність за
      // відрізок, якого ніхто не вибирав.
      if (bad.length) {
        ctx.toast(`Не розібрав рядки: ${bad.join(", ")}. Очікую «РРРР-ММ-ДД число».`, false);
        return null;
      }
      if (!points.length) {
        ctx.toast("Нема чого вклеювати.", false);
        return null;
      }
      return {
        path: "fund-prices",
        body: { fund_id: id, points: points.map((p) => ({ date: p.date, price: p.value })) },
        msg: `Вклеєно позначок: ${points.length}`,
      };
    });
  });
}
