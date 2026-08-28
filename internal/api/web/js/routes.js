// Маршрути застосунку: розбір хеша й адреси форм.
//
// Окремим модулем, а не всередині app.js, з однієї причини: адреси
// потрібні В'ЮШКАМ — саме вони будують посилання «Купівля»,
// «Поповнення», «Відкрити налаштування». Якби routeFor жив у app.js,
// в'юшка мусила б імпортувати app.js, який імпортує саму в'юшку, і граф
// імпортів замкнувся б у кільце. Модулі таке переживають, але кільце в
// графі — це те, що читач помічає останнім і плутається першим.
//
// Саме хеш, а не шлях: статику віддає http.FileServerFS
// (internal/api/server.go), і будь-який шлях, крім "/", повернув би 404
// — SPA-fallback довелось би дописувати в Go. Хеш цього не вимагає й дає
// рівно те, чого бракувало: «назад» у браузері, закладку на позицію і
// посилання просто на форму.
//
// ГРАМАТИКА: #/<вкладка>/<рядок>/<панель> і, необов'язково, /<якір>.
//
// Навігація трисегментна. Четвертий сегмент — НЕ ярус дерева, а якір
// форми, тобто рівно те, чим доти був третій: він нікуди не веде, він
// доводить до потрібної форми на вже відкритій панелі. Пояснення, чому
// таких якорів лишилось три, — при ANCHORS нижче.

import { PATHS, FIRST, HOME, panesFor } from "./nav.js";

// ---------------------------------------------------------------------
// Старі адреси
// ---------------------------------------------------------------------
//
// Закладки, зроблені до майстер-деталі. Не 404 і не тихий відкат на
// головну: адреса вела в конкретне місце, і воно нікуди не поділось —
// лише переїхало.
//
// ЗНАЧЕННЯ МУСИТЬ БУТИ ГОТОВОЮ НОВОЮ АДРЕСОЮ. resolveLegacy ходить у цю
// таблицю ОДИН раз: якщо покласти сюди адресу, яку саму треба ще
// переводити, вона поїде в застосунок як є й упаде аж при рендері —
// найтихішим можливим чином.

// «@first:<вид>» — не літерал, а вказівка «перший рядок цього виду».
//
// Стара адреса instr/bonds називала ВИД, а новий рядок майстер-списку —
// конкретний папір. Жоден ISIN сюди зашити не можна: він залежить від
// даних, яких на момент розбору хеша ще немає. Тому таблиця віддає
// маркер, а розкриває його оболонка — там, де список рядків уже відомий
// (app.js, _resolveItem). Порожній вид розкривається в «Портфель
// цілком»: сторінка виду без жодної позиції й доти показувала порожній
// стан, і зведення каже те саме чесніше.
export const FIRST_OF = "@first:";

/** Вид, перший рядок якого треба знайти, або "" якщо це не маркер. */
export const markerKind = (item) =>
  (item || "").startsWith(FIRST_OF) ? item.slice(FIRST_OF.length) : "";

// Кроки воронки → панелі інспектора. Імена змінились рівно тому, що
// змінився ярус: крок належав виду, панель належить сутності.
const STEP_PANE = {
  state: "state",
  mine: "have",
  next: "next",
  act: "do",
  write: "record",
  terms: "terms",
};

// Старий підрозділ «Інструментів» → новий рядок «Портфеля».
const OLD_KIND = {
  bonds: `${FIRST_OF}bond`,
  funds: `${FIRST_OF}fund`,
  npf: `${FIRST_OF}npf`,
  deposits: `${FIRST_OF}deposit`,
  reserve: "reserve",
};

const LEGACY = new Map([
  // --- вкладка «Робота» ---
  ["now/todo", "work/todo/main"],
  ["now/buy", "work/buy/main"],
  ["now/buys", "work/buys/main"],
  // Кошик покупки став планом купівель: рядки переїхали в базу й дістали
  // дату. Адреса вела в конкретне місце, і воно нікуди не поділось.
  ["now/basket", "work/buys/main"],

  // --- «Портфель цілком» ---
  ["portfolio/positions", "portfolio/all/positions"],
  ["portfolio/growth", "portfolio/all/growth"],
  ["portfolio/period", "portfolio/all/period"],
  ["portfolio/structure", "portfolio/all/structure"],
  ["portfolio/limits", "portfolio/all/limits"],
  ["portfolio/compare", "portfolio/all/compare"],

  // --- «Гроші» ---
  ["money/balances", "money/all/balances"],
  ["money/flows", "money/all/flows"],
  ["money/tax", "money/all/tax"],
  ["money/import", "money/all/import"],
  ["money/reconcile", "money/all/reconcile"],

  // --- «План», «Політика», «Налаштування»: рядок сам собі сторінка ---
  ["plan/inflow", "plan/inflow/main"],
  ["plan/route", "plan/route/main"],
  ["plan/goal", "plan/goal/main"],
  ["plan/levers", "plan/levers/main"],
  ["plan/payouts", "plan/payouts/main"],
  ["policy/strategy", "policy/strategy/main"],
  ["policy/mix", "policy/mix/main"],
  ["policy/instruments", "policy/instruments/main"],
  ["policy/reserve", "policy/reserve/main"],
  ["policy/assumptions", "policy/assumptions/main"],
  ["settings/refs", "settings/refs/main"],
  ["settings/backup", "settings/backup/main"],

  // --- закладки ще з дерева «Активи / Ризик / Записати» ---
  //
  // «overview» тут БІЛЬШЕ НЕМАЄ, і це не пропуск: так тепер зветься жива
  // вкладка, і розкриває її правило FIRST. Запис у цій таблиці переміг
  // би назву вкладки — рівно та пастка, що описана нижче про «portfolio».
  //
  // Старий #/overview і БУВ дашбордом, а новий «Огляд» — той самий
  // дашборд, відроджений; тобто адреса повернулась туди, куди вела
  // завжди.

  ["assets", "portfolio/all/positions"],
  ["assets/positions", "portfolio/all/positions"],
  ["assets/growth", "portfolio/all/growth"],
  // Ці п'ять називали ВИД, як і instr/*, тож і ведуть так само — у
  // перший рядок цього виду. Без них вони падали на голе «assets» і
  // відкривали зведення: адреса лишалась робочою, але вела не туди, куди
  // написано, — а це найгірший сорт поламаного посилання.
  ["assets/bonds", `portfolio/${FIRST_OF}bond/state`],
  ["assets/funds", `portfolio/${FIRST_OF}fund/state`],
  ["assets/npf", `portfolio/${FIRST_OF}npf/state`],
  ["assets/deposits", `portfolio/${FIRST_OF}deposit/state`],
  ["assets/reserve", "portfolio/reserve/state"],
  ["risk", "portfolio/all/structure"],
  ["risk/structure", "portfolio/all/structure"],
  ["risk/limits", "portfolio/all/limits"],
  ["risk/compare", "portfolio/all/compare"],

  // «Записати» вело у форму для того, чого ще НЕМАЄ, — і саме на це
  // відповідає панель «Записати нове» зведеного рядка. Вести ці адреси в
  // @first:bond/record означало б відкрити форму «купити ще ЦЕЙ папір»
  // тому, хто прийшов купувати інший.
  ["entry", "portfolio/all/record"],
  ["entry/bond", "portfolio/all/record"],
  ["entry/deposit", "portfolio/all/record"],
  ["portfolio/buy", "portfolio/all/record"],
  ["portfolio/topup", "portfolio/all/record"],
  // Два винятки, і вони не довільні: форма внеску в НПФ мусить цілитись у
  // конкретний рахунок (npfDetailHTML), а журнал резерву в зведеному
  // рядку не живе.
  ["entry/npf", `portfolio/${FIRST_OF}npf/record`],
  ["entry/reserve", "portfolio/reserve/record"],

  ["entry/cash", "money/all/balances/cash"],
  ["entry/convert", "money/all/balances/convert"],
  ["entry/import", "money/all/import"],
  ["entry/reconcile", "money/all/reconcile"],
  // deposit тут — поповнення грошового РАХУНКУ, не вкладу: два різні
  // «поповнення» жили поруч у старій таблиці якорів і плутались.
  ["money/deposit", "money/all/balances/cash"],
  ["money/convert", "money/all/balances/convert"],

  ["plan/planflow", "plan/inflow/main/planflow"],
  // «Майбутнє» злилося з «Планом» ще торік, і закладка на нього досі
  // може лежати в чиємусь браузері.
  ["future", "plan/goal/main"],
]);

// Воронки: п'ять видів × шість кроків, у резерву п'ять. Циклом, а не
// руками, — двадцять дев'ять рядків копіпасти розійшлися б із STEP_PANE
// при першому ж перейменуванні панелі.
for (const [old, item] of Object.entries(OLD_KIND)) {
  LEGACY.set(`instr/${old}`, `portfolio/${item}/state`);
  for (const [step, pane] of Object.entries(STEP_PANE)) {
    // У резерву панелі «Що зробити» немає за природою (довід — у nav.js),
    // тож старий крок «act» веде на найближчу за змістом: що варто
    // відкласти далі.
    const to = item === "reserve" && pane === "do" ? "next" : pane;
    LEGACY.set(`instr/${old}/${step}`, `portfolio/${item}/${to}`);
  }
}

// ПАСТКА, ЯКУ ВАРТО ЗНАТИ. Голих «portfolio», «money», «plan», «policy»,
// «settings» тут БІЛЬШЕ НЕМАЄ, і це не пропуск: resolveLegacy пробує цю
// таблицю РАНІШЕ за правило FIRST, тож запис, який тут стояв би, переміг
// би назву живої вкладки — і будь-яка помилка в ньому вела б на сторінку,
// якої не існує, найтихішим чином.
//
// Голі «assets», «risk», «entry», «instr», «future», «overview»,
// навпаки, потрібні ЯВНО: цих розділів більше немає, тож FIRST їх не
// знає й сам не розкриє.
LEGACY.set("instr", "portfolio/all/positions");

// Куди веде адреса, якої немає в дереві. Порядок кроків має значення:
// «money/deposit» мусить піти в таблицю переїздів РАНІШЕ, ніж спрацює
// правило голої вкладки, — інакше «money» знайшлось би саме собою й
// відкрило б баланси замість форми.
//
// Зациклитись це не може навіть із помилкою в таблиці: значення, якого
// немає в дереві, наступним проходом не знайдеться ні тут, ні в LEGACY,
// впаде в правило голої вкладки або в HOME — і на цьому спиниться.
function resolveLegacy(parts) {
  const [a = "", b = "", c = ""] = parts;
  const hit = LEGACY.get(`${a}/${b}/${c}`)
    || LEGACY.get(`${a}/${b}`)
    || LEGACY.get(a);
  if (hit) return hit;
  if (FIRST.has(a)) return `${a}/${FIRST.get(a)}`;
  return HOME;
}

/** Чи веде трійка кудись справжнього.
 *
 *  Дві перевірки, а не одна, і різниця між ними та сама, що описана в
 *  nav.js при kindOf: PATHS знає СТАТИЧНІ трійки, а panesFor уміє
 *  сказати «така панель законна для паперу» ще до того, як приїдуть самі
 *  папери. Без другої половини кожне відкриття закладки на позицію
 *  викидало б у HOME, бо на момент розбору хеша даних ще немає. */
function known(tab, item, pane) {
  if (PATHS.has(`${tab}/${item}/${pane}`)) return true;
  const panes = panesFor(tab, item);
  return !!panes && panes.some((p) => p.key === pane);
}

/** Розібрати хеш. redirect — канонічний шлях, на який треба замінити
 *  адресу; порожній рядок означає, що адреса вже канонічна.
 *
 *  item може приїхати маркером «@first:<вид>» — його розкриває оболонка,
 *  коли рядки вже завантажені (див. FIRST_OF вище).
 *
 *  Заміняти адресу мусить той, хто маршрутизує, і саме через
 *  location.replace(): присвоєння лишило б стару адресу в історії, «назад»
 *  повернуло б на неї, вона знову перенаправила б уперед — і кнопка
 *  «назад» перестала б працювати взагалі. */
export function parseRoute(hash) {
  // decodeURIComponent — бо id рядка буває власною назвою: фонд
  // «Inzhur OFFICE» і брокер із пробілом їдуть в адресу закодованими.
  const parts = String(hash || "").replace(/^#\/?/, "").split("/")
    .filter(Boolean).map(decode);
  const [tab, item, pane, anchor] = parts;
  if (tab && item && pane && known(tab, item, pane)) {
    return { tab, item, pane, anchor: anchor || "", redirect: "" };
  }
  const to = resolveLegacy(parts);
  const seg = to.split("/");
  return {
    tab: seg[0], item: seg[1], pane: seg[2], anchor: seg[3] || "", redirect: to,
  };
}

// Зіпсоване відсоткове кодування не мусить валити маршрутизацію: у
// найгіршому випадку сегмент лишається як є й не знаходиться в дереві,
// тобто йде звичайним шляхом невідомої адреси.
function decode(s) {
  try { return decodeURIComponent(s); } catch (_) { return s; }
}

/** Сегмент адреси з довільного id. Виносити не було куди: id рядка
 *  будує оболонка, а розбирає parseRoute, і кодування мусить бути одним
 *  на обидва боки.
 *
 *  Двокрапка вертається як є. encodeURIComponent екранує її разом з
 *  усім іншим, і «bond:UA4000231625» перетворювалось на
 *  «bond%3AUA4000231625» — адреса робоча, але нечитабельна рівно там, де
 *  її читають найчастіше: у рядку браузера й у надісланому посиланні.
 *  У фрагменті URI двокрапка законна й нічого не розділяє; розділювачем
 *  тут є «/», і саме його екранування (разом із пробілами в назві фонду)
 *  і є те, заради чого функція існує. */
export const seg = (s) => encodeURIComponent(String(s)).replace(/%3A/g, ":");

// ---------------------------------------------------------------------
// Якорі форм
// ---------------------------------------------------------------------
//
// Якір — ЧЕТВЕРТИЙ сегмент, і потрібен він рівно там, де форм на панелі
// БІЛЬШЕ ОДНОЇ. Це те саме правило, що й доти; змінився лише номер
// сегмента, бо третій зайняла панель.
//
// Записів було дев'ять, лишилось три — і шість зниклих не прибрані, а
// ПІДВИЩЕНІ: state/mine/next/act/write/terms стали справжніми панелями з
// власною адресою, тобто те, заради чого їх колись зробили якорями, тепер
// дає сама навігація.
//
// Порожніх записів «про запас» тут немає навмисно: мертвий якір читається
// як зразок і тиражується.
export const ANCHORS = {
  // «Що заходить» несе форму потоку, форму часток і форму замка — і
  // посилання «додай перше джерело доходу» мусить сказати, яку саме.
  planflow: "#planFlowForm",
  // «Баланси й валюта» несуть дві форми — рівно той випадок, заради якого
  // якорі й є.
  cash: "#cashForm",
  convert: "#convForm",
};

// Куди веде кожне іменоване посилання на форму. Імена лишились старі —
// їх пишуть в'юшки (routeFor("buy")), і перейменування зачепило б п'ять
// місць заради нуля користі.
const FORM_ROUTE = {
  buy: "portfolio/all/record",
  topup: "portfolio/all/record",
  deposit: "money/all/balances/cash",
  convert: "money/all/balances/convert",
  planflow: "plan/inflow/main/planflow",
};

/** Адреса, названа ТОЧНО, або порожній рядок, якщо її треба вгадувати.
 *
 *  Якір мусить бути ОКРЕМИМ випадком: дерево знає панелі, тож
 *  «вкладка/рядок/панель/якір» у ньому не знайшовся б. Невідомий якір —
 *  це помилка в коді, і хай вона поводиться так само, як невідома
 *  адреса. */
function exact(what) {
  if (FORM_ROUTE[what]) return FORM_ROUTE[what];
  const s = String(what).split("/");
  if (s.length >= 3 && known(s[0], s[1], s[2])
    && (s.length === 3 || (s.length === 4 && ANCHORS[s[3]]))) {
    return String(what);
  }
  return "";
}

/** Чи ВПІЗНАНО цю адресу — тобто чи вона десь названа, а не вгадана
 *  правилом голої вкладки.
 *
 *  Питання не те саме, що «чи веде кудись живого», і різниця в ньому
 *  дорога. `risk/limitz` з описки нікому не відома, але resolveLegacy
 *  бачить у ній голе «risk» і чесно відкриває структуру портфеля:
 *  посилання зламане, сторінка правдоподібна, скарги немає. Правило
 *  голої вкладки для того й існує — «набрав половину адреси» не помилка, —
 *  але воно ж і ховає описку, бо не відрізняє половину від хибної
 *  цілої.
 *
 *  Споживач один — web-routes-check.mjs, і це не абстракція з одним
 *  користувачем: тут не заведено нового шва, а названо те, що routeFor
 *  уже знає й досі мовчки викидало. Перевірка, яка не має цього питати,
 *  ловить лише мертві адреси й пропускає правдоподібні. */
export const routeKnown = (what) =>
  !!exact(what) || LEGACY.has(String(what)) || FIRST.has(String(what));

/** Адреса, за якою відкривається потрібна форма, панель або вкладка.
 *
 *  Через ту саму resolveLegacy, що й parseRoute, і це ВИПРАВЛЕННЯ, а не
 *  охайність. Доти routeFor у таблицю переїздів не заглядав — і три дії
 *  черги задач, які й далі називають адреси старого дерева
 *  (views/tasks.js: risk/limits, assets/deposits, assets/funds), тихо
 *  вели на головну. Кнопка казала «Подивитись ліміти» й відкривала чергу
 *  задач; помітити це можна було, лише знаючи наперед, куди мало вести. */
export function routeFor(what) {
  return `#/${exact(what) || resolveLegacy(String(what).split("/"))}`;
}
