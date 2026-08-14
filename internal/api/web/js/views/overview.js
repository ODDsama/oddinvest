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
  esc, curSym, monthYearGen, dayMonth, pct, plural, capitalUAH,
  uah2 as fmtUAH, cur2 as fmtCur,
} from "../format.js";
import { infoBtn } from "../info.js";
import { tile } from "../components.js";
import { KIND_LABEL } from "../constants.js";
import { basketHTML, wireBasket } from "./basket.js";

// Помічник реінвесту тягнеться раз на прохід, а читає його окрема картка.
let reinvest = [];

// Банер дії показуємо ЗАВЖДИ — він ніколи не порожній і завжди каже,
// що робити: почати, купити або накопичувати далі.
export function actionBannerHTML(ctx) {
  const s = ctx.summary || {};
  const hasPortfolio = (s.nominal_uah_eq || 0) > 0;
  const box = (cls, icon, title, sub, btn = "") =>
    `<div class="banner ${cls}"><div class="b-ic">${icon}</div><div class="b-tx">
       <div class="b-t">${title}</div>${sub ? `<div class="b-s">${sub}</div>` : ""}</div>${btn}</div>`;

  if (!hasPortfolio) {
    return box("neutral", "◦", "Почни з першої покупки",
      "Додай папір — і застосунок почне вести драбину, календар і проєкції.",
      `<button data-go="buy">Купити папір</button>`);
  }

  // Банер говорить тією самою стрічкою, що й «Що купити»: інакше вони
  // радили б різне. Порівнюємо за РЕАЛЬНОЮ дохідністю, а не за ціною —
  // сертифікат за 10 ₴ доступний завжди, але це не робить його найкращим.
  const list = reinvest || [];
  const byReal = (a, b) => (b.real_pct || 0) - (a.real_pct || 0);
  const bestAny = [...list].sort(byReal)[0];
  const bestCan = [...list].filter((r) => r.can_buy).sort(byReal)[0];
  const label = (r) => suggestTitle(r).name;

  if (bestCan) {
    const action = KIND_ACTION[bestCan.kind] || "купити";
    const where = (bestCan.brokers || []).map((f) => `${esc(f.broker)} ×${f.qty}`).join(" · ");
    const cost = bestCan.cost_per_bond
      ? fmtCur(Number(bestCan.cost_per_bond.amount), curSym(bestCan.currency)) : "";
    // Якщо є щось дохідніше, але ще не по кишені — кажемо про це прямо:
    // «можеш зараз» не має ховати «краще зачекати».
    // Слово «реальних» тут теж обов'язкове: доти воно стояло лише біля
    // першого числа, і два відсотки в одному реченні виглядали як два
    // різні виміри.
    const better = bestAny && !bestAny.can_buy && (bestAny.real_pct || 0) > (bestCan.real_pct || 0)
      ? ` Дохідніше — ${label(bestAny)} (${pct(bestAny.real_pct)} реальних), але ще не по кишені.`
      : "";
    return box("ok", "●",
      `Можеш ${action} ${label(bestCan)} — ${pct(bestCan.real_pct)} реальних`,
      `${cost}${where ? ` · ${where}` : ""}.${better}`,
      `<button data-go="${bestCan.kind === "deposit" ? "topup" : "buy"}">${
        bestCan.kind === "deposit" ? "Поповнити" : "Купити"}</button>`);
  }

  // Нічого не по кишені: кажемо, ЩО саме найкраще і скільки до нього.
  const np = s.next_payment;
  const perDay = (s.month_target_uah || 0) / 30;
  if (bestAny) {
    const purseCur = Math.max(0, ...Object.values(s.brokers || {}).map((m) => m[bestAny.currency] || 0));
    const need = Math.max(0, Number((bestAny.cost_per_bond || {}).amount || 0) - purseCur);
    const days = perDay > 0 ? Math.ceil(need / perDay) : 0;
    const eta = days > 0 ? ` ≈ <b>${days}</b> дн. за твоїм темпом` : "";
    const sub = `${label(bestAny)} коштує ${fmtCur(Number((bestAny.cost_per_bond || {}).amount || 0), curSym(bestAny.currency))}, на рахунку ${fmtCur(purseCur, curSym(bestAny.currency))}.` +
      (np ? ` Виплата ${dayMonth(np.date)} додасть ${Number(np.amount).toLocaleString("uk-UA", { minimumFractionDigits: 2 })} ${curSym(np.currency)}.` : "");
    return box("wait", "○",
      `Купувати ще рано — бракує ${fmtCur(need, curSym(bestAny.currency))}${eta}`, sub);
  }

  // Стрічка порожня (немає довідника чи виплат) — старий чесний мінімум.
  const need = Math.max(0, (s.reinvest_min_uah || 0) - (s.account_uah || 0));
  return box("wait", "○", `Купувати ще рано — бракує ${fmtUAH(need)}`,
    `На рахунку ${fmtUAH(s.account_uah)}, найдешевший папір ${fmtUAH(s.reinvest_min_uah)}.`);
}

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
    <div class="sub">Суми за день складені. Повний календар — у «Майбутньому»</div></div>`;
}

export function nbuStaleHTML(ctx) {
  const at = (ctx.summary || {}).nbu_refreshed_at;
  if (!at) return "";
  const days = Math.floor((Date.now() - new Date(at).getTime()) / 86400000);
  if (days < 3) return "";
  return `<div class="banner wait" style="padding:10px 16px"><div class="b-tx">
    <div class="b-s" style="opacity:1">Довідник НБУ не оновлювався <b>${days} дн.</b> —
    ставки й графіки виплат можуть бути несвіжі. Натисни «↻ Оновити НБУ».</div></div></div>`;
}

// Що купити: папери, відранжовані за РЕАЛЬНОЮ дохідністю в сьогоднішніх
// гривнях. Показуємо кілька позицій ЗАВЖДИ — попередній варіант зникав
// саме тоді, коли ти плануєш наступний крок, а ще був таблицею-звалищем
// на весь довідник, тож тут свідомо лише верхівка.
// Дієслово дії. Іменник несе сама назва (suggestTitle), інакше виходило б
// «докласти у вклад вклад ПУМБ».
const KIND_ACTION = { bond: "купити", fund: "купити", deposit: "поповнити" };

// Заголовок рядка: ISIN / «сертифікат X» / «вклад Банк». Другий рядок —
// уточнення природи: у паперу й вкладу є строк, у сертифіката немає.
function suggestTitle(r) {
  if (r.kind === "fund") {
    return { name: `сертифікат ${esc(r.label)}`, sub: `${curSym(r.currency)} · без строку` };
  }
  if (r.kind === "deposit") {
    // «ставка» — бо це умова договору до податку, а не дохідність:
    // без слова вона читалась як третє число поруч із реальною й
    // номінальною в тому самому рядку.
    return { name: `вклад ${esc(r.label)}`, sub: `${curSym(r.currency)} · ставка ${pct(r.rate_pct)} · до ${monthYearGen(r.maturity)}` };
  }
  return { name: esc(r.label || r.isin), sub: `${curSym(r.currency)} · до ${monthYearGen(r.maturity)}` };
}

// Назва рядка поради. Валюту несе сума поруч, строк переїхав у розкриття,
// а вид інструмента каже пігулка — тож слова «вклад» і «сертифікат» у
// назві стали б повтором того, що вже видно.
function suggestName(r) {
  // Ставка лишається при вкладі: це умова договору, а не дохідність, і
  // саме вона відрізняє два вклади в одній валюті один від одного.
  if (r.kind === "deposit") return `${esc(r.label)} <span class="muted">${pct(r.rate_pct)}</span>`;
  return esc(r.label || r.isin);
}

export function reinvestHTML(ctx) {
  // По одній найкращій пропозиції на ТИП × ВАЛЮТУ. Раніше ключем була
  // сама валюта — чотири майже однакові папери не давали вибору, і
  // порівняти гривню з доларом було сенсом блоку. Тепер інструментів три,
  // і ключ лише за валютою ховав би два з них: усі гривневі пропозиції
  // згорталися б в одну, і фонд із облігацією просто зникали б за
  // вкладом. Порівнювати треба і валюти, і природу інструмента.
  const byKey = new Map();
  for (const r of reinvest || []) {
    const k = `${r.kind || "bond"}|${r.currency}`;
    if (!byKey.has(k)) byKey.set(k, r);
  }
  const rows = [...byKey.values()].sort((a, b) => (b.real_pct || 0) - (a.real_pct || 0));
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
    const open = openSuggest.has(key);
    const kind = r.kind === "fund" ? "fund" : r.kind === "deposit" ? "deposit" : "bond";
    const cost = r.cost_per_bond ? fmtCur(Number(r.cost_per_bond.amount), curSym(r.currency)) : "";
    const purseCur = Math.max(0, ...Object.values(s.brokers || {}).map((m) => m[r.currency] || 0));
    const need = Number((r.cost_per_bond || {}).amount || 0) - purseCur;
    // Коли не по кишені — кажемо СКІЛЬКИ бракує: «ще не по кишені» саме по
    // собі не підказує, скільки лишилось відкласти.
    const status = r.can_buy
      ? `<span class="ok-t">вистачає${r.affordable > 1 ? ` ×${r.affordable}` : ""}</span>`
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
      <span class="pill pill-${kind}">${KIND_LABEL[kind]}</span>
      <span class="sg-n"><b>${suggestName(r)}</b> <span class="muted">${cost}</span></span>
      <span class="sg-s muted">${status}</span>
      <b class="sg-y">${pct(r.real_pct)}</b>
      ${kind === "deposit" ? "" : `<button class="sm quiet" data-bskadd="${esc(kind)}|${esc(
        kind === "fund" ? r.label : r.isin)}" title="Додати в кошик і побачити наслідки">+</button>`}
    </div>
    <div class="sg-d sub-xs" data-sgdetail="${key}"${open ? "" : ` style="display:none"`}>${details}${auc}</div>`;
  };

  // Групуємо за тим, що вирішує: чи можу купити зараз. Доти шість
  // пропозицій мали однакову вагу, і те, що до однієї бракує двох гривень,
  // а до іншої тисячі, треба було вишукувати в дрібному тексті.
  const ready = rows.filter((r) => r.can_buy);
  const soon = rows.filter((r) => !r.can_buy);
  const group = (title, list) => list.length
    ? `<div class="sg-h">${title}</div>${list.map(item).join("")}` : "";
  return `<div class="card"><h2 class="h-row" style="justify-content:space-between">
    <span>Що купити ${infoBtn("reinvest")}</span>
    ${purse ? `<span class="muted" style="font-size:var(--oi-fs-sm)">${purse}</span>` : ""}</h2>
    ${group("Можеш купити зараз", ready)}
    ${group(ready.length ? "Ще збираєш" : "Купувати ще рано — ось наскільки близько", soon)}
    <div class="sub">Відсоток — реальний: після податку й знецінення, тож валюти порівнянні.
      Каретка показує, звідки він узявся.</div></div>`;
}

// Розкриті пропозиції живуть поза рендером: ctx.reload() стирає main
// цілком, і без цього кожне оновлення згортало б те, що ти щойно відкрив.
const openSuggest = new Set();

export function wireReinvest(ctx, main) {
  main.querySelectorAll("[data-sgexp]").forEach((b) =>
    b.addEventListener("click", () => {
      const key = b.dataset.sgexp;
      const row = main.querySelector(`[data-sgdetail="${key}"]`);
      if (!row) return;
      const open = row.style.display === "none";
      row.style.display = open ? "" : "none";
      b.classList.toggle("open", open);
      b.setAttribute("aria-expanded", String(open));
      if (open) openSuggest.add(key); else openSuggest.delete(key);
    }));
}

export async function renderOverview(ctx, main) {
  const s = ctx.summary || {};
  // Капітал — це ВСЕ, що в тебе є. Рахує спільний capitalUAH, а не власна
  // сума: тут довго складались лише номінал, рахунок і фонди, тож тіло
  // банківських вкладів у капітал не входило взагалі — рівно та сама
  // помилка, що колись була з фондами, лише на інструмент пізніше.
  const cap = capitalUAH(s);
  const np = s.next_payment;
  const accrued = s.accrued_uah || 0;
  // Долар — одиниця, якою тут справді міряють. Курс беремо зі зведення;
  // доки його там не було, це число просто не можна було показати.
  const usdRate = (s.rates || {}).USD || 0;
  const capSub = [
    usdRate > 0 ? `≈ ${fmtCur(cap / usdRate, "$")}` : "",
    accrued > 0 ? `+ ${fmtUAH(accrued)} НКД зароблено` : "",
    // Резерв названий окремо: він у капіталі, але не працює, і без цього
    // рядка сума виглядала б як «стільки в мене інвестовано».
    s.reserve_uah > 0 ? `з них ${fmtUAH(s.reserve_uah)} у резерві` : "",
  ].filter(Boolean).map((t) => `<div class="sub">${t}</div>`).join("");
  const tiles = `<div class="tiles flush">
    ${tile("Капітал", fmtUAH(cap), capSub)}
    ${tile("Цей місяць", s.month_target_uah > 0 ? `${s.month_progress_pct || 0}%` : "—",
      s.month_target_uah > 0
        ? `<div class="progress"><span style="width:${Math.min(100, s.month_progress_pct || 0)}%"></span></div>
           <div class="sub">${
             s.month_deposited_uah === undefined
               ? `вкладено ${fmtUAH(s.month_invested_uah)}` // старий бекенд рахував купівлі
               : `внесено ${fmtUAH(s.month_deposited_uah)}`} з ${fmtUAH(s.month_target_uah)}</div>
           ${s.month_withdrawn_uah > 0
             ? `<div class="sub-xs">нетто: поповнення ${
                 fmtUAH((s.month_deposited_uah || 0) + s.month_withdrawn_uah)} − зняття ${fmtUAH(s.month_withdrawn_uah)}</div>` : ""}
           ${s.month_invested_uah > 0
             ? `<div class="sub">куплено паперів на ${fmtUAH(s.month_invested_uah)}</div>` : ""}`
        : `<div class="sub">задай ціль і дедлайн — план порахується сам</div>`)}
    ${tile("Наступна виплата",
      np ? `${Number(np.amount).toLocaleString("uk-UA", { minimumFractionDigits: 2 })} ${curSym(np.currency)}` : "—",
      np ? `<div class="sub">${dayMonth(np.date)}</div>` : "")}
  </div>`;

  // Помічник живе на окремому маршруті: тягнемо разом з оглядом, щоб
  // картка не «доїжджала» після решти.
  try { reinvest = await ctx.api("GET", "reinvest"); }
  catch (_) { reinvest = []; }
  main.innerHTML = `
    ${nbuStaleHTML(ctx)}
    ${actionBannerHTML(ctx)}
    <div class="quick">
      <button data-go="buy">Купівля</button>
      <button data-go="deposit">Поповнення</button>
      <button data-go="convert">Конвертація</button>
    </div>
    ${tiles}
    <div class="ov-grid">${reinvestHTML(ctx)}${paymentsPreviewHTML(ctx)}</div>
    ${await basketHTML(ctx)}`;

  wireReinvest(ctx, main);
  wireBasket(ctx, main);
  main.querySelectorAll("[data-go]").forEach((b) =>
    b.addEventListener("click", () => ctx.goto(b.dataset.go)));
}

