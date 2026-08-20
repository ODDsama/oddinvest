// Розділ «Інструменти» — воронка на кожен вид: одна сторінка, шість кроків
// у тому порядку, у якому з інструментом і працюють.
//
// ЩО ТУТ ЗМІНИЛОСЬ І ЧОМУ
//
// Доти дані жили в «Активах», форми — в «Записати», умови — в «Політиці».
// Щоб попрацювати з ОВДП, треба було стрибати між трьома розділами, і
// питання «як я працюю з ОВДП» не мало жодного місця, де воно тримається
// одним поглядом.
//
// Розділ «Записати» цим розпускається, і його власна аргументація
// (nav.js, стара шапка) варта того, щоб відповісти на неї прямо. Він
// існував тому, що форми були розсіяні по ПОВЕРХНЯХ: купівля стояла в
// «Портфелі», бо там журнал лотів, — тобто посеред ЧУЖОГО питання. Воронка
// групує по СУТНОСТІ; вісь групування змінилась із дієслова («записати»)
// на іменник («ОВДП»), а правило «один підрозділ — одна сутність» лишилось
// чинним. Ціна відома й прийнята: дієслівного входу більше немає, і той,
// хто думає «мені треба щось записати», мусить спершу згадати, ЩО саме.
//
// ЩО СЮДИ НЕ ПЕРЕЇХАЛО
//
// «Виписка» й «Звірка» лишились пакетними входами в «Грошах»: виписка
// Inzhur розкладається на сертифікати, лот ОВДП і рух грошей — три гілки в
// handleImportInzhur, — тож у воронку «Фонди» вона влізла б лише збрехавши
// на дві третини.
//
// Порівняння ІНСТРУМЕНТІВ МІЖ СОБОЮ теж лишилось на місці — в «Що купити».
// Крок «Що зробити» показує зріз свого виду й посилається туди: помічник
// ранжує ОВДП, фонд, вклад і НПФ однією реальною дохідністю, і це головне,
// що він уміє. Розрізати його по воронках означало б зламати рівно те,
// заради чого він існує.

import { esc, pct, uah2 as fmtUAH } from "../format.js";
import { tile, empty } from "../components.js";
import { routeFor } from "../routes.js";
import { onSubmit, onDelete } from "../forms.js";
import { wireDisclosures } from "../disclosure.js";
import {
  positionsTableHTML, loadPositionsData, wirePositionRows,
} from "./positions.js";
import { bondBuyFormHTML, bondSaleFormHTML } from "./bonds.js";
import { depositFormHTML, closedDepositsHTML } from "./deposits.js";
import { npfDetailHTML } from "../npf.js";
import {
  reserveTilesHTML, reserveJournalHTML, reserveFormHTML,
} from "./money-cards.js";
import { reinvestHTML, reserveFillHTML, wireReinvest, loadReinvest } from "./now-view.js";
import { kindTasksHTML } from "./tasks.js";
import { wireBasket } from "./basket.js";
import { stepRailHTML, stepper, wireSteps } from "./steps.js";

// Вид інструмента живе ТУТ, а не в nav.js: дерево навігації не має знати
// домену. Ключ ліворуч — підрозділ адреси, праворуч — те, чим цей вид
// зветься в даних.
//
// Множина й однина розходяться не випадково: позиції й поради оперують
// одниною (kind: "bond"), а частки й дохідності по видах — множиною
// ("bonds"), бо там це ІМʼЯ ГРУПИ, а не одного паперу. Мапа тримає обидва,
// щоб жодна сторінка не вгадувала.
const KINDS = {
  bonds: { kind: "bond", group: "bonds", title: "ОВДП", sumKey: "nominal_uah_eq" },
  funds: { kind: "fund", group: "funds", title: "Фонди", sumKey: "funds_uah" },
  npf: { kind: "npf", group: "npf", title: "НПФ", sumKey: "npf_uah" },
  deposits: { kind: "deposit", group: "deposits", title: "Вклади", sumKey: "deposits_uah" },
};

// Куди веде крок «Умови» — сторінки політики, які цим видом керують.
const TERMS = {
  bond: [["policy/mix", "Цільова частка й ліміти"], ["policy/assumptions", "Припущення про ставки"]],
  fund: [["policy/mix", "Цільова частка й ліміти"], ["settings/refs", "Каталог фондів"]],
  npf: [["policy/instruments", "Умови реінвесту"], ["settings/refs", "Пенсійні рахунки"]],
  deposit: [["policy/instruments", "Мінімум і ставка вкладу"], ["policy/mix", "Цільова частка"]],
  reserve: [["policy/reserve", "Витрати, запас і стеля поповнення"]],
};

function termsHTML(kind) {
  const links = (TERMS[kind] || []).map(([to, label]) =>
    `<a class="lnk" href="${routeFor(to)}">${esc(label)}</a>`).join(" · ");
  return `<div class="card"><div class="sub">Чим керується те, що радить помічник:
    ${links}</div></div>`;
}

/** Крок 1 — стан виду чотирма плитками.
 *
 *  Жодного числа тут не рахується: сума, частка й дохідність приходять
 *  готовими зі стану. Порахувати середню дохідність по виду «на місці»
 *  було б на кілька рядків коротше й завело б п'яту копію дохідностей у
 *  JS — рівно ту помилку, проти якої існує state/capital.go. Саме тому в
 *  документі з'явилось kind_yield_real_pct.
 *
 *  Відсутній ключ дохідності означає «нема чого міряти», а не нуль: вид,
 *  якого в портфелі немає, показує прочерк, а не «0,0%». */
function kindTilesHTML(ctx, spec, items) {
  const s = ctx.summary || {};
  const sum = s[spec.sumKey] || 0;
  // Резерву серед SPECS немає — його сторінку малює money-cards.js, і
  // частку він бере з summary.reserve. Тут лишаються самі інвестиції, а
  // отже й знаменник у всіх один: портфель без подушки.
  const share = (s.rebalance || [])
    .find((r) => r.dimension === "kind" && r.key === spec.group);
  const real = (s.kind_yield_real_pct || {})[spec.group];
  const n = items.length;
  return `<div class="tiles flush">
    ${tile(spec.title, fmtUAH(sum), "", { hero: true })}
    ${tile("Частка портфеля", share ? pct(share.current_pct) : "—",
    share && share.target_pct
      ? `<div class="sub">ціль ${pct(share.target_pct)}</div>`
      : `<div class="sub-xs">від портфеля без резерву</div>`)}
    ${tile("Позицій", n ? String(n) : "—")}
    ${tile("Реальна дохідність", real ? pct(real) : "—",
    real ? `<div class="sub">після податку й знецінення</div>` : "")}
  </div>`;
}

/** Крок 3 — що з цим видом станеться найближчим часом.
 *
 *  Календар і драбина беруться зі стану й звужуються до цього виду. Сигнали,
 *  що вимагають РІШЕННЯ (внесок прострочено, вклад гаситься, вікно фонду
 *  закривається), тут не повторюються: вони вже стоять нагорі сторінки
 *  задачею, і другий раз тим самим текстом — це не наголос, а шум. */
function nextForKindHTML(ctx, spec) {
  const s = ctx.summary || {};
  const rows = (s.ladder || []).filter((r) => r.year);
  const ladder = spec.kind === "bond" && rows.length
    ? `<div class="card"><h3>Коли повертається тіло</h3>${rows.slice(0, 6).map((r) =>
      `<div class="pv-row"><span class="muted">${r.year}</span>
        <span>${fmtUAH(r.uah || 0)}</span></div>`).join("")}
      <div class="sub">Драбина цілком — у «Портфелі → Ліміти».</div></div>`
    : "";
  const np = s.next_payment;
  const pay = np
    ? `<div class="card"><h3>Найближча виплата</h3>
        <div class="pv-row"><span class="muted">${esc(np.date)}</span>
          <span>${Number(np.amount).toLocaleString("uk-UA", { minimumFractionDigits: 2 })}</span></div>
        <div class="sub">Повний календар — у «Плані».</div></div>`
    : "";
  return (ladder + pay) || `<div class="card">${empty("Попереду поки нічого",
    "Тут з'являться найближчі виплати й строки, щойно вони будуть.")}</div>`;
}

/** Крок 5 — форми запису. Усі до одної — наявні функції, викликані з того
 *  самого контексту, з якого їх кликав розділ «Записати». Жодної нової
 *  форми ця воронка не заводить. */
function writeHTML(ctx, spec, d) {
  if (spec.kind === "bond") {
    return `<div class="card"><h3>Нова покупка</h3>${bondBuyFormHTML(ctx, d.lots)}</div>
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

/** Сторінка одного інструмента. Чотири види відрізняються рівно тим, що
 *  лежить у KINDS, — решта спільна. */
function funnelPage(sub) {
  const spec = KINDS[sub];
  return async function render(ctx, main) {
    const [d] = await Promise.all([loadPositionsData(ctx), loadReinvest(ctx)]);
    const path = `instr/${sub}`;
    const keys = ["state", "mine", "next", "act", "write", "terms"];
    const step = stepper(keys);
    const rowDetail = spec.kind !== "npf";
    const table = positionsTableHTML(ctx, d.positions, d.lots, d.sales, d.deposits, {
      kinds: [spec.kind], title: spec.title, rowDetail,
      empty: EMPTY[spec.kind],
    });
    const act = reinvestHTML(ctx, { kinds: [spec.kind], title: "Що взяти з цього виду" })
      || `<div class="card">${empty("Порад по цьому виду немає",
        "Помічник радить лише те, що проходить за твоїми умовами. Порівняти види між "
        + "собою можна там, де вони стоять поруч.",
        { href: routeFor("now/buy"), label: "Що купити" })}</div>`;

    main.innerHTML = `
      ${stepRailHTML(path, keys, ctx.anchor || "state")}
      ${kindTasksHTML(ctx, spec.kind)}
      ${step("state", kindTilesHTML(ctx, spec, itemsOfKind(d, spec)))}
      ${step("mine", table + (spec.kind === "deposit" ? closedDepositsHTML(ctx, d.deposits) : ""))}
      ${step("next", nextForKindHTML(ctx, spec))}
      ${step("act", act + `<div class="card"><div class="sub">Порівняти з іншими видами —
        <a class="lnk" href="${routeFor("now/buy")}">у «Що купити»</a>: там ОВДП, фонд, вклад і
        НПФ стоять поруч і міряні однією реальною дохідністю.</div></div>`)}
      ${step("write", writeHTML(ctx, spec, d))}
      ${step("terms", termsHTML(spec.kind))}`;

    wirePositionRows(ctx, main, d.deposits);
    wireReinvest(ctx, main);
    // Кнопки «+» малює reinvestHTML, а слухач до них живе в basket.js. Доти
    // wireBasket кликала ЛИШЕ сторінка кошика — тобто рівно те місце, де
    // жодного data-bskadd немає, — і «+» у списку порад не робив нічого.
    // Проводити треба там, де кнопки намальовані.
    wireBasket(ctx, main);
    wireSteps(main);
  };
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

// Скільки позицій цього виду. Рахуємо з тих самих даних, з яких будується
// таблиця, — інакше плитка «Позицій» і таблиця під нею могли б розійтись.
function itemsOfKind(d, spec) {
  if (spec.kind === "bond") return d.positions || [];
  if (spec.kind === "deposit") return (d.deposits || []).filter((x) => !x.closed_date);
  return [];
}

export const bonds = funnelPage("bonds");
export const funds = funnelPage("funds");
export const npf = funnelPage("npf");
export const deposits = funnelPage("deposits");

/** Резерв — п'ять кроків, а не шість, і це не непослідовність.
 *
 *  Кроку «Що зробити» в нього немає ЗА ПРИРОДОЮ: /api/reinvest виду
 *  «резерв» не віддає взагалі, бо реальної дохідності в резерву не існує —
 *  це гроші, і в сьогоднішніх гривнях вони дають мінус знецінення.
 *  Поставити його в один стовпчик із паперами можна було б лише вигадавши
 *  число, а вигадане число в головній колонці гірше за чесну відсутність
 *  (те саме сказано біля Locked у handlers_reinvest.go).
 *
 *  Замість нього крок «Що далі» несе єдину пораду, яка тут доречна:
 *  скільки з вільних грошей варто відкласти перш ніж купувати.
 *
 *  І ЦЯ СТОРІНКА — ЄДИНА З П'ЯТИ БЕЗ СМУГИ ЗАДАЧ УГОРІ. У решти воронок
 *  смуга й крок дії відповідають на різне: смуга каже, що чекає рішення, а
 *  крок показує, що з цього виду варто взяти. Тут це одне й те саме речення.
 *  Задача виду «резерв» у черзі рівно одна — reserve-fill (state_tasks.go), —
 *  і крок «Що далі» вже несе її: той самий заголовок, та сама сума, лише
 *  повніша проза (частка від плану місяця й скільки ти вже поклав цього
 *  місяця). Смуга дописувала другий такий рядок за півекрана від першого.
 *
 *  Прибрана саме СМУГА, а не банер, і вибір не довільний: банер каже більше,
 *  а крок «Що далі» без нього лишився б або з підписом «поповнювати зараз
 *  нічого», який просто неправдивий, поки задача вимагає 5 770 ₴, або другим
 *  показом тієї ж задачі — тобто тим самим дублем, лише переставленим.
 *
 *  Ціна названа вголос: проза резерву й далі існує у ДВОХ мовах — тут і в
 *  state_tasks.go, — і вони вже розійшлись; Home Assistant читає задачі, тобто
 *  бачить коротший варіант. Звести їх в одну можна лише перенісши текст у Go,
 *  і це окрема робота. А щойно з'явиться ДРУГА задача виду «резерв», рішення
 *  прибрати смугу треба переглянути: тоді вона перестане бути дублем. */
export async function reserve(ctx, main) {
  const ops = await ctx.soft("reserve", []);
  const path = "instr/reserve";
  const keys = ["state", "mine", "next", "write", "terms"];
  const step = stepper(keys);
  const tiles = reserveTilesHTML(ctx) || `<div class="card">${empty(
    "Резерву ще немає",
    "Резерв — те, що доступне миттєво й без втрат, коли гроші раптом знадобились.",
    { href: routeFor("instr/reserve/write"), label: "Записати рух" })}</div>`;
  main.innerHTML = `
    ${stepRailHTML(path, keys, ctx.anchor || "state")}
    ${step("state", tiles)}
    ${step("mine", reserveJournalHTML(ops))}
    ${step("next", reserveFillHTML(ctx) || `<div class="card"><div class="sub">
      Поповнювати зараз нічого: або запас уже зібраний, або вільних грошей на рахунку немає.
      </div></div>`)}
    ${step("write", reserveFormHTML())}
    ${step("terms", termsHTML("reserve"))}`;
  onSubmit(ctx, main.querySelector("#resForm"), (f) => ({
    path: "reserve",
    body: {
      amount: f.amount.value.trim(), currency: f.currency.value,
      place: f.place.value.trim(), date: f.date.value, note: f.note.value.trim(),
    },
    msg: "Рух резерву записано",
  }));
  onDelete(ctx, main, "[data-delres]", (b) => ({
    path: "reserve/" + b.dataset.delres, msg: "Рух видалено",
  }));
  wireSteps(main);
  wireDisclosures(main);
}
