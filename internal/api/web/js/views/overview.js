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

import { esc, curSym, monthYearGen, dayMonth, uah2 as fmtUAH, cur2 as fmtCur } from "../format.js";
import { infoBtn } from "../info.js";
import { tile } from "../components.js";

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
    const better = bestAny && !bestAny.can_buy && (bestAny.real_pct || 0) > (bestCan.real_pct || 0)
      ? ` Дохідніше — ${label(bestAny)} (${(bestAny.real_pct || 0).toFixed(1)}%), але ще не по кишені.`
      : "";
    return box("ok", "●",
      `Можеш ${action} ${label(bestCan)} — ${(bestCan.real_pct || 0).toFixed(1)}% реальних`,
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

export function paymentsPreviewHTML(ctx) {
  const rows = ((ctx.summary || {}).top_payments || []).slice(0, 4);
  const body = rows.length
    ? rows.map((p) => `<div class="pv-row"><span class="muted">${dayMonth(p.date)}</span>
        <span>${Number(p.amount).toLocaleString("uk-UA", { minimumFractionDigits: 2 })} ${curSym(p.currency)}</span></div>`).join("")
    : `<div class="sub">Виплат попереду немає.</div>`;
  return `<div class="card"><h2>Найближчі виплати</h2>${body}
    <div class="muted" style="font-size:12px;margin-top:8px">Повний календар — у «Майбутньому»</div></div>`;
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
    return { name: `вклад ${esc(r.label)}`, sub: `${curSym(r.currency)} · ${r.rate_pct}% · до ${monthYearGen(r.maturity)}` };
  }
  return { name: esc(r.label || r.isin), sub: `${curSym(r.currency)} · до ${monthYearGen(r.maturity)}` };
}

// Чим підперта дохідність: у паперу — YTM, у решти — підпис із бекенда
// (yield_basis). Ховати різницю між обіцянкою і оцінкою не можна.
function yieldNote(r) {
  if (r.kind === "bond" && r.ytm_pct) return `YTM ${(r.ytm_pct || 0).toFixed(1)}%`;
  return esc(r.yield_basis || "");
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
  const items = rows.map((r) => {
    const fits = (r.brokers || []).map((f) => `${esc(f.broker)} ×${f.qty}`).join(" · ");
    const cost = r.cost_per_bond ? fmtCur(Number(r.cost_per_bond.amount), curSym(r.currency)) : "";
    // Коли не по кишені — кажемо СКІЛЬКИ бракує: «ще не по кишені» саме
    // по собі не підказує, скільки лишилось відкласти.
    const purseCur = Math.max(0, ...Object.values(s.brokers || {}).map((m) => m[r.currency] || 0));
    const need = Number((r.cost_per_bond || {}).amount || 0) - purseCur;
    const t = suggestTitle(r);
    const note = yieldNote(r);
    return `<div style="margin-bottom:10px">
      <div style="display:flex;justify-content:space-between;align-items:baseline;gap:8px">
        <span><b>${t.name}</b> <span class="muted" style="font-size:12px">${t.sub}</span></span>
        <span><b>${(r.real_pct || 0).toFixed(1)}%</b> <span class="muted" style="font-size:12px">реальних</span></span>
      </div>
      <div class="sub-xs">${cost}${note ? ` · ${note}` : ""}${
        fits ? ` · ${fits}` : need > 0 ? ` · бракує ${fmtCur(need, curSym(r.currency))}` : ""}</div>
      ${r.reason ? `<div class="sub-xs">${esc(r.reason)}</div>` : ""}
    </div>`;
  }).join("");
  return `<div class="card"><h2 class="h-row" style="justify-content:space-between">
    <span>Що купити ${infoBtn("reinvest")}</span></h2>
    ${purse ? `<div class="muted" style="font-size:12px;margin-bottom:8px">${purse}</div>` : ""}
    ${items}</div>`;
}

export async function renderOverview(ctx, main) {
  const s = ctx.summary || {};
  // Капітал — це ВСЕ, що працює: номінал паперів, гроші на рахунку й
  // сертифікати фондів. Останні довго рахувались окремо просто тому,
  // що з'явились пізніше, і капітал занижувався на їхню вартість.
  const cap = (s.nominal_uah_eq || 0) + (s.account_uah || 0) + (s.funds_uah || 0);
  const np = s.next_payment;
  const accrued = s.accrued_uah || 0;
  const tiles = `<div class="tiles flush">
    ${tile("Капітал", fmtUAH(cap),
      accrued > 0 ? `<div class="sub">+ ${fmtUAH(accrued)} НКД зароблено</div>` : "")}
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
    <div class="ov-grid">${reinvestHTML(ctx)}${paymentsPreviewHTML(ctx)}</div>`;

  main.querySelectorAll("[data-go]").forEach((b) =>
    b.addEventListener("click", () => ctx.goto(b.dataset.go)));
}

