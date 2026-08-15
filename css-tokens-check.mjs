// Перевірка шару токенів фронтенду. Тільки для CI.
//
// Правило проєкту — «жодного літерального кольору чи розміру, тільки
// --oi-*» — тримається на двох речах, і жодну з них не видно очима:
//
//   1. кожен ужитий var(--oi-…) справді десь оголошений. Описка в імені
//      не ламає нічого гучно: CSS просто мовчки бере запасне значення
//      або не малює правило зовсім. Один такий друк («#1d4straight»
//      замість «#1d4a6b») пережив увесь редизайн і знайшовся випадково;
//   2. оголошений токен має споживача. Мертві накопичуються самі:
//      --oi-fs-hero пролежав без жодного вжитку цілий реліз, а поруч із
//      ним стояв --oi-breakpoint-narrow із тією ж історією.
//
// Друге важливіше, ніж здається. Мертвий токен — це не сміття, а
// НЕПРАВДА: він читається як «так у застосунку роблять», і наступний
// автор чесно бере його за зразок.
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

const declared = new Map();  // токен -> де оголошений
const used = new Map();      // токен -> де вжитий уперше

for (const f of files) {
  const src = readFileSync(f, "utf8");
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

console.log(`токенів оголошено ${declared.size}, вжито ${used.size}`);

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

process.exit(missing.length || dead.length ? 1 : 0);
