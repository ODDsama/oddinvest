// Чим ризикую: розклад по брокерах і валютах, ребалансування,
// ліквідність, процентний ризик, бенчмарк і драбина.

import { esc, curSym, pct, uah2 as fmtUAH, cur2 as fmtCur, fundsCost, capitalUAH } from "../format.js";
import { infoBtn } from "../info.js";
import { svgBars, svgGrouped, svgDonut } from "../charts.js";
import { tile, yieldNote } from "../components.js";
import { disclosure } from "../disclosure.js";


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


// Плитки дохідностей. Головне число всюди РЕАЛЬНЕ — те саме, що в
// таблиці позицій нижче; номінальне лишається під ним дрібним.
//
// Доти плитки показували самі номінальні, а таблиця під ними — самі
// реальні, тож той самий фонд стояв на екрані двома різними числами
// (5.78% і 2.8%) без жодного натяку, що бази різні. Читалось це як
// помилка, і небезпідставно.
export function yieldTilesHTML(ctx) {
  const s0 = ctx.summary || {};
  const py = s0.portfolio_yield || {}, pyReal = s0.portfolio_yield_real || {};
  const xr = s0.xirr || {};
  // XIRR — фактично реалізоване, номінальне за природою: це те, що
  // справді сталося з грошима, а не оцінка наперед. Реального двійника
  // в нього немає, тож слово ставимо явно.
  const xirrTiles = Object.keys(xr).length
    ? Object.entries(xr).map(([c, v]) => tile(`XIRR ${curSym(c)}`, pct(v, 2),
        `<div class="sub-xs">номінальних, за фактом</div>`)).join("")
    : tile("XIRR", "—",
        `<div class="sub">гроші мають попрацювати ≥30 днів у середньому</div>`);
  return `<div class="tiles flush">
    ${tile("Вкладено (грн-екв.)", fmtUAH(s0.invested_uah + fundsCost(s0)),
      fundsCost(s0) > 0 ? `<div class="sub">з них ${fmtUAH(fundsCost(s0))} у фондах</div>` : "")}
    ${tile("Номінал (грн-екв.)", fmtUAH(s0.nominal_uah_eq))}
    ${s0.deposits_uah > 0 ? tile("Вклади (грн-екв.)", fmtUAH(s0.deposits_uah),
      `<div class="sub">тіло діючих банківських вкладів</div>`) : ""}
    ${s0.reserve_uah > 0 ? tile(`Резерв (грн-екв.) ${infoBtn("reserve")}`, fmtUAH(s0.reserve_uah),
      `<div class="sub">не працює навмисно — саме тому доступний миттєво</div>`) : ""}
    ${tile("Накопичений купон", fmtUAH(s0.accrued_uah || 0),
      `<div class="sub">зароблено, ще не виплачено</div>`)}
    ${Object.entries(py).map(([c, v]) => tile(`ОВДП ${curSym(c)}`,
      pct(pyReal[c] != null ? pyReal[c] : v),
      yieldNote(v, "до погашення, від сплаченої ціни"))).join("")}
    ${s0.funds_yield_pct > 0 ? tile("Фонди", pct(s0.funds_yield_real_pct),
      yieldNote(s0.funds_yield_pct, s0.funds_yield_basis || "")) : ""}
    ${s0.blended_yield_pct > 0 ? tile(`Дохідність портфеля ${infoBtn("yields")}`,
      pct(s0.blended_yield_real_pct),
      yieldNote(s0.blended_yield_pct, "ОВДП і фонди разом, зважено вкладеним")) : ""}
    ${xirrTiles}
  </div>`;
}

// Валютні частки й цілі — тут, а не окремою вкладкою «План»: це
// характеристика того, ЩО ВЖЕ КУПЛЕНО, і читається вона поруч із
// рештою складу, а не через клік.
export function shareTilesHTML(ctx) {
  const s0 = ctx.summary || {}, st = s0.settings || {};
  const shareTile = (lbl, cur, tgt) => tile(lbl, (cur || 0).toFixed(1) + "%",
    tgt ? `<div class="sub">ціль ${tgt}%</div>` : "");
  return `<div class="tiles flush">
    ${shareTile("Частка USD", s0.usd_share_pct, st.usd_target_share_pct)}
    ${shareTile("Частка EUR", s0.eur_share_pct, st.eur_target_share_pct)}
  </div>`;
}

// ---------- структура й ризик ----------
// Валютне ребалансування: скільки бракує до цільових часток і чи це
// взагалі досяжно (найдешевший папір може бути більший за цільову суму).
export function rebalanceCard(ctx) {
  // Лише валютні рядки: `rebalance` тепер несе кілька вимірів, і решта
  // має власні картки з власними формулюваннями. Порожній `dimension` —
  // старий бекенд, де інших вимірів не існувало.
  const rows = ((ctx.summary && ctx.summary.rebalance) || [])
    .filter((r) => !r.dimension || r.dimension === "currency");
  if (!rows.length) return "";
  const sym = { USD: "$", EUR: "€" };
  const num = (v, d = 2) => Number(v || 0).toLocaleString("uk-UA", { maximumFractionDigits: d });
  const body = rows.map((r) => {
    const s = sym[r.currency] || r.currency;
    // Одиниця входу тепер — облігація АБО мінімальний вклад ($100/€100),
    // що з них дешевше. Формулювання залежить від того, що саме перемогло.
    const dep = r.unit_kind === "deposit";
    const unitLabel = dep ? `мінімальний вклад у ${esc(r.currency)}` : `найдешевший ${esc(r.currency)}-папір`;
    const unitShort = dep ? "Мінімальний вклад" : "Найдешевший папір";
    const unitPlural = dep ? "вклад(и)" : "папер(и)";
    const head = `<b>${esc(r.currency)}</b> — ціль ${r.target_pct}%, зараз ${r.current_pct}%`;
    if (r.deficit_uah <= 0) {
      // Дефіциту немає — але це два різні стани, і плутати їх не можна:
      // 20% при цілі 20% і 58% при цілі 20% однаково «без дефіциту», хоч
      // друге це перекіс майже втричі. Доти обидва підписувались «ціль
      // досягнута ✅», і перебір читався як добра новина.
      const over = (r.current_pct || 0) - (r.target_pct || 0);
      return over > 1
        ? `<div style="margin-bottom:12px">${head} —
             <span style="color:var(--oi-warn)">перебір на ${over.toFixed(1)} п.п.</span><br>
             <span class="sub">Добирати цю валюту більше не треба. Довести частку до цілі можна
             або продажем, або — простіше — купівлею в інших валютах, поки капітал росте.</span></div>`
        : `<div style="margin-bottom:12px">${head} — <span style="color:var(--oi-ok)">ціль досягнута ✅</span></div>`;
    }
    const need = `Бракує до цілі: <b>${fmtUAH(r.deficit_uah)}</b> (≈ ${num(r.deficit_native)} ${s})`;
    if (!r.feasible) {
      return `<div style="margin-bottom:12px">${head}<br>${need}<br>
        <span style="color:var(--oi-warn)">⚠ Ще зарано:</span> ${unitLabel} коштує
        ${fmtUAH(r.bond_cost_uah)} (${num(r.bond_cost_native, 0)} ${s}) — це більше за всю цільову суму.
        Стільки вписалося б у ціль ${r.target_pct}% при капіталі <b>${fmtUAH(r.min_portfolio_uah)}</b>.</div>`;
    }
    const buy = r.can_buy > 0
      ? `вистачає на <b>${r.can_buy}</b> ${unitPlural}`
      : `бракує — сконвертуй ще ≈ <b>${fmtUAH(r.convert_uah)}</b>`;
    return `<div style="margin-bottom:12px">${head}<br>${need}<br>
      ${unitShort}: ${num(r.bond_cost_native, 0)} ${s} ≈ ${fmtUAH(r.bond_cost_uah)}.
      Готівка: ${num(r.cash_native)} ${s} — ${buy}.</div>`;
  }).join("");
  return `<div class="card"><h2>Валютне ребалансування</h2>
    <div class="muted" style="margin-bottom:10px">Частки рахуються від УСЬОГО капіталу — папери,
      рахунок, фонди, вклади й резерв, — тож це те саме число, що в плитці «Частка USD» вище.
      Доти тут був інший знаменник, і два числа на одному екрані не сходились.</div>
    ${body}</div>`;
}

// Структура за ВИДОМ інструмента: чим ти ризикуєш, а не в чому тримаєш.
//
// Окрема картка від валютної навмисно. Це два різні питання, і відповіді
// на них не замінюють одна одну: портфель на 100% в ОВДП і портфель на
// 100% у фондах можуть мати однакові валютні частки й геть різну
// поведінку — у першого ризик процентний і державний, у другого ринковий
// і керуючої компанії.
const KIND_TITLE = {
  bonds: "ОВДП", funds: "Фонди", deposits: "Вклади", reserve: "Резерв",
};

export function kindMixCard(ctx) {
  const s = ctx.summary || {};
  const rows = (s.rebalance || []).filter((r) => r.dimension === "kind");
  if (!rows.length) return "";
  const cap = capitalUAH(s);
  // Нерозподілене — це те, під що цілі не ставили. Показуємо числом і
  // НЕ нормалізуємо: підмінити введені 40/20 на 67/33, не питаючи, було б
  // гірше за визнання, що сума не сходиться.
  const targetSum = rows.reduce((a, r) => a + (r.target_pct || 0), 0);
  const body = rows.map((r) => {
    const title = KIND_TITLE[r.key] || r.key;
    const nowUAH = cap * (r.current_pct || 0) / 100;
    // Рядок без цілі — довідковий (резерв). Його ціль живе у власній
    // картці й міряється місяцями витрат, а не часткою капіталу: частка
    // рухалась би щоразу, коли росте портфель, навіть якби резерву ніхто
    // не чіпав.
    if (!r.target_pct) {
      return `<div style="margin-bottom:12px"><b>${esc(title)}</b> —
        <span class="muted">${r.current_pct}% (${fmtUAH(nowUAH)}), цілі за часткою немає</span><br>
        <span class="sub">${r.key === "reserve"
          ? "ціль резерву задана в місяцях витрат — див. картку «Резерв» у «Грошах»"
          : "показано довідково"}</span></div>`;
    }
    const over = (r.current_pct || 0) > (r.target_pct || 0);
    const bar = Math.min(100, (r.target_pct ? (r.current_pct / r.target_pct) * 100 : 0));
    const line = r.deficit_uah > 0
      ? `бракує <b>${fmtUAH(r.deficit_uah)}</b>${
          r.bond_cost_uah > 0 && !r.feasible
            ? ` · <span style="color:var(--oi-warn)">⚠ найдешевший вхід ${fmtUAH(r.bond_cost_uah)}
                — це більше за всю цільову суму</span>`
            : r.bond_cost_uah > 0 ? ` · найдешевший вхід ${fmtUAH(r.bond_cost_uah)}` : ""}`
      : over
        ? `<span style="color:var(--oi-warn)">перебір</span> — ціль уже перевищена`
        : `<span style="color:var(--oi-ok)">ціль досягнута ✅</span>`;
    return `<div style="margin-bottom:12px">
      <b>${esc(title)}</b> — ціль ${r.target_pct}%, зараз ${r.current_pct}%
      <span class="muted">(${fmtUAH(nowUAH)})</span><br>
      <div class="progress" style="margin:4px 0"><span style="width:${bar}%;background:${
        over ? "var(--oi-warn)" : r.deficit_uah > 0 ? "var(--oi-info)" : "var(--oi-ok)"}"></span></div>
      ${line}</div>`;
  }).join("");
  return `<div class="card"><h2 class="h-row">Структура за видом інструмента ${infoBtn("kindmix")}</h2>
    <div class="muted" style="margin-bottom:10px">Валютна ціль каже, В ЧОМУ тримати гроші; ця —
      ЧИМ ризикувати. Частки рахуються від того самого капіталу.</div>
    ${body}
    <div class="sub">${targetSum < 99.5
      ? `Цілями розподілено ${targetSum.toFixed(0)}% капіталу — решта ${(100 - targetSum).toFixed(0)}%
         лишається на твій розсуд. Застосунок не «дотягує» суму до сотні сам: це підмінило б те, що ти ввів.`
      : targetSum > 100.5
        ? `Цілі в сумі дають ${targetSum.toFixed(0)}% — більше за капітал, тож усі одразу недосяжні.`
        : `Цілі в сумі дають 100% капіталу.`}</div>
  </div>`;
}

// Де портфель зібраний надто щільно.
//
// Три виміри — три різні питання, і саме тому вони в одній картці, але
// окремими блоками: «що буде, якщо цей емітент не заплатить», «що буде,
// якщо ця установа зникне», «що буде, якщо саме того року ставки
// впадуть». Об'єднати їх в один список означало б поставити поруч числа
// з різними знаменниками.
const CONC_BLOCK = {
  isin: ["В одному папері", "% капіталу", "емітент не заплатить — скільки це коштує"],
  broker: ["В одній установі", "% капіталу", "брокер або банк зникне — скільки там лежить"],
  year: ["В одному році погашень", "% усіх погашень", "усе повернеться разом і піде за гіршою ставкою"],
};

export function concentrationCard(ctx) {
  const rows = (ctx.summary || {}).concentration || [];
  if (!rows.length) return "";
  const blocks = Object.keys(CONC_BLOCK).map((dim) => {
    const list = rows.filter((r) => r.dimension === dim);
    if (!list.length) return "";
    const [title, unit, why] = CONC_BLOCK[dim];
    const limit = list[0].limit_pct;
    const items = list.map((r) => {
      const over = r.over_uah > 0;
      const bar = Math.min(100, (r.share_pct / Math.max(limit, r.share_pct)) * 100);
      // Ключ поруч із назвою корисний для облігації (ISIN — те, що шукають
      // у брокера), але для фонду він лише повторює назву: ключ там —
      // службовий «fund:Назва».
      const showKey = r.label && !r.key.endsWith(r.label);
      return `<div style="margin-bottom:8px">
        <div style="display:flex;justify-content:space-between;gap:8px">
          <span>${esc(r.label || r.key)}${showKey ? ` <span class="muted">${esc(r.key)}</span>` : ""}</span>
          <span${over ? ` style="color:var(--oi-warn)"` : ""}><b>${r.share_pct}%</b>
            <span class="muted">${fmtUAH(r.amount_uah)}</span></span>
        </div>
        <div class="progress" style="margin-top:3px"><span style="width:${bar}%;background:${
          over ? "var(--oi-warn)" : "var(--oi-info)"}"></span></div>
        ${over ? `<div class="sub-xs" style="color:var(--oi-warn)">понад ліміт на ${fmtUAH(r.over_uah)}</div>` : ""}
      </div>`;
    }).join("");
    // Резерв ні за ким не стоїть — у нього «місце», а не контрагент, — тож
    // частки установ у сумі й не мусять давати 100%. Без цього рядка
    // «24% в найбільшій установі» читалось би так, ніби решта грошей
    // загубилась.
    const s = ctx.summary || {};
    const gap = dim === "broker" && s.reserve_uah > 0
      ? `<div class="sub-xs">Резерв (${fmtUAH(s.reserve_uah)}) сюди не входить: у нього немає
         контрагента, який міг би зникнути, — тому частки в сумі й не дають 100%.</div>` : "";
    return `<div style="margin-bottom:16px">
      <div style="margin-bottom:6px"><b>${title}</b> — ліміт ${limit}${
        unit === "% капіталу" ? "% капіталу" : "% усіх погашень"}
        <span class="muted">· ${why}</span></div>
      ${items}${gap}</div>`;
  }).join("");
  const broken = rows.filter((r) => r.over_uah > 0).length;
  return `<div class="card"><h2 class="h-row">Концентрація ${infoBtn("concentration")}</h2>
    <div class="muted" style="margin-bottom:10px">${broken
      ? `Перевищено лімітів: <b>${broken}</b>.`
      : "Усі задані ліміти витримані."}
      Це спостереження, а не заборона: поради в «Що купити» від нього не змінюються й нічого
      не ховається. Ліміт міг бути порушений із причин, яких застосунок не знає.</div>
    ${blocks}</div>`;
}

// «А якби я просто тримав долари?» — питання, на яке досі не було
// відповіді, бо історія курсів з'явилась лише коли знецінення почали
// міряти. Бенчмарк може виявитись кращим за портфель: у цьому й сенс
// вимірювання, а не привід його ховати.
export function benchmarkCard(ctx, b) {
  if (!b || !b.benchmark_uah) return "";
  const won = (b.diff_uah || 0) >= 0;
  const col = won ? "var(--oi-ok)" : "var(--oi-danger)";
  return `<div class="card">${disclosure("benchmark", "А якби просто долари", `
    <div class="tiles" style="margin:0 0 10px">
      ${tile("Твій портфель", fmtUAH(b.portfolio_uah))}
      ${tile("Долари під матрацом", fmtUAH(b.benchmark_uah),
        `<div class="sub">${fmtCur(b.usd_bought, "$")} по курсах тих днів</div>`)}
      ${tile("Різниця", `<span style="color:${col}">${won ? "+" : ""}${fmtUAH(b.diff_uah)}</span>`,
        `<div class="sub" style="color:${col}">${won ? "+" : ""}${pct(b.diff_pct)}</div>`)}
    </div>
    <div class="muted" style="font-size:13px">Кожне твоє поповнення переведено в долари за курсом
      ТОГО дня, а сума оцінена сьогоднішнім (${fmtUAH(b.rate_now)}/$).
      Бенчмарк навмисно не приносить відсотків — це поведінка «нічого не робити», з якою й
      порівнюють. Купони, дивіденди й відсотки в нього не входять: вони й є те, що ти отримав
      натомість.${b.note ? ` <b>${esc(b.note)}.</b>` : ""}</div>`,
    `${won ? "+" : ""}${pct(b.diff_pct)}`)}</div>`;
}

// Коли гроші стають доступні. Питання не про дохідність, а про те, що
// робити, коли вони раптом знадобились, — і воно в Україні не
// теоретичне.
export function liquidityCard(ctx) {
  const l = (ctx.summary || {}).liquidity;
  if (!l) return "";
  const hint = fmtUAH(l.now_uah);
  return `<div class="card">${disclosure("liquidity", "Ліквідність", `
    <div class="tiles" style="margin:0 0 10px">
      ${tile("Зараз", fmtUAH(l.now_uah), `<div class="sub">на рахунках</div>`)}
      ${tile("За 30 днів", fmtUAH(l.in_30_uah), `<div class="sub">разом із виплатами</div>`)}
      ${tile("За 90 днів", fmtUAH(l.in_90_uah), `<div class="sub">разом із виплатами</div>`)}
      ${l.locked_uah > 0 ? tile("Замкнено", fmtUAH(l.locked_uah),
        l.unlock_date ? `<div class="sub">найближче відкриється ${esc(l.unlock_date)}</div>` : "") : ""}
    </div>
    <div class="muted" style="font-size:13px">Вікна накопичувальні: «за 90 днів» уже містить «за 30».
      Рахуються гроші на рахунках плюс купони, погашення й тіла вкладів, що гасяться у вікні.
      «Замкнено» — тіла вкладів зі строком далі: дістати їх можна лише розірвавши вклад, тобто
      втративши відсотки.<br><br>
      <b>Облігації сюди не входять.</b> Продати їх на вторинному ринку можна, але застосунок не
      моделює ринкової ціни — вигадане число тут було б гірше за чесну відсутність.</div>`,
    hint)}</div>`;
}

// Ставки б'ють по портфелю двічі, і це різні удари. Ціною — лише по
// ОВДП, бо тільки вони переоцінюються на вторинному ринку. Строком — по
// ОВДП і вкладах разом: обидва гасяться, і повернуті гроші доведеться
// вкладати заново за ставкою, якої сьогодні ніхто не знає. Фонди не
// беруть участі в жодному: сертифікат не гаситься й ціни від ставки
// напряму не має.
export function rateRiskCard(ctx) {
  const rr = ctx.summary && ctx.summary.rate_risk;
  if (!rr || (!rr.duration_years && !rr.reinvest_years)) return "";

  const priceBlock = rr.duration_years ? `
    <h4>Чутливість ціни · лише ОВДП</h4>
    <div class="tiles" style="margin:0 0 10px">
      <div class="tile"><div class="lbl">Дюрація (Маколея)</div><div class="val">${rr.duration_years} р.</div></div>
      <div class="tile"><div class="lbl">Модифікована</div><div class="val">${rr.modified_dur}</div></div>
      <div class="tile"><div class="lbl">Приведена вартість</div><div class="val">${fmtUAH(rr.pv_uah)}</div></div>
    </div>
    <div class="table-scroll"><table>
      <thead><tr><th>Зміна ставок</th><th class="num">Вартість</th><th class="num">У грошах</th></tr></thead>
      <tbody>${(rr.scenarios || []).map((x) => {
        const col = x.change_pct >= 0 ? "var(--oi-ok)" : "var(--oi-danger)";
        const sgn = (v) => (v > 0 ? "+" : "");
        return `<tr><td>${sgn(x.delta_pp)}${x.delta_pp} п.п.</td>
          <td class="num" style="color:${col}">${sgn(x.change_pct)}${x.change_pct}%</td>
          <td class="num" style="color:${col}">${sgn(x.change_uah)}${fmtUAH(x.change_uah)}</td></tr>`;
      }).join("")}</tbody></table></div>
    <div class="muted" style="margin-top:8px;font-size:13px">Модифікована дюрація показує, на скільки %
      змінюється ціна паперів при зміні ставок на 1 п.п. <b>Тримаєш до погашення — просадка лише
      паперова</b>: ризик реалізується при продажі на вторинці. Вклади сюди не входять — переоцінити
      їх нікуди, сума погашення записана в договорі.</div>` : "";

  const reinvestBlock = rr.reinvest_years ? `
    <h4${priceBlock ? ` style="margin-top:18px"` : ""}>Строк до перевкладення · ОВДП і вклади</h4>
    <div class="tiles" style="margin:0 0 10px">
      <div class="tile"><div class="lbl">Середній строк</div><div class="val">${rr.reinvest_years} р.</div>
        <div class="sub">поки гроші повернуться</div></div>
      <div class="tile"><div class="lbl">Повернеться всього</div><div class="val">${fmtUAH(rr.returning_uah)}</div>
        <div class="sub">тіло + відсотки, грн-екв.</div></div>
      <div class="tile"><div class="lbl">З них за 12 міс.</div><div class="val">${fmtUAH(rr.reinvest_soon_uah)}</div>
        <div class="sub">перевкладати за новою ставкою</div></div>
    </div>
    <div class="muted" style="font-size:13px">Це ризик протилежного знаку: якщо ставки ПАДАЮТЬ, папери
      дорожчають, але повернуті гроші доведеться вкладати дешевше. Чим коротший середній строк, тим
      швидше портфель переїде на нові ставки — вгору чи вниз.</div>` : "";

  // Згорнутий: сюди звертаються раз на кілька місяців, а місця блок
  // займає більше за таблицю позицій.
  const hint = rr.duration_years
    ? `дюрація ${rr.duration_years} р.`
    : `перевкладення через ${rr.reinvest_years} р.`;
  return `<div class="card">${disclosure("risk", "Ризик ставок",
    priceBlock + reinvestBlock, hint)}</div>`;
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
    <div class="sub">Скільки капіталу повертається за роками — номінал ОВДП і тіло вкладів разом
      (окремо UAH / USD / EUR). Фонди не входять: сертифікат не гаситься.</div>
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


