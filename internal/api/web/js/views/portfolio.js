// Розділ «Портфель» — що в мене є.
//
// Усе про склад: позиції ОВДП і сертифікати фондів, лоти, продажі,
// дивіденди, дохідності — і характеристики того, що вже куплено:
// частки по брокерах і валютах, драбина погашень, процентний ризик,
// історія «Як росте». Раніше це було розкидано по трьох вкладках
// («Портфель», «План») і «Огляду», хоча відповідає на одне питання.
//
// Облігації й сертифікати навмисно розкладені однаково — позиції, лоти,
// продажі, — але не змішані в одну таблицю: у сертифіката немає ні
// номіналу, ні графіка купонів, і спільна таблиця зламала б обидві.

import {
  esc, curSym, today, dayMonth,
  uah2 as fmtUAH, cur2 as fmtCur, money as fmtMoney, fundsCost,
} from "../format.js";
import { infoBtn } from "../info.js";
import { svgBars, svgGrouped, svgDonut, seriesChart } from "../charts.js";
import { tile } from "../components.js";
import { FUND_KIND } from "../constants.js";

// Журнал операцій із фондами на час одного рендеру: таблиці позицій,
// лотів, продажів і дивідендів усі читають той самий список, і тягнути
// його чотири рази заради цього не варто.
//
// Розділ «Гроші» теж будує з нього таблиці виписки, тому список і
// живе тут, а не всередині renderPortfolio: заповнює його той, хто
// першим прийшов, а читають обидва.
let fundOps = [];
export function setFundOps(ops) { fundOps = ops || []; }

// Знімки для кривої «Як росте»: тягнуться раз, читаються графіком і
// таблицею під ним.
let snapsCache = [];

// ---------- склад і структура ----------

// Кільце часток вкладеного капіталу по брокерах. Малюємо SVG-donut
// руками (без зовнішніх бібліотек): кожен сегмент — коло зі stroke-
// dasharray, зсунуте на суму попередніх. Група повернута на -90°, щоб
// старт був угорі.
export function brokerDonutHTML(ctx) {
  const ibb = (ctx.summary || {}).invested_by_broker || {};
  const names = Object.keys(ibb).filter((n) => ibb[n] > 0).sort((a, b) => ibb[b] - ibb[a]);
  if (names.length < 2) return "";
  const total = names.reduce((s, n) => s + ibb[n], 0);
  const { svg, colors } = svgDonut(names.map((n) => ({ label: n, value: ibb[n] })));
  const legend = names.map((n, i) => {
    const pct = (ibb[n] / total) * 100;
    return `<div class="pv-row"><span><i class="swatch" style="background:${colors[i]}"></i>${esc(n)}</span>
      <span>${pct.toFixed(0)}% · ${fmtUAH(ibb[n])}</span></div>`;
  }).join("");
  return `<div class="card wide"><h4>Частки по брокерах ${infoBtn("broker")}</h4>
    <div class="donut-row">${svg}<div class="donut-legend">${legend}</div></div>
    <div class="sub">За вкладеним капіталом (вартість входу залишків).</div></div>`;
}

// Стовпчики драбини — форма повернень у часі. Живуть усередині картки
// драбини, а не окремою: два блоки з однаковим заголовком «Драбина
// погашень» читались як помилка, хоч і показували різне.
function ladderBarsHTML(ctx) {
  const lad = (ctx.summary || {}).ladder_uah || [];
  if (!lad.length) return "";
  return svgBars(lad.map((r) => ({ label: String(r.year), value: r.uah })), { showVals: true });
}

// Валютні частки проти цільових.
export function currencyChartHTML(ctx) {
  const s = ctx.summary || {}, st = s.settings || {};
  const usdT = Number(st.usd_target_share_pct || 0), eurT = Number(st.eur_target_share_pct || 0);
  if (!(usdT > 0 || eurT > 0)) return "";
  const groups = [
    { label: "UAH", a: 100 - (s.usd_share_pct || 0) - (s.eur_share_pct || 0), b: Math.max(0, 100 - usdT - eurT) },
    { label: "USD", a: s.usd_share_pct || 0, b: usdT },
    { label: "EUR", a: s.eur_share_pct || 0, b: eurT },
  ];
  return `<div class="card"><h4>Валюта: факт vs ціль ${infoBtn("currency")}</h4>${svgGrouped(groups)}
    <div class="lg"><span><i style="background:var(--oi-series-invested)"></i>факт</span>
      <span><i style="background:var(--oi-series-neutral)"></i>ціль</span></div></div>`;
}

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
export function fundTable(ctx, kind, head, cells, empty) {
  const ops = fundOps || [];
  const money = (m) => m ? fmtCur(m.amount, curSym(m.currency)) : "—";
  // Найновіші зверху: дивишся майже завжди на щойно імпортоване.
  const rows = ops.filter((o) => o.kind === kind)
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

export function fundOpsHTML(ctx) {
  const ops = fundOps || [];
  // Закритий фонд — не позиція: показувати «0 серт.» означає питати про
  // те, чого вже немає. Його купівлі, продажі й дивіденди лишаються в
  // таблицях нижче — історія записів нікуди не зникає.
  //
  // Виняток — фонд із дірою в журналі: він лишається на видноті саме
  // тому, що його числа неправильні, і сховати його означало б
  // повторити ту помилку, з якої все й почалось.
  const funds = ((ctx.summary || {}).funds || []).filter((f) => f.qty > 0 || f.short > 0);
  if (!ops.length && !funds.length) return "";
  const positions = funds.length ? `<table><thead><tr>
    <th>Фонд</th><th class="num">К-сть</th><th class="num">Ціна</th><th class="num">Вартість</th>
    <th class="num">Вкладено</th><th class="num">Прибуток</th><th class="num">Дивіденди</th>
    <th class="num">Дохідність</th></tr></thead><tbody>
    ${funds.map((f) => {
      const pnl = f.market_value - f.cost_basis;
      const col = pnl >= 0 ? "var(--oi-ok)" : "var(--oi-danger)";
      const short = f.short > 0
        ? `<div style="color:var(--oi-warn);font-size:11px">⚠ продано на ${f.short} серт. більше,
           ніж куплено — у журналі бракує надходження, і числа рядка занижені</div>` : "";
      return `<tr><td><b>${esc(f.fund)}</b>${short}</td><td class="num">${f.qty}</td>
        <td class="num">${(f.last_price || 0).toFixed(4)} ${curSym(f.currency)}${
          f.last_price_date ? `<div class="sub-xs">${dayMonth(f.last_price_date)}</div>` : ""}</td>
        <td class="num">${fmtUAH(f.market_value)}</td><td class="num">${fmtUAH(f.cost_basis)}</td>
        <td class="num" style="color:${col}">${pnl >= 0 ? "+" : ""}${fmtUAH(pnl)}${
          f.realized ? `<div class="sub-xs">продажі ${fmtUAH(f.realized)}</div>` : ""}</td>
        <td class="num">${fmtUAH(f.dividends_net)}${
          f.dividends_tax > 0 ? `<div class="sub-xs">податок ${fmtUAH(f.dividends_tax)}</div>` : ""}</td>
        <td class="num">${f.yield_net_pct > 0 ? f.yield_net_pct.toFixed(1) + "%" : "—"}</td></tr>`;
    }).join("")}</tbody></table>`
    : `<div class="muted">Сертифікатів немає — імпортуй виписку в «Рахунку» або додай купівлю вище.</div>`;

  return `<div class="card"><h2 class="h-row" style="justify-content:space-between">
      <span>Сертифікати фондів ${infoBtn("fundops")}</span></h2>
      <div class="muted" style="margin-bottom:12px">Інший інструмент, ніж ОВДП: ні погашення, ні номіналу,
        ні графіка купонів — натомість ринкова ціна й нерегулярні дивіденди, з яких утримується податок.
        Записи сюди вносить <b>імпорт виписки</b>, руками їх не додають.</div>
      ${positions}
    </div>

    <div class="card"><h2>Лоти фондів</h2>
      ${fundTable(ctx, "buy",
        `<th class="num">ID</th><th>Фонд</th><th class="num">К-сть</th><th class="num">Ціна</th><th class="num">Сплачено</th>`,
        (o, c) => `<td class="num">${o.qty}</td><td class="num">${c.price(o)}</td><td class="num">${c.money(o.amount)}</td>`,
        "Купівель ще немає.")}
    </div>`;
}

// Правка веде в ту форму, якій операція належить: купівлю правиш там,
// де купуєш. «Скасувати» видно лише в режимі правки — поки її не
// натиснули, форма пам'ятає, що вона змінює, а не додає.
// Лишилось саме видалення: форм більше немає, правити записи виписки
// руками не дають.
export function wireFundOps(ctx, main) {
  main.querySelectorAll("[data-delfund]").forEach((b) =>
    b.addEventListener("click", async () => {
      const o = (fundOps || []).find((x) => x.id === +b.dataset.delfund);
      const what = o ? `${FUND_KIND[o.kind] || o.kind} ${o.fund} від ${o.date}` : "запис #" + b.dataset.delfund;
      if (!confirm(`Видалити ${what}? Позиція й ціна перерахуються.`)) return;
      try {
        await ctx.api("DELETE", "funds/" + b.dataset.delfund);
        ctx.toast("Запис видалено");
        await ctx.reload();
      } catch (err) { ctx.toast(String(err.message || err), false); }
    }));
}

export async function renderPortfolio(ctx, main) {
  const s0 = ctx.summary || {};
  const [positions, lots, sales, ops] = await Promise.all([
    ctx.api("GET", "positions"),
    ctx.api("GET", "lots"),
    ctx.api("GET", "sales"),
    ctx.api("GET", "funds").catch(() => []),
  ]);
  setFundOps(ops);
  // «Дохідність» — YTM до погашення від сплаченої ціни, «XIRR» — фактично
  // реалізоване. Тримаємо поруч, бо сенс саме в порівнянні.
  const py = s0.portfolio_yield || {}, xr = s0.xirr || {};
  const pct = (v) => v != null ? v.toFixed(2) + "%" : "—";
  const xirrTiles = Object.keys(xr).length
    ? Object.entries(xr).map(([c, v]) => tile(`XIRR ${curSym(c)}`, pct(v))).join("")
    : tile("XIRR", "—",
        `<div class="sub">гроші мають попрацювати ≥30 днів у середньому</div>`);
  const portTiles = `<div class="tiles flush">
    ${tile("Вкладено (грн-екв.)", fmtUAH(s0.invested_uah + fundsCost(s0)),
      fundsCost(s0) > 0 ? `<div class="sub">з них ${fmtUAH(fundsCost(s0))} у фондах</div>` : "")}
    ${tile("Номінал (грн-екв.)", fmtUAH(s0.nominal_uah_eq))}
    ${tile("Накопичений купон", fmtUAH(s0.accrued_uah || 0),
      `<div class="sub">зароблено, ще не виплачено</div>`)}
    ${Object.entries(py).map(([c, v]) => tile(`ОВДП ${curSym(c)}`, pct(v),
      `<div class="sub">до погашення, від сплаченої ціни</div>`)).join("")}
    ${s0.funds_yield_pct > 0 ? tile("Фонди", pct(s0.funds_yield_pct),
      `<div class="sub">дивіденди після податку, річні</div>`) : ""}
    ${s0.blended_yield_pct > 0 ? tile("Дохідність портфеля", pct(s0.blended_yield_pct),
      `<div class="sub">ОВДП і фонди разом, зважено вкладеним</div>`) : ""}
    ${xirrTiles}
  </div>`;

  // Валютні частки й цілі — тут, а не окремою вкладкою «План»: це
  // характеристика того, ЩО ВЖЕ КУПЛЕНО, і читається вона поруч із
  // рештою складу, а не через клік.
  const st = s0.settings || {};
  const shareTile = (lbl, cur, tgt) => tile(lbl, (cur || 0).toFixed(1) + "%",
    tgt ? `<div class="sub">ціль ${tgt}%</div>` : "");
  const shares = `<div class="tiles flush">
    ${shareTile("Частка USD", s0.usd_share_pct, st.usd_target_share_pct)}
    ${shareTile("Частка EUR", s0.eur_share_pct, st.eur_target_share_pct)}
  </div>`;

  const chart = await chartBlockHTML(ctx);
  main.innerHTML = `
    ${portTiles}
    ${chart}
    <div class="chart-grid">
      ${brokerDonutHTML(ctx)}
      ${currencyChartHTML(ctx)}
    </div>
    ${shares}
    ${rebalanceCard(ctx)}
    ${ladderTableHTML(ctx)}
    ${rateRiskCard(ctx)}
    <div class="card">
      <h2>Нова покупка</h2>
      <form id="lotForm">
        <label style="position:relative">ISIN<input name="isin" required placeholder="UA4000..." autocomplete="off">
          <div id="bondSuggest" class="suggest"></div></label>
        <label>Кількість<input name="qty" type="number" min="1" step="1" required></label>
        <label>Ціна за папір (брудна)<input name="price_per_bond" inputmode="decimal" placeholder="995.00" required></label>
        <label>Комісія (сумарно)<input name="fee" inputmode="decimal" placeholder="0.00"></label>
        <label>Валюта<select name="currency">
          <option value="">авто (з довідника)</option>
          <option value="UAH">UAH</option>
          <option value="USD">USD</option>
          <option value="EUR">EUR</option>
        </select></label>
        <label>Дата купівлі<input name="buy_date" type="date" value="${today()}" required></label>
        <label>Брокер<select name="channel_sel">${ctx.channelOptions(lots)}</select>
          <input name="channel" placeholder="назва каналу" style="margin-top:6px;display:none"></label>
        <label>Нотатка<input name="note"></label>
        <button type="submit">Додати</button>
      </form>
      <div class="muted" id="bondInfo" style="margin-top:8px"></div>
    </div>

    <div class="card">
      <h2>Позиції</h2>
      ${positions.length ? `<table><thead><tr>
        <th>ISIN</th><th class="num">К-сть</th><th class="num">Вкладено</th><th class="num">Номінал</th>
        <th>Погашення</th><th class="num">Днів</th><th>Наст. виплата</th></tr></thead><tbody>
        ${positions.map((p) => `<tr>
          <td>${esc(p.isin)}</td><td class="num">${p.qty}</td><td class="num">${fmtMoney(p.invested)}</td>
          <td class="num">${fmtMoney(p.nominal)}</td><td>${esc(p.maturity)}</td><td class="num">${p.days_to_maturity}</td>
          <td>${p.next_pay_date ? esc(p.next_pay_date) + " · " + fmtMoney(p.next_pay_amount) : "—"}</td></tr>`).join("")}
        </tbody></table>` : `<div class="muted">Позицій немає.</div>`}
    </div>

    <div class="card">
      <h2>Лоти</h2>
      ${lots.length ? `<table><thead><tr>
        <th>ID</th><th>ISIN</th><th class="num">К-сть</th><th class="num">Залишок</th><th class="num">Ціна</th>
        <th class="num">Комісія</th><th>Куплено</th><th>Брокер</th><th></th></tr></thead><tbody>
        ${lots.map((l) => `<tr>
          <td>${l.id}</td><td>${esc(l.isin)}</td><td class="num">${l.qty}</td><td class="num">${l.remaining}</td>
          <td class="num">${fmtMoney(l.price_per_bond)}</td><td class="num">${fmtMoney(l.fee)}</td>
          <td>${esc(l.buy_date)}</td><td>${esc(l.channel || "")}</td>
          <td class="row-actions"><button class="sm warn" data-del="${l.id}">✕</button></td></tr>`).join("")}
        </tbody></table>` : `<div class="muted">Лотів немає.</div>`}
    </div>

    <div class="card">
      <h2>Продаж (вторинний ринок)</h2>
      <form id="saleForm">
        <label>Лот<select name="lot_id" required>
          <option value="">— лот —</option>
          ${lots.filter((l) => l.remaining > 0).map((l) =>
            `<option value="${l.id}" data-cur="${l.price_per_bond.currency}">#${l.id} · ${esc(l.isin)} · зал. ${l.remaining}</option>`).join("")}
        </select></label>
        <label>Дата продажу<input name="sale_date" type="date" value="${today()}" required></label>
        <label>Кількість<input name="qty" type="number" min="1" step="1" required></label>
        <label>Чиста ціна/папір<input name="clean_per_bond" inputmode="decimal" placeholder="1001.50" required></label>
        <label>НКД (сумарно)<input name="accrued" inputmode="decimal" placeholder="0.00"></label>
        <label>Нотатка<input name="note"></label>
        <button type="submit">Записати</button>
      </form>
      ${sales.length ? `<table style="margin-top:14px"><thead><tr>
        <th>Дата</th><th>ISIN</th><th class="num">К-сть</th><th class="num">Чиста</th>
        <th class="num">НКД</th><th class="num">Результат</th></tr></thead><tbody>
        ${sales.map((s) => `<tr>
          <td>${esc(s.sale_date)}</td><td>${esc(s.isin)}</td><td class="num">${s.qty}</td>
          <td class="num">${fmtMoney(s.clean_per_bond)}</td><td class="num">${fmtMoney(s.accrued)}</td>
          <td class="num">${fmtMoney(s.realized_result)}</td></tr>`).join("")}</tbody></table>` : ""}
    </div>

    ${fundOpsHTML(ctx)}
  `;

  main.querySelectorAll("[data-del]").forEach((b) =>
    b.addEventListener("click", async () => {
      if (!confirm("Видалити лот #" + b.dataset.del + "?")) return;
      try { await ctx.api("DELETE", "lots/" + b.dataset.del); ctx.toast("Лот видалено"); ctx.reload(); }
      catch (err) { ctx.toast(String(err.message || err), false); }
    }));

  const isinInput = main.querySelector('input[name="isin"]');
  const sug = main.querySelector("#bondSuggest");
  const hideSug = () => sug.classList.remove("show");
  let dbt;
  isinInput.addEventListener("input", () => {
    clearTimeout(dbt);
    const q = isinInput.value.trim();
    if (q.length < 2) { hideSug(); return; }
    dbt = setTimeout(async () => {
      try {
        const bonds = await ctx.api("GET", "bonds/search?q=" + encodeURIComponent(q));
        if (!bonds || !bonds.length) { hideSug(); return; }
        sug.innerHTML = bonds.map((b) =>
          `<div class="suggest-item" data-isin="${esc(b.isin)}">${esc(b.isin)} · ${esc(b.descr || "")} · ${b.rate_pct}% · до ${esc(b.maturity)}</div>`).join("");
        sug.classList.add("show");
      } catch (_) { hideSug(); }
    }, 300);
  });
  sug.addEventListener("mousedown", (e) => {
    const it = e.target.closest("[data-isin]");
    if (!it) return;
    e.preventDefault();
    isinInput.value = it.dataset.isin;
    hideSug();
    isinInput.dispatchEvent(new Event("change"));
  });
  isinInput.addEventListener("blur", () => setTimeout(hideSug, 150));

  // авто-заповнення з довідника при виборі ISIN — далі лише коригуєш
  isinInput.addEventListener("change", async () => {
    const isin = isinInput.value.trim();
    const info = main.querySelector("#bondInfo");
    if (!isin) { if (info) info.textContent = ""; return; }
    try {
      const b = await ctx.api("GET", "bonds/" + encodeURIComponent(isin));
      if (!b || !b.nominal) return;
      const f = main.querySelector("#lotForm");
      if (["UAH", "USD", "EUR"].includes(b.nominal.currency)) f.currency.value = b.nominal.currency;
      if (!f.price_per_bond.value.trim()) f.price_per_bond.value = b.nominal.amount;
      if (info) info.textContent = `${esc(b.descr || "")} · ${b.rate_pct}% · погашення ${esc(b.maturity)} · номінал ${fmtMoney(b.nominal)}`;
    } catch (_) { if (info) info.textContent = ""; }
  });

  // «інший…» відкриває поле для нової назви каналу
  const chSel = main.querySelector('[name="channel_sel"]');
  const chIn = main.querySelector('[name="channel"]');
  if (chSel && chIn) {
    chSel.addEventListener("change", () => {
      const other = chSel.value === "__other__";
      chIn.style.display = other ? "" : "none";
      if (other) { chIn.value = ""; chIn.focus(); }
    });
  }

  main.querySelector("#lotForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target;
    const channel = f.channel_sel.value === "__other__"
      ? f.channel.value.trim()
      : f.channel_sel.value.trim();
    try {
      await ctx.api("POST", "lots", {
        isin: f.isin.value.trim(), qty: parseInt(f.qty.value, 10),
        price_per_bond: f.price_per_bond.value.trim(), fee: f.fee.value.trim(),
        currency: f.currency.value.trim(), buy_date: f.buy_date.value,
        channel: channel, note: f.note.value.trim(),
      });
      ctx.toast("Лот додано"); ctx.reload();
    } catch (err) { ctx.toast(String(err.message || err), false); }
  });

  main.querySelector("#saleForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target;
    const opt = f.lot_id.selectedOptions[0];
    try {
      await ctx.api("POST", "sales", {
        lot_id: parseInt(f.lot_id.value, 10), sale_date: f.sale_date.value,
        qty: parseInt(f.qty.value, 10), clean_per_bond: f.clean_per_bond.value.trim(),
        accrued: f.accrued.value.trim(), currency: opt ? opt.dataset.cur : "UAH",
        note: f.note.value.trim(),
      });
      ctx.toast("Продаж записано"); ctx.reload();
    } catch (err) { ctx.toast(String(err.message || err), false); }
  });

  wireFundOps(ctx, main);
  // Таблиця знімків — у самому низу і згорнута: це архів, до якого
  // звертаються рідко, але коли треба — потрібні саме числа, а не крива.
  main.insertAdjacentHTML("beforeend", snapshotsTableHTML(ctx));
}

// ---------- структура й ризик ----------
// Валютне ребалансування: скільки бракує до цільових часток і чи це
// взагалі досяжно (найдешевший папір може бути більший за цільову суму).
export function rebalanceCard(ctx) {
  const rows = (ctx.summary && ctx.summary.rebalance) || [];
  if (!rows.length) return "";
  const sym = { USD: "$", EUR: "€" };
  const num = (v, d = 2) => Number(v || 0).toLocaleString("uk-UA", { maximumFractionDigits: d });
  const body = rows.map((r) => {
    const s = sym[r.currency] || r.currency;
    const head = `<b>${esc(r.currency)}</b> — ціль ${r.target_pct}%, зараз ${r.current_pct}%`;
    if (r.deficit_uah <= 0) {
      return `<div style="margin-bottom:12px">${head} — <span style="color:var(--oi-ok)">ціль досягнута ✅</span></div>`;
    }
    const need = `Бракує до цілі: <b>${fmtUAH(r.deficit_uah)}</b> (≈ ${num(r.deficit_native)} ${s})`;
    if (!r.feasible) {
      return `<div style="margin-bottom:12px">${head}<br>${need}<br>
        <span style="color:var(--oi-warn)">⚠ Ще зарано:</span> найдешевший ${esc(r.currency)}-папір коштує
        ${fmtUAH(r.bond_cost_uah)} (${num(r.bond_cost_native, 0)} ${s}) — це більше за всю цільову суму.
        Один такий папір вписався б у ціль ${r.target_pct}% при капіталі <b>${fmtUAH(r.min_portfolio_uah)}</b>.</div>`;
    }
    const buy = r.can_buy > 0
      ? `вистачає на <b>${r.can_buy}</b> папер(и)`
      : `на папір бракує — сконвертуй ще ≈ <b>${fmtUAH(r.convert_uah)}</b>`;
    return `<div style="margin-bottom:12px">${head}<br>${need}<br>
      Найдешевший папір: ${num(r.bond_cost_native, 0)} ${s} ≈ ${fmtUAH(r.bond_cost_uah)}.
      Готівка: ${num(r.cash_native)} ${s} — ${buy}.</div>`;
  }).join("");
  return `<div class="card"><h2>Валютне ребалансування</h2>
    <div class="muted" style="margin-bottom:10px">Частки рахуються від сукупного капіталу (номінал + рахунок).</div>
    ${body}</div>`;
}

// Процентний ризик: дюрація за реальним графіком виплат + сценарії.
export function rateRiskCard(ctx) {
  const rr = ctx.summary && ctx.summary.rate_risk;
  if (!rr || !rr.duration_years) return "";
  const scen = (rr.scenarios || []).map((x) => {
    const col = x.change_pct >= 0 ? "var(--oi-ok)" : "var(--oi-danger)";
    const sgn = v => (v > 0 ? "+" : "");
    return `<tr><td>${sgn(x.delta_pp)}${x.delta_pp} п.п.</td>
      <td class="num" style="color:${col}">${sgn(x.change_pct)}${x.change_pct}%</td>
      <td class="num" style="color:${col}">${sgn(x.change_uah)}${fmtUAH(x.change_uah)}</td></tr>`;
  }).join("");
  return `<div class="card"><h2>Ризик ставок</h2>
    <div class="tiles" style="margin:0 0 10px">
      <div class="tile"><div class="lbl">Дюрація (Маколея)</div><div class="val">${rr.duration_years} р.</div></div>
      <div class="tile"><div class="lbl">Модифікована</div><div class="val">${rr.modified_dur}</div></div>
      <div class="tile"><div class="lbl">Приведена вартість</div><div class="val">${fmtUAH(rr.pv_uah)}</div></div>
    </div>
    
    <table><thead><tr><th>Зміна ставок</th><th class="num">Вартість</th><th class="num">У грошах</th></tr></thead>
      <tbody>${scen}</tbody></table>
    <div class="muted" style="margin-top:8px;font-size:13px">Дюрація — середньозважений строк повернення грошей.
      Модифікована показує, на скільки % змінюється вартість при зміні ставок на 1 п.п.
      <b>Тримаєш до погашення — просадка лише паперова</b>: ризик реалізується при продажі на вторинці.</div>
  </div>`;
}

// Драбина: спершу стовпчики, під ними числа з розбивкою по валютах.
// Разом, а не двома картками: графік показує ФОРМУ (де діри, де горб),
// таблиця — суми, і одне без одного відповідає лише на пів питання.
export function ladderTableHTML(ctx) {
  const lad = (ctx.summary || {}).ladder || [];
  const maxV = Math.max(1, ...lad.map((r) => Math.max(r.uah || 0, r.usd || 0, r.eur || 0)));
  const bar = (v, color) => v > 0
    ? `<span class="bar" style="width:${Math.max(4, (v / maxV) * 120)}px;background:${color}"></span>` : "";
  const fx = (v, sym) => v ? Number(v).toLocaleString("uk-UA", { minimumFractionDigits: 2 }) + " " + sym : "—";
  return `<div class="card">
    <h2 class="h-row">Драбина погашень ${infoBtn("ladder")}</h2>
    <div class="sub">Скільки номіналу повертається за роками (окремо UAH / USD / EUR).</div>
    ${ladderBarsHTML(ctx)}
    ${lad.length ? `<table><thead><tr>
      <th>Рік</th><th class="num">UAH</th><th></th><th class="num">USD</th><th></th><th class="num">EUR</th><th></th></tr></thead><tbody>
      ${lad.map((r) => `<tr>
        <td>${r.year}</td>
        <td class="num">${r.uah ? fmtUAH(r.uah) : "—"}</td><td>${bar(r.uah, "var(--oi-accent)")}</td>
        <td class="num">${fx(r.usd, "$")}</td><td>${bar(r.usd, "var(--oi-info)")}</td>
        <td class="num">${fx(r.eur, "€")}</td><td>${bar(r.eur, "var(--oi-warn)")}</td></tr>`).join("")}</tbody></table>`
      : `<div class="muted">Драбина порожня — додайте папери в портфель.</div>`}
  </div>`;
}


// ---------- історія ----------

export function snapNonZero(s) {
  return (s.invested_uah || 0) > 0 || (s.nominal_uah_eq || 0) > 0 || (s.account_uah || 0) > 0;
}

// Блок «Як росте» — живе на «Огляді» (дивишся часто, окрема вкладка зайва).
export async function chartBlockHTML(ctx) {
  const all = await ctx.api("GET", "snapshots").catch(() => []);
  // Порожні знімки до появи портфеля (зроблені автоматично о 06:10 ще без
  // даних) не малюємо — інакше вони «якорять» графік у нулі й лінія
  // виглядає як фейковий стрибок 0 → капітал за один день.
  let i = 0;
  while (i < (all || []).length && !snapNonZero(all[i])) i++;
  const snaps = (all || []).slice(i);
  snapsCache = snaps;
  if (snaps.length < 2) {
    return `<div class="card"><h2 class="h-row">Як росте ${infoBtn("growth")}</h2>
      <div class="muted">Крива будується з добових знімків (пишуться щодня о 06:10,
      або одразу після «↻ Оновити НБУ»). Потрібно ≥2 знімки з даними — наразі ${snaps.length}.
      Порожні знімки до появи портфеля не рахуються.</div></div>`;
  }
  const dates = snaps.map((s) => s.date);
  // План — накопичувальна сума фактично діючих цілей: кожен день додає
  // target_того_дня / днів_у_місяці. Тож зміна цілі впливає лише вперед,
  // а минула частина лінії лишається такою, якою план був тоді.
  const daysInMonth = (ds) => { const p = ds.split("-"); return new Date(+p[0], +p[1], 0).getDate(); };
  let acc = 0, anyTarget = false;
  const plan = snaps.map((s) => {
    const t = s.month_target_uah || 0;
    if (t > 0) anyTarget = true;
    acc += t / daysInMonth(s.date);
    return acc;
  });
  const series = [
    { name: "Вкладено (грн-екв.)", color: "var(--oi-series-invested)", values: snaps.map((s) => s.invested_uah) },
    { name: "Номінал", color: "var(--oi-series-nominal)", values: snaps.map((s) => s.nominal_uah_eq) },
    { name: "Рахунок", color: "var(--oi-series-account)", values: snaps.map((s) => s.account_uah || 0) },
    ...(snaps.some((s) => (s.funds_uah || 0) > 0)
      // Нулі на початку — не «не було», а «тоді ще не рахували»:
      // колонка у знімку з'явилась пізніше за самі фонди.
      ? [{ name: "Фонди", color: "var(--oi-series-funds)", values: snaps.map((s) => s.funds_uah || 0) }] : []),
  ];
  if (anyTarget) series.push({ name: "План (накопич.)", color: "var(--oi-series-plan)", values: plan, dash: true });
  const x = (ctx.summary || {}).xirr || {};
  const xp = Object.entries(x).filter(([, v]) => v != null).map(([c, v]) => `${curSym(c)} ${v.toFixed(2)}%`);
  const xirrLine = xp.length
    ? `Фактична дохідність (XIRR): <b>${xp.join(" · ")}</b> — деталі у «Портфелі»`
    : `Фактична дохідність (XIRR) з'явиться, коли набереться 30 днів історії`;
  const { svg, legend } = seriesChart(dates, series);
  return `<div class="card"><h2 class="h-row">Як росте ${infoBtn("growth")}</h2>
    <div style="overflow-x:auto">${svg}</div><div style="margin-top:8px">${legend}</div>
    <div class="muted" style="margin-top:8px;font-size:13px">«План (накопич.)» — цільовий темп вкладень наростаючим підсумком (місячна ціль ÷ дні місяця). Факт вище пунктиру = випереджаєш план, нижче = відстаєш.</div>
    <div class="muted" style="margin-top:8px;font-size:13px;border-top:1px solid var(--oi-border);padding-top:8px">${xirrLine}</div></div>`;
}

export function snapshotsTableHTML(ctx) {
  const snaps = snapsCache || [];
  if (snaps.length < 2) return "";
  return `<div class="card"><h2>Останні знімки</h2>
    <table><thead><tr><th>Дата</th><th class="num">Вкладено</th><th class="num">Номінал</th>
      <th class="num">Частка USD</th><th class="num">Не перевкл.</th></tr></thead>
    <tbody>${snaps.slice(-14).reverse().map((s) => `<tr>
      <td>${esc(s.date)}</td><td class="num">${fmtUAH(s.invested_uah)}</td><td class="num">${fmtUAH(s.nominal_uah_eq)}</td>
      <td class="num">${(s.usd_share_pct || 0).toFixed(1)}%</td><td class="num">${fmtUAH(s.uninvested_uah)}</td></tr>`).join("")}</tbody></table>
  </div>`;
}

