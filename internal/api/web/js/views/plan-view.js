// Розділ «План» — джерела доходу й витрат, з датами. Чотири сторінки.
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
// ВЕРДИКТ СТОЇТЬ НА ВСІХ ЧОТИРЬОХ, і це єдине навмисне дублювання в
// розділі. Він не картка серед карток, а рамка: три числа й одне речення
// про те, чи план узагалі виводить на ціль. Сторінка «Важелі» без нього
// відповідала б на питання «що зрушить», не сказавши, чи є що зрушувати.
//
// Кожна сторінка тягне рівно своє. Стрічку (plan) читають усі чотири —
// з неї вердикт бере підрядок про поточний місяць, — але це один запит на
// весь обхід розділу: GET-и йдуть через кеш store.js і скидаються лише
// записом.

import { infoBtn } from "../info.js";
import { wireDisclosures } from "../disclosure.js";
import {
  income12mChartHTML, capitalChartHTML, projectionHTML, incomeHTML, drawdownHTML,
  renderCalendar, calendarPlaceholderHTML,
} from "./future.js";
import { goalsHTML, sensitivityHTML } from "./forecast.js";
import { planVerdictHTML, profileHTML, planVsFactHTML } from "./plan-cards.js";
import {
  planFlowsListHTML, planFlowFormHTML, revisionsHTML, wirePlanFlows,
} from "./plan-flows.js";
import { receiptsHTML, wirePlanReceipts } from "./plan-receipts.js";
import { renderRoute } from "./route.js";
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
    ${planVerdictHTML(ctx, timeline)}
    ${timeline ? receiptsHTML(timeline) : ""}
    ${timeline ? profileHTML(timeline) : ""}
    ${timeline ? planVsFactHTML(timeline, ctx.summary) : ""}
    <div class="card">
      <h2 class="card-head"><span>Джерела доходу й витрат</span></h2>
      <div class="note">Кожен потік — сума з датою, періодичністю й тим, яка його частка
        доходить до портфеля. Колонка «дає ₴/міс» показує внесок саме цього рядка в число
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
 *  Стоїть між «Що заходить» і «Ціль і прогноз» навмисно: перше каже, які
 *  гроші будуть, останнє — куди вони виводять разом, а маршрут відповідає
 *  на те саме питання для кожного надходження окремо й із датою.
 *
 *  Малює себе сам (renderRoute), бо доїжджає окремим запитом помітно
 *  пізніше за вердикт: /api/route коштує стільки ж, скільки «Що купити» з
 *  датами доступності. */
export async function route(ctx, main) {
  const timeline = await ctx.soft("plan", null);
  main.innerHTML = planVerdictHTML(ctx, timeline);
  await renderRoute(ctx, main);
}

/** Що зрушить ціль: чутливість до припущень. */
export async function levers(ctx, main) {
  const timeline = await ctx.soft("plan", null);
  main.innerHTML = `
    ${planVerdictHTML(ctx, timeline)}
    ${sensitivityHTML(ctx)}`;
  wireDisclosures(main);
}

/** Календар виплат за датами.
 *
 *  Заглушка лишається, хоч сторінка тепер і своя: календар доїжджає
 *  окремим запитом помітно пізніше за вердикт, і без неї сторінка
 *  підстрибувала б на цілу картку рівно тоді, коли її вже читають.
 *  append:true не «дописує в кінець розділу», як було в спільній вкладці, а
 *  замінює саме заглушку — place() у future.js шукає її першою. */
export async function payouts(ctx, main) {
  const timeline = await ctx.soft("plan", null);
  main.innerHTML = `
    ${planVerdictHTML(ctx, timeline)}
    ${calendarPlaceholderHTML()}`;
  await renderCalendar(ctx, main, { append: true });
}
