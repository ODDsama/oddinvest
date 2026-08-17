// Розділ «План» — джерела доходу й витрат, з датами.
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
// Сам файл — тонкий композитор: тисяча п'ятсот рядків роз'їхалась по
// чотирьох модулях поруч, і межі між ними проведені не за розміром, а за
// сутністю — картки без форм, потоки, надходження, дії. Наступний крок
// розділить і цю функцію: кожна з чотирьох секцій нижче стане власною
// сторінкою, і тоді композитору не доведеться вантажити стрічку, потоки й
// дії разом заради того, на що дивляться поодинці.

import { infoBtn } from "../info.js";
import { section, wireDisclosures } from "../disclosure.js";
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
import {
  planActionsListHTML, planSetSharesFormHTML, planLockFormHTML, wirePlanActions,
} from "./plan-actions.js";

// Розділ веде читача причиною до наслідку: що заходить → куди це йде →
// чим це зрушити → коли саме платять. Доти перші дві половини стояли в
// РІЗНИХ вкладках, і читач мав тримати в голові, що «до цілі бракує ще
// 41 769» тут і «внесок 41 769/міс» там — одне й те саме число.
//
// Одинадцять карток пласким списком неможливі, тож групи — через
// section(); жодна з двох вкладок доти його не використовувала.
export async function renderPlan(ctx, main) {
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
    ${section("inflow", "Що заходить", `
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
      </div>`, { open: true, hint: "потоки й дії" })}
    ${section("outcome", "Куди це йде", `
      ${goalsHTML(ctx)}
      ${incomeHTML(ctx)}
      <div class="chart-grid">
        ${income12mChartHTML(ctx)}
        ${capitalChartHTML(ctx)}
      </div>
      ${projectionHTML(ctx)}
      ${drawdownHTML(ctx)}`, { hint: "ціль, дохід, проєкції" })}
    ${section("levers", "Що зрушить ціль", sensitivityHTML(ctx), { hint: "чутливість до припущень" })}
    ${section("payouts", "Виплати", calendarPlaceholderHTML(), { hint: "календар за датами" })}`;

  wirePlanFlows(ctx, main, flows);
  wirePlanActions(ctx, main, actions, timeline);
  if (timeline) wirePlanReceipts(ctx, main, timeline);
  // Без цього «Ще» й самі секції згортались би після кожного збереження:
  // ctx.reload() переписує main, а пам'ять розкриття живе саме тут.
  // «План» був єдиним розділом, який цього не робив.
  wireDisclosures(main);
  // Календар — окремим запитом, тож після решти розмітки; власний
  // try/catch усередині лишає прогнози на місці, якщо він упаде.
  await renderCalendar(ctx, main, { append: true });
}
