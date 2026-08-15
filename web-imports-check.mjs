// Чи резолвиться КОЖЕН імпорт у web/js. Тільки для CI й `make ui`.
//
// `node --check` бачить лише синтаксис і пропускає «does not provide an
// export named X» — найімовірнішу помилку після перейменування чи переносу
// функції між модулями. Саме так після розбиття portfolio.js на модулі
// розділ «Портфель» поклався на «fmtCur is not defined».
//
// Файл лежить у корені репозиторію, а не поруч із модулями, з тієї ж
// причини, що й css-tokens-check.mjs: internal/api/web цілком вшивається в
// бінарник директивою `//go:embed web`, і скрипт звідти поїхав би
// користувачам у браузер.
//
// Доти цей код жив heredoc'ом усередині .github/workflows/ci.yml — тобто
// існував рівно в CI, і локально відтворити його було нічим.

import { readdirSync, statSync } from "node:fs";
import { pathToFileURL } from "node:url";

const ROOT = "internal/api/web/js";

// Модулі писались під браузер і чіпають ці глобальні прямо на рівні
// модуля. Заглушки мусять бути ДО першого import — інакше перший же
// `class X extends HTMLElement` впаде на етапі обчислення бази класу.
globalThis.HTMLElement = class { attachShadow() { return {}; } };
globalThis.customElements = { get: () => undefined, define: () => {} };
globalThis.localStorage = { getItem: () => null, setItem: () => {} };
globalThis.Document = { prototype: {} };

const walk = (d) => readdirSync(d).flatMap((f) => {
  const p = `${d}/${f}`;
  return statSync(p).isDirectory() ? walk(p) : [p];
});

let bad = 0;
for (const m of walk(ROOT).filter((f) => f.endsWith(".js")).sort()) {
  try {
    await import(pathToFileURL(m).href);
    console.log(`ok   ${m}`);
  } catch (e) {
    bad++;
    console.log(`FAIL ${m}\n     ${e.message.split("\n")[0]}`);
  }
}

process.exit(bad ? 1 : 0);
