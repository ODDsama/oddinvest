// Розділ «Портфель» — що в мене є.
//
// Усе про склад: позиції ОВДП і сертифікати фондів, лоти, продажі,
// дивіденди, дохідності, історія «Як росте». Раніше це було розкидано по
// трьох вкладках («Портфель», «План») і «Огляду», хоча відповідає на
// одне питання.
//
// Розклад по брокерах і валютах, ребалансування, концентрація, драбина,
// ліквідність, ставки й бенчмарк переїхали в окрему вкладку «Ризик»
// (views/risk-view.js) — вони відповідають не на «що я маю», а на «чим я
// ризикую», і причина розрізу записана в nav.js. risk.js як
// джерело функцій лишається спільним для обох розділів.
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
import { setNPF, wireNPF } from "../npf.js";
import { disclosure, section, wireDisclosures } from "../disclosure.js";
import { positionsTableHTML, wirePositions } from "./positions.js";
import { yieldTilesHTML } from "./risk.js";
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
    <div class="sub mt">Сертифікати фондів сюди не вносять руками —
      їх приносить імпорт виписки в розділі «Гроші».</div>
  </div>`;
}


// «Портфель» = склад цілком.
//
// Таблиця позицій стоїть ДРУГОЮ, а не дев'ятою: вона й є відповідь на
// питання розділу — «що я маю». Решта — історія й запис операцій, обидві
// секціями з памʼяттю в localStorage; «Записати операцію» згорнута
// за замовчуванням, «Як росте» — ні, бо саме заради неї сюди заходять
// частіше, ніж заради форми.
export async function renderPortfolio(ctx, main) {
  const [positions, lots, sales, ops, deposits, npfAcc, npfOps, npfNav] = await Promise.all([
    ctx.api("GET", "positions"),
    ctx.api("GET", "lots"),
    ctx.api("GET", "sales"),
    ctx.soft("funds", []),
    ctx.soft("term-deposits", []),
    // М'яко, як фонди й вклади: на старій БД НПФ немає, і валити через це
    // весь розділ означало б показати порожній екран замість портфеля.
    ctx.soft("npf-accounts", []),
    ctx.soft("npf", []),
    ctx.soft("npf-nav", []),
  ]);
  setFundOps(ops);
  setNPF({ accounts: npfAcc, ops: npfOps, nav: npfNav });

  const chart = await chartBlockHTML(ctx);
  main.innerHTML = `
    ${yieldTilesHTML(ctx)}
    ${positionsTableHTML(ctx, positions, lots, sales, deposits)}

    ${section("history", "Як росте", `
      ${chart}
      ${snapshotsTableHTML(ctx)}`, { open: true })}

    ${section("entry", "Записати операцію", `
      ${entryCardHTML(ctx, lots)}
      ${closedDepositsHTML(ctx, deposits)}`, { hint: "кілька разів на місяць" })}
  `;

  wirePositions(ctx, main);
  wireBonds(ctx, main);
  wireFundOps(ctx, main);
  wireNPF(ctx, main);
  wireDeposits(ctx, main, deposits);
  wireHistory(ctx, main);
  // Останнім: до цього моменту вся розмітка вже на місці, включно з тією,
  // що всередині згорнутих секцій.
  wireDisclosures(main);
}

