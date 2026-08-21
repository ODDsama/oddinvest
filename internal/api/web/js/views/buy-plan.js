// Наслідки плану купівель: що станеться з портфелем і з ЦІЛЯМИ.
//
// Питання, на яке відповідає ця картка, — не «що вигідніше» (на нього
// відповідає «Що купити»), а «що зміниться, якщо я це візьму». Валютні
// частки, частки за видом, подушка, точка незалежності й місячний план
// рухаються разом, і побачити їх рух наперед можна лише так.
//
// Жодного числа тут не рахується: усе приходить із POST /api/whatif —
// того самого документа стану, тільки над портфелем, у якому покупки вже
// записані. Тому «зараз» і «стане» завжди міряні однією лінійкою.
//
// ПРАВИЛО СКЛАДУ: рядок малюється, лише якщо ЦЕЙ набір покупок може його
// структурно зрушити. Подушка з'являється тільки тоді, коли в наборі є
// резервний вклад; частка виду — тільки для видів, які в наборі є. Рядок,
// який завжди каже «без змін», привчає не читати картку.
//
// ЧОГО ТУТ СВІДОМО НЕМАЄ — і чому, бо інакше наступний автор допише це
// заново:
//
//   XIRR. Свіжі гроші у валюті опускають середньозважений вік потоків
//   нижче 30 днів, і показник для цієї валюти ЗНИКАЄ (state_builder.go).
//   «12.4% → —» читається як поломка. Та й XIRR питає про минуле
//   вкладених грошей, а в покупки минулого немає.
//
//   Просадка (drawdown). Падає майже на кожній покупці, бо готівка стає
//   замкненим інструментом. Це правда, але як сигнал У МОМЕНТ КУПІВЛІ
//   вона перекручена: картка позначала б кожен вклад як погіршення.
//
//   Чутливість (sensitivity). Смуга, зсунута однією покупкою, — шум на
//   тій роздільності, яку людина читає.
//
//   Крива капіталу цілком. Розглянуто й відкинуто: дві криві розміром із
//   картку не відрізняються оком. Замість графіка — одне число.
//
//   forecast.rows[realistic].goal_pct. Здається очевидним вибором і не
//   годиться: внесок виводиться з цілі бісекцією, тож реалістичний
//   сценарій сходиться рівно на 100% ЗА ПОБУДОВОЮ, хай би що ти купив.
//   Замість нього — month_target_uah, тобто скільки треба вносити
//   щомісяця, щоб дійти до цілі. Ось воно рухається.
//
//   Зведена дохідність. Рухається, але «13.9% замість 14.1%» не дає дії
//   й підштовхує оптимізувати число замість цілей за частками.

import { esc, uah2 as fmtUAH } from "../format.js";
import { infoBtn } from "../info.js";
import { KIND_GROUP } from "../constants.js";

const asPct = (v) => `${v.toFixed(2)}%`;
const asMonths = (v) => `${v.toFixed(1)} міс.`;

/** Запит наслідків. Через raw, а не через store: це ЧИТАННЯ, яке просто
 *  ходить методом POST (тіло не влазить у query), і скидати ним кеш
 *  усього застосунку — означало б перемальовувати розділ на кожен
 *  натиск клавіші у формі.
 *
 *  body — {saved?, exclude?, draft?}; signal дає перервати запит, який
 *  устиг застаріти (див. проводку превʼю в plan-buys.js). */
export async function fetchWhatIf(ctx, body = {}, signal = null) {
  const resp = await ctx.store.raw("whatif", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    ...(signal ? { signal } : {}),
  });
  if (!resp.ok) throw new Error(`${resp.status}: ${(await resp.text()).slice(0, 200)}`);
  return resp.json();
}

// Рядок «зараз → стане». Показуємо ОБИДВА числа, а не саму різницю:
// «+2.4 в.п.» не каже, чи ти при цьому перескочив ціль.
//
// fmt приходить іззовні, бо гроші, відсотки й місяці форматуються
// по-різному, а гривня без розділювачів тисяч у застосунку не пишеться.
function delta(label, now, will, fmt, tail = "") {
  const moved = Math.abs((will || 0) - (now || 0)) > 0.005;
  const arrow = moved ? `<b>${fmt(will || 0)}</b>` : `<span class="muted">без змін</span>`;
  return `<div class="pv-row"><span>${esc(label)}</span>
    <span><span class="muted">${fmt(now || 0)} →</span> ${arrow}${tail}</span></div>`;
}

const targetTail = (t) => (t == null ? "" : ` <span class="muted fine-xs">· ціль ${t}%</span>`);

// Дати порівнюються як рядки: обидві ISO, і Date тут дав би лише привід
// думати про часовий пояс.
function dateDelta(label, now, will) {
  const a = now || "—", b = will || "—";
  const arrow = a === b ? `<span class="muted">без змін</span>` : `<b>${esc(b)}</b>`;
  return `<div class="pv-row"><span>${esc(label)}</span>
    <span><span class="muted">${esc(a)} →</span> ${arrow}</span></div>`;
}

// Частка виду — лише для видів, які в наборі Є. Показати всі п'ять
// означало б намалювати другу «Структуру за видом» усередині картки
// наслідків.
function kindRows(before, after, lines) {
  const want = new Set((lines || [])
    // Резервний вклад тіла у «вклади» не додає — воно йде в подушку
    // (0032), — тож рядок «Вклади» від нього не зрушив би ніколи. Так
    // само й майбутній рядок: він живе в прогнозі, а не в портфелі, і
    // сьогоднішньої частки не чіпає за побудовою.
    .filter((l) => !l.is_reserve && !l.future)
    .map((l) => ({
      bond: "bonds", fund: "funds", deposit: "deposits", npf: "npf",
    })[l.kind]).filter(Boolean));
  if (!want.size) return "";
  const byKey = (doc, key) => (doc.rebalance || []).find(
    (r) => r.dimension === "kind" && r.key === key);
  return [...want].map((key) => {
    const a = byKey(before, key), b = byKey(after, key);
    if (!a || !b) return "";
    return delta(KIND_GROUP[key] || key, a.current_pct, b.current_pct,
      asPct, targetTail(a.target_pct));
  }).join("");
}

// Подушка й драбина — лише коли в наборі є РЕЗЕРВНИЙ вклад. Це єдине, що
// їх рухає (state_builder.go виводить тіло такого вкладу з «вкладів» у
// резерв), тож без нього обидва рядки завжди казали б «без змін».
function reserveRows(before, after, lines) {
  // Майбутній резервний вклад подушки СЬОГОДНІ не збільшує — він у
  // прогнозі. Рядок про нього казав би «без змін», тобто заперечував би
  // те, заради чого його й завели.
  if (!(lines || []).some((l) => l.is_reserve && !l.future)) return "";
  const a = before.reserve || {}, b = after.reserve || {};
  if (!a.target_months && !b.target_months) return "";
  const cover = (r) => Number(r.ladder_covers_months || 0);
  return delta("Подушка", a.months, b.months, asMonths,
    a.target_months ? ` <span class="muted fine-xs">· ціль ${a.target_months} міс.</span>` : "")
    + (cover(a) || cover(b)
      ? delta("Драбина покриває", cover(a), cover(b), asMonths) : "");
}

// Ліміти, які покупка ПЕРЕВОДИТЬ у перевищення. Ті, що вже перевищені,
// не показуємо: про них уже сказано в «Концентрації», і повторити це тут
// означало б звинуватити план у чужому.
function newBreaches(before, after) {
  const was = new Set(((before || {}).concentration || [])
    .filter((c) => c.over_uah > 0).map((c) => c.dimension + "|" + c.key));
  const now = ((after || {}).concentration || [])
    .filter((c) => c.over_uah > 0 && !was.has(c.dimension + "|" + c.key));
  if (!now.length) return "";
  const what = { isin: "папері", broker: "установі", year: "році погашень" };
  return `<div class="sub-xs mt-sm">⚠ після цієї покупки ліміт буде перевищено:
    ${now.map((c) => `в одному ${what[c.dimension] || c.dimension}
      <b>${esc(c.label || c.key)}</b> — ${c.share_pct.toFixed(1)}% при ліміті ${c.limit_pct}%`).join("; ")}</div>`;
}

/** Картка наслідків. res — відповідь /api/whatif; before береться з
 *  ctx.summary, тобто з того самого документа, яким намальований увесь
 *  застосунок.
 *
 *  Порожній набір не малює НІЧОГО: рамка з написом «без змін» на кожному
 *  рядку — це шум, який навчає не читати картку. Порожній стан сторінки
 *  малює той, хто кличе. */
export function impactHTML(ctx, res) {
  const before = ctx.summary || {}, after = (res || {}).after || {};
  const lines = ((res || {}).basket || {}).lines || [];
  if (!lines.length) return "";
  const st = before.settings || {};
  const durNow = (before.rate_risk || {}).duration_years || 0;
  const durWill = (after.rate_risk || {}).duration_years || 0;
  const indNow = before.independence, indWill = after.independence;
  return `<div class="card"><h2>Що зміниться ${infoBtn("basket")}</h2>
    <div class="note">Одні й ті самі числа, поміряні однією лінійкою: ліворуч —
      портфель як він є, праворуч — він же з усіма рядками плану.</div>
    <div class="sub mb-sm">Портфель</div>
    ${delta("Капітал", before.capital_uah, after.capital_uah, fmtUAH)}
    ${delta("Частка USD", before.usd_share_pct, after.usd_share_pct, asPct,
    targetTail(st.usd_target_share_pct))}
    ${delta("Частка EUR", before.eur_share_pct, after.eur_share_pct, asPct,
    targetTail(st.eur_target_share_pct))}
    ${kindRows(before, after, lines)}
    ${durNow > 0 || durWill > 0
    ? delta("Дюрація", durNow, durWill, (v) => `${v.toFixed(2)} р.`) : ""}
    ${reserveRows(before, after, lines)}
    ${indNow || before.month_target_uah ? `<div class="rule-top">
      <div class="sub mb-sm">Цілі</div>
      ${indNow && indWill ? dateDelta("Незалежність настане", indNow.plan_date, indWill.plan_date)
    + delta("Капітал на той момент", indNow.capital_uah, indWill.capital_uah, fmtUAH) : ""}
      ${before.month_target_uah
    ? delta("Треба вносити щомісяця", before.month_target_uah,
      after.month_target_uah, fmtUAH) : ""}
    </div>` : ""}
    ${newBreaches(before, after)}
  </div>`;
}
