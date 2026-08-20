// Розділ «Огляд» — що робити зараз.
//
// Один екран рішення: чи є гроші, чи вистачає на папір, який саме папір
// і коли надійде наступна виплата. Більше тут немає нічого.
//
// Раніше цей розділ ніс дванадцять блоків — прогноз, пасивний дохід,
// фонди, чотири графіки й таблицю знімків, — і половина з них дублювала
// вміст інших вкладок. Кожен із них відповідав на цікаве питання, але не
// на те, з яким сюди заходять. Вони роз'їхались туди, де питання їхнє:
// склад і історія — у «Портфель», потоки й прогнози — у «Майбутнє».

import {
  esc, curSym, monthYearGen, dayMonth, pct, plural, capitalUAH, today,
  uah2 as fmtUAH, cur2 as fmtCur,
} from "../format.js";
import { infoBtn } from "../info.js";
import { tile, kindPill, empty } from "../components.js";
import { isOpen, remember } from "../uistate.js";
import { routeFor } from "../routes.js";
import { CONTRIB, contribTriad, shareOfNeed } from "../contrib.js";
import { basketHTML, wireBasket } from "./basket.js";
import { tasksHTML } from "./tasks.js";
import { allocationCardHTML } from "./allocation.js";

// Помічник реінвесту тягнеться раз на прохід, а читає його окрема картка.
let reinvest = [];

// Банер дії показуємо ЗАВЖДИ — він ніколи не порожній і завжди каже,
// що робити: почати, купити або накопичувати далі.
// Банера дії тут більше немає, і це не спрощення, а розширення. Він
// відповідав на питання розділу ОДНІЄЮ порадою за раз і мав дві кнопки без
// жодного слухача (data-go), тобто єдина CTA головної сторінки нічого не
// робила. Його три гілки — «почни», «можеш купити», «ще збираєш» — стали
// трьома задачами в views/tasks.js, де вони стоять поруч із рештою того,
// що теж потребує рішення.

// Виплати одного дня — це ОДИН прихід грошей, і питання до картки саме
// таке: скільки впаде на рахунок і коли. Доти кожен потік малювався
// окремим рядком, тож один день займав два, а список рвався на четвертому
// незалежно від того, що там за потоки.
//
// Гірше було з погашеннями: 18 листопада той самий папір платить купон
// 81,75 ₴ і повертає тіло 1 000 ₴, але друга половина не влазила в чотири
// рядки — і картка показувала 82 ₴ там, де прийде 1 082 ₴.
//
// Групуємо за датою І ВАЛЮТОЮ: складати гривню з доларом не можна навіть
// одного дня. Джерело — повний календар, а не top_payments: той обрізаний
// до N потоків ще на бекенді, тож після склеювання дат рядків лишалось би
// менше, ніж є днів.
function groupPayments(list, limit) {
  const out = [];
  const idx = new Map();
  for (const p of list) {
    const key = p.date + "|" + p.currency;
    let g = idx.get(key);
    if (!g) {
      if (out.length >= limit) continue;
      g = { date: p.date, currency: p.currency, amount: 0, n: 0 };
      idx.set(key, g);
      out.push(g);
    }
    g.amount += Number(p.amount) || 0;
    g.n++;
  }
  return out;
}

export function paymentsPreviewHTML(ctx) {
  const s = ctx.summary || {};
  // calendar — повний горизонт; top_payments лишається запасним джерелом
  // для старшого бекенда, який календаря ще не надсилав.
  const src = (s.calendar || []).length ? s.calendar : (s.top_payments || []);
  const rows = groupPayments(src, 4);
  const body = rows.length
    ? rows.map((p) => `<div class="pv-row"><span class="muted">${dayMonth(p.date)}${
        p.n > 1 ? ` <span class="sub-xs">· ${p.n} ${plural(p.n, "виплата", "виплати", "виплат")}</span>` : ""}</span>
        <span>${fmtCur(p.amount, p.currency)}</span></div>`).join("")
    : `<div class="sub">Виплат попереду немає.</div>`;
  return `<div class="card"><h2>Найближчі виплати</h2>${body}
    <div class="sub">Суми за день складені. Повний календар — у «Плані»</div></div>`;
}

// Що купити: папери, відранжовані за РЕАЛЬНОЮ дохідністю в сьогоднішніх
// гривнях. Показуємо кілька позицій ЗАВЖДИ — попередній варіант зникав
// саме тоді, коли ти плануєш наступний крок, а ще був таблицею-звалищем
// на весь довідник, тож тут свідомо лише верхівка.
// Порядок пропозицій на «Огляді»: ЗАМКНЕНЕ завжди нижче, далі за реальною
// дохідністю.
//
// Перша половина обов'язкова попри те, що сервер уже впорядкував список так
// само. Причина конкретна: фронтенд перевпорядковує його сам — і банер, і
// таблиця, — тобто має ВЛАСНИЙ порядок, який без урахування замка скасовував
// би серверний. НПФ із 31.7% ставав першим над вкладом із 6.8%, і банер радив
// би «внести в пенсійний» на прибулий купон — рівно те, чого пониження й мало
// не допустити.
//
// Дублювання правила в двох мовах тут неминуче (порядок потрібен обом), і саме
// тому воно назване вголос в обох місцях, а не лишене домовленістю. Одна
// функція на банер і на таблицю: два різні порядки на одному екрані означали
// б, що банер радить одне, а список під ним інше.
const byReal = (a, b) =>
  (a.locked ? 1 : 0) - (b.locked ? 1 : 0) || (b.real_pct || 0) - (a.real_pct || 0);

// Назва рядка поради. Валюту несе сума поруч, строк переїхав у розкриття,
// а вид інструмента каже пігулка — тож слова «вклад» і «сертифікат» у
// назві стали б повтором того, що вже видно.
function suggestName(r) {
  // Ставка лишається при вкладі: це умова договору, а не дохідність, і
  // саме вона відрізняє два вклади в одній валюті один від одного.
  if (r.kind === "deposit") return `${esc(r.label)} <span class="muted">${pct(r.rate_pct)}</span>`;
  // Замок у самій назві: без нього рядок читається як звичайна
  // альтернатива вкладу, а він нею не є ні на день.
  if (r.locked) return `${esc(r.label)} <span class="t-warn">🔒 до ${esc(r.locked_until || "?")}</span>`;
  return esc(r.label || r.isin);
}

// Поповнення резерву — ОКРЕМИЙ блок, а не рядок у ранжованому списку, і це
// не питання верстки.
//
// У списку є рівно одне головне число — реальна дохідність, і саме вона
// вирішує порядок. У резерву її немає: це гроші, і в сьогоднішніх гривнях
// вони дають мінус знецінення. Щоб поставити резерв у той самий стовпчик,
// довелося б або вписати вигадане число, або завести для нього особливий
// випадок у сортуванні — обидва шляхи прямо заборонені коментарем до
// Locked у handlers_reinvest.go, а там ішлося про інструмент, у якого
// дохідність хоча б справжня.
//
// СПОЖИВАЧ ЛИШИВСЯ ОДИН — крок «Що далі» на сторінці резерву
// (instrument-view.js). Зі сторінки «Що купити» цей банер прибраний: черга
// задач уже несе рядок reserve-fill із тим самим заголовком і тією ж сумою
// (state_tasks.go), тож на сусідніх сторінках стояло одне речення двічі. Два
// однакові речення — не наголос, а розсинхрон, що чекає нагоди: проза тут і
// проза там уже встигли розійтись (тут є «ти вже поклав N ₴», там немає).
//
// Числа приходять ГОТОВИМИ зі стану (reserve.fill_*). Порахувати частку
// тут було б на два рядки коротше й завело б другу копію арифметики
// резерву — рівно ту помилку, через яку колись плитка й картка показували
// різні числа.
export function reserveFillHTML(ctx) {
  const r = (ctx.summary || {}).reserve;
  if (!r || !(r.fill_now_uah > 0)) return "";
  // Що саме обмежило суму — стеля чи сам розрив. Мовчати про це не можна:
  // сума без причини читається як вимога, а не як стеля, яку людина сама
  // собі поставила.
  const capped = r.fill_month_uah >= (r.gap_uah || 0);
  // Уже відкладене цього місяця називаємо ЛИШЕ коли воно є: без нього сума
  // тут дорівнює місячній частці, і рядок «з них уже покладено 0» був би
  // шумом. А коли є — саме воно й пояснює, чому порада менша за частку.
  const done = r.fill_moved_uah > 0
    ? ` З місячної частки ${fmtUAH(r.fill_month_uah)} ти вже поклав ${
      fmtUAH(r.fill_moved_uah)} — лишилось стільки.` : "";
  const why = capped
    ? `Це все, чого бракує до цілі — ${fmtUAH(r.target_uah)}, тобто ${r.target_months} ${
      plural(r.target_months, "місяць", "місяці", "місяців")} витрат.${done}`
    // База — гроші МІСЯЦЯ, а не готівка на рахунках: подушку наповнюють із
    // нових грошей, і на малому рахунку стеля від готівки давала поради на
    // кшталт «відклади 2,48 ₴» при розриві в 359 500 ₴.
    : `${pct(r.fill_share_pct, 0)} від того, що заводить план цього місяця
       (${fmtUAH(r.fill_from_uah)}) — стеля, яку ти сам поставив. До цілі ще ${
  fmtUAH(r.gap_uah)}, решта грошей лишається на папери.${done}`;
  return `<div class="banner wait"><div class="b-ic">○</div><div class="b-tx">
    <div class="b-t">Спершу поповнити резерв — ${fmtUAH(r.fill_now_uah)}</div>
    <div class="b-s">${why} Сума в гривневому еквіваленті: у чому саме відкладати — вирішуєш ти.
      <a class="lnk" href="${routeFor("entry/reserve")}">Записати рух</a>.</div>
  </div></div>`;
}

/** Ранжовані поради. opts.kinds звужує до одного виду, opts.title міняє
 *  заголовок картки.
 *
 *  Звуження — для воронки інструмента, і саме звуження, а не окрема
 *  реалізація: рядок там мусить виглядати й рахуватись так само, як у
 *  спільному списку, інакше та сама пропозиція показувала б два різні
 *  числа на двох сторінках. Порівняння ВИДІВ між собою при цьому нікуди не
 *  дівається — воно живе в «Що купити» цілим, і воронка на нього
 *  посилається, а не підміняє його своїм зрізом. */
export function reinvestHTML(ctx, opts = {}) {
  const { kinds = null, title = "Що купити" } = opts;
  // По одній найкращій пропозиції на ТИП × ВАЛЮТУ. Раніше ключем була
  // сама валюта — чотири майже однакові папери не давали вибору, і
  // порівняти гривню з доларом було сенсом блоку. Тепер інструментів три,
  // і ключ лише за валютою ховав би два з них: усі гривневі пропозиції
  // згорталися б в одну, і фонд із облігацією просто зникали б за
  // вкладом. Порівнювати треба і валюти, і природу інструмента.
  const byKey = new Map();
  for (const r of reinvest || []) {
    if (kinds && !kinds.includes(r.kind || "bond")) continue;
    const k = `${r.kind || "bond"}|${r.currency}`;
    if (!byKey.has(k)) byKey.set(k, r);
  }
  const rows = [...byKey.values()].sort(byReal);
  if (!rows.length) return "";
  const s = ctx.summary || {};
  const purse = Object.entries(s.brokers || {})
    .flatMap(([b, byCur]) => Object.entries(byCur)
      .filter(([, v]) => v > 0)
      .map(([c, v]) => `${esc(b)} ${fmtCur(v, curSym(c))}`)).join(" · ");
  // Рядок пропозиції. Одне головне число — РЕАЛЬНА дохідність: саме за нею
  // список і впорядкований, і саме вона порівнює гривню з доларом. Решта
  // (номінальна, підстава, розклад по брокерах) — у розкритті: доти вона
  // стояла в рядку дрібним текстом, і шість пропозицій по чотири рядки
  // читались як суцільна стіна, у якій не видно головного.
  const item = (r) => {
    const key = `${r.kind || "bond"}|${r.currency}`;
    const open = isOpen(OPEN_SCOPE, key);
    const kind = ["fund", "deposit", "npf"].includes(r.kind) ? r.kind : "bond";
    const cost = r.cost_per_bond ? fmtCur(Number(r.cost_per_bond.amount), curSym(r.currency)) : "";
    const purseCur = Math.max(0, ...Object.values(s.brokers || {}).map((m) => m[r.currency] || 0));
    const need = Number((r.cost_per_bond || {}).amount || 0) - purseCur;
    // Коли не по кишені — кажемо СКІЛЬКИ бракує: «ще не по кишені» саме по
    // собі не підказує, скільки лишилось відкласти.
    const status = r.can_buy
      ? `<span class="t-ok">вистачає${r.affordable > 1 ? ` ×${r.affordable}` : ""}</span>`
      : need > 0 ? `бракує ${fmtCur(need, curSym(r.currency))}` : "";
    const fits = (r.brokers || []).map((f) => `${esc(f.broker)} ×${f.qty}`).join(" · ");
    // Останнє розміщення — окремим рядком, а не в загальній стрічці: це
    // єдине тут число із ЗОВНІШНЬОГО світу, скільки платить ринок за той
    // самий папір. Прозою бекенд його не дублює — у причині лишається
    // тільки попередження, коли розміщення застаріле.
    const auc = r.last_auction
      ? `<div>на аукціоні ${esc(dayMonth(r.last_auction))} давали ${pct(r.last_auction_pct)}</div>`
      : "";
    const details = [
      `${pct(r.nominal_pct != null ? r.nominal_pct : r.ytm_pct)} номінальних`,
      r.kind === "bond" ? "до погашення" : r.yield_basis,
      r.maturity ? `до ${monthYearGen(r.maturity)}` : "",
      fits, r.reason,
    ].filter(Boolean).map(esc).join(" · ");
    return `<div class="sg" data-sg="${key}">
      <button class="caret${open ? " open" : ""}" data-sgexp="${key}" aria-expanded="${open}"
        title="Показати, звідки взялася ця дохідність">▸</button>
      ${kindPill(kind)}
      <span class="sg-n"><b>${suggestName(r)}</b> <span class="muted">${cost}</span></span>
      <span class="sg-s muted">${status}</span>
      <b class="sg-y">${pct(r.real_pct)}</b>
      ${kind === "deposit" ? "" : `<button class="sm quiet" data-bskadd="${esc(kind)}|${esc(
        kind === "fund" ? r.label : r.isin)}" title="Додати в кошик і побачити наслідки">+</button>`}
    </div>
    <div class="sg-d sub-xs" data-sgdetail="${key}"${open ? "" : " hidden"}>${details}${auc}</div>`;
  };

  // Групуємо за тим, що вирішує: чи можу купити зараз. Доти шість
  // пропозицій мали однакову вагу, і те, що до однієї бракує двох гривень,
  // а до іншої тисячі, треба було вишукувати в дрібному тексті.
  const ready = rows.filter((r) => r.can_buy);
  const soon = rows.filter((r) => !r.can_buy);
  const group = (title, list) => list.length
    ? `<div class="sg-h">${title}</div>${list.map(item).join("")}` : "";
  return `<div class="card"><h2 class="card-head">
    <span>${esc(title)} ${infoBtn("reinvest")}</span>
    ${purse ? `<span class="muted fine">${purse}</span>` : ""}</h2>
    ${group("Можеш купити зараз", ready)}
    ${group(ready.length ? "Ще збираєш" : "Купувати ще рано — ось наскільки близько", soon)}
    <div class="sub">Відсоток — реальний: після податку й знецінення, тож валюти порівнянні.
      Каретка показує, звідки він узявся.</div></div>`;
}

// Розкриті пропозиції живуть поза рендером: ctx.reload() стирає main
// цілком, і без цього кожне оновлення згортало б те, що ти щойно відкрив.
// Спільне сховище (js/uistate.js), а не власний Set: той вмирав разом зі
// вкладкою, тобто перехід у «Портфель» і назад згортав усе.
const OPEN_SCOPE = "suggest";

export function wireReinvest(ctx, main) {
  main.querySelectorAll("[data-sgexp]").forEach((b) =>
    b.addEventListener("click", () => {
      const key = b.dataset.sgexp;
      const row = main.querySelector(`[data-sgdetail="${key}"]`);
      if (!row) return;
      // hidden, а не style.display: видимість рядка деталей — стан, і
      // атрибут його ЗАЯВЛЯЄ, тоді як інлайновий стиль лише малює.
      const open = row.hidden;
      row.hidden = !open;
      b.classList.toggle("open", open);
      b.setAttribute("aria-expanded", String(open));
      remember(OPEN_SCOPE, key, open);
    }));
}

// Найближча ПОДІЯ плану — дія (замок, зміна часток) чи вікно купівлі
// фонду, що закривається. НЕ терміни погашення (їх і так повно на
// «Майбутньому», тут місце для того, що вимагає РІШЕННЯ до дати, а не
// просто настане).
function nearestPlanEvent(doc, todayIso) {
  const items = [];
  (doc.actions || []).forEach((a) => items.push({
    date: a.date, label: a.name || (a.type === "lock" ? "замок" : "зміна часток"),
  }));
  (doc.instruments || []).forEach((it) => {
    if (it.buy_until) items.push({ date: it.buy_until, label: `${it.label}: вікно купівлі закривається` });
  });
  const upcoming = items.filter((e) => e.date >= todayIso).sort((a, b) => a.date < b.date ? -1 : 1);
  return upcoming[0] || null;
}

// Плитка «Цей місяць»: скільки з ПОТРІБНОГО вже зайшло.
//
// Знаменник — ТРЕБА, а не нестача понад план, і це не косметика. Гроші
// плану теж мусять реально надійти на рахунок, їх ніхто не кредитує
// наперед; міряючи проти нестачі, плитка лестила рівно на суму плану, а
// щойно план перекривав ціль — гасла в «—», хоч ціль стояла й місяць
// тривав.
//
// Ціна: month_progress_pct рахує бекенд саме проти month_target_uah, тож
// відсоток тут рахуємо самі. Обидва поля лишаються в документі байт у
// байт — їх жорстко читає інтеграція Home Assistant.
function monthTile(ctx, s) {
  const t = contribTriad(ctx);
  const done = s.month_deposited_uah === undefined ? s.month_invested_uah : s.month_deposited_uah;
  const doneLabel = s.month_deposited_uah === undefined
    ? `вкладено ${fmtUAH(s.month_invested_uah)}` // старий бекенд рахував купівлі
    : `внесено ${fmtUAH(s.month_deposited_uah)}`;
  const mp = s.month_plan;
  const extra = `${s.month_withdrawn_uah > 0
    ? `<div class="sub-xs">нетто: поповнення ${
      fmtUAH((s.month_deposited_uah || 0) + s.month_withdrawn_uah)} − зняття ${fmtUAH(s.month_withdrawn_uah)}</div>` : ""}
    ${s.month_invested_uah > 0
    ? `<div class="sub">куплено паперів на ${fmtUAH(s.month_invested_uah)}</div>` : ""}`;

  // ГОЛОВНЕ ЧИСЛО ПЛИТКИ — ПОКРИТТЯ ПЛАНУ МІСЯЦЯ, коли план є.
  //
  // Доти тут стояв відсоток від ЦІЛІ накопичення, і на живих даних це було
  // 17%: ціль вимагає 93 931 ₴/міс, а джерела доходу дають утричі менше.
  // Число правдиве й майже некорисне — воно міряє проти суми, якої людина не
  // вносить і не планує вносити цього місяця. Питання, яке ставлять щомісяця,
  // інше: «чи закинув я те, що збирався».
  //
  // Ціль нікуди не поділась — вона рядком під смугою. Два знаменники
  // лишаються на екрані обидва й підписані кожен: план каже, скільки
  // принесуть джерела доходу, ціль — скільки треба, щоб дійти куди хочеш.
  // Злити їх в один прогрес означало б повторити ту саму підміну, від якої
  // існує contrib.js.
  if (mp && mp.plan_uah > 0) {
    const cov = mp.covered_pct || 0;
    const goalLine = t.hasGoal
      ? `<div class="sub-xs">ціль вимагає ${fmtUAH(t.need)}/міс — це ${
        Math.round(shareOfNeed(done, t.need) || 0)}%</div>`
      : "";
    return tile("Цей місяць", `${Math.round(cov)}%`,
      `<div class="progress"><span style="--oi-fill:${Math.min(100, cov)}%"></span></div>
       <div class="sub">${doneLabel} з ${fmtUAH(mp.plan_uah)} за планом місяця${mp.left_uah > 0
    ? ` · <span class="t-warn">ще закинути ${fmtUAH(mp.left_uah)}</span>`
    : ` · <span class="t-ok">закинуто все</span>`}</div>
       ${goalLine}${extra}`);
  }

  if (t.hasGoal) {
    const share = shareOfNeed(done, t.need) || 0;
    return tile("Цей місяць", `${Math.round(share)}%`,
      `<div class="progress"><span style="--oi-fill:${share}%"></span></div>
       <div class="sub">${doneLabel} з ${fmtUAH(t.need)}${
  t.hasPlan ? ` · ${CONTRIB.plan.label.toLowerCase()} дає ${fmtUAH(t.plan)}` : ""}</div>
       ${extra}`);
  }
  // Без цілі «треба» не існує — лишається стара пара з бекенда.
  if (s.month_target_uah > 0) {
    return tile("Цей місяць", `${s.month_progress_pct || 0}%`,
      `<div class="progress"><span style="--oi-fill:${Math.min(100, s.month_progress_pct || 0)}%"></span></div>
       <div class="sub">${doneLabel} з ${fmtUAH(s.month_target_uah)}</div>
       ${extra}`);
  }
  // Ні цілі, ні плану доходу — міряти нема від чого взагалі.
  return tile("Цей місяць", "—",
    `<div class="sub">задай ціль і дедлайн — план порахується сам</div>`);
}

// Друга половина плитки: чи є план, чи вистачає його на ціль, і якщо ні
// ні на що дивитись — найближча дата, коли доведеться щось вирішити.
function planTileSub(ctx, doc) {
  const t = contribTriad(ctx);
  if (!t.hasPlan) {
    return `<div class="sub"><a class="lnk" href="${routeFor("planflow")}">додай перше джерело доходу</a></div>`;
  }
  if (t.hasGoal) {
    // Обидва канонічні слова в одному рядку — щоб плитка сама пояснювала,
    // від чого рахується нестача.
    return t.gap > 0
      ? `<div class="sub">${CONTRIB.gap.label.toLowerCase()} ${fmtUAH(t.gap)}/міс до потрібних ${fmtUAH(t.need)}/міс</div>`
      : `<div class="sub t-ok">із запасом виводить на ціль</div>`;
  }
  const ev = doc && nearestPlanEvent(doc, today());
  return ev
    ? `<div class="sub">${monthYearGen(ev.date)} — ${esc(ev.label)}</div>`
    : `<div class="sub">задай ціль у «Налаштуваннях», щоб побачити, чи цього досить</div>`;
}

// Помічник тягнеться ДВІЧІ — і «Що робити», і «Що купити» його читають.
// Банер відповідає на «чи можна вже купувати», а список показує, що саме;
// це одні й ті самі поради, лише з різною глибиною. Модульна змінна
// лишається тією ж, що й була, тож обидві сторінки бачать однакове.
export async function loadReinvest(ctx) {
  try { reinvest = await ctx.api("GET", "reinvest"); }
  catch (_) { reinvest = []; }
}

/** Що робити зараз — головна сторінка застосунку.
 *
 *  ЧЕРГА ЗАДАЧ ІДЕ ПЕРШОЮ І САМА. Вона і є відповідь на питання розділу;
 *  усе інше на сторінці — контекст до неї.
 *
 *  Плитки лишились, але ПІСЛЯ черги, а не замість неї. Вони відповідають на
 *  «як справи» — капітал, темп місяця, найближча виплата, план, — і це
 *  чесне питання, просто інше. Доти вони стояли зверху, і сторінка з назвою
 *  «Що робити» відкривалась оглядом.
 *
 *  Попередження про застарілий довідник НБУ зі сторінки зникло: воно стало
 *  задачею в черзі. Лишити обидва означало б сказати те саме двічі на
 *  одному екрані.
 *
 *  Запит тут рівно один, і він не про задачі: черга вже приїхала в
 *  summary.tasks готовою (бекенд рахує її в state_tasks.go). /api/plan
 *  лишається заради плитки «План» — найближча подія (замок, вікно купівлі
 *  фонду) живе тільки там.
 */
export async function todo(ctx, main) {
  const s = ctx.summary || {};
  const cap = capitalUAH(s);
  const np = s.next_payment;
  const accrued = s.accrued_uah || 0;
  const usdRate = (s.rates || {}).USD || 0;
  const capSub = [
    usdRate > 0 ? `≈ ${fmtCur(cap / usdRate, "$")}` : "",
    accrued > 0 ? `+ ${fmtUAH(accrued)} НКД зароблено` : "",
    // Резерв названий окремо: він у капіталі, але не працює, і без цього
    // рядка сума виглядала б як «стільки в мене інвестовано».
    s.reserve_uah > 0 ? `з них ${fmtUAH(s.reserve_uah)} у резерві` : "",
  ].filter(Boolean).map((t) => `<div class="sub">${t}</div>`).join("");
  const planDoc = await ctx.soft("plan", null);
  main.innerHTML = `
    ${tasksHTML(ctx)}
    <div class="tiles flush">
      ${tile("Капітал", fmtUAH(cap), capSub, { hero: true })}
      ${monthTile(ctx, s)}
      ${tile("Наступна виплата",
    np ? `${Number(np.amount).toLocaleString("uk-UA", { minimumFractionDigits: 2 })} ${curSym(np.currency)}` : "—",
    np ? `<div class="sub">${dayMonth(np.date)}</div>` : "")}
      ${tile("План",
    s.plan_provides_uah > 0 ? `${fmtUAH(s.plan_provides_uah)}/міс` : "—",
    planTileSub(ctx, planDoc))}
    </div>
    ${paymentsPreviewHTML(ctx)}`;
}

/** Що купити — карта розподілу: скільки чого має бути й наскільки кожна ціль
 *  закрита.
 *
 *  ЩО ЗВІДСИ ПІШЛО Й ЧОМУ. Сторінка несла ще два блоки — банер «Спершу
 *  поповнити резерв» і ранжований список порад, — і обидва повторювали те, що
 *  вже стоїть на сусідній сторінці «Що робити». Черга задач (state_tasks.go)
 *  віддає рядок reserve-fill із тим самим заголовком і тією ж сумою, а поруч
 *  із ним saving: «Купувати ще рано — бракує 100,00 $ · найкраще зараз —
 *  Новий вклад · за твоїм темпом ≈ 1 день». Список під тим самим вироком
 *  розкладав його на шість рядків «бракує», у яких нестача майже дорівнювала
 *  ціні (у кишені лежало 6,19 ₴), — тобто повторював відповідь «нічого»
 *  шість разів і гірше, ніж одна задача.
 *
 *  Сам список нікуди не дівся: reinvestHTML далі малюється у воронках
 *  «Інструментів» кроком «Що взяти з цього виду», і саме тому функція
 *  лишається експортованою. Без свого місця тимчасово лишилось ПОРІВНЯННЯ
 *  ВИДІВ між собою — це зважене рішення власника, а не недогляд, і
 *  повертається воно одним доданком нижче.
 *
 *  Порожнього стану тут немає навмисно: allocationCardHTML сама каже, чого їй
 *  бракує (needsSetting), коли не задано жодної цілі. */
export async function buy(ctx, main) {
  main.innerHTML = allocationCardHTML(ctx);
}

/** Кошик покупки: що буде з портфелем, якщо взяти оце.
 *
 *  Порожній стан малюється ТУТ, а не в basketHTML: та лишається карткою,
 *  яка з порожнім кошиком не малює нічого (і правильно робить — на
 *  «Що робити» порожня рамка лише заважала б), а от сторінка, на яку можна
 *  прийти за посиланням, мусить сказати хоч щось. */
export async function basket(ctx, main) {
  main.innerHTML = await basketHTML(ctx)
    || `<div class="card"><h2>Кошик покупки</h2>${empty(
      "Кошик порожній",
      "Додай сюди те, що збираєшся взяти, — і побачиш, що станеться з капіталом, частками й ризиком ДО того, як гроші підуть.",
      { href: routeFor("now/buy"), label: "Подивитись, що купити" })}</div>`;
  wireBasket(ctx, main);
}
