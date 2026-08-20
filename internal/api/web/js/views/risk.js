// Чим ризикую: розклад по брокерах і валютах, ребалансування,
// ліквідність, процентний ризик, бенчмарк і драбина.

import {
  esc, curSym, dayMonth, pct, uah2 as fmtUAH, cur2 as fmtCur,
  fundsCost, marketCostUAH,
} from "../format.js";
import { infoBtn } from "../info.js";
import { svgBars, svgGrouped, svgDonut, fluid } from "../charts.js";
import { tile, yieldNote, needsSetting, empty, legend } from "../components.js";
import { routeFor } from "../routes.js";
import { disclosure } from "../disclosure.js";
import { KIND_GROUP } from "../constants.js";


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
    return `<div class="pv-row"><span><i class="swatch" style="--oi-c:${colors[i]}"></i>${esc(n)}</span>
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
  return fluid((w, h) => svgBars(
    lad.map((r) => ({ label: String(r.year), value: r.uah })), { showVals: true, W: w, H: h }));
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
  return `<div class="card"><h4>Валюта: факт vs ціль ${infoBtn("currency")}</h4>${
    fluid((w, h) => svgGrouped(groups, { W: w, H: h }))}
    ${legend([
      { color: "var(--oi-series-invested)", label: "факт" },
      { color: "var(--oi-series-neutral)", label: "ціль" },
    ])}</div>`;
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
    ${tile("Вкладено: ОВДП + фонди (грн-екв.)", fmtUAH(marketCostUAH(s0)),
      `${fundsCost(s0) > 0 ? `<div class="sub">з них ${fmtUAH(fundsCost(s0))} у фондах</div>` : ""}
       <div class="sub-xs">без вкладів і резерву — вони коштують рівно те, що в них</div>`)}
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
  if (!rows.length) {
    return needsSetting("Валютне ребалансування",
      "Цільові частки USD і EUR не задані, тож відхилятись немає від чого. "
      + "Задай їх у «Стратегії» — і тут буде видно, чого і на скільки бракує.",
    routeFor("policy/strategy"));
  }
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
        ? `<div class="mb">${head} —
             <span class="t-warn">перебір на ${over.toFixed(1)} п.п.</span><br>
             <span class="sub">Добирати цю валюту більше не треба. Довести частку до цілі можна
             або продажем, або — простіше — купівлею в інших валютах, поки капітал росте.</span></div>`
        : `<div class="mb">${head} — <span class="t-ok">ціль досягнута ✅</span></div>`;
    }
    const need = `Бракує до цілі: <b>${fmtUAH(r.deficit_uah)}</b> (≈ ${num(r.deficit_native)} ${s})`;
    if (!r.feasible) {
      return `<div class="mb">${head}<br>${need}<br>
        <span class="t-warn">⚠ Ще зарано:</span> ${unitLabel} коштує
        ${fmtUAH(r.bond_cost_uah)} (${num(r.bond_cost_native, 0)} ${s}) — це більше за всю цільову суму.
        Стільки вписалося б у ціль ${r.target_pct}% при капіталі <b>${fmtUAH(r.min_portfolio_uah)}</b>.</div>`;
    }
    const buy = r.can_buy > 0
      ? `вистачає на <b>${r.can_buy}</b> ${unitPlural}`
      : `бракує — сконвертуй ще ≈ <b>${fmtUAH(r.convert_uah)}</b>`;
    return `<div class="mb">${head}<br>${need}<br>
      ${unitShort}: ${num(r.bond_cost_native, 0)} ${s} ≈ ${fmtUAH(r.bond_cost_uah)}.
      Готівка: ${num(r.cash_native)} ${s} — ${buy}.</div>`;
  }).join("");
  return `<div class="card"><h2>Валютне ребалансування</h2>
    <div class="note">Частки рахуються від УСЬОГО капіталу — папери,
      рахунок, фонди, вклади, резерв і НПФ, — тож це те саме число, що в плитці «Частка USD» вище.
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
// Назви видів живуть у constants.js: цю саму п'ятірку читає ще й
// «Скільки чого за стратегією» (views/allocation.js), і два власні списки
// розійшлись би тихо.

export function kindMixCard(ctx) {
  const s = ctx.summary || {};
  const rows = (s.rebalance || []).filter((r) => r.dimension === "kind");
  if (!rows.length) {
    return needsSetting(`Структура за видом інструмента ${infoBtn("kindmix")}`,
      "Цілі за видом (ОВДП / фонди / НПФ / вклади / резерв) не задані. "
      + "Задай їх у «Частках і межах» — і «Що купити» почне зважати ще й на них, "
      + "а не лише на валютну частку.",
    routeFor("policy/mix"));
  }
  // Нерозподілене — це те, під що цілі не ставили. Показуємо числом і
  // НЕ нормалізуємо: підмінити введені 40/20 на 67/33, не питаючи, було б
  // гірше за визнання, що сума не сходиться.
  const targetSum = rows.reduce((a, r) => a + (r.target_pct || 0), 0);
  const body = rows.map((r) => {
    const title = KIND_GROUP[r.key] || r.key;
    // Гривні беруться ГОТОВИМИ з рядка, а не множенням капіталу на частку.
    // Доти тут стояло cap × current_pct / 100 — і частка приїжджає
    // округленою до двох знаків, тож вклад на рівно 14 000 ₴ показувався
    // як 14 001,20 ₴. Помітно це стало, коли ту саму суму почала називати
    // ще й картка «Скільки чого за стратегією»: одна й та сама позиція
    // стояла на двох сторінках двома різними числами.
    const nowUAH = r.current_uah || 0;
    // Рядок без цілі — довідковий: вид у портфелі є, а ціль під нього не
    // ставили. Резерву тут більше немає взагалі — він вийшов зі знаменника,
    // і його частка живе у власній картці разом із місяцями витрат.
    if (!r.target_pct) {
      return `<div class="mb"><b>${esc(title)}</b> —
        <span class="muted">${r.current_pct}% (${fmtUAH(nowUAH)}), цілі за часткою немає</span><br>
        <span class="sub">показано довідково</span></div>`;
    }
    const over = (r.current_pct || 0) > (r.target_pct || 0);
    const bar = Math.min(100, (r.target_pct ? (r.current_pct / r.target_pct) * 100 : 0));
    const line = r.deficit_uah > 0
      ? `бракує <b>${fmtUAH(r.deficit_uah)}</b>${
          r.bond_cost_uah > 0 && !r.feasible
            ? ` · <span class="t-warn">⚠ найдешевший вхід ${fmtUAH(r.bond_cost_uah)}
                — це більше за всю цільову суму</span>`
            : r.bond_cost_uah > 0 ? ` · найдешевший вхід ${fmtUAH(r.bond_cost_uah)}` : ""}`
      : over
        ? `<span class="t-warn">перебір</span> — ціль уже перевищена`
        : `<span class="t-ok">ціль досягнута ✅</span>`;
    return `<div class="mb">
      <b>${esc(title)}</b> — ціль ${r.target_pct}%, зараз ${r.current_pct}%
      <span class="muted">(${fmtUAH(nowUAH)})</span><br>
      <div class="progress mt-xs mb-xs"><span style="--oi-fill:${bar}%;--oi-c:${
        over ? "var(--oi-warn)" : r.deficit_uah > 0 ? "var(--oi-info)" : "var(--oi-ok)"}"></span></div>
      ${line}</div>`;
  }).join("");
  return `<div class="card"><h2 class="h-row">Структура за видом інструмента ${infoBtn("kindmix")}</h2>
    <div class="note">Валютна ціль каже, В ЧОМУ тримати гроші; ця — ЧИМ ризикувати.
      Знаменники в них РІЗНІ: валютна частка міряється від усього капіталу (долар у
      матраці — теж валютний ризик), а ця — від портфеля БЕЗ резерву. У подушки своя
      ціль, у місяцях витрат, і в частці ОВДП їй місця не потрібно.</div>
    ${body}
    <div class="sub">${targetSum < 99.5
      ? `Цілі за видом дають ${targetSum.toFixed(0)}% замість 100 — ${(100 - targetSum).toFixed(0)}%
         портфеля не кероване жодною ціллю. Застосунок не «дотягує» суму сам: це підмінило б
         те, що ти ввів. Довести до сотні можна руками або готовим набором.`
      : targetSum > 100.5
        ? `Цілі в сумі дають ${targetSum.toFixed(0)}% — більше за портфель, тож усі одразу недосяжні.`
        : `Цілі в сумі дають 100%. Невкладена готівка при цьому в знаменнику лишається,
           тож поки гроші лежать на рахунку, всі види разом стоять трохи нижче цілі.`}</div>
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
  if (!rows.length) {
    return needsSetting(`Концентрація ${infoBtn("concentration")}`,
      "Ліміти концентрації не задані, а дефолтів у них немає навмисно: "
      + "«не більше 20% в один папір» — це порада, а застосунок їх не дає. "
      + "Задай свої в «Частках і межах» — і тут буде видно, де портфель до них підійшов.",
    routeFor("policy/mix"));
  }
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
      return `<div class="mb-sm">
        <div class="kv">
          <span>${esc(r.label || r.key)}${showKey ? ` <span class="muted">${esc(r.key)}</span>` : ""}</span>
          <span${over ? ` class="t-warn"` : ""}><b>${r.share_pct}%</b>
            <span class="muted">${fmtUAH(r.amount_uah)}</span></span>
        </div>
        <div class="progress mt-xs"><span style="--oi-fill:${bar}%;--oi-c:${
          over ? "var(--oi-warn)" : "var(--oi-info)"}"></span></div>
        ${over ? `<div class="sub-xs t-warn">понад ліміт на ${fmtUAH(r.over_uah)}</div>` : ""}
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
    return `<div class="mb-lg">
      <div class="mb-sm"><b>${title}</b> — ліміт ${limit}${
        unit === "% капіталу" ? "% капіталу" : "% усіх погашень"}
        <span class="muted">· ${why}</span></div>
      ${items}${gap}</div>`;
  }).join("");
  const broken = rows.filter((r) => r.over_uah > 0).length;
  return `<div class="card"><h2 class="h-row">Концентрація ${infoBtn("concentration")}</h2>
    <div class="note">${broken
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
  const tone = won ? "t-ok" : "t-danger";
  return `<div class="card">${disclosure("benchmark", "А якби просто долари", `
    <div class="tiles flush">
      ${tile("Твій портфель", fmtUAH(b.portfolio_uah))}
      ${tile("Долари під матрацом", fmtUAH(b.benchmark_uah),
        `<div class="sub">${fmtCur(b.usd_bought, "$")} по курсах тих днів</div>`)}
      ${tile("Різниця", `<span class="${tone}">${won ? "+" : ""}${fmtUAH(b.diff_uah)}</span>`,
        `<div class="sub ${tone}">${won ? "+" : ""}${pct(b.diff_pct)}</div>`)}
    </div>
    <div class="muted fine">Кожне твоє поповнення переведено в долари за курсом
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
    <div class="tiles flush">
      ${tile("Зараз", fmtUAH(l.now_uah), `<div class="sub">на рахунках</div>`)}
      ${tile("За 30 днів", fmtUAH(l.in_30_uah), `<div class="sub">разом із виплатами</div>`)}
      ${tile("За 90 днів", fmtUAH(l.in_90_uah), `<div class="sub">разом із виплатами</div>`)}
      ${l.locked_uah > 0 ? tile("Замкнено", fmtUAH(l.locked_uah),
        // Пенсійна частина — ОКРЕМИМ рядком, і без неї плитка вводила б в
        // оману найгучніше саме там, де замкненого найбільше:
        // unlock_date бере НАЙБЛИЖЧУ дату, тобто вклад, а гроші в НПФ
        // недоступні до пенсійного віку. «Замкнено 1.4 млн, найближче
        // відкриється 2027-03» — формально правда й повна неправда по суті.
        `${l.locked_npf_uah > 0
          ? `<div class="sub">з них у НПФ ${fmtUAH(l.locked_npf_uah)} — до пенсійного віку</div>` : ""}
         ${l.unlock_date ? `<div class="sub">найближче відкриється ${esc(l.unlock_date)}</div>` : ""}`) : ""}
    </div>
    <div class="muted fine">Вікна накопичувальні: «за 90 днів» уже містить «за 30».
      Рахуються гроші на рахунках плюс купони, погашення й тіла вкладів, що гасяться у вікні.
      «Замкнено» — тіла вкладів зі строком далі (дістати їх можна лише розірвавши вклад, тобто
      втративши відсотки) і пенсійні активи, яких не дістати взагалі до 50 років.<br><br>
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
    <div class="tiles flush">
      <div class="tile"><div class="lbl">Дюрація (Маколея)</div><div class="val">${rr.duration_years} р.</div></div>
      <div class="tile"><div class="lbl">Модифікована</div><div class="val">${rr.modified_dur}</div></div>
      <div class="tile"><div class="lbl">Приведена вартість</div><div class="val">${fmtUAH(rr.pv_uah)}</div></div>
    </div>
    <div class="table-scroll"><table>
      <thead><tr><th>Зміна ставок</th><th class="num">Вартість</th><th class="num">У грошах</th></tr></thead>
      <tbody>${(rr.scenarios || []).map((x) => {
        const tone = x.change_pct >= 0 ? "t-ok" : "t-danger";
        const sgn = (v) => (v > 0 ? "+" : "");
        return `<tr><td>${sgn(x.delta_pp)}${x.delta_pp} п.п.</td>
          <td class="num ${tone}">${sgn(x.change_pct)}${x.change_pct}%</td>
          <td class="num ${tone}">${sgn(x.change_uah)}${fmtUAH(x.change_uah)}</td></tr>`;
      }).join("")}</tbody></table></div>
    <div class="muted mt-sm fine">Модифікована дюрація показує, на скільки %
      змінюється ціна паперів при зміні ставок на 1 п.п. <b>Тримаєш до погашення — просадка лише
      паперова</b>: ризик реалізується при продажі на вторинці. Вклади сюди не входять — переоцінити
      їх нікуди, сума погашення записана в договорі.</div>` : "";

  const reinvestBlock = rr.reinvest_years ? `
    <h4${priceBlock ? ` class="mt-lg"` : ""}>Строк до перевкладення · ОВДП і вклади</h4>
    <div class="tiles flush">
      <div class="tile"><div class="lbl">Середній строк</div><div class="val">${rr.reinvest_years} р.</div>
        <div class="sub">поки гроші повернуться</div></div>
      <div class="tile"><div class="lbl">Повернеться всього</div><div class="val">${fmtUAH(rr.returning_uah)}</div>
        <div class="sub">тіло + відсотки, грн-екв.</div></div>
      <div class="tile"><div class="lbl">З них за 12 міс.</div><div class="val">${fmtUAH(rr.reinvest_soon_uah)}</div>
        <div class="sub">перевкладати за новою ставкою</div></div>
    </div>
    <div class="muted fine">Це ризик протилежного знаку: якщо ставки ПАДАЮТЬ, папери
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
    ? `<span class="bar" style="--oi-w:${Math.max(4, (v / maxV) * 120)}px;--oi-c:${color}"></span>` : "";
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
      : empty("Драбини ще немає",
          "Драбина показує, скільки повертається кожного року. Вона будується з погашень, тож з'явиться разом із першим папером.",
          { href: routeFor("buy"), label: "Записати покупку" })}
  </div>`;
}


// ---------- історія ----------



// ---------- крива первинного ринку ----------

// Під скільки Мінфін розміщує ОВДП на кожен строк — єдиний зовнішній
// орієнтир у застосунку. Доти дохідність портфеля не порівнювалась ні з
// чим, і питання «13.4% — це нормально?» не мало відповіді взагалі.
//
// Живе в «Портфелі», а не на «Огляді»: «Огляд» відповідає на «що робити
// зараз», а це контекст — із чим порівняти те, що вже маєш.
//
// Один графік НА ВАЛЮТУ. Гривня стоїть біля 16%, долар біля 3%: на
// спільній осі валютна крива лягла б пласкою смужкою по нулю, і саме та
// крива, заради якої дивишся, стала б нечитанною.
//
// Числа тут — дохідність РОЗМІЩЕННЯ: середньозважена ставка, прийнята
// первинними дилерами в дилерських обсягах того дня. Те, що заплатиш у
// брокера, буде іншим, і картка каже це вголос.
export function marketCurveCard(ctx, curve) {
  const rows = (curve || []).filter((r) => r.pct > 0);
  if (!rows.length) return "";
  const byCur = new Map();
  for (const r of rows) {
    if (!byCur.has(r.currency)) byCur.set(r.currency, []);
    byCur.get(r.currency).push(r);
  }
  // Обставини найсвіжішого аукціону цієї валюти: як його брали.
  //
  // Формулювання несуче. Це факти ПРО АУКЦІОН, а не про твій папір:
  // «попит» — скільки просили проти скільки взяли, «прийняті заявки» —
  // смуга, у якій зійшлись дилери. Жодне з них не є ціною, і слова
  // «коштує» тут бути не повинно.
  // Обсяги аукціону — це мільйони й мільярди, і копійки в них лише
  // заважають: «1 000 000 000,00 ₴» довше читати, ніж «1,0 млрд ₴», а
  // точність тут нічого не вирішує. Спільний compact() не підходить —
  // він знає лише «М» і на мільярді дав би «5000,0М».
  const volume = (v, cur) => {
    const s = v >= 1e9 ? `${(v / 1e9).toFixed(1)} млрд`
      : v >= 1e6 ? `${(v / 1e6).toFixed(1)} млн`
        : Math.round(v).toLocaleString("uk");
    return `${s.replace(".", ",")} ${curSym(cur)}`;
  };
  const context = (r) => {
    const bits = [];
    if (r.demand > 0) bits.push(`попит ${r.demand.toFixed(1)}×`);
    if (r.min_pct > 0 && r.max_pct > 0 && r.max_pct > r.min_pct) {
      bits.push(`прийняті заявки ${pct(r.min_pct)}–${pct(r.max_pct)}`);
    }
    if (r.sold > 0) bits.push(`розміщено ${volume(r.sold, r.currency)}`);
    return bits.join(" · ");
  };
  const blocks = [...byCur.entries()].map(([c, list]) => {
    const hasPrev = list.some((r) => r.prev_pct > 0);
    const groups = list.map((r) => ({ label: r.bucket, a: r.pct, b: r.prev_pct || 0 }));
    const fresh = list.reduce((a, b) => (a.date > b.date ? a : b));
    const ctxLine = context(fresh);
    return `<div class="card"><h4>${esc(curSym(c))} ${esc(c)}</h4>
      ${fluid((w, h) => svgGrouped(groups, { W: w, H: h }))}
      ${legend([
        { color: "var(--oi-series-invested)", label: "зараз" },
        hasPrev && { color: "var(--oi-series-neutral)", label: "рік тому" },
      ])}
      <div class="sub-xs">останнє розміщення ${esc(dayMonth(fresh.date))}${
        ctxLine ? ` · ${esc(ctxLine)}` : ""}</div></div>`;
  }).join("");
  return `<div class="card"><h2 class="h-row">Скільки платить ринок ${infoBtn("market")}</h2>
    <div class="sub">Дохідність, під яку Мінфін РОЗМІЩУЄ ОВДП на аукціоні, за строками.
      Це те, з чим можна порівняти власну дохідність портфеля вище — більше в застосунку
      порівнювати нема з чим. Але це ставка первинних дилерів у дилерських обсягах:
      у брокера ціна інша, і дохідність теж.</div>
    <div class="chart-grid">${blocks}</div></div>`;
}
