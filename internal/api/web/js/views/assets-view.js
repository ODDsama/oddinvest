// Розділ «Активи» — що в мене є. Сім сторінок з ОДНІЄЇ таблиці позицій.
//
// Головне рішення розділу успадковане, а не нове: позиції всіх видів
// живуть в одній таблиці (views/positions.js). Колись їх було три зі
// своїми колонками, бо в сертифіката немає ні номіналу, ні графіка
// купонів; заперечення було справедливе до КОЛОНОК, а не до самої ідеї,
// тож спільними лишились тільки ті, що мають сенс для кожного —
// вкладено, вартість, реальна дохідність, строк, — а специфіка пішла в
// рядок деталей. Виграш саме той, заради якого все й робилось: видно весь
// портфель одразу й видно, що з нього дохідніше.
//
// Тому розріз на сторінки йде ПО РЯДКАХ. «Позиції» — та сама таблиця
// цілком; сторінка виду — вона ж, звужена фільтром. Числа в колонках
// усюди означають те саме, тож ОВДП зі сторінки ОВДП і фонд зі сторінки
// фондів порівнюються поглядом, як і раніше. Три таблиці з трьома
// наборами колонок сюди НЕ повертаються.
//
// Дані тягне один спільний loadAssets на всі сім сторінок, і це не ліньки.
// fund-ops.js і npf.js тримають журнал у модульній змінній, яку мусить
// виставити той, хто дані вантажив (setFundOps/setNPF); пропущений виклик
// не падає, а тихо малює порожні деталі. Один вхід замість семи знімає
// цей клас помилок цілком. Платня — нуль: GET-и йдуть через кеш store.js,
// тож обхід усіх семи сторінок коштує один набір запитів.

import { setFundOps, wireFundOps } from "../fund-ops.js";
import { setNPF, wireNPF } from "../npf.js";
import { wireDisclosures } from "../disclosure.js";
import { positionsTableHTML, wirePositions } from "./positions.js";
import { yieldTilesHTML } from "./risk.js";
import { wireBonds } from "./bonds.js";
import { closedDepositsHTML, wireDeposits } from "./deposits.js";
import { chartBlockHTML, snapshotsTableHTML, wireHistory } from "./history.js";
import { routeFor } from "../routes.js";

async function loadAssets(ctx) {
  const [positions, lots, sales, ops, deposits, npfAcc, npfOps, npfNav] = await Promise.all([
    ctx.api("GET", "positions"),
    ctx.api("GET", "lots"),
    ctx.api("GET", "sales"),
    ctx.soft("funds", []),
    ctx.soft("term-deposits", []),
    // М'яко, як фонди й вклади: на старій БД НПФ немає, і валити через це
    // сторінку означало б показати порожній екран замість портфеля.
    ctx.soft("npf-accounts", []),
    ctx.soft("npf", []),
    ctx.soft("npf-nav", []),
  ]);
  setFundOps(ops);
  setNPF({ accounts: npfAcc, ops: npfOps, nav: npfNav });
  return { positions, lots, sales, deposits };
}

// Проводка рядків деталей. У них живуть форми продажу, поповнення й
// закриття вкладу, тож підв'язувати треба на КОЖНІЙ сторінці, де таблиця
// взагалі є. Усі чотири функції терплять відсутність своїх цілей —
// forms.js вартує кожну querySelector, а querySelectorAll просто дає
// порожній список, — тож зайвий виклик тут нічого не коштує.
function wireRows(ctx, main, deposits) {
  wirePositions(ctx, main);
  wireBonds(ctx, main);
  wireFundOps(ctx, main);
  wireNPF(ctx, main);
  wireDeposits(ctx, main, deposits);
  wireDisclosures(main);
}

/** Усе разом: та сама таблиця, що й була, з плитками дохідностей над нею.
 *  Єдина сторінка розділу, де види стоять поруч і порівнюються. */
export async function positions(ctx, main) {
  const d = await loadAssets(ctx);
  main.innerHTML = `
    ${yieldTilesHTML(ctx)}
    ${positionsTableHTML(ctx, d.positions, d.lots, d.sales, d.deposits)}`;
  wireRows(ctx, main, d.deposits);
}

// Сторінка одного виду: таблиця, звужена фільтром, плюс те, що має сенс
// лише для нього. Спільна обгортка, бо шість сторінок відрізняються рівно
// трьома значеннями — видом, назвою й текстом порожнього стану.
function kindPage(kind, title, emptyOpts, extra = null) {
  return async function render(ctx, main) {
    const d = await loadAssets(ctx);
    main.innerHTML = positionsTableHTML(ctx, d.positions, d.lots, d.sales, d.deposits,
      { kinds: [kind], title, empty: emptyOpts })
      + (extra ? extra(ctx, d) : "");
    wireRows(ctx, main, d.deposits);
  };
}

export const bonds = kindPage("bond", "ОВДП", {
  text: "Папери з'являться тут після першої покупки.",
  action: { href: routeFor("buy"), label: "Записати покупку" },
});

// Сертифікати не вносять руками, і сказати це треба саме тут: питання
// «а де додати фонд» виникає на цій сторінці, а відповідь на нього доти
// лежала в чужій формі в «Портфелі» й посилалась на розділ, якого вже
// немає.
export const funds = kindPage("fund", "Фонди", {
  text: "Сертифікати заводить імпорт виписки — руками їх не вносять.",
  action: { href: routeFor("entry/import"), label: "Завантажити виписку" },
}, () => `<div class="card"><div class="sub">Журнал сертифікатів веде виписка:
  купівлі, продажі й дивіденди приходять файлом і правляться тільки видаленням.
  Два джерела правди — виписка й рука — розійшлися б, і розійшлися б тихо.</div></div>`);

export const npf = kindPage("npf", "НПФ", {
  text: "Пенсійний рахунок з'явиться тут, коли буде заведений у довідниках.",
  action: { href: routeFor("settings/refs"), label: "Довідники" },
});

// Закриті вклади — єдине, чого немає в таблиці: вона показує лише чинні.
// Питання «скільки я взяв із того, що вже закрив» більше ніде не має місця.
export const deposits = kindPage("deposit", "Вклади", {
  text: "Вклади з'являться тут після першого відкритого.",
  action: { href: routeFor("topup"), label: "Відкрити вклад" },
}, (ctx, d) => closedDepositsHTML(ctx, d.deposits));

export const reserve = kindPage("reserve", "Резерв", {
  text: "Резерв — це те, що лежить доступним миттєво й без втрат, коли гроші раптом знадобились.",
  action: { href: routeFor("entry/reserve"), label: "Записати рух" },
});

/** Як росте: крива капіталу й таблиця знімків. Єдина сторінка розділу без
 *  таблиці позицій — вона відповідає не «що я маю», а «звідки це взялось». */
export async function growth(ctx, main) {
  const chart = await chartBlockHTML(ctx);
  main.innerHTML = `${chart}${snapshotsTableHTML(ctx)}`;
  wireHistory(ctx, main);
  wireDisclosures(main);
}
