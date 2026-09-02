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

import { empty } from "../components.js";
import { routeFor } from "../routes.js";
import { wireCrud } from "../crud.js";
import { wireRefs } from "../refs.js";
import { wireDisclosures } from "../disclosure.js";
import { bondBuyFormHTML } from "./bonds.js";
import { depositFormHTML, closedDepositsHTML } from "./deposits.js";
import { reserveFormHTML, reserveFields, reserveBody } from "./money-cards.js";
import { goalCreateFormHTML, goalFields, goalBody } from "./goals.js";
import {
  positionsTableHTML, loadPositionsData, wirePositionRows,
} from "./positions.js";
import {
  yieldTilesHTML, yieldMixCard, shareTilesHTML, brokerDonutHTML, currencyChartHTML,
  rebalanceCard, kindMixCard, concentrationCard, ladderTableHTML,
  liquidityCard, rateRiskCard, decisionsCard, marketCurveCard,
} from "./risk.js";
import { rivalsCard, wireRivals, rivalsPath } from "./rivals.js";
import { chartBlockHTML, snapshotsTableHTML, wireHistory } from "./history.js";

// Підсумок місяця живе окремим модулем і реекспортується сюди: сторінка
// належить «Портфелю», а її вміст не має спільного з рештою цього файла
// нічого, крім розділу.
export { period } from "./period.js";
export { year } from "./year.js";

/** Усе разом: та сама таблиця, що й була, з плитками дохідностей над нею.
 *  Єдина панель, де види стоять поруч і порівнюються.
 *
 *  ЖУРНАЛ ЗАКРИТИХ ВКЛАДІВ СТОЇТЬ САМЕ ТУТ, і це переїзд, а не новина.
 *  Доти він жив на сторінці виду «Вклади», під таблицею живих. Сторінки
 *  виду більше немає, а рядка в майстер-списку закритий вклад не має за
 *  визначенням — він уже не позиція. Лишити його там, де він був,
 *  означало б викинути його зовсім; покласти в рядок якогось живого
 *  вкладу — приписати одному вкладу історію іншого.
 *
 *  «Позиції» — те місце, куди приходять із питанням «а де той вклад,
 *  що я розірвав»: тут стоїть усе, що в портфелі є, і згорнутою секцією
 *  внизу — те, чого вже немає. */
export async function positions(ctx, main) {
  const d = ctx.positions || await loadPositionsData(ctx);
  main.innerHTML = `
    ${yieldTilesHTML(ctx)}
    ${yieldMixCard(ctx)}
    ${positionsTableHTML(ctx, d.positions, d.lots, d.sales, d.deposits)}
    ${closedDepositsHTML(ctx, d.deposits || [])}`;
  wirePositionRows(ctx, main, d);
  wireDisclosures(main);
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

/** З чим порівняти: механічні альтернативи, власні поради й ринок.
 *
 *  Три різні «а якби інакше», і саме тому вони стоять разом. «Ціна моїх
 *  рішень» порівнює з тим, хто не вирішував нічого; журнал рішень — із
 *  тим, що радив сам застосунок; крива аукціонів — із ринком сьогодні.
 *
 *  Усі три тягнуть власні дані, і всі — м'яко: маршрут може бути новішим
 *  за бекенд, а сторінка з двома картками краща за порожню. */
export async function compare(ctx, main) {
  const [riv, curve, dec] = await Promise.all([
    ctx.soft(rivalsPath(), null),
    ctx.soft("auctions/curve", []),
    ctx.soft("decisions", null),
  ]);
  main.innerHTML = `
    ${rivalsCard(ctx, riv)}
    ${decisionsCard(ctx, dec)}
    ${marketCurveCard(ctx, curve)}`;
  wireRivals(ctx, main);
  wireDisclosures(main);
}

/** «Записати нове» — вхід для позиції, якої в списку ще НЕМАЄ.
 *
 *  Без цієї панелі майстер-список замикав би застосунок на вже купленому:
 *  щоб записати перший папір нового виду, довелось би спершу мати рядок
 *  цього виду. Класична курка з яйцем, і саме вона й з'явилась би, якби
 *  всі форми жили тільки в позиціях.
 *
 *  ЖОДНОЇ НОВОЇ ФОРМИ ТУТ НЕ ЗАВОДИТЬСЯ. Це ті самі bondBuyFormHTML,
 *  depositFormHTML і reserveFormHTML, які записують ті самі сутності з
 *  панелей позицій; різниця лише в тому, що сюди приходять, коли позиції
 *  ще немає. Друга форма на ту саму сутність розійшлася б із першою при
 *  найпершій правці полів.
 *
 *  Фондів і НПФ тут немає, і кожен — зі своєї причини. Сертифікати не
 *  вносять руками взагалі: журнал веде виписка, і два джерела правди
 *  розійшлися б тихо. Пенсійний внесок мусить цілитись у конкретний
 *  рахунок, тобто його форма належить рядку цього рахунку, а не
 *  зведенню. */
export async function record(ctx, main) {
  const d = ctx.positions || await loadPositionsData(ctx);
  const hasNPF = ((ctx.summary || {}).npf || []).length > 0;
  main.innerHTML = `
    <div class="card"><h2>Купівля ОВДП</h2>${bondBuyFormHTML(ctx)}</div>
    <div class="card"><h2>Відкрити вклад</h2>${depositFormHTML(ctx)}
      <div class="sub">Поповнення й дострокове закриття — у рядку самого вкладу.</div></div>
    <div class="card"><h2>Рух резерву</h2>${reserveFormHTML(ctx)}</div>
    ${goalCreateFormHTML(ctx)}
    <div class="card">${empty("Фонд і НПФ записуються не тут",
    "Сертифікати заводить виписка — руками їх не вносять, бо два джерела правди "
    + "розійшлися б тихо. Пенсійний внесок мусить цілитись у конкретний рахунок, "
    + "тобто його форма належить рядку цього рахунку.",
    { href: routeFor("money/import"), label: "Завантажити виписку" })}
      <div class="sub">${hasNPF
    ? `Внесок у НПФ — <a class="lnk" href="${routeFor("entry/npf")}">у рядку рахунку</a>.`
    : `Пенсійного рахунку ще немає — <a class="lnk" href="${routeFor("settings/refs")}">заведи його в довідниках</a>.`}</div></div>`;

  // Ті самі проводки, що й у панелях позицій: форми ті самі, тож і
  // слухачі ті самі. Усі вони терплять відсутність своєї цілі, тож
  // зайвий виклик нічого не коштує.
  wirePositionRows(ctx, main, d);
  wireCrud(ctx, main, {
    resource: "reserve", form: "#resForm", title: "Рух резерву",
    fields: reserveFields, body: reserveBody,
    msg: { add: "Рух резерву записано", edit: "Рух резерву виправлено", del: "Рух видалено" },
  });
  // Ціль ЗАВОДИТЬСЯ порожньою — на відміну від решти форм цієї сторінки,
  // які записують операцію. Це не непослідовність: ціль і є наміром, а не
  // фактом, і поставити її наперед — головне, чого від неї хочуть. Рухи під
  // нею записують уже в її власному рядку.
  wireCrud(ctx, main, {
    resource: "goals", form: "#goalForm2", title: "Ціль",
    fields: goalFields, body: goalBody,
    msg: { add: "Ціль заведено", edit: "Ціль виправлено", del: "Ціль видалено" },
  });
  wireRefs(main);
  wireDisclosures(main);
}
