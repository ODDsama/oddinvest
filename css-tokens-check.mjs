// Перевірка шару токенів фронтенду. Тільки для CI.
//
// Правило проєкту — «жодного літерального кольору чи розміру, тільки
// --oi-*» — тримається на чотирьох речах, і жодної з них не видно очима:
//
//   1. кожен ужитий var(--oi-…) справді десь оголошений. Описка в імені
//      не ламає нічого гучно: CSS просто мовчки бере запасне значення
//      або не малює правило зовсім. Один такий друк («#1d4straight»
//      замість «#1d4a6b») пережив увесь редизайн і знайшовся випадково;
//   2. оголошений токен має споживача. Мертві накопичуються самі:
//      --oi-fs-hero пролежав без жодного вжитку цілий реліз, а поруч із
//      ним стояв --oi-breakpoint-narrow із тією ж історією;
//   3. атрибут style у розмітці не задає нічого, крім кастомних
//      властивостей;
//   4. літерального #hex немає ніде, крім теми.
//
// Друге важливіше, ніж здається. Мертвий токен — це не сміття, а
// НЕПРАВДА: він читається як «так у застосунку роблять», і наступний
// автор чесно бере його за зразок.
//
// Усі п'ять фатальні. Три останні деякий час лише звітували — див.
// STRICT_MARKUP нижче, там записано, чому інакше було не можна.
//
// Конфіг лежить у корені репозиторію, а не поруч із модулями, з тієї ж
// причини, що й eslint.config.mjs: internal/api/web цілком вшивається в
// бінарник директивою `//go:embed web`, і файл звідти поїхав би
// користувачам у браузер.

import { readdirSync, statSync, readFileSync } from "node:fs";

const ROOT = "internal/api/web";

// Токени, яким споживач не потрібен, — з причиною. Список навмисно
// короткий: якщо він росте, значить правило перестало щось означати.
const ALLOW_UNUSED = new Map([
  // У media-запиті var() не працює: точку зламу доводиться писати
  // літералом у base.css. Токен лишається довідковим — щоб число мало
  // ім'я й місце, де його шукати, — і споживача мати не може за
  // будовою CSS, а не через недогляд.
  ["--oi-bp-sm", "точка зламу: у @media var() не працює"],
  ["--oi-bp-md", "точка зламу: у @media var() не працює"],
  ["--oi-bp-lg", "точка зламу: у @media var() не працює"],
]);

const walk = (d) => readdirSync(d).flatMap((f) => {
  const p = `${d}/${f}`;
  return statSync(p).isDirectory() ? walk(p) : [p];
});

const files = walk(ROOT).filter((f) => /\.(css|js|html)$/.test(f));

// Коментарі геть, але рядки на місці: звіт має вказувати на рядок у файлі,
// а не на рядок у якомусь очищеному тексті. Тому вміст коментаря
// затирається пробілами, а не вирізається.
const blank = (m) => m.replace(/[^\n]/g, " ");
const stripComments = (src) => src
  .replace(/\/\*[\s\S]*?\*\//g, blank)
  .replace(/(^|[^:])(\/\/[^\n]*)/g, (_, p, c) => p + blank(c));

const declared = new Map();  // токен -> де оголошений
const used = new Map();      // токен -> де вжитий уперше

// Коментарі не сканує НІЧОГО тут. Доти сканувало — і файл, у якому
// пояснено, чому саме прибрано --oi-shadow, самою цією згадкою знову
// оголошував його мертвим токеном. Тобто перевірка карала за те, що
// причину рішення записали; а записана причина — головне, що в цих
// файлах є.
for (const f of files) {
  const src = stripComments(readFileSync(f, "utf8"));
  for (const m of src.matchAll(/(--oi-[a-z0-9-]+)\s*:/g)) {
    if (!declared.has(m[1])) declared.set(m[1], f);
  }
  for (const m of src.matchAll(/var\(\s*(--oi-[a-z0-9-]+)/g)) {
    if (!used.has(m[1])) used.set(m[1], f);
  }
}

const missing = [...used.keys()].filter((k) => !declared.has(k)).sort();
const dead = [...declared.keys()]
  .filter((k) => !used.has(k) && !ALLOW_UNUSED.has(k)).sort();

// 3. Атрибут style у розмітці. Правило «жодного літерала в розмітці» доти
//    трималось на дисципліні — і не трималось: сусідні картки розходились
//    на пару пікселів, бо `margin-bottom` писався інлайном по дванадцять
//    разів, і кожне входження мало власний шанс розійтися.
//
//    Дозволено рівно одне: style, який задає ТІЛЬКИ кастомні властивості.
//    Це і є шов для динаміки — колір ряду на графіку, ширина смужки, —
//    і він єдиний, бо все інше має ім'я класу.
const STYLE_OK = /^\s*(--[a-z0-9-]+\s*:[^;]*;?\s*)+$/i;
const styleBad = [];
for (const f of files.filter((x) => /\.(js|html)$/.test(x))) {
  stripComments(readFileSync(f, "utf8")).split("\n").forEach((line, i) => {
    for (const m of line.matchAll(/style="([^"]*)"/g)) {
      if (!STYLE_OK.test(m[1])) styleBad.push(`${f}:${i + 1}  style="${m[1]}"`);
    }
  });
}

// 4. Літеральний колір. Це правило записане в шапках трьох файлів трьома
//    різними абзацами й не перевірялось нічим — саме тому «#1d4straight» і
//    прожив стільки, скільки прожив.
const HEX_OK = new Set([
  // Тло першого кадру. Стоїть до того, як завантажиться будь-який CSS із
  // токенами, тож узяти його звідти нізвідки — і це прямо описано там-таки.
  `${ROOT}/index.html`,
  // Єдиний файл, де кольору місце.
  `${ROOT}/css/theme-web.css`,
]);
const hexBad = [];
for (const f of files) {
  if (HEX_OK.has(f)) continue;
  stripComments(readFileSync(f, "utf8")).split("\n").forEach((line, i) => {
    const m = line.match(/#[0-9a-fA-F]{3,8}\b/);
    if (m) hexBad.push(`${f}:${i + 1}  ${m[0]}`);
  });
}

// 5. Кожен клас, ужитий у розмітці, десь оголошений.
//
//    Це та сама діра, що й описка в імені токена, лише глухіша: CSS на
//    невідомий клас не скаржиться взагалі, він просто нічого не малює.
//    Так у застосунку два роки прожив .warn-t — його ставили на «не
//    вистачить і на місяць» у прогнозі й на нестачу грошей у кошику, а
//    правила для нього не існувало ніколи. Обидва попередження виходили
//    звичайним текстом, і помітити це можна було, лише знаючи наперед,
//    якого кольору вони мали бути.
const cssClasses = new Set();
for (const f of files.filter((x) => x.endsWith(".css"))) {
  const src = stripComments(readFileSync(f, "utf8"));
  // Лише селекторна частина: усе до першої `{` кожного правила.
  for (const block of src.split("}")) {
    const sel = block.split("{")[0];
    for (const m of sel.matchAll(/\.(-?[A-Za-z_][\w-]*)/g)) cssClasses.add(m[1]);
  }
}

// Класи, яким правило не потрібне за будовою, — з причиною. Список
// навмисно короткий і весь про одне: це ГАЧКИ, за які JS бере елемент
// усередині рядка, де data-атрибут був би зайвим. Усе, що потрапляє сюди
// не з цієї причини, — забуте правило, а не виняток.
const ALLOW_UNSTYLED = new Map([
  ["skel", "обгортка скелета: несе лише aria-hidden, вигляд у дітей"],
  ["recAct", "гачок звірки: querySelector у money.js"],
  ["recDiff", "гачок звірки: querySelector у money.js"],
  ["recFix", "гачок звірки: querySelector у money.js"],
  ["cat-name", "гачок довідника: querySelector у settings.js"],
]);

const classUse = new Map();
const addUse = (name, where) => {
  if (!name || name.includes("$") || cssClasses.has(name)) return;
  if (ALLOW_UNSTYLED.has(name)) return;
  if (!classUse.has(name)) classUse.set(name, where);
};
for (const f of files.filter((x) => /\.(js|html)$/.test(x))) {
  stripComments(readFileSync(f, "utf8")).split("\n").forEach((line, i) => {
    const where = `${f}:${i + 1}`;
    for (const m of line.matchAll(/class="([^"]*)"/g)) {
      // Атрибут із підстановкою пропускаємо ЦІЛКОМ: ім'я класу там
      // збирається в JS, а рештки виразу (`||`, `?`) інакше приїжджають
      // сюди як «класи».
      if (m[1].includes("${")) continue;
      for (const c of m[1].split(/\s+/)) addUse(c.trim(), where);
    }
    for (const m of line.matchAll(/classList\.(?:add|remove|toggle)\(\s*"([\w-]+)"/g)) {
      addUse(m[1], where);
    }
  });
}
const classBad = [...classUse].map(([c, w]) => `${w}  .${c}`);

// Звіт скінчився: усі три перевірки на нулі, і від цієї миті вони тримають
// межу, а не описують її. Вмикати їх одразу було не можна — на день появи
// в розмітці лежало сто дев'яносто входжень style=, і фатальна перевірка
// означала б або червоний CI, або двісті правок одним комітом.
const STRICT_MARKUP = true;

console.log(`токенів оголошено ${declared.size}, вжито ${used.size}`);
console.log(`style= повз кастомні властивості: ${styleBad.length}`);
console.log(`літеральних #hex поза темою: ${hexBad.length}`);
console.log(`класів у розмітці без правила: ${classBad.length}`);

if (missing.length) {
  console.log("\nВЖИТІ, АЛЕ НЕ ОГОЛОШЕНІ (описка в імені або зник токен):");
  for (const k of missing) console.log(`  ${k}  ← ${used.get(k)}`);
}

if (dead.length) {
  console.log("\nОГОЛОШЕНІ БЕЗ ЖОДНОГО СПОЖИВАЧА:");
  for (const k of dead) console.log(`  ${k}  ← ${declared.get(k)}`);
  console.log("\nАбо вжити, або прибрати. Якщо споживача не може бути за");
  console.log("будовою CSS — додати в ALLOW_UNUSED разом із причиною.");
}

const show = (list, title, tail) => {
  if (!list.length) return;
  console.log(`\n${title}${STRICT_MARKUP ? "" : "  (поки лише звіт)"}:`);
  for (const l of list.slice(0, 40)) console.log(`  ${l}`);
  if (list.length > 40) console.log(`  …і ще ${list.length - 40}`);
  console.log(tail);
};

show(styleBad, "STYLE= ЗАДАЄ НЕ ЛИШЕ КАСТОМНІ ВЛАСТИВОСТІ",
  "\nСтатика — в клас. Динаміка — через --oi-*, яку читає правило в base.css.");
show(hexBad, "ЛІТЕРАЛЬНИЙ КОЛІР ПОЗА css/theme-web.css",
  "\nКолір живе в темі й приходить сюди токеном.");
show(classBad, "КЛАС У РОЗМІТЦІ, ЯКОГО НЕМАЄ В CSS",
  "\nАбо описка, або правило забули написати. Клас без правила нічого не\n" +
  "малює й ні на що не скаржиться — саме тому його треба ловити тут.\n" +
  "Якщо правило не потрібне за будовою — у ALLOW_UNSTYLED разом із причиною.");

process.exit(
  missing.length || dead.length ||
  (STRICT_MARKUP && (styleBad.length || hexBad.length || classBad.length)) ? 1 : 0,
);
