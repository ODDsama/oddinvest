// Чим ризикую: розклад по брокерах і валютах, ребалансування,
// ліквідність, процентний ризик, бенчмарк і драбина.

import {
  esc, curSym, dayMonth, pct, pp, uah2 as fmtUAH, cur2 as fmtCur,
  fundsCost, marketCostUAH, uahSharePct, uahTargetPct,
} from "../format.js";
import { infoBtn } from "../info.js";
import { opsGrid } from "../grid.js";
import { svgBars, svgGrouped, svgDonut, fluid } from "../charts.js";
import { tile, yieldNote, yieldPair, needsSetting, empty, legend, kindPill } from "../components.js";
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
  // Гривня — залишок від валютних цілей, і виводить його format.js. Та сама
  // формула знадобилась картці «Валюта за стратегією» на «Що купити», а
  // третя копія віднімання розійшлася б із двома першими мовчки.
  const groups = [
    { label: "UAH", a: uahSharePct(s), b: uahTargetPct(s) },
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



/** Картка «з чого складається дохідність портфеля».
 *
 *  Навіщо вона є. Плитка над нею показує ОДНЕ число й каже, по скількох
 *  грошах воно пораховане, — але не каже, чиї це гроші й чому решта поза
 *  ним. Доти відповіді не було ніде: чотири дохідності по видах лежать у
 *  kind_yield_real_pct від фази 9, а єдиний екран, що їх читав, показував
 *  ПО ОДНІЙ на сторінку воронки. Щоб побачити чотири числа, треба було
 *  обійти чотири сторінки, і поставити їх поруч не давав жоден.
 *
 *  Рядки покривають УВЕСЬ капітал, а не лише те, що заробляє. Саме в цьому
 *  сенс: сума стовпця «грн-екв.» дорівнює capital_uah за побудовою
 *  (state/capital.go), тож видно кожну гривню — і ту, що працює, і ту, що
 *  ні. Твердження «зважено по інвестованому» з обіцянки підпису стає
 *  рядком, який можна перевірити очима.
 *
 *  Резерв і готівка стоять із прочерком, а не з нулем. Нуль читався б як
 *  «заробляє нічого», тобто як вимір; прочерк каже «дохідності немає за
 *  природою» — та сама межа, що в kind_yield_real_pct, де резерву немає й
 *  бути не може.
 *
 *  Нічого не рахується тут: усі числа приходять готовими зі стану. Друга
 *  копія зважування в JS — рівно та помилка, проти якої існує
 *  state/capital.go. */
export function yieldMixCard(ctx) {
  const s = ctx.summary || {};
  const real = s.kind_yield_real_pct || {}, nom = s.kind_yield_pct || {};
  const MONEY = {
    bonds: s.nominal_uah_eq, funds: s.funds_uah,
    deposits: s.deposits_uah, npf: s.npf_uah,
  };
  const rows = [];
  for (const k of ["bonds", "funds", "deposits", "npf"]) {
    const money = MONEY[k] || 0;
    if (!money) continue;
    rows.push({ id: k, name: KIND_GROUP[k], money, real: real[k], nominal: nom[k] });
  }
  // Те, що поза числом, — окремими рядками й останніми: спершу те, що
  // працює, потім ціна спокою.
  if (s.reserve_uah > 0) {
    rows.push({ id: "reserve", name: KIND_GROUP.reserve, money: s.reserve_uah, idle: true });
  }
  // Цілі накопичення — тієї ж природи, що й подушка: гроші в капіталі є,
  // дохідності в них немає. Без цього рядка обіцянка картки («сума стовпця
  // з грішми дорівнює капіталу») не сходилась рівно на цілі.
  if (s.goals_uah > 0) {
    rows.push({ id: "goals", name: KIND_GROUP.goals, money: s.goals_uah, idle: true });
  }
  if (s.account_uah > 0) {
    rows.push({ id: "cash", name: "Готівка в брокерів", money: s.account_uah, idle: true });
  }
  if (!rows.length) return "";
  const total = rows.reduce((a, r) => a + r.money, 0);
  return `<div class="card">
    <h2 class="h-row">З чого складається дохідність ${infoBtn("yields")}</h2>
    <div class="note">Кожен рядок — гроші й те, що вони приносять. Сума стовпця
      з грішми дорівнює капіталу, тож видно й ту частину, яка не заробляє.</div>
    ${opsGrid({
    cols: [
      { key: "name", label: "Вид", cell: (r) => esc(r.name) },
      { key: "money", label: "грн-екв.", num: true, cell: (r) => fmtUAH(r.money) },
      { key: "share", label: "Частка", num: true, prio: 3,
        cell: (r) => pct(total > 0 ? (r.money / total) * 100 : 0) },
      { key: "yield", label: "Дохідність", num: true,
        cell: (r) => (r.idle
          ? `<span class="muted">—</span>`
          : (r.real != null ? yieldPair(r.real, r.nominal) : `<span class="muted">—</span>`)) },
    ],
    rows,
    caption: "Склад дохідності: вид, гроші, частка капіталу, дохідність",
  })}
    ${s.blended_yield_base_uah > 0 ? `<div class="sub-xs muted">Зведена дохідність
      рахується по ${fmtUAH(s.blended_yield_base_uah)} — це рядки з дохідністю.
      Решта капіталу або не заробляє за природою (подушка, готівка), або її ставка
      застосунку невідома (вклад без заданої ставки).</div>` : ""}
    ${s.blended_yield_split ? `<div class="sub-xs muted">З них
      <b>${fmtUAH(s.blended_yield_split.measured_uah)}</b> заробили
      ${pct(s.blended_yield_split.measured_real_pct)} — це факт по прожитому;
      решта <b>${fmtUAH(s.blended_yield_split.promised_uah)}</b> показує
      ${pct(s.blended_yield_split.promised_real_pct)} ОБІЦЯНИХ: YTM облігацій
      зафіксований до погашення, ставка вкладу договірна. Обіцянка не гірша за
      факт — вона просто про інше.</div>` : ""}
  </div>`;
}

/** Рядок «зароблено X з N ₴ · обіцяно Y з M ₴».
 *
 *  Слова навмисно прості. Підпис «різні основи» поруч правильний і
 *  незрозумілий саме тому, що написаний мовою моделі: власник питав «чому
 *  тут так мало», дивлячись на 6.5%, і відповіді на екрані не було. Тепер
 *  видно, що 1.7% з тих грошей зароблено, а 14.2% лише обіцяно.
 *
 *  Відсотки тут РЕАЛЬНІ — ті самі, що головне число плитки, а не
 *  номінальні з рядка над ними. Порожньо, коли розкладати нема чого:
 *  бекенд віддає split лише за наявності обох половин. */
function splitNote(sp) {
  if (!sp) return "";
  return `<div class="sub-xs muted">зароблено ${pct(sp.measured_real_pct)} з
    ${fmtUAH(sp.measured_uah)} · обіцяно ${pct(sp.promised_real_pct)} з
    ${fmtUAH(sp.promised_uah)}</div>`;
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
  const xr = s0.xirr || {}, rz = s0.realized || {};
  // XIRR — фактично реалізоване, номінальне за природою: це те, що
  // справді сталося з грошима, а не оцінка наперед. Реального двійника
  // в нього немає, тож слово ставимо явно.
  //
  // Коли ставки ще немає, плитка НЕ порожніє: результат у гривнях чесний
  // з першого дня, бо не ануалізований, а підпис називає, чого саме
  // бракує річному числу й скільки лишилось. Слово «Результат», а не
  // «Зароблено»: на молодому портфелі число законно відʼємне (папір
  // куплений із премією, а купон ще не прийшов), і «зароблено −97 ₴» було
  // б дрібною неправдою там, де все інше чесне. Доти тут стояв прочерк, підписаний
  // «≥30 днів», і портфель із тримісячною історією читався як зламаний —
  // поріг же міряє вік ГРОШЕЙ, і свіжий внесок відкидає його назад.
  //
  // Поріг береться з документа (min_days), а не вписаний тут: те саме
  // число вже живе в бекенді, і друга копія розійшлася б із першою.
  const gainOf = (c, r) =>
    `${fmtCur(r.gain, c)} · ${pct(r.gain_pct, 2)} від вкладеного`;
  // Зведене «заробило» — ПЕРЕД валютними плитками: це головне число, а
  // валютні уточнюють його розрізом. Розгалуження те саме, що нижче: доки
  // ставки немає, показуємо результат у гривнях, бо він не ануалізований і
  // чесний з першого дня.
  //
  // Плитки немає зовсім, коли немає обʼєкта: це не «нуль», а «сказати
  // нічого» — курсу на дату якогось руху не знайшлось, і вигадувати його
  // дорожче, ніж промовчати.
  const tot = s0.total_return;
  const totalTile = !tot ? "" : (tot.xirr_pct != null
    ? tile(`Заробило ${infoBtn("xirr")}`, pct(tot.xirr_pct, 2),
        `<div class="sub">${fmtUAH(tot.gain_uah)} · ${pct(tot.gain_pct, 2)} від вкладеного</div>
         <div class="sub-xs">усе разом, у гривні за курсом на дату кожного руху${
  tot.fx_max_lag_days > 0 ? ` · найстаріший курс відстав на ${tot.fx_max_lag_days} дн.` : ""}</div>`)
    : tile(`Заробило ${infoBtn("xirr")}`, fmtUAH(tot.gain_uah),
        `<div class="sub">${pct(tot.gain_pct, 2)} від вкладеного, НЕ в річних</div>
         <div class="sub-xs">річна ставка зʼявиться, коли гроші попрацюють
           ${tot.min_days} днів у середньому — зараз ${tot.money_days}</div>`));
  // Валютні плитки ховаються, коли валюта одна: тоді зведене число й
  // валютне — ОДНЕ І ТЕ САМЕ, бо згортати нема чого. Спіймано вживу на
  // гривневому портфелі, де «Заробило 98,39 ₴» і «Результат ₴ 98,39 ₴»
  // стояли поруч двома плитками. Це рівно та хвороба, від якої в контракті
  // зʼявився capital_uah: людина дивиться на екран і не знає, котре з двох
  // чисел її.
  //
  // Коли зведеного немає (курсу на дату якогось руху не знайшлось),
  // валютні лишаються: краще розріз, ніж порожньо.
  const showPerCur = !tot || Object.keys(rz).length > 1;
  const xirrTiles = !showPerCur ? "" : Object.keys(rz).length
    ? Object.entries(rz).map(([c, r]) => (xr[c] != null
        ? tile(`XIRR ${curSym(c)} ${infoBtn("xirr")}`, pct(xr[c], 2),
            `<div class="sub">${gainOf(c, r)}</div>
             <div class="sub-xs">номінальних, за фактом</div>`)
        : tile(`Результат ${curSym(c)} ${infoBtn("xirr")}`, fmtCur(r.gain, c),
            `<div class="sub">${pct(r.gain_pct, 2)} від вкладеного, НЕ в річних</div>
             <div class="sub-xs">річна ставка (XIRR) зʼявиться, коли гроші попрацюють
               ${r.min_days} днів у середньому — зараз ${r.money_days}</div>`))).join("")
    : tile("XIRR", "—",
        `<div class="sub">потоків замало, щоб було що міряти</div>`);
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
      `${yieldNote(s0.funds_yield_pct, s0.funds_yield_basis || "")}
       ${splitNote(s0.funds_yield_split)}`) : ""}
    ${s0.blended_yield_pct > 0 ? tile(`Дохідність портфеля ${infoBtn("yields")}`,
      pct(s0.blended_yield_real_pct),
      // Підпис каже ДВІ речі, і обидві раніше були неправдою. Склад:
      // доти в число входили лише ОВДП і фонди, хоч зветься воно
      // портфелем. І ваги: стояло «зважено вкладеним», а вкладеним не
      // була жодна з двох ваг — ОВДП важили номіналом, фонди ринковою
      // вартістю.
      //
      // Скільки саме грошей число покриває, каже рядок під ним: без
      // нього «по інвестованому» лишається обіцянкою підпису, а з ним
      // видно, що поза числом — подушка й готівка, а не забутий вид.
      `${yieldNote(s0.blended_yield_pct, s0.blended_yield_basis || "")}
       ${s0.blended_yield_base_uah > 0
         ? `<div class="sub-xs muted">по ${fmtUAH(s0.blended_yield_base_uah)} з
             ${fmtUAH(s0.capital_uah)} капіталу · подушка й готівка не заробляють</div>`
         : ""}`) : ""}
    ${totalTile}
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
      // Вклади без цілі — випадок особливий, і мовчати про це не можна.
      // «Цілі за часткою немає» тут читалося б як недогляд налаштувань,
      // хоча насправді її немає НАВМИСНО: вклад — черга до валютного
      // паперу, а не вид, під який ставлять частку. Скільки з нього ця
      // черга виправдовує, каже транзит; решта — гроші, що чекають на
      // щось інше, і це теж варто бачити.
      const why = r.key === "deposits"
        ? (r.transit_uah > 0
            ? `з них ${fmtUAH(r.transit_uah)} тримає черга до наступного валютного паперу`
              + (nowUAH > r.transit_uah
                  ? `, решта ${fmtUAH(nowUAH - r.transit_uah)} — понад неї` : "")
            : "власної цілі у вкладів немає: це черга до валютного паперу, а не вид під частку")
        : "показано довідково";
      return `<div class="mb"><b>${esc(title)}</b> —
        <span class="muted">${r.current_pct}% (${fmtUAH(nowUAH)}), цілі за часткою немає</span><br>
        <span class="sub">${why}</span></div>`;
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
    // ТРАНЗИТ у рядку ОВДП — вирізка з його ж цілі, а не окремий вид.
    // Валютний папір коштує $1000, і доки цільова сума у валюті не
    // ділиться на нього націло, залишок фізично не може бути облігацією.
    // Без цього рядка ціль ОВДП виглядала б недобраною без причини — а
    // причина в тому, що гроші вже в дорозі.
    const transit = r.transit_pct > 0
      ? `<br><span class="sub">з них <b>${r.transit_pct}%</b> (${fmtUAH(r.transit_uah)})
         поки у валютному вкладі: на ще один папір не зібрано</span>`
      : "";
    return `<div class="mb">
      <b>${esc(title)}</b> — ціль ${r.target_pct}%, зараз ${r.current_pct}%
      <span class="muted">(${fmtUAH(nowUAH)})</span><br>
      <div class="progress mt-xs mb-xs"><span style="--oi-fill:${bar}%;--oi-c:${
        over ? "var(--oi-warn)" : r.deficit_uah > 0 ? "var(--oi-info)" : "var(--oi-ok)"}"></span></div>
      ${line}${transit}</div>`;
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

// Картки «А якби просто долари» тут більше немає, і ось чому.
//
// Вона ставила правильне питання й відповідала на нього одним суперником
// і без кривої. Тепер це рядок «Ціни моїх рішень» (views/rivals.js), де
// поруч стоять гривня, євро й ринкова ОВДП, а гроші міряються двома
// рівнями. Число не змінилось джерелом: /api/benchmark лишився — його
// їсть віха «Обіграв просто долари» — і став обгорткою над тим самим
// рушієм, тож двох рахунків одного числа не з'явилось.

/** Ретроспектива помічника: чи слухаюсь і чи справдилось.
 *
 *  Стоїть у «Порівнянні» поруч із «а якби просто долари» не випадково: там
 *  уже живе питання «а якби інакше», і це його продовження. Тільки
 *  бенчмарк порівнює з нічогонеробленням, а це — з тим, що радив сам
 *  застосунок.
 *
 *  ЗВЕДЕННЯ МОВЧИТЬ, ДОКИ РІШЕНЬ МАЛО, і поріг приходить із бекенда
 *  (min_rows), а не вписаний тут: різниця в кілька десятих п.п. на трьох
 *  рішеннях — шум, за яким міняють режим рейтингу. Друга копія порога в
 *  браузері розійшлася б із першою мовчки.
 *
 *  Жодної арифметики (CLAUDE.md §5): і втрачені п.п., і середні по
 *  режимах приходять готовими з /api/decisions. */
export function decisionsCard(ctx, d) {
  const rows = (d && d.rows) || [];
  if (!rows.length) return "";
  const sum = d.summary;
  const hint = sum ? `${sum.followed}/${sum.count}` : "";
  return `<div class="card">${disclosure("decisions", "Чи працює те, що радить помічник", `
    ${sum ? decisionsSummaryHTML(sum) : `<div class="note">Зведення з'явиться,
      коли рішень набереться ${d.min_rows}: на кількох рядках різниця в
      десяті відсоткового пункта — це шум, а не висновок.</div>`}
    ${opsGrid({
    cols: [
      { key: "date", label: "Коли", cell: (r) => esc(r.made_on) },
      { key: "what", label: "Що взяв",
        cell: (r) => `${kindPill(r.kind)} ${esc(r.ref)}` },
      { key: "amount", label: "Сума", num: true, prio: 2,
        cell: (r) => (r.amount && r.amount.amount
          ? fmtCur(Number(r.amount.amount), curSym(r.amount.currency)) : "—") },
      { key: "promised", label: "Обіцяли", num: true,
        cell: (r) => pct(r.promised_pct) },
      { key: "actual", label: "За фактом", num: true,
        cell: (r) => (r.basis === "за фактом виплат"
          ? pct(r.actual_pct)
          : `<span class="muted">${esc(r.basis || "—")}</span>`) },
      { key: "drift", label: "Розхід", num: true, prio: 2,
        cell: (r) => (r.basis === "за фактом виплат"
          ? `<span class="${r.drift_pp >= 0 ? "t-ok" : "t-danger"}">${pp(r.drift_pp)}</span>`
          : "—") },
      { key: "rank", label: "Рядок", num: true, prio: 3,
        cell: (r) => (r.rank_pos ? String(r.rank_pos) : "—") },
      { key: "vstop", label: "Замість", prio: 3,
        cell: (r) => (r.top_label
          ? `<span class="muted">${esc(r.top_label)} · ${pp(r.vs_top_pp)}</span>`
          : "—") },
    ],
    rows,
    caption: "Журнал рішень: дата, що взяв, сума, обіцяна дохідність, за фактом, розходження, рядок рейтингу, чим знехтував",
  })}
    <div class="muted fine">«За фактом» рахується лише для облігацій: рішення про папір
      стає одним лотом, чиї потоки відокремлені від решти портфеля. Операція фонду не
      відокремлюється (позиція — сальдо журналу), а вклад і НПФ факту не потребують —
      там обіцянка і є фактом за побудовою. Ставка зʼявляється не одразу: доки гроші не
      пролежали достатньо, річна дохідність нічого не означає.
      <br>«Замість» показує, що стояло верхнім рядком і наскільки твій вибір дохідніший
      за нього. Відʼємне число — не помилка: у режимі «план» рейтинг зважує дохідність
      разом із дефіцитом до цілі, тож верхнім цілком законно стоїть менш дохідний рядок,
      який зрушує портфель до політики.</div>`, hint)}</div>`;
}

// Зведення трьома реченнями, а не плитками: тут кожне число має сенс лише
// разом зі своїм знаменником («9 з 12»), а плитка показує чисельник
// великим і знаменник дрібним — тобто наголошує рівно не те.
function decisionsSummaryHTML(s) {
  const modes = (s.by_mode || []).filter((m) => m.mode).map((m) =>
    `<div class="pv-row"><span class="muted">режим «${esc(m.mode)}»</span>
      <span>${m.followed}/${m.count} за верхнім рядком${m.measured
  ? ` · розхід ${pp(m.drift_pp_avg)}` : ""}</span></div>`).join("");
  return `<div class="mb-lg">
    <div class="pv-row"><span class="muted">Узяв верхній рядок</span>
      <span><b>${s.followed}</b> із ${s.count}</span></div>
    ${s.vs_top_pp_avg
    ? `<div class="pv-row"><span class="muted">Коли брав не верхній — різниця дохідності</span>
        <span>${pp(s.vs_top_pp_avg)}</span></div>` : ""}
    ${s.measured
    ? `<div class="pv-row"><span class="muted">Обіцянка проти факту (${s.measured})</span>
        <span class="${s.drift_pp_avg >= 0 ? "t-ok" : "t-danger"}">${pp(s.drift_pp_avg)}</span></div>`
    : ""}
    ${s.reserve_count
    ? `<div class="pv-row"><span class="muted">Рухів у подушку (${s.reserve_count}) — доступне тоді давало</span>
        <span>${pp(s.reserve_forgone_pct_avg)}</span></div>
      <div class="sub-xs">Це НЕ втрачене. Подушку тримають, щоб не продавати папір у
        поганий місяць, і в рядки вище вона не входить: верхнім рядком рейтингу вона
        не стоїть ніколи, тож у частці «взяв верхній» кожен її рух читався б як
        порушення дисципліни.</div>` : ""}
    ${s.goal_count
    ? `<div class="pv-row"><span class="muted">Рухів у цілі (${s.goal_count}) — доступне тоді давало</span>
        <span>${pp(s.goal_forgone_pct_avg)}</span></div>
      <div class="sub-xs">Так само НЕ втрачене й так само поза рядками вище — але
        окремо від подушки: на авто збирають, ЩОБ його купити, а матрац тримають, щоб
        не чіпати. Одне число на двох сховало б, скільки з відкладеного піде назад
        у життя, а скільки лишиться лежати.</div>` : ""}
    ${modes}
  </div>`;
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
      ${l.breakable_uah > 0 ? tile("Можна забрати достроково", fmtUAH(l.breakable_uah),
        // ОКРЕМА плитка, а не рядок під «Замкнено», бо це інше твердження,
        // а не відтінок того самого. Строковий вклад в Україні
        // безвідкличний за замовчуванням — дострокове повернення можливе
        // лише там, де це прямо в договорі, — і доти застосунок вважав
        // таким КОЖЕН вклад. Тепер він розрізняє, і зсипати обидва в одне
        // число означало б казати «цього не дістати» про гроші, які
        // дістати можна.
        `<div class="sub">відкличні вклади: тіло повернуть, відсотки згорять</div>`) : ""}
    </div>
    <div class="muted fine">Вікна накопичувальні: «за 90 днів» уже містить «за 30».
      Рахуються гроші на рахунках плюс купони, погашення й тіла вкладів, що гасяться у вікні.
      «Замкнено» — тіла БЕЗВІДКЛИЧНИХ вкладів зі строком далі й пенсійні активи: ні того, ні
      того не дістати ніяк. «Можна забрати достроково» — вклади, чий договір це дозволяє: тіло
      повернуть, відсотки згорять. Скільки саме коштує розірвання, тут не сказано навмисно —
      штрафну ставку знає банк, і вигадане число поруч зі справжнім знецінило б обидва.
      За замовчуванням строковий вклад в Україні безвідкличний, тож позначку «відкличний»
      ставлять на самому вкладі за договором.<br><br>
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

  // Знак і тон — поруч, бо обидва читають одне й те саме число: плюс
  // зелений, мінус червоний. Доти вони жили всередині map'а рядків.
  const sgn = (v) => (v > 0 ? "+" : "");
  const tone = (x) => (x.change_pct >= 0 ? "t-ok" : "t-danger");

  const priceBlock = rr.duration_years ? `
    <h4>Чутливість ціни · лише ОВДП</h4>
    <div class="tiles flush">
      <div class="tile"><div class="lbl">Дюрація (Маколея)</div><div class="val">${rr.duration_years} р.</div></div>
      <div class="tile"><div class="lbl">Модифікована</div><div class="val">${rr.modified_dur}</div></div>
      <div class="tile"><div class="lbl">Приведена вартість</div><div class="val">${fmtUAH(rr.pv_uah)}</div></div>
    </div>
    ${opsGrid({
    cols: [
      { key: "delta", label: "Зміна ставок",
        cell: (x) => `${sgn(x.delta_pp)}${x.delta_pp} п.п.` },
      { key: "change_pct", label: "Вартість", num: true,
        cell: (x) => `<span class="${tone(x)}">${sgn(x.change_pct)}${x.change_pct}%</span>` },
      { key: "change_uah", label: "У грошах", num: true,
        cell: (x) => `<span class="${tone(x)}">${sgn(x.change_uah)}${fmtUAH(x.change_uah)}</span>` },
    ],
    rows: rr.scenarios || [],
    caption: "Чутливість ціни ОВДП до зміни ставок: зсув, зміна вартості у відсотках і в грошах",
  })}
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
    ${opsGrid({
    cols: [
      { key: "year", label: "Рік", cell: (r) => String(r.year) },
      { key: "uah", label: "UAH", num: true, cell: (r) => (r.uah ? fmtUAH(r.uah) : "—") },
      { key: "uah_bar", label: "", cell: (r) => bar(r.uah, "var(--oi-accent)") },
      { key: "usd", label: "USD", num: true, cell: (r) => fx(r.usd, "$") },
      { key: "usd_bar", label: "", cell: (r) => bar(r.usd, "var(--oi-info)") },
      { key: "eur", label: "EUR", num: true, cell: (r) => fx(r.eur, "€") },
      { key: "eur_bar", label: "", cell: (r) => bar(r.eur, "var(--oi-warn)") },
    ],
    rows: lad,
    caption: "Драбина погашень по роках: скільки повертається в кожній валюті",
  }) || empty("Драбини ще немає",
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
