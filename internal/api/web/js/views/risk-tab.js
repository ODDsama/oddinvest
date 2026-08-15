// Розділ «Ризик» — чим я ризикую.
//
// Композиція над risk.js: усі функції-картки тут уже готові — вони
// малювались рівно в цьому вигляді й у «Портфелі», просто під іншими
// секціями. Розріз між «Портфель» і «Ризик» записаний у constants.js
// (там-таки причина, чому вкладок стало шість); тут лишається зібрати
// три секції й підвʼязати те, чого в risk.js немає, — бенчмарк і криву
// аукціонів, які тягнуться окремими запитами.
//
// Судження, яке варте того, щоб назвати вголос: «Скільки платить ринок»
// (marketCurveCard) стояла в «Портфелі» саме як контекст до власної
// дохідності — risk.js:454 пояснює це прямо. Дохідності лишились у
// «Портфелі». Переносимо картку сюди все одно: інакше «Портфель» знову
// має блок, який не відповідає на його питання, а «з чим порівняти»
// («Ризик») — точнісінько те питання, на яке вона відповідає.

import {
  shareTilesHTML, brokerDonutHTML, currencyChartHTML, rebalanceCard, kindMixCard,
  concentrationCard, ladderTableHTML, liquidityCard, rateRiskCard,
  benchmarkCard, marketCurveCard,
} from "./risk.js";
import { section, wireDisclosures } from "../disclosure.js";

export async function renderRisk(ctx, main) {
  const [bench, curve] = await Promise.all([
    ctx.soft("benchmark", null),
    ctx.soft("auctions/curve", []),
  ]);

  main.innerHTML = `
    ${shareTilesHTML(ctx)}

    ${section("risk-structure", "Як розкладене", `
      <div class="chart-grid">
        ${brokerDonutHTML(ctx)}
        ${currencyChartHTML(ctx)}
      </div>
      ${rebalanceCard(ctx)}
      ${kindMixCard(ctx)}`, { open: true, hint: "брокери, валюти, види" })}

    ${section("risk-limits", "Чим ризикую", `
      ${concentrationCard(ctx)}
      ${ladderTableHTML(ctx)}
      ${liquidityCard(ctx)}
      ${rateRiskCard(ctx)}`, { open: true, hint: "концентрація, строки, ставки" })}

    ${section("risk-compare", "З чим порівняти", `
      ${benchmarkCard(ctx, bench)}
      ${marketCurveCard(ctx, curve)}`, { hint: "долари, аукціони Мінфіну" })}
  `;

  // Проводки в цих картках немає жодної — усі inline-обробники (звірка,
  // форми, розкриття рядків) лишились у своїх views. Тут лишається
  // тільки механіка розкриття/згортання секцій.
  wireDisclosures(main);
}
