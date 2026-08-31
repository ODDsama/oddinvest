// Чи веде КОЖНЕ посилання туди, куди написано. Тільки для CI й `make ui`.
//
// Це та сама перевірка мертвих посилань, яку js/nav.js обіцяв при PATHS:
// «літерал href="#/…", якого немає в дереві, — це поламане посилання, і
// знайти його має скрипт, а не читач».
//
// ЧОМУ ЦЬОГО НЕ ЛОВИТЬ НІЩО ІНШЕ. Адреса в цьому застосунку — звичайний
// рядок. Описка в ній синтаксично бездоганна, резолвиться, проходить
// eslint і не чіпає жодного токена; а routeFor на невідомий шлях віддає
// не помилку, а головну сторінку. Тобто кнопка каже «Подивитись ліміти»,
// відкриває чергу задач — і жодне місце про це не повідомляє. Саме так
// три дії черги прожили зламаними: routeFor не заглядав у таблицю
// переїздів, і risk/limits, assets/deposits та assets/funds тихо вели на
// головну.
//
// ПЕРЕВІРОК ДВІ, і вони про різне.
//
//   1. ЖИВІ ПОСИЛАННЯ — знімаються з коду й не можуть відстати. Кожен
//      літерал, який код передає в routeFor, кожен href="#/…" і кожне
//      поле `to` в таблицях адрес мусить вести кудись справжнього. Нове
//      посилання перевіряється саме тим, що воно з'явилось.
//
//   2. КОНТРАКТ ПЕРЕЇЗДІВ — таблиця нижче, і вивести її з коду не можна:
//      це обіцянка старим закладкам, тобто факт про минуле. Перша версія
//      цього скрипта питала лише «чи веде кудись живого» — і пропустила
//      п'ять адрес, бо assets/bonds чесно вело в зведення портфеля
//      замість паперу. Адреса робоча, посилання зламане.
//
// Файл лежить у корені репозиторію, а не поруч із модулями, з тієї ж
// причини, що й css-tokens-check.mjs: internal/api/web цілком вшивається
// в бінарник директивою `//go:embed web`, і скрипт звідти поїхав би
// користувачам у браузер.

import { readdirSync, statSync, readFileSync } from "node:fs";

import { parseRoute, routeFor, routeKnown, markerKind } from
  "./internal/api/web/js/routes.js";
import { PATHS, TABS, HOME, panesFor } from "./internal/api/web/js/nav.js";

const ROOT = "internal/api/web/js";

// Коментарі геть, але рядки на місці: звіт має вказувати на рядок у
// файлі, а не на рядок у якомусь очищеному тексті.
const blank = (m) => m.replace(/[^\n]/g, " ");
const stripComments = (src) => src
  .replace(/\/\*[\s\S]*?\*\//g, blank)
  .replace(/(^|[^:])(\/\/[^\n]*)/g, (_, p, c) => p + blank(c));

const walk = (d) => readdirSync(d).flatMap((f) => {
  const p = `${d}/${f}`;
  return statSync(p).isDirectory() ? walk(p) : [p];
});

// ---------------------------------------------------------------------
// 1. Живі посилання
// ---------------------------------------------------------------------

// Три форми, якими код називає адресу. Більше їх бути не повинно: коли
// з'явиться четверта, вона мусить потрапити сюди, інакше цілий шар
// посилань стане невидимим для перевірки.
//
// Підстановку (`${…}`) пропускаємо цілком: там адреса збирається в JS, і
// перевіряти нема чого — літерала немає. Такі місця беруть значення з
// таблиць, а таблиці перевіряються полем `to`.
const SHAPES = [
  [/routeFor\(\s*"([^"$]+)"\s*\)/g, "routeFor"],
  [/href="#\/([^"$]+)"/g, "href"],
  [/\bto:\s*"([^"$]+)"/g, "таблиця"],
];

const links = [];
for (const f of walk(ROOT).filter((x) => x.endsWith(".js"))) {
  const src = stripComments(readFileSync(f, "utf8"));
  src.split("\n").forEach((line, i) => {
    for (const [re, kind] of SHAPES) {
      for (const m of line.matchAll(re)) {
        links.push({ what: m[1], where: `${f}:${i + 1}`, kind });
      }
    }
  });
}

/** Куди насправді веде адреса й чи існує там панель. */
function land(addr) {
  const r = parseRoute(`#/${addr}`);
  // Маркер «@first:<вид>» розкриває оболонка, коли рядки вже
  // завантажені; для перевірки досить знати, що вид законний, а панель
  // існує в наборі панелей позиції.
  const item = markerKind(r.item) ? "reserve" : r.item;
  const panes = panesFor(r.tab, item);
  return {
    addr: [r.tab, r.item, r.pane, r.anchor].filter(Boolean).join("/"),
    live: !!panes && panes.some((p) => p.key === r.pane),
  };
}

const bad = [];
for (const l of links) {
  const to = routeFor(l.what).replace(/^#\//, "");
  // ДВА питання, і друге важливіше.
  //
  // «Чи веде кудись живого» ловить саму лише МЕРТВУ адресу. Описка
  // зазвичай не мертва: «risk/limitz» падає на голе «risk» і чесно
  // відкриває структуру портфеля — сторінка правдоподібна, скарги немає,
  // а кнопка при цьому каже «Подивитись ліміти».
  //
  // Тому головне питання — чи адресу ВПІЗНАЛИ: чи вона десь названа
  // (жива трійка, форма, запис переїзду, назва вкладки), чи її вгадало
  // правило голої вкладки. Правило потрібне живій людині, яка набрала
  // половину адреси руками; коду воно не потрібне ніколи.
  if (!routeKnown(l.what)) {
    bad.push(`${l.where}  ${l.kind}("${l.what}") → ${to}  — такої адреси ніде не названо`);
  } else if (!land(to).live) {
    bad.push(`${l.where}  ${l.kind}("${l.what}") → ${to}  — такої панелі немає`);
  }
}

// ---------------------------------------------------------------------
// 2. Контракт переїздів
// ---------------------------------------------------------------------
//
// Обіцянка закладкам, зробленим до майстер-деталі. Кожен рядок — стара
// адреса й місце, куди її зміст переїхав. Вивести це з коду не можна:
// таблиця LEGACY у routes.js і є та сама відповідь, тож звіряти її саму
// з собою було б безглуздо. Тут стоїть НАМІР, записаний окремо.

// «@» — перший рядок цього виду; який саме папір, залежить від даних.
const F = (kind) => `portfolio/@first:${kind}`;

const MOVED = {
  // старе дерево, 31 сторінка
  "now/todo": "work/todo/main",
  "now/buy": "work/buy/main",
  "now/buys": "work/buys/main",
  "instr/bonds": `${F("bond")}/state`,
  "instr/funds": `${F("fund")}/state`,
  "instr/npf": `${F("npf")}/state`,
  "instr/deposits": `${F("deposit")}/state`,
  "instr/reserve": "portfolio/reserve/state",
  "portfolio/positions": "portfolio/all/positions",
  "portfolio/growth": "portfolio/all/growth",
  "portfolio/period": "portfolio/all/period",
  "portfolio/structure": "portfolio/all/structure",
  "portfolio/limits": "portfolio/all/limits",
  "portfolio/compare": "portfolio/all/compare",
  "money/balances": "money/all/balances",
  "money/flows": "money/all/flows",
  "money/tax": "money/all/tax",
  "money/import": "money/all/import",
  "money/reconcile": "money/all/reconcile",
  "plan/inflow": "plan/inflow/main",
  "plan/route": "plan/route/main",
  "plan/goal": "plan/goal/main",
  "plan/levers": "plan/levers/main",
  "plan/payouts": "plan/payouts/main",
  "policy/strategy": "policy/strategy/main",
  "policy/mix": "policy/mix/main",
  "policy/instruments": "policy/instruments/main",
  "policy/reserve": "policy/reserve/main",
  "policy/assumptions": "policy/assumptions/main",
  "settings/refs": "settings/refs/main",
  "settings/backup": "settings/backup/main",

  // закладки, старіші за те дерево
  overview: HOME,
  assets: "portfolio/all/positions",
  "assets/positions": "portfolio/all/positions",
  "assets/growth": "portfolio/all/growth",
  "assets/bonds": `${F("bond")}/state`,
  "assets/funds": `${F("fund")}/state`,
  "assets/npf": `${F("npf")}/state`,
  "assets/deposits": `${F("deposit")}/state`,
  "assets/reserve": "portfolio/reserve/state",
  risk: "portfolio/all/structure",
  "risk/structure": "portfolio/all/structure",
  "risk/limits": "portfolio/all/limits",
  "risk/compare": "portfolio/all/compare",
  entry: "portfolio/all/record",
  "entry/bond": "portfolio/all/record",
  "entry/deposit": "portfolio/all/record",
  "entry/npf": `${F("npf")}/record`,
  "entry/reserve": "portfolio/reserve/record",
  "entry/cash": "money/all/balances/cash",
  "entry/convert": "money/all/balances/convert",
  "entry/import": "money/all/import",
  "entry/reconcile": "money/all/reconcile",
  "portfolio/buy": "portfolio/all/record",
  "portfolio/topup": "portfolio/all/record",
  "money/deposit": "money/all/balances/cash",
  "money/convert": "money/all/balances/convert",
  "plan/planflow": "plan/inflow/main/planflow",
  future: "plan/goal/main",
  "now/basket": "work/buys/main",
  instr: "portfolio/all/positions",

  // голі назви живих вкладок розкриває правило FIRST, а не таблиця
  work: "work/todo/main",
  portfolio: "portfolio/all/positions",
  money: "money/all/balances",
  // «План» голим хешем веде тепер у «Борги»: доки борг живий, він
  // з'їдає гроші місяця раніше за все інше, і план поверх грошей, яких
  // уже немає, читається неправильно. Стара закладка на саму ціль
  // (plan/goal) лишається чинною й нікуди не переїжджала.
  plan: "plan/debts/main",
  policy: "policy/strategy/main",
  settings: "settings/refs/main",

  // сміття мусить приводити на головну, а не в нікуди
  nope: HOME,
  "portfolio/nope/x": "portfolio/all/positions",
};

// Кроки воронок: п'ять видів × шість кроків. У резерву кроку «act» не
// існувало, а сама панель «Що зробити» в нього відсутня за природою —
// тож той крок ведеться на найближчу за змістом.
const STEP_PANE = {
  state: "state", mine: "have", next: "next",
  act: "do", write: "record", terms: "terms",
};
const OLD_KIND = {
  bonds: F("bond"), funds: F("fund"), npf: F("npf"),
  deposits: F("deposit"), reserve: "portfolio/reserve",
};
for (const [old, item] of Object.entries(OLD_KIND)) {
  for (const [step, pane] of Object.entries(STEP_PANE)) {
    if (old === "reserve" && step === "act") continue;
    const to = old === "reserve" && pane === "do" ? "next" : pane;
    MOVED[`instr/${old}/${step}`] = `${item}/${to}`;
  }
}

for (const [from, want] of Object.entries(MOVED)) {
  const got = land(from).addr;
  if (got !== want) bad.push(`переїзд  ${from} → ${got}  — мало бути ${want}`);
}

// ---------------------------------------------------------------------
// 3. Кодування id
// ---------------------------------------------------------------------
//
// Рядок майстер-списку зветься власною назвою: фонд «Inzhur OFFICE» і
// брокер із пробілом їдуть в адресу закодованими. Обіг мусить вернути те
// саме — інакше закладка на позицію відкриває чужу.
const ROUND = [
  ["money", "acct:Inzhur OFFICE", "flows"],
  ["portfolio", "fund:Inzhur OFFICE", "state"],
  ["portfolio", "bond:UA4000231625", "do"],
];
for (const [tab, item, pane] of ROUND) {
  const enc = encodeURIComponent(item).replace(/%3A/g, ":");
  const r = parseRoute(`#/${tab}/${enc}/${pane}`);
  if (r.tab !== tab || r.item !== item || r.pane !== pane) {
    bad.push(`кодування  ${tab}/${item}/${pane} → ${r.tab}/${r.item}/${r.pane}`);
  }
}

// ---------------------------------------------------------------------

console.log(`посилань у коді ${links.length}, переїздів ${Object.keys(MOVED).length}`
  + `, статичних адрес ${PATHS.size}, вкладок ${TABS.length}`);

if (bad.length) {
  console.log("\nВЕДЕ НЕ ТУДИ:");
  for (const b of bad) console.log(`  ${b}`);
  console.log("\nАдреса — звичайний рядок, і описка в ній нічого не ламає гучно:");
  console.log("невідомий шлях віддає не помилку, а найближчу правдоподібну");
  console.log("сторінку. Або виправ посилання, або допиши переїзд у LEGACY.");
}

process.exit(bad.length ? 1 : 0);
