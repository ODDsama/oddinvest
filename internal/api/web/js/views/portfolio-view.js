// Розділ «Портфель» — що я маю і як воно розкладене. П'ять сторінок.
//
// Злиття колишніх «Активів» і «Ризику», і причина проста: обидва питали про
// ОДНЕ ЦІЛЕ. «Що я маю», «як воно виросло», «як розкладене», «де ліміти»,
// «з чим порівняти» — це п'ять кутів одного погляду на портфель, а не два
// різні розділи. Розводило їх те, що в «Активах» лежали ще й посутнісні
// сторінки — ОВДП, фонди, вклади; вони пішли у воронки, і те, що лишилось,
// злиплося саме собою.
//
// «Позиції» першими й нефільтрованими: об'єднана таблиця всіх видів — те,
// заради чого сюди заходять, і причина, чому її колись звели з трьох
// (views/portfolio.js). Числа в її колонках означають те саме для кожного
// виду, тож ОВДП і фонд порівнюються поглядом. Три таблиці з трьома
// наборами колонок сюди НЕ повертаються — і сторінки виду тепер теж не
// таблиці, а воронки.
//
// Плитки часток стоять лише на «Структурі». Повторити їх на всіх трьох
// «ризикових» сторінках було б спокусливо — вони дешеві й доречні скрізь, —
// але тоді «чим ризикую» починалося б із того самого, чим і «як
// розкладене», і різниця між сторінками зникла б з першого екрана.
//
// «Скільки платить ринок» лишається в «Порівнянні», а не в складі: вона
// відповідає рівно на «з чим порівняти», і в таблиці позицій була б блоком
// не про її питання.

import { wireDisclosures } from "../disclosure.js";
import {
  positionsTableHTML, loadPositionsData, wirePositionRows,
} from "./positions.js";
import {
  yieldTilesHTML, yieldMixCard, shareTilesHTML, brokerDonutHTML, currencyChartHTML,
  rebalanceCard, kindMixCard, concentrationCard, ladderTableHTML,
  liquidityCard, rateRiskCard, benchmarkCard, decisionsCard, marketCurveCard,
} from "./risk.js";
import { chartBlockHTML, snapshotsTableHTML, wireHistory } from "./history.js";

// Підсумок місяця живе окремим модулем і реекспортується сюди: сторінка
// належить «Портфелю», а її вміст не має спільного з рештою цього файла
// нічого, крім розділу.
export { period } from "./period.js";

/** Усе разом: та сама таблиця, що й була, з плитками дохідностей над нею.
 *  Єдина сторінка, де види стоять поруч і порівнюються. */
export async function positions(ctx, main) {
  const d = await loadPositionsData(ctx);
  main.innerHTML = `
    ${yieldTilesHTML(ctx)}
    ${yieldMixCard(ctx)}
    ${positionsTableHTML(ctx, d.positions, d.lots, d.sales, d.deposits)}`;
  wirePositionRows(ctx, main, d);
}

/** Як росте: крива капіталу й таблиця знімків. Відповідає не «що я маю», а
 *  «звідки це взялось». */
export async function growth(ctx, main) {
  const chart = await chartBlockHTML(ctx);
  main.innerHTML = `${chart}${snapshotsTableHTML(ctx)}`;
  wireHistory(ctx, main);
  wireDisclosures(main);
}

/** Як розкладене: брокери, валюти, види інструментів. */
export async function structure(ctx, main) {
  main.innerHTML = `
    ${shareTilesHTML(ctx)}
    <div class="chart-grid">
      ${brokerDonutHTML(ctx)}
      ${currencyChartHTML(ctx)}
    </div>
    ${rebalanceCard(ctx)}
    ${kindMixCard(ctx)}`;
  wireDisclosures(main);
}

/** Чим ризикую: концентрація, строки, ліквідність, ставки. */
export async function limits(ctx, main) {
  main.innerHTML = `
    ${concentrationCard(ctx)}
    ${ladderTableHTML(ctx)}
    ${liquidityCard(ctx)}
    ${rateRiskCard(ctx)}`;
  wireDisclosures(main);
}

/** З чим порівняти: долари під матрацом і аукціони Мінфіну.
 *
 *  Обидві картки тягнуть власні дані, і обидві — м'яко: маршрут може бути
 *  новішим за бекенд, а сторінка з однією карткою краща за порожню. */
export async function compare(ctx, main) {
  const [bench, curve, dec] = await Promise.all([
    ctx.soft("benchmark", null),
    ctx.soft("auctions/curve", []),
    // Журнал рішень — третє «а якби інакше»: бенчмарк порівнює з
    // нічогонеробленням, аукціони — з ринком, а це — з тим, що радив
    // сам застосунок.
    ctx.soft("decisions", null),
  ]);
  main.innerHTML = `
    ${benchmarkCard(ctx, bench)}
    ${decisionsCard(ctx, dec)}
    ${marketCurveCard(ctx, curve)}`;
  wireDisclosures(main);
}
