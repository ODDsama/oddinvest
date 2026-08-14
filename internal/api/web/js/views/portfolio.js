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

import { infoBtn } from "../info.js";
import { setFundOps, wireFundOps } from "../fund-ops.js";
import { disclosure, section, wireDisclosures } from "../disclosure.js";
import { positionsTableHTML, wirePositions } from "./positions.js";
import {
  brokerDonutHTML, currencyChartHTML, yieldTilesHTML, shareTilesHTML,
  rebalanceCard, kindMixCard, concentrationCard, ladderTableHTML, benchmarkCard,
  liquidityCard, rateRiskCard, marketCurveCard,
} from "./risk.js";
import { bondBuyFormHTML, bondSaleFormHTML, wireBonds } from "./bonds.js";
import { depositFormHTML, closedDepositsHTML, wireDeposits } from "./deposits.js";
import { chartBlockHTML, snapshotsTableHTML, wireHistory } from "./history.js";

// Одна картка на всі три форми: «записати операцію» — це одне питання,
// а не три різні, і три окремі картки казали б протилежне.
//
// Усі форми — під згорнутими заголовками: вони займали п'яту частину висоти
// розділу, хоч потрібні кілька разів на місяць. До складу портфеля заходять
// дивитись, а не заповнювати.
function entryCardHTML(ctx, lots) {
  return `<div class="card"><h2>Записати операцію</h2>
    ${disclosure("buy", "Нова покупка ОВДП", bondBuyFormHTML(ctx, lots))}
    ${disclosure("sale", "Продаж на вторинному ринку", bondSaleFormHTML(ctx, lots))}
    ${disclosure("dep", `Новий вклад ${infoBtn("deposit")}`, depositFormHTML(ctx))}
    <div class="sub" style="margin-top:var(--oi-gap)">Сертифікати фондів сюди не вносять руками —
      їх приносить імпорт виписки в розділі «Гроші».</div>
  </div>`;
}


// «Портфель» = склад цілком.
//
// Порядок змінено на один, але важливий: таблиця позицій стоїть ДРУГОЮ,
// а не дев'ятою. Вона й є відповідь на питання розділу — «що я маю», — а
// перед нею лежало вісім карток про те, як воно розкладене й чим
// ризикує: характеристики того, чого ще не видно.
//
// Решта не зникла, а згрупована в чотири іменовані секції, і три з
// чотирьох згорнуті за замовчуванням. Розбити вкладку надвоє не можна
// (шоста вкладка — це відповідь на розділ, який не тримає своє питання),
// та й не треба: питання в розділу одне, просто відповідей на нього
// шістнадцять, і секція каже, яка відповідь на що.
//
// Механізм той самий <details> із пам'яттю в localStorage, тож людина,
// яка розгорне «Чим ризикую», більше його не згортатиме щоразу.
export async function renderPortfolio(ctx, main) {
  const [positions, lots, sales, ops, deposits, bench, curve] = await Promise.all([
    ctx.api("GET", "positions"),
    ctx.api("GET", "lots"),
    ctx.api("GET", "sales"),
    ctx.soft("funds", []),
    ctx.soft("term-deposits", []),
    ctx.soft("benchmark", null),
    ctx.soft("auctions/curve", []),
  ]);
  setFundOps(ops);
  ctx._deposits = deposits; // wireDeposits реконструює вклад для закриття

  const chart = await chartBlockHTML(ctx);
  main.innerHTML = `
    ${yieldTilesHTML(ctx)}
    ${positionsTableHTML(ctx, positions, lots, sales, deposits)}

    ${section("history", "Як росте", `
      ${chart}
      ${snapshotsTableHTML(ctx)}`, { open: true })}

    ${section("structure", "Як розкладене", `
      <div class="chart-grid">
        ${brokerDonutHTML(ctx)}
        ${currencyChartHTML(ctx)}
      </div>
      ${shareTilesHTML(ctx)}
      ${rebalanceCard(ctx)}
      ${kindMixCard(ctx)}`, { hint: "брокери, валюти, види" })}

    ${section("risk", "Чим ризикую", `
      ${concentrationCard(ctx)}
      ${ladderTableHTML(ctx)}
      ${liquidityCard(ctx)}
      ${rateRiskCard(ctx)}
      ${benchmarkCard(ctx, bench)}
      ${marketCurveCard(ctx, curve)}`, { hint: "концентрація, строки, ставки" })}

    ${section("entry", "Записати операцію", `
      ${entryCardHTML(ctx, lots)}
      ${closedDepositsHTML(ctx, deposits)}`, { hint: "кілька разів на місяць" })}
  `;

  wirePositions(ctx, main);
  wireBonds(ctx, main);
  wireFundOps(ctx, main);
  wireDeposits(ctx, main);
  wireHistory(ctx, main);
  // Останнім: до цього моменту вся розмітка вже на місці, включно з тією,
  // що всередині згорнутих секцій.
  wireDisclosures(main);
}

