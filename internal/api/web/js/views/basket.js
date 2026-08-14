// Кошик покупки: збери набір і побач наслідки ДО того, як платити.
//
// Питання, на яке відповідає ця картка, — не «що вигідніше» (на нього
// відповідає «Що купити» вище), а «що станеться з портфелем, якщо я це
// візьму». Валютні частки, капітал, дюрація й ліміти концентрації
// рухаються разом, і побачити їх рух наперед можна лише так.
//
// Жодного числа тут не рахується: усе приходить із POST /api/whatif —
// того самого документа стану, тільки над портфелем, у якому покупки вже
// записані. Тому «стане» і «зараз» завжди міряні однією лінійкою.
//
// Вміст живе в localStorage, як і відповіді конфігуратора: у базі його
// немає навмисно, інакше зʼявився б другий спосіб задати покупки.

import { esc, curSym, cur2 as fmtCur, uah2 as fmtUAH } from "../format.js";
import { infoBtn } from "../info.js";

const KEY = "oddinvest.basket";
const asPct = (v) => `${v.toFixed(2)}%`;

export function readBasket() {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) || "[]");
    return Array.isArray(v) ? v : [];
  } catch (_) { return []; }
}

function writeBasket(items) {
  try { localStorage.setItem(KEY, JSON.stringify(items)); } catch (_) { /* приватний режим */ }
}

/** Додати крок покупки. Той самий папір — не другий рядок, а +1 до
 *  кількості: два рядки «UA… ×1» поруч читались би як помилка. */
export function addToBasket(r) {
  const items = readBasket();
  const kind = r.kind === "fund" ? "fund" : "bond";
  const id = kind === "fund" ? r.label : r.isin;
  if (!id) return;
  const found = items.find((x) => x.kind === kind && x.id === id);
  if (found) found.qty += 1;
  else items.push({ kind, id, qty: 1 });
  writeBasket(items);
}

export function clearBasket() { writeBasket([]); }

/** Запит наслідків. Через raw, а не через store: це ЧИТАННЯ, яке просто
 *  ходить методом POST (тіло не влазить у query), і скидати ним кеш
 *  усього застосунку — означало б перемальовувати розділ на кожен клік
 *  по «+». */
async function fetchWhatIf(ctx, items) {
  const resp = await ctx.store.raw("whatif", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      items: items.map((x) => ({
        kind: x.kind, qty: x.qty,
        isin: x.kind === "bond" ? x.id : undefined,
        fund: x.kind === "fund" ? x.id : undefined,
      })),
    }),
  });
  if (!resp.ok) throw new Error(`${resp.status}: ${(await resp.text()).slice(0, 200)}`);
  return resp.json();
}

// Рядок «зараз → стане». Показуємо ОБИДВА числа, а не саму різницю:
// «+2.4 в.п.» не каже, чи ти при цьому перескочив ціль.
//
// fmt приходить іззовні, бо гроші й відсотки форматуються по-різному, а
// гривня без розділювачів тисяч у застосунку не пишеться ніде.
function delta(label, now, will, fmt, target) {
  const moved = Math.abs((will || 0) - (now || 0)) > 0.005;
  const arrow = moved ? `<b>${fmt(will || 0)}</b>` : `<span class="muted">без змін</span>`;
  return `<div class="pv-row"><span>${esc(label)}</span>
    <span><span class="muted">${fmt(now || 0)} →</span> ${arrow}${
      target ? ` <span class="muted" style="font-size:11px">· ціль ${target}%</span>` : ""
    }</span></div>`;
}

// Ліміти, які покупка ПЕРЕВОДИТЬ у перевищення. Ті, що вже перевищені,
// не показуємо: про них уже сказано в «Концентрації», і повторити це тут
// означало б звинуватити кошик у чужому.
function newBreaches(before, after) {
  const was = new Set(((before || {}).concentration || [])
    .filter((c) => c.over_uah > 0).map((c) => c.dimension + "|" + c.key));
  const now = ((after || {}).concentration || [])
    .filter((c) => c.over_uah > 0 && !was.has(c.dimension + "|" + c.key));
  if (!now.length) return "";
  const what = { isin: "папері", broker: "установі", year: "році погашень" };
  return `<div class="sub-xs" style="margin-top:8px">⚠ після цієї покупки ліміт буде перевищено:
    ${now.map((c) => `в одному ${what[c.dimension] || c.dimension}
      <b>${esc(c.label || c.key)}</b> — ${c.share_pct.toFixed(1)}% при ліміті ${c.limit_pct}%`).join("; ")}</div>`;
}

function linesHTML(basket) {
  return `<table><thead><tr><th>Що</th><th class="num">К-сть</th>
    <th class="num">За штуку</th><th class="num">Разом</th><th>Де</th><th></th></tr></thead><tbody>
    ${(basket.lines || []).map((l) => `<tr>
      <td>${esc(l.label)}</td><td class="num">${l.qty}</td>
      <td class="num">${fmtCur(Number(l.unit.amount), curSym(l.currency))}</td>
      <td class="num">${fmtCur(Number(l.total.amount), curSym(l.currency))}</td>
      <td>${esc(l.broker)}${l.broker_assumed
        ? `<span class="muted" style="font-size:11px"> · обрано за залишком</span>` : ""}</td>
      <td class="row-actions"><button class="sm warn" data-bskdel="${esc(l.kind)}|${esc(l.label)}">✕</button></td>
    </tr>`).join("")}</tbody></table>`;
}

/** Картка кошика. Порожній кошик не малює НІЧОГО — «Огляд» відповідає на
 *  одне питання одним поглядом, і порожня рамка на ньому лише заважає. */
export async function basketHTML(ctx) {
  const items = readBasket();
  if (!items.length) return "";
  let res;
  try {
    res = await fetchWhatIf(ctx, items);
  } catch (err) {
    return `<div class="card"><h2>Кошик покупки</h2>
      <div class="muted">Не вдалось порахувати наслідки: ${esc(err.message || err)}</div>
      <button class="sm quiet" data-bskclear>Очистити кошик</button></div>`;
  }
  const before = ctx.summary || {}, after = res.after || {}, basket = res.basket || {};
  const st = before.settings || {};
  const totals = (basket.totals || [])
    .map((t) => fmtCur(Number(t.amount), curSym(t.currency))).join(" · ");
  const shorts = (basket.shorts || []).map((s) =>
    `${esc(s.broker)}: бракує ${fmtCur(Number(s.short.amount), curSym(s.currency))}`).join(" · ");
  const durNow = (before.rate_risk || {}).duration_years || 0;
  const durWill = (after.rate_risk || {}).duration_years || 0;
  return `<div class="card"><h2 class="h-row" style="justify-content:space-between">
      <span>Кошик покупки ${infoBtn("basket")}</span>
      <button class="sm quiet" data-bskclear>Очистити</button></h2>
    ${linesHTML(basket)}
    <div class="pv-row" style="margin-top:10px"><span><b>Разом</b></span><span><b>${totals}</b></span></div>
    ${shorts ? `<div class="sub-xs warn-t" style="margin-top:4px">${esc(shorts)}</div>`
      : `<div class="sub-xs ok-t" style="margin-top:4px">грошей вистачає</div>`}
    <div style="border-top:1px solid var(--oi-border);padding-top:10px;margin-top:10px">
      <div class="sub" style="margin-bottom:6px">Що станеться з портфелем</div>
      ${delta("Капітал", before.capital_uah, after.capital_uah, fmtUAH)}
      ${delta("Частка USD", before.usd_share_pct, after.usd_share_pct, asPct, st.usd_target_share_pct)}
      ${delta("Частка EUR", before.eur_share_pct, after.eur_share_pct, asPct, st.eur_target_share_pct)}
      ${durNow > 0 || durWill > 0
        ? delta("Дюрація", durNow, durWill, (v) => `${v.toFixed(2)} р.`) : ""}
    </div>
    ${newBreaches(before, after)}
    <div class="sub-xs" style="margin-top:8px">Ціна тут — «номінал + НКД» для паперу й остання
      відома ціна для сертифіката. У брокера може бути інша, і тоді інші будуть усі числа нижче.</div>
  </div>`;
}

/** Проводка. Викликається після рендеру «Огляду». */
export function wireBasket(ctx, main) {
  main.querySelectorAll("[data-bskadd]").forEach((b) =>
    b.addEventListener("click", () => {
      const [kind, id] = b.dataset.bskadd.split("|");
      addToBasket({ kind, isin: id, label: id });
      ctx.reload();
    }));
  main.querySelectorAll("[data-bskdel]").forEach((b) =>
    b.addEventListener("click", () => {
      const [kind, id] = b.dataset.bskdel.split("|");
      writeBasket(readBasket().filter((x) => !(x.kind === kind && x.id === id)));
      ctx.reload();
    }));
  main.querySelectorAll("[data-bskclear]").forEach((b) =>
    b.addEventListener("click", () => { clearBasket(); ctx.reload(); }));
}
