// Розділ «Ризик» — чим я ризикую. Три сторінки.
//
// Композиція над risk.js: усі функції-картки тут уже готові — вони
// малювались рівно в цьому вигляді й у «Портфелі», просто під іншими
// секціями. Розріз між складом і ризиком записаний у nav.js; тут лишається
// зібрати три сторінки й підв'язати те, чого в risk.js немає, — бенчмарк і
// криву аукціонів, які тягнуться окремими запитами.
//
// Судження, яке варте того, щоб назвати вголос: «Скільки платить ринок»
// (marketCurveCard) стояла в «Портфелі» саме як контекст до власної
// дохідності — risk.js це пояснює прямо. Дохідності лишились у складі.
// Картка живе тут усе одно: інакше «Активи» знову мають блок, який не
// відповідає на їхнє питання, а «з чим порівняти» — точнісінько те
// питання, на яке вона відповідає.
//
// Плитки часток стоять лише на «Структурі». Повторити їх на всіх трьох
// сторінках було б спокусливо — вони дешеві й доречні скрізь, — але тоді
// «чим ризикую» починалося б із того самого, чим і «як розкладене», і
// різниця між сторінками зникла б з першого екрана.

import {
  shareTilesHTML, brokerDonutHTML, currencyChartHTML, rebalanceCard, kindMixCard,
  concentrationCard, ladderTableHTML, liquidityCard, rateRiskCard,
  benchmarkCard, marketCurveCard,
} from "./risk.js";
import { wireDisclosures } from "../disclosure.js";

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
  const [bench, curve] = await Promise.all([
    ctx.soft("benchmark", null),
    ctx.soft("auctions/curve", []),
  ]);
  main.innerHTML = `
    ${benchmarkCard(ctx, bench)}
    ${marketCurveCard(ctx, curve)}`;
  wireDisclosures(main);
}
