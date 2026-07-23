// Розділ «Портфель» — що в мене є.
//
// Усе про склад: позиції ОВДП і сертифікати фондів, лоти, продажі,
// дивіденди, дохідності — і характеристики того, що вже куплено:
// частки по брокерах і валютах, драбина погашень, процентний ризик,
// історія «Як росте». Раніше це було розкидано по трьох вкладках
// («Портфель», «План») і «Огляду», хоча відповідає на одне питання.
//
// Позиції всіх трьох інструментів — в ОДНІЙ таблиці. Раніше вони жили
// трьома секціями зі своїми колонками, бо в сертифіката немає ні
// номіналу, ні графіка купонів, і спільна таблиця зламала б обидві.
// Заперечення було справедливе до колонок, а не до самої ідеї: спільними
// лишились тільки ті, що мають сенс для кожного (вкладено, вартість,
// реальна дохідність, строк), а специфіка пішла в рядок-деталі, який
// розкривається по кліку. Виграш — те, заради чого все й робилось: видно
// весь портфель одразу й видно, що з нього дохідніше.

import {
  esc, curSym, today, dayMonth, plural,
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
// filter — необов'язковий: «Портфель» показує лоти ОДНОГО фонду в рядку
// його позиції, «Імпорт» — усі. Без нього поводиться як раніше.
export function fundTable(ctx, kind, head, cells, empty, filter) {
  const ops = fundOps || [];
  const money = (m) => m ? fmtCur(m.amount, curSym(m.currency)) : "—";
  // Найновіші зверху: дивишся майже завжди на щойно імпортоване.
  const rows = ops.filter((o) => o.kind === kind && (!filter || filter(o)))
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

// ---------- єдина таблиця позицій ----------

const KIND_LABEL = { bond: "ОВДП", fund: "Фонд", deposit: "Вклад" };
const POS_COLS = 7;

// Які рядки розкриті. Живе поза рендером навмисно: ctx.reload() стирає
// main.innerHTML цілком, і без цього кожне поповнення згортало б той
// вклад, у якому ти його щойно записав.
const openRows = new Set();

// Реальна дохідність — колонка, заради якої таблиця й спільна. Підпис
// під числом каже, з чого воно взялося: обіцянка (купон, ставка) чи
// оцінка (дивіденди фонду). Ховати цю різницю за одним числом нечесно.
function realCell(pct, basis) {
  if (!pct) return `<td class="num muted col-yield">—</td>`;
  return `<td class="num col-yield">${pct.toFixed(1)}%<div class="sub-xs">${esc(basis || "")}</div></td>`;
}

function bondDetailHTML(p, lots, sales) {
  const myLots = lots.filter((l) => l.isin === p.isin);
  const mySales = sales.filter((s) => s.isin === p.isin);
  const next = p.next_pay_date
    ? `<div class="sub">Наступна виплата: ${esc(p.next_pay_date)} · ${fmtMoney(p.next_pay_amount)}</div>` : "";
  const lotsTbl = myLots.length ? `<h4>Лоти</h4><table><thead><tr>
      <th>ID</th><th class="num">К-сть</th><th class="num">Залишок</th><th class="num">Ціна</th>
      <th class="num">Комісія</th><th>Куплено</th><th>Брокер</th><th></th></tr></thead><tbody>
      ${myLots.map((l) => `<tr><td>${l.id}</td><td class="num">${l.qty}</td><td class="num">${l.remaining}</td>
        <td class="num">${fmtMoney(l.price_per_bond)}</td><td class="num">${fmtMoney(l.fee)}</td>
        <td>${esc(l.buy_date)}</td><td>${esc(l.channel || "")}</td>
        <td class="row-actions"><button class="sm warn" data-del="${l.id}">✕</button></td></tr>`).join("")}
      </tbody></table>` : "";
  const salesTbl = mySales.length ? `<h4 style="margin-top:12px">Продажі</h4><table><thead><tr>
      <th>Дата</th><th class="num">К-сть</th><th class="num">Чиста</th>
      <th class="num">НКД</th><th class="num">Результат</th></tr></thead><tbody>
      ${mySales.map((s) => `<tr><td>${esc(s.sale_date)}</td><td class="num">${s.qty}</td>
        <td class="num">${fmtMoney(s.clean_per_bond)}</td><td class="num">${fmtMoney(s.accrued)}</td>
        <td class="num">${fmtMoney(s.realized_result)}</td></tr>`).join("")}
      </tbody></table>` : "";
  return next + lotsTbl + salesTbl;
}

function fundDetailHTML(ctx, f) {
  const bits = [`Дивіденди ${fmtUAH(f.dividends_net)}`];
  if (f.dividends_tax > 0) bits.push(`податок ${fmtUAH(f.dividends_tax)}`);
  if (f.realized) bits.push(`продажі ${fmtUAH(f.realized)}`);
  if (f.last_price_date) bits.push(`ціна від ${dayMonth(f.last_price_date)}`);
  return `<div class="sub">${bits.join(" · ")}</div>
    <h4 style="margin-top:12px">Лоти</h4>
    ${fundTable(ctx, "buy",
      `<th class="num">ID</th><th>Фонд</th><th class="num">К-сть</th><th class="num">Ціна</th><th class="num">Сплачено</th>`,
      (o, c) => `<td class="num">${o.qty}</td><td class="num">${c.price(o)}</td><td class="num">${c.money(o.amount)}</td>`,
      "Купівель ще немає.", (o) => o.fund === f.fund)}`;
}

function depositDetailHTML(d) {
  const topups = d.topups || [];
  // Поповнення дозволяємо лише позначеному вкладу, а розірвання — будь-
  // якому: закрити достроково можна який завгодно.
  const topupForm = d.replenishable ? `<h4>Поповнити</h4>
    <form class="topup-form" data-topup-form="${d.id}">
      <label>Дата поповнення<input name="date" type="date" value="${today()}" required></label>
      <label>Сума<input name="amount" inputmode="decimal" value="${d.principal.amount}" required></label>
      <button type="submit">Поповнити</button>
    </form>` : "";
  const topupsTbl = topups.length ? `<h4 style="margin-top:12px">Поповнення</h4><table><tbody>
    ${topups.map((t) => `<tr><td class="muted">${esc(t.date)}</td><td class="num">${fmtMoney(t.amount)}</td>
      <td class="row-actions"><button class="sm warn" data-deltopup="${d.id}:${t.id}">✕</button></td></tr>`).join("")}
    </tbody></table>` : "";
  return `${topupForm}${topupsTbl}
    <h4 style="margin-top:12px">Розірвати достроково</h4>
    <form class="close-form" data-close-form="${d.id}">
      <label>Дата розірвання<input name="closed_date" type="date" value="${today()}" required></label>
      <label>Отримано (тіло + відсотки)<input name="closed_amount" inputmode="decimal" placeholder="${d.balance.amount}" required></label>
      <button type="submit">Підтвердити розірвання</button>
    </form>`;
}

// Три інструменти зводяться до одного вигляду рядка. sortBy — вкладене
// в НАТИВНІЙ валюті: сортуємо лише всередині групи одного інструмента,
// тож валюти між собою тут не зустрічаються.
function positionItems(ctx, positions, lots, sales, deposits) {
  const bonds = positions.map((p) => ({
    key: "bond:" + p.isin, kind: "bond",
    name: `<b>${esc(p.isin)}</b><div class="sub-xs">${p.qty} шт.</div>`,
    invested: fmtMoney(p.invested), value: fmtMoney(p.nominal),
    pct: p.real_pct, basis: p.yield_basis,
    term: `${esc(p.maturity)}<div class="sub-xs">${p.days_to_maturity} дн.</div>`,
    actions: "", sortBy: Number((p.invested || {}).amount || 0),
    detail: bondDetailHTML(p, lots, sales),
  }));

  // Закритий фонд — не позиція: показувати «0 серт.» означає питати про
  // те, чого вже немає. Виняток — фонд із дірою в журналі: він лишається
  // на видноті саме тому, що його числа неправильні.
  const funds = ((ctx.summary || {}).funds || [])
    .filter((f) => f.qty > 0 || f.short > 0)
    .map((f) => {
      const pnl = f.market_value - f.cost_basis;
      const col = pnl >= 0 ? "var(--oi-ok)" : "var(--oi-danger)";
      const short = f.short > 0
        ? `<div class="sub-xs" style="color:var(--oi-warn)">⚠ продано на ${f.short} серт. більше,
           ніж куплено — у журналі бракує надходження, числа занижені</div>` : "";
      return {
        key: "fund:" + f.fund, kind: "fund",
        name: `<b>${esc(f.fund)}</b>${short}`,
        invested: fmtUAH(f.cost_basis),
        value: `${fmtUAH(f.market_value)}<div class="sub-xs" style="color:${col}">${pnl >= 0 ? "+" : ""}${fmtUAH(pnl)}</div>`,
        pct: f.real_pct, basis: f.yield_basis,
        // У сертифіката строку немає — на його місці те, що для фонду
        // важить натомість: скільки їх і почім останній раз.
        term: `<span class="muted">безстроково</span><div class="sub-xs">${f.qty} серт. · ${(f.last_price || 0).toFixed(4)} ${curSym(f.currency)}</div>`,
        actions: "", sortBy: f.cost_basis,
        detail: fundDetailHTML(ctx, f),
      };
    });

  const deps = deposits.filter((d) => !d.closed_date).map((d) => {
    const topups = (d.topups || []).length;
    return {
      key: "dep:" + d.id, kind: "deposit",
      name: `<b>${esc(d.bank || "—")}</b><div class="sub-xs">${PAYOUT_LABEL[d.payout] || d.payout}${d.capitalized ? " · кап." : ""}</div>`,
      invested: `${fmtMoney(d.principal)}${topups ? `<div class="sub-xs">+${topups} поповн.</div>` : ""}`,
      value: fmtMoney(d.balance),
      pct: d.real_pct, basis: d.yield_basis,
      term: `${esc(d.maturity_date)}<div class="sub-xs">${daysUntil(d.maturity_date)} дн. · ${d.rate_pct}%</div>`,
      actions: `<label class="sub-xs" style="flex-direction:row;align-items:center;gap:4px;display:inline-flex"
             title="Чи приймає цей вклад поповнення — від цього залежить, чи радить його помічник">
          <input type="checkbox" data-repl="${d.id}" style="width:auto"${d.replenishable ? " checked" : ""}>попов.</label>
        <button class="sm warn" data-deldep="${d.id}">✕</button>`,
      sortBy: Number(d.principal.amount), detail: depositDetailHTML(d),
    };
  });

  const bySize = (a, b) => b.sortBy - a.sortBy;
  return [...bonds.sort(bySize), ...funds.sort(bySize), ...deps.sort(bySize)];
}

export function positionsTableHTML(ctx, positions, lots, sales, deposits) {
  const items = positionItems(ctx, positions, lots, sales, deposits);
  if (!items.length) {
    return `<div class="card"><h2>Позиції</h2>
      <div class="muted">Позицій ще немає — почни з покупки або відкрий вклад нижче.</div></div>`;
  }
  const rows = items.map((it) => {
    const open = openRows.has(it.key);
    return `<tr>
      <td class="col-kind"><button class="caret${open ? " open" : ""}" data-exp="${it.key}"
            aria-expanded="${open}" title="Показати, звідки взялася позиція">▸</button><span
          class="pill pill-${it.kind}">${KIND_LABEL[it.kind]}</span></td>
      <td>${it.name}</td>
      <td class="num">${it.invested}</td>
      <td class="num">${it.value}</td>
      ${realCell(it.pct, it.basis)}
      <td>${it.term}</td>
      <td class="row-actions" style="white-space:nowrap">${it.actions}</td></tr>
    <tr class="detail-row" data-detail="${it.key}"${open ? "" : ` style="display:none"`}>
      <td colspan="${POS_COLS}">${it.detail}</td></tr>`;
  }).join("");

  return `<div class="card"><h2 class="h-row">Позиції ${infoBtn("fundops")}</h2>
    <div class="table-scroll"><table><thead><tr>
      <th class="col-kind">Тип</th><th>Назва</th><th class="num">Вкладено</th><th class="num">Вартість</th>
      <th class="num col-yield">Дохідність</th><th>Строк</th><th></th></tr></thead>
      <tbody>${rows}</tbody></table></div>
    <div class="sub" style="margin-top:10px">Дохідність — реальна річна після податку, у сьогоднішній
      купівельній спроможності: саме вона порівнює ОВДП, фонд і вклад між собою. Стрілка розкриває
      лоти, продажі й поповнення.</div>
  </div>`;
}

// Стан згорнутих секцій переживає перезавантаження — за прикладом
// перемикача ₴/$ у прогнозі. Розкривати ті самі три секції щоразу
// заново дратує більше, ніж саме згортання допомагає.
const FOLDS_KEY = "oddinvest.folds";

function readFolds() {
  try { return JSON.parse(localStorage.getItem(FOLDS_KEY) || "{}") || {}; }
  catch (_) { return {}; }
}

export function wireDisclosures(main) {
  const folds = readFolds();
  main.querySelectorAll("[data-fold]").forEach((d) => {
    if (folds[d.dataset.fold]) d.open = true;
    // Слухач на кожному, а не делегований: подія toggle не спливає.
    d.addEventListener("toggle", () => {
      const cur = readFolds();
      if (d.open) cur[d.dataset.fold] = true; else delete cur[d.dataset.fold];
      try { localStorage.setItem(FOLDS_KEY, JSON.stringify(cur)); } catch (_) {}
    });
  });
}

export function wirePositions(ctx, main) {
  main.querySelectorAll("[data-exp]").forEach((b) =>
    b.addEventListener("click", () => {
      const key = b.dataset.exp;
      const row = main.querySelector(`[data-detail="${key}"]`);
      if (!row) return;
      const open = row.style.display === "none";
      row.style.display = open ? "" : "none";
      b.classList.toggle("open", open);
      b.setAttribute("aria-expanded", String(open));
      if (open) openRows.add(key); else openRows.delete(key);
    }));
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

// Плитки дохідностей: YTM до погашення від сплаченої ціни поряд з XIRR
// (фактично реалізованим) — сенс саме в порівнянні одного з одним.
function yieldTilesHTML(ctx) {
  const s0 = ctx.summary || {};
  const py = s0.portfolio_yield || {}, xr = s0.xirr || {};
  const pct = (v) => v != null ? v.toFixed(2) + "%" : "—";
  const xirrTiles = Object.keys(xr).length
    ? Object.entries(xr).map(([c, v]) => tile(`XIRR ${curSym(c)}`, pct(v))).join("")
    : tile("XIRR", "—",
        `<div class="sub">гроші мають попрацювати ≥30 днів у середньому</div>`);
  return `<div class="tiles flush">
    ${tile("Вкладено (грн-екв.)", fmtUAH(s0.invested_uah + fundsCost(s0)),
      fundsCost(s0) > 0 ? `<div class="sub">з них ${fmtUAH(fundsCost(s0))} у фондах</div>` : "")}
    ${tile("Номінал (грн-екв.)", fmtUAH(s0.nominal_uah_eq))}
    ${s0.deposits_uah > 0 ? tile("Вклади (грн-екв.)", fmtUAH(s0.deposits_uah),
      `<div class="sub">тіло діючих банківських вкладів</div>`) : ""}
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
}

// Валютні частки й цілі — тут, а не окремою вкладкою «План»: це
// характеристика того, ЩО ВЖЕ КУПЛЕНО, і читається вона поруч із
// рештою складу, а не через клік.
function shareTilesHTML(ctx) {
  const s0 = ctx.summary || {}, st = s0.settings || {};
  const shareTile = (lbl, cur, tgt) => tile(lbl, (cur || 0).toFixed(1) + "%",
    tgt ? `<div class="sub">ціль ${tgt}%</div>` : "");
  return `<div class="tiles flush">
    ${shareTile("Частка USD", s0.usd_share_pct, st.usd_target_share_pct)}
    ${shareTile("Частка EUR", s0.eur_share_pct, st.eur_target_share_pct)}
  </div>`;
}

// ---------- запис операцій ----------
// Усі форми — в одній картці й під згорнутими заголовками. Вони
// займали п'яту частину висоти розділу, хоч потрібні кілька разів на
// місяць: до складу портфеля заходять дивитись, а не заповнювати.
//
// Розмітка форм при цьому рендериться ЗАВЖДИ — <details> лише ховає.
// wireBonds/wireDeposits шукають #lotForm, #saleForm, #termDepForm через
// querySelector без перевірки на null, і умовний рендер обірвав би
// проводку разом з усім, що вішається після неї.

// disclosure — згорнута секція з підписом. hint — сірий текст праворуч
// від заголовка, коли треба сказати, коли цим користуються.
function disclosure(key, title, body, hint = "") {
  return `<details class="disclosure" data-fold="${key}">
    <summary>${title}${hint ? `<span class="hint">${hint}</span>` : ""}</summary>
    <div class="disclosure-body">${body}</div>
  </details>`;
}

function bondBuyFormHTML(ctx, lots) {
  return `
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
      <div class="muted" id="bondInfo" style="margin-top:8px"></div>`;
}

function bondSaleFormHTML(ctx, lots) {
  return `
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
      <div class="sub" style="margin-top:10px">Записані продажі видно в рядку того паперу, якого вони
        стосуються — розкрий позицію стрілкою.</div>`;
}

// Одна картка на всі три форми: «записати операцію» — це одне питання,
// а не три різні, і три окремі картки казали б протилежне.
function entryCardHTML(ctx, lots) {
  return `<div class="card"><h2>Записати операцію</h2>
    ${disclosure("buy", "Нова покупка ОВДП", bondBuyFormHTML(ctx, lots))}
    ${disclosure("sale", "Продаж на вторинному ринку", bondSaleFormHTML(ctx, lots))}
    ${disclosure("dep", `Новий вклад ${infoBtn("deposit")}`, depositFormHTML(ctx))}
    <div class="sub" style="margin-top:12px">Сертифікати фондів сюди не вносять руками —
      їх приносить імпорт виписки в розділі «Гроші».</div>
  </div>`;
}

function wireBonds(ctx, main) {
  main.querySelectorAll("[data-del]").forEach((b) =>
    b.addEventListener("click", async () => {
      if (!confirm("Видалити лот #" + b.dataset.del + "?")) return;
      try { await ctx.api("DELETE", "lots/" + b.dataset.del); ctx.toast("Лот видалено"); ctx.reload(); }
      catch (err) { ctx.toast(String(err.message || err), false); }
    }));

  // Селектори — від САМОЇ форми, а не від main. Поля звуться загально
  // («isin», «channel»), а розділ тепер зводить в одну сторінку три
  // інструменти: перший-ліпший майбутній інпут із такою назвою тихо
  // перехопив би автопідказку, і зламалось би не там, де змінили.
  const buyForm = main.querySelector("#lotForm");
  const isinInput = buyForm.querySelector('input[name="isin"]');
  const sug = buyForm.querySelector("#bondSuggest");
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
      const f = buyForm;
      if (["UAH", "USD", "EUR"].includes(b.nominal.currency)) f.currency.value = b.nominal.currency;
      if (!f.price_per_bond.value.trim()) f.price_per_bond.value = b.nominal.amount;
      if (info) info.textContent = `${esc(b.descr || "")} · ${b.rate_pct}% · погашення ${esc(b.maturity)} · номінал ${fmtMoney(b.nominal)}`;
    } catch (_) { if (info) info.textContent = ""; }
  });

  // «інший…» відкриває поле для нової назви каналу
  const chSel = buyForm.querySelector('[name="channel_sel"]');
  const chIn = buyForm.querySelector('[name="channel"]');
  if (chSel && chIn) {
    chSel.addEventListener("change", () => {
      const other = chSel.value === "__other__";
      chIn.style.display = other ? "" : "none";
      if (other) { chIn.value = ""; chIn.focus(); }
    });
  }

  buyForm.addEventListener("submit", async (e) => {
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
}

// «Портфель» = склад цілком. Порядок відповідає порядку питань: скільки
// всього і як росте → як розкладене → ЩО САМЕ я маю (одна таблиця на всі
// інструменти) → чим ризикую → чим записати нову операцію.
export async function renderPortfolio(ctx, main) {
  const [positions, lots, sales, ops, deposits] = await Promise.all([
    ctx.api("GET", "positions"),
    ctx.api("GET", "lots"),
    ctx.api("GET", "sales"),
    ctx.api("GET", "funds").catch(() => []),
    ctx.api("GET", "term-deposits").catch(() => []),
  ]);
  setFundOps(ops);
  ctx._deposits = deposits; // wireDeposits реконструює вклад для закриття

  const chart = await chartBlockHTML(ctx);
  main.innerHTML = `
    ${yieldTilesHTML(ctx)}
    ${chart}
    <div class="chart-grid">
      ${brokerDonutHTML(ctx)}
      ${currencyChartHTML(ctx)}
    </div>
    ${shareTilesHTML(ctx)}
    ${rebalanceCard(ctx)}
    ${ladderTableHTML(ctx)}
    ${positionsTableHTML(ctx, positions, lots, sales, deposits)}
    ${entryCardHTML(ctx, lots)}
    ${rateRiskCard(ctx)}
    ${closedDepositsHTML(ctx, deposits)}
    ${snapshotsTableHTML(ctx)}
  `;

  wirePositions(ctx, main);
  wireBonds(ctx, main);
  wireFundOps(ctx, main);
  wireDeposits(ctx, main);
  // Останнім: до цього моменту вся розмітка вже на місці, включно з тією,
  // що всередині згорнутих секцій.
  wireDisclosures(main);
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
  // Згорнутий: до дюрації звертаються раз на кілька місяців, а місця
  // вона займала більше за таблицю позицій.
  return `<div class="card">${disclosure("risk", "Ризик ставок", `
    <div class="tiles" style="margin:0 0 10px">
      <div class="tile"><div class="lbl">Дюрація (Маколея)</div><div class="val">${rr.duration_years} р.</div></div>
      <div class="tile"><div class="lbl">Модифікована</div><div class="val">${rr.modified_dur}</div></div>
      <div class="tile"><div class="lbl">Приведена вартість</div><div class="val">${fmtUAH(rr.pv_uah)}</div></div>
    </div>
    
    <table><thead><tr><th>Зміна ставок</th><th class="num">Вартість</th><th class="num">У грошах</th></tr></thead>
      <tbody>${scen}</tbody></table>
    <div class="muted" style="margin-top:8px;font-size:13px">Дюрація — середньозважений строк повернення грошей.
      Модифікована показує, на скільки % змінюється вартість при зміні ставок на 1 п.п.
      <b>Тримаєш до погашення — просадка лише паперова</b>: ризик реалізується при продажі на вторинці.</div>`,
    `дюрація ${rr.duration_years} р.`)}</div>`;
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
  // Тепер справді згорнута — раніше про це казав лише коментар. Це архів:
  // крива вище відповідає на те саме питання, а числа потрібні зрідка.
  const shown = Math.min(snaps.length, 14);
  return `<div class="card">${disclosure("snaps", "Останні знімки", `
    <div class="table-scroll"><table><thead><tr><th>Дата</th><th class="num">Вкладено</th><th class="num">Номінал</th>
      <th class="num">Частка USD</th><th class="num">Не перевкл.</th></tr></thead>
    <tbody>${snaps.slice(-14).reverse().map((s) => `<tr>
      <td>${esc(s.date)}</td><td class="num">${fmtUAH(s.invested_uah)}</td><td class="num">${fmtUAH(s.nominal_uah_eq)}</td>
      <td class="num">${(s.usd_share_pct || 0).toFixed(1)}%</td><td class="num">${fmtUAH(s.uninvested_uah)}</td></tr>`).join("")}</tbody></table></div>`,
    `${shown} ${plural(shown, "день", "дні", "днів")}`)}</div>`;
}

// ---------- банківські вклади ----------
// Третій інструмент: розклад, як в облігації, але оподаткований, як фонд,
// і без вторинного ринку. Діючі вклади живуть у спільній таблиці позицій
// — тут лишились форма відкриття й архів розірваних.

const PAYOUT_LABEL = { end: "у кінці строку", monthly: "щомісяця", quarterly: "щокварталу" };

function daysUntil(iso) {
  return Math.round((new Date(iso + "T00:00:00").getTime() - Date.now()) / 86400000);
}

function depositFormHTML(ctx) {
  return `
    <form id="termDepForm">
      <label>Банк<select name="bank">${ctx.brokerOptions()}</select></label>
      <label>Валюта<select name="currency"><option>UAH</option><option>USD</option><option>EUR</option></select></label>
      <label>Тіло<input name="principal" inputmode="decimal" placeholder="100000.00" required></label>
      <label>Ставка, %<input name="rate_pct" inputmode="decimal" placeholder="16.5" required></label>
      <label>Відкрито<input name="open_date" type="date" value="${today()}" required></label>
      <label>Погашення<input name="maturity_date" type="date" required></label>
      <label>Виплата відсотків<select name="payout">
        <option value="end">у кінці строку</option>
        <option value="monthly">щомісяця</option>
        <option value="quarterly">щокварталу</option>
      </select></label>
      <label style="flex-direction:row;align-items:center;gap:8px">
        <input name="capitalized" type="checkbox" style="width:auto">Капіталізація</label>
      <label style="flex-direction:row;align-items:center;gap:8px">
        <input name="replenishable" type="checkbox" style="width:auto">Поповнюваний</label>
      <label>Податок, %<input name="tax_pct" inputmode="decimal" placeholder="19.5 (за замовч.)"></label>
      <label>Нотатка<input name="note"></label>
      <button type="submit">Додати</button>
    </form>`;
}

// Розірвані вклади — вже не позиція, а історія: згорнуто.
export function closedDepositsHTML(ctx, deposits) {
  const closed = deposits.filter((d) => d.closed_date);
  if (!closed.length) return "";
  return `<div class="card">${disclosure("closeddep", "Закриті достроково", `
    <div class="table-scroll"><table><thead><tr><th>Банк</th><th class="num">Тіло</th><th>Розірвано</th>
      <th class="num">Отримано</th><th></th></tr></thead><tbody>
      ${closed.map((d) => `<tr>
        <td>${esc(d.bank || "—")}</td><td class="num">${fmtMoney(d.principal)}</td>
        <td>${esc(d.closed_date)}</td><td class="num">${fmtMoney(d.closed_amount)}</td>
        <td class="row-actions"><button class="sm warn" data-deldep="${d.id}">✕</button></td></tr>`).join("")}
      </tbody></table></div>`,
    `${closed.length} ${plural(closed.length, "вклад", "вклади", "вкладів")}`)}</div>`;
}

export function wireDeposits(ctx, main) {
  const byId = new Map((ctx._deposits || []).map((d) => [String(d.id), d]));

  main.querySelector("#termDepForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target;
    try {
      await ctx.api("POST", "term-deposits", {
        bank: f.bank.value, currency: f.currency.value,
        principal: f.principal.value.trim(), rate_pct: f.rate_pct.value.trim(),
        open_date: f.open_date.value, maturity_date: f.maturity_date.value,
        payout: f.payout.value, capitalized: f.capitalized.checked,
        replenishable: f.replenishable.checked,
        tax_pct: f.tax_pct.value.trim(), note: f.note.value.trim(),
      });
      ctx.toast("Вклад додано"); ctx.reload();
    } catch (err) { ctx.toast(String(err.message || err), false); }
  });

  // Перемикач «поповнюваний» просто на рядку: це властивість вкладу, яку
  // дізнаєшся вже після відкриття, і заводити заради неї окрему форму
  // редагування було б надміру. PUT шле вклад цілком — решту полів
  // беремо з уже завантаженого списку, як і при закритті.
  main.querySelectorAll("[data-repl]").forEach((cb) =>
    cb.addEventListener("change", async () => {
      const d = byId.get(cb.dataset.repl);
      if (!d) return;
      try {
        await ctx.api("PUT", "term-deposits/" + d.id, {
          bank: d.bank, currency: d.principal.currency,
          principal: d.principal.amount, rate_pct: String(d.rate_pct),
          open_date: d.open_date, maturity_date: d.maturity_date,
          payout: d.payout, capitalized: !!d.capitalized,
          replenishable: cb.checked,
          tax_pct: String(d.tax_pct), note: d.note || "",
          closed_date: d.closed_date || "", closed_amount: (d.closed_amount || {}).amount || "",
        });
        ctx.toast(cb.checked ? "Вклад поповнюваний" : "Вклад не поповнюваний");
        ctx.reload();
      } catch (err) { ctx.toast(String(err.message || err), false); cb.checked = !cb.checked; }
    }));

  main.querySelectorAll("[data-deldep]").forEach((b) =>
    b.addEventListener("click", async () => {
      if (!confirm("Видалити вклад #" + b.dataset.deldep + "?")) return;
      try { await ctx.api("DELETE", "term-deposits/" + b.dataset.deldep); ctx.toast("Вклад видалено"); ctx.reload(); }
      catch (err) { ctx.toast(String(err.message || err), false); }
    }));

  // Окремих кнопок «Поповнити» й «Закрити» більше немає: обидві форми
  // живуть у рядку-деталях, який відкриває та сама стрілка, що показує
  // лоти в ОВДП. Один жест на всі інструменти замість трьох кнопок.

  // Поповнення: сума в валюті вкладу, за замовчуванням = тіло відкриття.
  main.querySelectorAll("[data-topup-form]").forEach((f) =>
    f.addEventListener("submit", async (e) => {
      e.preventDefault();
      try {
        await ctx.api("POST", "term-deposits/" + f.dataset.topupForm + "/topups", {
          date: f.date.value, amount: f.amount.value.trim(),
        });
        ctx.toast("Поповнення додано"); ctx.reload();
      } catch (err) { ctx.toast(String(err.message || err), false); }
    }));

  main.querySelectorAll("[data-deltopup]").forEach((b) =>
    b.addEventListener("click", async () => {
      const [depID, topupID] = b.dataset.deltopup.split(":");
      try {
        await ctx.api("DELETE", "term-deposits/" + depID + "/topups/" + topupID);
        ctx.toast("Поповнення видалено"); ctx.reload();
      } catch (err) { ctx.toast(String(err.message || err), false); }
    }));

  // Розірвання = PUT усього вкладу з проставленими closed_*. Решту полів
  // беремо з уже завантаженого списку — банк перерахує сам, ми лише
  // вводимо фактично отриману суму.
  main.querySelectorAll("[data-close-form]").forEach((f) =>
    f.addEventListener("submit", async (e) => {
      e.preventDefault();
      const d = byId.get(f.dataset.closeForm);
      if (!d) return;
      try {
        await ctx.api("PUT", "term-deposits/" + d.id, {
          bank: d.bank, currency: d.principal.currency,
          principal: d.principal.amount, rate_pct: String(d.rate_pct),
          open_date: d.open_date, maturity_date: d.maturity_date,
          payout: d.payout, capitalized: !!d.capitalized,
          replenishable: !!d.replenishable, // PUT шле вклад цілком — без цього
                                            // закриття скидало б прапорець
          tax_pct: String(d.tax_pct), note: d.note || "",
          closed_date: f.closed_date.value, closed_amount: f.closed_amount.value.trim(),
        });
        ctx.toast("Вклад закрито"); ctx.reload();
      } catch (err) { ctx.toast(String(err.message || err), false); }
    }));
}

