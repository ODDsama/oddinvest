// Розділ «План» — джерела доходу й витрат, з датами. Шість сторінок: Борги,
// Мета капіталу й прогноз, Що заходить, Планові витрати, Маршрут, Календар.
//
// Двигун (state_projection.go) бачить ці потоки самостійно: вони живлять
// криву капіталу, віяло сценаріїв, чутливість і незалежність без жодної
// власної арифметики тут — той самий прийом, що й у кошика покупки.
// Розділ лише збирає їх і показує вердикт: скільки план дає і чи цього
// досить.
//
// Числа рядків («дає ₴/міс») теж приходять із бекенда, а не рахуються
// тут: періодичність, індексація, курс і частка в портфель означені раз, у
// state_plan.go. Порахувавши їх удруге в браузері, ми б гарантували собі
// розбіжність із плиткою вгорі при першій же правці двигуна.
//
// Порядок сторінок веде причиною до наслідку: що заходить → куди це йде →
// чим це зрушити → коли саме платять. Доти перші дві половини стояли в
// РІЗНИХ вкладках, і читач мав тримати в голові, що «до цілі бракує ще
// 41 769» тут і «внесок 41 769/міс» там — одне й те саме число. Потім вони
// стали чотирма згорнутими секціями однієї вкладки, тобто малювались усі
// заради тієї, яку розгорнули. Тепер це чотири адреси.
//
// ВЕРДИКТ — ЛИШЕ НА «МЕТІ КАПІТАЛУ Й ПРОГНОЗІ» (ревізія 2026-10-03).
// Доти він стояв рамкою на всіх сторінках розділу, і та сама картка з
// трьох чисел ішла першою на «Що заходить», «Маршруті» й «Календарі» —
// тобто кожна сторінка починалась не зі свого питання. Вердикт відповідає
// на «чи виводить план на мету», і це питання саме тієї сторінки.
// «Важелі», для яких рамка колись і заводилась, переїхали в «Політику».
//
// Кожна сторінка тягне рівно своє. Стрічку (plan) читають «Що заходить» і
// «Мета» — один запит на весь обхід розділу: GET-и йдуть через кеш
// store.js і скидаються лише записом.

import { infoBtn } from "../info.js";
import { wireDisclosures } from "../disclosure.js";
import { sym } from "../currency.js";
import {
  income12mChartHTML, capitalChartHTML, projectionHTML, incomeHTML, drawdownHTML,
  renderCalendar, calendarPlaceholderHTML,
} from "./future.js";
import { goalsHTML } from "./forecast.js";
import { planVerdictHTML, profileHTML, planVsFactHTML } from "./plan-cards.js";
import {
  planFlowsListHTML, planFlowFormHTML, revisionsHTML, wirePlanFlows,
} from "./plan-flows.js";
import { receiptsHTML, wirePlanReceipts } from "./plan-receipts.js";
import { renderRoute } from "./route.js";
export { debts } from "./debts.js";
// Планові витрати малюють себе самі: сторінка з єдиної картки, якій не
// потрібен ні вердикт плану, ні стрічка. Реекспорт, а не обгортка, —
// обгортка була б функцією, що лише передає два аргументи далі.
export { planExpenses } from "./plan-expenses.js";
import {
  planActionsListHTML, planSetSharesFormHTML, planLockFormHTML, wirePlanActions,
} from "./plan-actions.js";

/** Що заходить: обіцянки (потоки), факт проти них (надходження) і точкові
 *  рішення на дату (дії).
 *
 *  Профіль надходжень і «план проти факту» стоять саме тут, а не на «Цілі»,
 *  хоч обидва й малюють графіки: вони відповідають на «чи прийшло те, що
 *  планувалось», тобто на питання ЦІЄЇ сторінки, а не на «куди це веде». */
export async function inflow(ctx, main) {
  const [flows, actions, timeline] = await Promise.all([
    ctx.soft("plan/flows", []),
    ctx.soft("plan/actions", []),
    ctx.soft("plan", null),
  ]);

  main.innerHTML = `
    ${timeline ? receiptsHTML(timeline) : ""}
    ${timeline ? profileHTML(timeline) : ""}
    ${timeline ? planVsFactHTML(timeline, ctx.summary) : ""}
    <div class="card">
      <h2 class="card-head"><span>Джерела доходу й витрат</span></h2>
      <div class="note">Кожен потік — сума з датою, періодичністю й тим, яка його частка
        доходить до портфеля. Колонка «дає ${sym()}/міс» показує внесок саме цього рядка в число
        вгорі; підсумок під таблицею розкладає його на складники.</div>
      ${planFlowsListHTML(flows, (ctx.summary || {}).plan_provides_uah || 0)}
      ${revisionsHTML((timeline || {}).flow_revisions || [])}
      ${planFlowFormHTML(ctx)}
    </div>
    <div class="card">
      <h2 class="card-head"><span>Дії ${infoBtn("planActions")}</span></h2>
      <div class="note">Точкові рішення на дату: перевести майбутні внески в іншу валюту
        або замкнути суму під ставку на строк — вклад і накопичувальний фонд для проєкції
        не відрізняються, обидва просто лежать і платять за графіком.</div>
      ${planActionsListHTML(actions)}
      ${planSetSharesFormHTML(ctx)}
      ${planLockFormHTML()}
    </div>`;

  wirePlanFlows(ctx, main, flows);
  wirePlanActions(ctx, main, actions, timeline);
  if (timeline) wirePlanReceipts(ctx, main, timeline);
  // Без цього «Ще» всередині форм згортались би після кожного збереження:
  // ctx.reload() переписує сторінку, а пам'ять розкриття живе саме тут.
  wireDisclosures(main);
}

/** Куди це йде: ціль, дохід із наявного, проєкції на роки вперед. */
export async function goal(ctx, main) {
  const timeline = await ctx.soft("plan", null);
  main.innerHTML = `
    ${planVerdictHTML(ctx, timeline)}
    ${goalsHTML(ctx)}
    ${incomeHTML(ctx)}
    <div class="chart-grid">
      ${income12mChartHTML(ctx)}
      ${capitalChartHTML(ctx)}
    </div>
    ${projectionHTML(ctx)}
    ${drawdownHTML(ctx)}`;
  wireDisclosures(main);
}

/** Маршрут грошей: куди піде кожне майбутнє надходження.
 *
 *  Стоїть між «Що заходить» і «Мета капіталу й прогноз» навмисно: перше каже, які
 *  гроші будуть, останнє — куди вони виводять разом, а маршрут відповідає
 *  на те саме питання для кожного надходження окремо й із датою.
 *
 *  Малює себе сам (renderRoute), бо доїжджає окремим запитом: /api/route
 *  коштує стільки ж, скільки «Що купити». */
export async function route(ctx, main) {
  main.innerHTML = "";
  await renderRoute(ctx, main);
}

// «Важелі» (чутливість) переїхали в «Політика → Припущення й важелі»:
// вони крутять припущення, і читати їх треба поруч із ними.

/** Календар виплат за датами.
 *
 *  Заглушка лишається: календар доїжджає окремим запитом, і без неї
 *  сторінка блимала б порожнечею, доки він не прийде.
 *  append:true не «дописує в кінець розділу», як було в спільній вкладці, а
 *  замінює саме заглушку — place() у future.js шукає її першою. */
export async function payouts(ctx, main) {
  main.innerHTML = calendarPlaceholderHTML();
  await renderCalendar(ctx, main, { append: true });
}
