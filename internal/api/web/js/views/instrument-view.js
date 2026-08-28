// Панелі позиції: шість кроків у тому порядку, у якому з інструментом і
// працюють — стан, що маю, що далі, що зробити, записати, умови.
//
// ЩО ТУТ ЗМІНИЛОСЬ І ЧОМУ
//
// Спершу дані жили в «Активах», форми — в «Записати», умови — в
// «Політиці»: щоб попрацювати з ОВДП, доводилось стрибати між трьома
// розділами. Тоді це звели у ВОРОНКУ ВИДУ — одну сторінку «ОВДП» із
// шести кроків.
//
// Тепер воронка належить не виду, а САМІЙ ПОЗИЦІЇ, і це другий крок тієї
// самої думки. Вид був найдрібнішою сутністю, яку вміло дерево, але
// працюють не з «ОВДП» — працюють з папером, що гаситься в березні.
// Рядок майстер-списку і є цим папером; вибір паперу автоматично є
// вибором виду, а зріз по виду дають чипи над списком.
//
// ЩО ЛИШИЛОСЬ ВИДОВИМ, І ЦЕ НАВМИСНО
//
// Панель «Що зробити» показує зріз СВОГО ВИДУ, а не самої позиції, і так
// і підписана. Помічник ранжує ОВДП, фонд, вклад і НПФ однією реальною
// дохідністю — це головне, що він уміє, — і питання «що взяти» не має
// сенсу для одного паперу окремо: відповідь на нього завжди «а порівняно
// з чим». Повне порівняння лишилось у «Роботі → Що купити».
//
// Панель «Записати» теж видова: форма купівлі ОВДП однакова для всіх
// паперів, і стояти їй у позиції правильно — саме звідси докуповують той
// самий папір. Вхід для паперу, якого ще НЕМАЄ в списку, — окрема панель
// «Записати нове» у зведеному рядку.
//
// ЩО СЮДИ НЕ ПЕРЕЇХАЛО
//
// «Виписка» й «Звірка» лишились пакетними входами в «Грошах»: виписка
// Inzhur розкладається на сертифікати, лот ОВДП і рух грошей — три гілки в
// handleImportInzhur, — тож у панель «Фонду» вона влізла б лише збрехавши
// на дві третини.

import {
  esc, pct, money as fmtMoney, uah2 as fmtUAH, dayMonth, curSym,
} from "../format.js";
import { tile, empty } from "../components.js";
import { PAYOUT_LABEL } from "../constants.js";
import { routeFor } from "../routes.js";
import { wireCrud } from "../crud.js";
import { wireRefs } from "../refs.js";
import { wireDisclosures } from "../disclosure.js";
import {
  positionsTableHTML, loadPositionsData, wirePositionRows,
} from "./positions.js";
import { bondBuyFormHTML, bondSaleFormHTML } from "./bonds.js";
import { depositFormHTML } from "./deposits.js";
import { npfDetailHTML } from "../npf.js";
import {
  reserveTilesHTML, reserveJournalHTML, reserveFormHTML, reserveFields, reserveBody,
} from "./money-cards.js";
import { reinvestHTML, reserveFillHTML, wireReinvest, loadReinvest } from "./now-view.js";
import { kindTasksHTML } from "./tasks.js";
import { switchHTML, loadSwitch, wireSwitch } from "./switch.js";
import { kindOfItem } from "../master.js";

// Вид інструмента живе ТУТ, а не в nav.js: дерево навігації не має знати
// домену. Ключ ліворуч — підрозділ адреси, праворуч — те, чим цей вид
// зветься в даних.
//
// Множина й однина розходяться не випадково: позиції й поради оперують
// одниною (kind: "bond"), а частки й дохідності по видах — множиною
// ("bonds"), бо там це ІМʼЯ ГРУПИ, а не одного паперу. Мапа тримає обидва,
// щоб жодна сторінка не вгадувала.
const KINDS = {
  bond: { kind: "bond", group: "bonds", title: "ОВДП", sumKey: "nominal_uah_eq" },
  fund: { kind: "fund", group: "funds", title: "Фонди", sumKey: "funds_uah" },
  npf: { kind: "npf", group: "npf", title: "НПФ", sumKey: "npf_uah" },
  deposit: { kind: "deposit", group: "deposits", title: "Вклади", sumKey: "deposits_uah" },
};

// Куди веде панель «Умови» — сторінки політики, які цим видом керують.
//
// Форма запису та сама, що в ACTIONS (views/tasks.js): { to, label }.
// Доти тут була пара в масиві, і це був другий спосіб записати ту саму
// думку — «адреса плюс підпис посилання». Ціна другого способу не
// стилістична: web-routes-check.mjs шукає адреси саме за полем `to`, і
// таблиця в іншій формі лишалась би невидимою для перевірки, тобто описка
// в ній не ловилась би нічим.
const TERMS = {
  bond: [
    { to: "policy/mix", label: "Цільова частка й ліміти" },
    { to: "policy/assumptions", label: "Припущення про ставки" },
  ],
  fund: [
    { to: "policy/mix", label: "Цільова частка й ліміти" },
    { to: "settings/refs", label: "Каталог фондів" },
  ],
  npf: [
    { to: "policy/instruments", label: "Умови реінвесту" },
    { to: "settings/refs", label: "Пенсійні рахунки" },
  ],
  deposit: [
    { to: "policy/instruments", label: "Мінімум і ставка вкладу" },
    { to: "policy/mix", label: "Цільова частка" },
  ],
  reserve: [
    { to: "policy/reserve", label: "Витрати, запас і стеля поповнення" },
  ],
};

function termsHTML(kind) {
  const links = (TERMS[kind] || []).map(({ to, label }) =>
    `<a class="lnk" href="${routeFor(to)}">${esc(label)}</a>`).join(" · ");
  return `<div class="card"><div class="sub">Чим керується те, що радить помічник:
    ${links}</div></div>`;
}

/** Сирий рядок цієї позиції — той самий об'єкт, з якого малюється
 *  таблиця й рахувався майстер-список.
 *
 *  Шукається за ключем адреси, а не за індексом: індекс міняється від
 *  кожної покупки, а «bond:UA4000231625» лишається собою, доки папір
 *  існує. Порожнеча тут законна: закладку могли поставити на папір, який
 *  уже погашений, — і панель мусить сказати це словами, а не показати
 *  нулі. */
function sourceOf(ctx, d, kind, key) {
  const s = ctx.summary || {};
  if (kind === "bond") return (d.positions || []).find((p) => p.isin === key);
  if (kind === "fund") return (s.funds || []).find((f) => f.fund === key);
  if (kind === "npf") return (s.npf || []).find((n) => n.name === key);
  if (kind === "deposit") return (d.deposits || []).find((t) => String(t.id) === key);
  return null;
}

/** Панель «Стан» ОДНІЄЇ позиції: чотири числа, усі готові.
 *
 *  Жодне з них тут не рахується — ні вартість, ні дохідність, ні строк.
 *  Кожне приходить із того самого документа, з якого його бере таблиця
 *  нижче й рядок майстер-списку ліворуч; порахувати їх «на місці» було б
 *  третьою копією тих самих величин на одному екрані.
 *
 *  Порожнє число дає прочерк, а не нуль. Найгостріше це на папері, якого
 *  немає в довіднику НБУ: номінал і строк у нього справді невідомі, і
 *  нуль там читався б як «нічого не повернеться».
 *
 *  ЧЕТВЕРТА ПЛИТКА В КОЖНОГО ВИДУ СВОЯ, і це не непослідовність: в ОВДП
 *  головне питання «коли повернеться тіло», у фонді — «скільки їх і
 *  почім», у НПФ — «коли можна забрати», у вкладі — «коли платять».
 *  Однакова плитка на всіх чотирьох означала б показати найважливіше
 *  лише одному з них. */
function positionTilesHTML(ctx, kind, row) {
  if (!row) {
    return `<div class="card">${empty("Цієї позиції більше немає",
      "Папір погашено, фонд закрито або запис видалено. Список ліворуч показує те, "
      + "що є зараз.")}</div>`;
  }
  const yieldTile = (real, basis) => tile("Реальна дохідність",
    real ? pct(real) : "—",
    real ? `<div class="sub">після податку й знецінення${
      basis ? ` · ${esc(basis)}` : ""}</div>` : "");

  if (kind === "bond") {
    return `<div class="tiles flush">
      ${tile("Номінал", row.unknown ? "—" : fmtMoney(row.nominal), "", { hero: true })}
      ${tile("Вкладено", fmtMoney(row.invested),
    `<div class="sub-xs">${row.qty} шт.</div>`)}
      ${yieldTile(row.real_pct, row.yield_basis)}
      ${tile("Погашення", row.unknown ? "—" : esc(row.maturity),
    row.unknown
      ? `<div class="sub-xs t-warn">немає в довіднику НБУ</div>`
      : `<div class="sub">через ${row.days_to_maturity} дн.</div>`)}
    </div>`;
  }
  if (kind === "fund") {
    return `<div class="tiles flush">
      ${tile("Вартість", fmtUAH(row.market_value), "", { hero: true })}
      ${tile("Собівартість", fmtUAH(row.cost_basis))}
      ${yieldTile(row.real_pct, row.yield_basis)}
      ${tile("Сертифікатів", String(row.qty),
    `<div class="sub-xs">по ${(row.last_price || 0).toFixed(4)} ${curSym(row.currency)}${
      row.last_price_date ? ` від ${dayMonth(row.last_price_date)}` : ""}</div>`)}
    </div>`;
  }
  if (kind === "npf") {
    return `<div class="tiles flush">
      ${tile("Вартість", fmtUAH(row.value_uah), "", { hero: true })}
      ${tile("Внесено", fmtUAH(row.cost_uah))}
      ${yieldTile(row.real_pct, row.yield_basis)}
      ${tile("Доступ", row.access_date ? esc(row.access_date) : "—",
    row.access_date ? "" : `<div class="sub-xs">дата не задана в довіднику</div>`)}
    </div>`;
  }
  return `<div class="tiles flush">
    ${tile("Залишок", fmtMoney(row.balance || row.principal), "", { hero: true })}
    ${tile("Ставка", pct(row.rate_pct),
    `<div class="sub-xs">податок ${pct(row.tax_pct)}</div>`)}
    ${tile("Виплата", esc(PAYOUT_LABEL[row.payout] || row.payout || "—"))}
    ${tile("Погашення", esc(row.maturity_date || "—"))}
  </div>`;
}

/** Панель «Що далі» ОДНІЄЇ позиції: найближча подія саме її.
 *
 *  Сигнали, що вимагають РІШЕННЯ (внесок прострочено, вклад гаситься),
 *  тут не повторюються: вони вже стоять смугою задач на панелі «Стан», і
 *  другий раз тим самим текстом — це не наголос, а шум. */
function nextForPositionHTML(ctx, kind, row) {
  if (!row) return "";
  const line = (when, what) =>
    `<div class="pv-row"><span class="muted">${esc(when)}</span><span>${what}</span></div>`;

  if (kind === "bond" && row.next_pay_date) {
    return `<div class="card"><h2>Найближча виплата</h2>
      ${line(row.next_pay_date, fmtMoney(row.next_pay_amount))}
      <div class="sub">Тіло повертається ${esc(row.maturity)}. Повний календар —
        у «Плані».</div></div>`;
  }
  if (kind === "deposit" && row.maturity_date) {
    return `<div class="card"><h2>Коли закінчується</h2>
      ${line(row.maturity_date, esc(PAYOUT_LABEL[row.payout] || ""))}
      <div class="sub">Поповнення й дострокове закриття — у розкритті рядка
        на панелі «Що маю».</div></div>`;
  }
  if (kind === "fund" && row.next_payout) {
    return `<div class="card"><h2>Найближча виплата</h2>
      ${line(row.next_payout, "дивіденд")}</div>`;
  }
  if (kind === "npf" && row.access_date) {
    return `<div class="card"><h2>Коли можна забрати</h2>
      ${line(row.access_date, "")}
      <div class="sub">До цієї дати гроші не виводяться — це умова рахунку,
        а не порада.</div></div>`;
  }
  return `<div class="card">${empty("Попереду поки нічого",
    "Тут з'являться найближчі виплати й строки цієї позиції, щойно вони будуть.")}</div>`;
}

// ЧОГО ТУТ БІЛЬШЕ НЕМАЄ, І ЧОМУ
//
// kindTilesHTML, nextForKindHTML і itemsOfKind малювали стан ВИДУ:
// сума по всіх ОВДП, частка виду в портфелі, кількість позицій, драбина
// виду. Вони пішли разом зі сторінкою виду, а не разом із їхніми
// доводами — доводи чинні й переїхали в positionTilesHTML вище:
//
//   — жодне число не рахується тут; сума, частка й дохідність приходять
//     готовими зі стану. Саме заради цього в документі свого часу
//     з'явився kind_yield_real_pct: порахувати середню по виду «на
//     місці» було б на кілька рядків коротше й завело б п'яту копію
//     дохідностей у JS (state/capital.go);
//   — відсутнє число означає «нема чого міряти», а не нуль. Вид, якого в
//     портфелі немає, показував прочерк — тепер прочерк показує позиція,
//     у якої немає дохідності.
//
// Зведення по видах не зникло як питання: на нього відповідає
// «Портфель цілком → Структура», де види стоять поруч і порівнюються.
// Показувати його ще й у кожній позиції означало б відповідати на
// питання про вид у місці, яке питає про папір.

/** Крок 5 — форми запису. Усі до одної — наявні функції, викликані з того
 *  самого контексту, з якого їх кликав розділ «Записати». Жодної нової
 *  форми ця воронка не заводить. */
function writeHTML(ctx, spec, d) {
  if (spec.kind === "bond") {
    return `<div class="card"><h3>Нова покупка</h3>${bondBuyFormHTML(ctx)}</div>
      <div class="card"><h3>Продаж на вторинному ринку</h3>${bondSaleFormHTML(ctx, d.lots)}</div>`;
  }
  if (spec.kind === "deposit") {
    return `<div class="card"><h3>Відкрити вклад</h3>${depositFormHTML(ctx)}
      <div class="sub">Поповнення й дострокове закриття — у розкритті позиції на кроці 2.</div></div>`;
  }
  if (spec.kind === "npf") {
    // Форма НПФ не своя: внесок мусить цілитись у конкретний рахунок, тож
    // це той самий npfDetailHTML, що й у рядку деталей. Саме тому на цій
    // сторінці таблиця малюється БЕЗ розкриття — інакше форма стояла б на
    // екрані двічі й обидві підв'язав би wireNPF.
    const rows = (ctx.summary || {}).npf || [];
    if (!rows.length) {
      return `<div class="card">${empty("Рахунків ще немає",
        "Пенсійний рахунок заводиться в довідниках — там задаються ставка, податок і дата доступу.",
        { href: routeFor("settings/refs"), label: "Довідники" })}</div>`;
    }
    return rows.map((n) => `<div class="card"><h3>${esc(n.name)}</h3>
      ${npfDetailHTML(ctx, n)}</div>`).join("");
  }
  // Фонди. Форми немає ЗА ПОБУДОВОЮ, і крок лишається на місці саме щоб це
  // сказати: питання «а де додати фонд» виникає рівно тут.
  return `<div class="card">${empty("Сертифікати не вносять руками",
    "Журнал веде виписка: купівлі, продажі й дивіденди приходять файлом і правляться "
    + "тільки видаленням. Два джерела правди — виписка й рука — розійшлися б, і розійшлися б тихо.",
    { href: routeFor("money/import"), label: "Завантажити виписку" })}</div>`;
}

/** Панель позиції. Вид береться з id рядка (master.js), тож чотири види
 *  відрізняються рівно тим, що лежить у KINDS, — решта спільна.
 *
 *  Дані позицій приходять із оболонки (ctx.positions): вона вже
 *  завантажила їх, щоб намалювати майстер-список, і другий обхід тих
 *  самих восьми маршрутів коштував би кадр на кожному перемиканні
 *  панелі. */
export async function positionPane(ctx, main) {
  const spec = KINDS[kindOfItem(ctx.item)];
  if (!spec) {
    main.innerHTML = `<div class="card">${empty("Невідомий вид позиції",
      "Адреса називає вид, якого застосунок не знає.")}</div>`;
    return;
  }
  // Пороги перекладання — лише ОВДП: у сертифіката немає ані чистої
  // ціни, ані НКД, а вклад розривається за договором, не за ринком
  // (аргумент цілком — у шапці domain/switch.go).
  const loaders = [ctx.positions ? Promise.resolve(ctx.positions) : loadPositionsData(ctx),
    loadReinvest(ctx)];
  if (spec.kind === "bond") loaders.push(loadSwitch(ctx));
  const [d] = await Promise.all(loaders);

  main.innerHTML = panePaneHTML(ctx, spec, d);

  wirePositionRows(ctx, main, d);
  wireReinvest(ctx, main);
  if (spec.kind === "bond") wireSwitch(ctx, main);
  // Кнопки «+» проводить сама wireReinvest вище: їх малює reinvestHTML,
  // тобто той самий модуль. Один малює, той самий і проводить.
}

function panePaneHTML(ctx, spec, d) {
  const rowDetail = spec.kind !== "npf";
  const row = sourceOf(ctx, d, spec.kind, ctx.key);
  switch (ctx.pane) {
  case "state":
    // Смуга задач стоїть саме на першій панелі: те, що чекає рішення,
    // мусить трапитись на очі до того, як почнеш читати числа. Вона
    // видова — задачі приходять із бекенда по виду, а не по позиції, — і
    // це чесніше, ніж мовчати про сусідній папір того ж виду.
    return kindTasksHTML(ctx, spec.kind)
      + positionTilesHTML(ctx, spec.kind, row);
  case "have":
    // Одна позиція, а не весь вид: рядок ліворуч уже сказав, про кого
    // мова. Розкриття показує лоти, продажі, поповнення — те, з чого ця
    // позиція складається.
    return positionsTableHTML(ctx, d.positions, d.lots, d.sales, d.deposits, {
      only: ctx.item, title: spec.title, rowDetail, empty: EMPTY[spec.kind],
    });
  case "next":
    return nextForPositionHTML(ctx, spec.kind, row);
  case "do":
    return (reinvestHTML(ctx, { kinds: [spec.kind], title: "Що взяти з цього виду" })
      || `<div class="card">${empty("Порад по цьому виду немає",
        "Помічник радить лише те, що проходить за твоїми умовами. Порівняти види між "
        + "собою можна там, де вони стоять поруч.",
        { href: routeFor("work/buy/main"), label: "Що купити" })}</div>`)
      + (spec.kind === "bond" ? switchHTML() : "")
      + `<div class="card"><div class="sub">Порівняти з іншими видами —
        <a class="lnk" href="${routeFor("work/buy/main")}">у «Що купити»</a>: там ОВДП, фонд,
        вклад і НПФ стоять поруч і міряні однією реальною дохідністю.</div></div>`;
  case "record":
    return writeHTML(ctx, spec, d);
  default:
    return termsHTML(spec.kind);
  }
}

// Порожні стани сторінок виду: кожна мусить пояснити свою порожнечу своїм
// голосом — «позиція з'явиться після покупки паперу» на сторінці вкладів
// було б порадою не в те місце.
const EMPTY = {
  bond: {
    text: "Папери з'являться тут після першої покупки.",
    action: { href: routeFor("instr/bonds/write"), label: "Записати покупку" },
  },
  fund: {
    text: "Сертифікати заводить імпорт виписки — руками їх не вносять.",
    action: { href: routeFor("money/import"), label: "Завантажити виписку" },
  },
  npf: {
    text: "Пенсійний рахунок з'явиться тут, коли буде заведений у довідниках.",
    action: { href: routeFor("settings/refs"), label: "Довідники" },
  },
  deposit: {
    text: "Вклади з'являться тут після першого відкритого.",
    action: { href: routeFor("instr/deposits/write"), label: "Відкрити вклад" },
  },
};

// itemsOfKind рахувала позиції виду для плитки «Позицій» — з тих самих
// даних, з яких будувалась таблиця, щоб число над таблицею й сама
// таблиця не розійшлись. Плитки виду більше немає (довід вище), а
// «скільки їх» тепер каже чип над майстер-списком — і рахує його
// chipsOf(master.js) з тих самих рядків, які показує список. Правило
// збереглось, місце змінилось.

// Експортів «на вид» — bonds/funds/npf/deposits — тут більше немає. Вони
// були сторінками, а сторінка виду перестала існувати: вид тепер зріз
// списку, а не адреса. Хто прийшов за ними зі старої закладки, потрапляє
// на перший рядок цього виду — таблиця переїздів у routes.js.

/** Резерв — п'ять панелей, а не шість, і це не непослідовність.
 *
 *  Панелі «Що зробити» в нього немає ЗА ПРИРОДОЮ: /api/reinvest виду
 *  «резерв» не віддає взагалі, бо реальної дохідності в резерву не
 *  існує — це гроші, і в сьогоднішніх гривнях вони дають мінус
 *  знецінення. Поставити його в один стовпчик із паперами можна було б
 *  лише вигадавши число, а вигадане число в головній колонці гірше за
 *  чесну відсутність (те саме сказано біля Locked у
 *  handlers_reinvest.go).
 *
 *  Замість неї панель «Що далі» несе єдину пораду, яка тут доречна:
 *  скільки з вільних грошей варто відкласти перш ніж купувати.
 *
 *  І В РЕЗЕРВУ НЕМАЄ СМУГИ ЗАДАЧ, на відміну від решти позицій. У них
 *  смуга й панель дії відповідають на різне: смуга каже, що чекає
 *  рішення, а панель показує, що з цього виду варто взяти. Тут це одне й
 *  те саме речення. Задача виду «резерв» у черзі рівно одна —
 *  reserve-fill (state_tasks.go), — і панель «Що далі» вже несе її: той
 *  самий заголовок, та сама сума, лише повніша проза. Смуга дописувала б
 *  другий такий рядок за півекрана від першого.
 *
 *  Ціна названа вголос: проза резерву й далі існує у ДВОХ мовах — тут і в
 *  state_tasks.go, — і вони вже розійшлись; Home Assistant читає задачі,
 *  тобто бачить коротший варіант. Звести їх в одну можна лише перенісши
 *  текст у Go, і це окрема робота. А щойно з'явиться ДРУГА задача виду
 *  «резерв», рішення прибрати смугу треба переглянути: тоді вона
 *  перестане бути дублем. */
export async function reservePane(ctx, main) {
  const ops = await ctx.soft("reserve", []);
  main.innerHTML = reservePaneHTML(ctx, ops);
  wireCrud(ctx, main, {
    resource: "reserve", form: "#resForm", title: "Рух резерву", rows: ops,
    fields: reserveFields, body: reserveBody,
    msg: {
      add: "Рух резерву записано", edit: "Рух резерву виправлено",
      del: "Рух видалено",
    },
  });
  wireRefs(main);
  wireDisclosures(main);
}

function reservePaneHTML(ctx, ops) {
  switch (ctx.pane) {
  case "state":
    return reserveTilesHTML(ctx) || `<div class="card">${empty(
      "Резерву ще немає",
      "Резерв — те, що доступне миттєво й без втрат, коли гроші раптом знадобились.",
      { href: routeFor("portfolio/reserve/record"), label: "Записати рух" })}</div>`;
  case "have":
    return reserveJournalHTML(ops);
  case "next":
    return reserveFillHTML(ctx) || `<div class="card"><div class="sub">
      Поповнювати зараз нічого: або запас уже зібраний, або вільних грошей на рахунку немає.
      </div></div>`;
  case "record":
    return reserveFormHTML(ctx);
  default:
    return termsHTML("reserve");
  }
}
