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
  const rmin = s.reinvest_min || {};
  const hasPortfolio = (s.nominal_uah_eq || 0) > 0;
  // Готовність рахуємо ПО БРОКЕРАХ: сумарний баланс може «вистачати»,
  // хоча в кожного окремо грошей замало — і банер брехав би.
  const brokers = s.brokers || {};
  const ready = [];
  for (const b of Object.keys(brokers)) {
    for (const c of Object.keys(brokers[b] || {})) {
      if (rmin[c] > 0 && brokers[b][c] >= rmin[c]) {
        ready.push({ broker: b, cur: c, n: Math.floor(brokers[b][c] / rmin[c]) });
      }
    }
  }
  const box = (cls, icon, title, sub, btn = "") =>
    `<div class="banner ${cls}"><div class="b-ic">${icon}</div><div class="b-tx">
       <div class="b-t">${title}</div>${sub ? `<div class="b-s">${sub}</div>` : ""}</div>${btn}</div>`;

  if (!hasPortfolio) {
    return box("neutral", "◦", "Почни з першої покупки",
      "Додай папір — і застосунок почне вести драбину, календар і проєкції.",
      `<button data-go="buy">Купити папір</button>`);
  }
  if (ready.length) {
    const parts = ready.map((r) => `<b>${r.n}</b> ${esc(r.cur)}-папер(и) у <b>${esc(r.broker)}</b>`).join(", ");
    return box("ok", "●", `Можеш купити ${parts}`,
      ready.map((r) => `${esc(r.broker)} · ${esc(r.cur)}: ${fmtCur(brokers[r.broker][r.cur], r.cur)}, папір від ${fmtCur(rmin[r.cur], r.cur)}`).join(" · "),
      `<button data-go="buy">Купити</button>`);
  }
  const need = Math.max(0, (s.reinvest_min_uah || 0) - (s.account_uah || 0));
  const np = s.next_payment;
  // Скільки ще накопичувати за поточним темпом внесків — щоб банер казав
  // «коли», а не лише «скільки». Купон, що покриває нестачу, — окремо.
  const perDay = (s.month_target_uah || 0) / 30;
  const days = perDay > 0 ? Math.ceil(need / perDay) : 0;
  const eta = days > 0 ? ` ≈ <b>${days}</b> дн. за твоїм темпом` : "";
  const sub = `На рахунку ${fmtUAH(s.account_uah)}, найдешевший папір ${fmtUAH(s.reinvest_min_uah)}.` +
    (np ? ` Купон ${dayMonth(np.date)} додасть ${Number(np.amount).toLocaleString("uk-UA", { minimumFractionDigits: 2 })} ${curSym(np.currency)}.` : "");
  return box("wait", "○", `Купувати ще рано — бракує ${fmtUAH(need)}${eta}`, sub);
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
export function reinvestHTML(ctx) {
  // По одному найкращому паперу на валюту, а не просто верхівка списку.
  // Верхівка збиралася з однієї валюти (сортування спершу дивиться на
  // валютний дефіцит), і чотири майже однакові папери не давали вибору —
  // тоді як порівняти гривню з доларом і є сенсом цього блоку.
  const byCur = new Map();
  for (const r of reinvest || []) {
    if (!byCur.has(r.currency)) byCur.set(r.currency, r);
  }
  const rows = [...byCur.values()].sort((a, b) => (b.real_pct || 0) - (a.real_pct || 0));
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
    return `<div style="margin-bottom:10px">
      <div style="display:flex;justify-content:space-between;align-items:baseline;gap:8px">
        <span><b>${esc(r.isin)}</b> <span class="muted" style="font-size:12px">${curSym(r.currency)} · до ${monthYearGen(r.maturity)}</span></span>
        <span><b>${(r.real_pct || 0).toFixed(1)}%</b> <span class="muted" style="font-size:12px">реальних</span></span>
      </div>
      <div class="sub-xs">${cost} · YTM ${(r.ytm_pct || 0).toFixed(1)}%${
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

