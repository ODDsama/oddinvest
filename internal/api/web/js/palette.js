// Палітра: Ctrl+K — і будь-яка сторінка, рядок або форма за два-три
// символи.
//
// Сторінок уже за тридцять, дерево — три рівні (вкладка → рядок →
// панель), і «де та картка з податком» коштувало трьох кліків і памʼяті
// про те, під якою вкладкою вона живе. Палітра не додає адрес: усе, що
// вона знає, вона бере з тих самих таблиць, що й навігація (nav.js
// TABS/panesFor, master.js рядки, routes.js форми), тож адреси в ній
// відомі ЗА ПОБУДОВОЮ. web-routes-check динамічні посилання не бачить —
// і не мусить: тут немає жодного літерала «#/…», якого не було б у
// дереві.
//
// ПАСТКА, названа в app.js при _posData: список позицій вантажиться лише
// на вкладці «Портфель». Палітра з іншої вкладки довантажує його сама,
// один раз на відкриття — інакше папери й вклади в пошуку були б лише
// тоді, коли ти й так дивишся на них.
//
// Останні пʼять переходів — у localStorage під власним ключем, а не в
// uistate.js: той про розкриті рядки, і змішувати два різні питання під
// одним ключем означало б стирати одне при чистці другого.

import { esc } from "./format.js";
import { TABS, panesFor } from "./nav.js";
import { routeFor, seg } from "./routes.js";
import { portfolioRows, moneyRows, staticRows } from "./master.js";
import { loadPositionsData } from "./views/positions.js";

const RECENT_KEY = "oddinvest.palette";
const LIMIT = 12;

// Форми — іменовані входи routes.js (FORM_ROUTE). Підписи тут, бо там
// лише адреси: форма не знає, як її звуть.
const FORMS = [
  ["buy", "Записати покупку паперу чи фонду"],
  ["topup", "Поповнити вклад"],
  ["deposit", "Внести гроші на рахунок"],
  ["convert", "Обміняти валюту"],
  ["planflow", "Додати джерело доходу"],
];

function recent() {
  try { return JSON.parse(localStorage.getItem(RECENT_KEY) || "[]"); } catch (_) { return []; }
}

function remember(entry) {
  try {
    const list = [entry, ...recent().filter((e) => e.href !== entry.href)].slice(0, 5);
    localStorage.setItem(RECENT_KEY, JSON.stringify(list));
  } catch (_) { /* приватний режим — палітра працює й без памʼяті */ }
}

/** Усі місця, куди можна піти: {label, sub, href}. */
async function entries(ctx, posData) {
  const out = [];
  for (const t of TABS) {
    if (t.mark) continue;
    let rows;
    if (t.dynamic === "positions") {
      rows = portfolioRows(ctx, posData || {});
    } else if (t.dynamic === "accounts") {
      rows = moneyRows(ctx);
    } else {
      rows = staticRows(t, ctx);
    }
    for (const r of rows) {
      const panes = panesFor(t.key, r.id);
      for (const p of panes) {
        const name = r.id === "all" ? t.label : r.name;
        out.push({
          label: panes.length > 1 ? `${name} · ${p.label}` : name,
          sub: r.id === "all" ? "" : t.label,
          href: `#/${t.key}/${seg(r.id)}/${p.key}`,
        });
      }
    }
  }
  for (const [key, label] of FORMS) {
    out.push({ label, sub: "форма", href: routeFor(key) });
  }
  // Інші портфелі — дією, а не адресою: перемикання не веде нікуди, воно
  // міняє, ЧИЇ дані показує та сама сторінка (portfolio.js).
  for (const p of ctx.portfolios || []) {
    if (p.slug === ctx.portfolio) continue;
    out.push({ label: `Портфель: ${p.name}`, sub: "перемкнути", run: () => ctx.setPortfolio(p.slug) });
  }
  return out;
}

/** Збіг: кожне слово запиту — початок слова або підрядок підпису.
 *  Початок підпису важить більше за початок слова, той — за підрядок. */
function score(entry, words) {
  const hay = `${entry.label} ${entry.sub}`.toLowerCase();
  let total = 0;
  for (const w of words) {
    if (hay.startsWith(w)) total += 3;
    else if (hay.includes(" " + w) || hay.includes("·" + w)) total += 2;
    else if (hay.includes(w)) total += 1;
    else return 0;
  }
  return total;
}

function listHTML(items, active) {
  if (!items.length) return `<div class="pal-none">Нічого не знайдено</div>`;
  return items.map((e, i) => `<a class="pal-row${i === active ? " on" : ""}" href="${e.href || "#"}"
      data-i="${i}"><span class="pal-l">${esc(e.label)}</span>${
  e.sub ? `<span class="pal-s">${esc(e.sub)}</span>` : ""}</a>`).join("");
}

/** Відкрити палітру. pop — <dialog id="palettePop"> з оболонки. */
export async function openPalette(ctx, pop, getPosData) {
  const box = pop.querySelector(".box");
  box.innerHTML = `<input class="pal-in" type="search" placeholder="Сторінка, папір, рахунок, форма…"
      aria-label="Пошук по застосунку" autocomplete="off">
    <div class="pal-list" role="listbox"></div>`;
  const input = box.querySelector(".pal-in");
  const list = box.querySelector(".pal-list");
  pop.showModal();
  input.focus();

  // Список позицій — довантажити, якщо оболонка його ще не має.
  let posData = getPosData();
  if (!posData) {
    try { posData = await loadPositionsData(ctx); } catch (_) { posData = {}; }
  }
  const all = await entries(ctx, posData);
  let shown = [];
  let active = 0;

  const go = (e) => {
    pop.close();
    // Дія без адреси (перемикання портфеля) у «Нещодавно» не потрапляє:
    // той список тримає лише href, і портфель, якого вже немає, лишався
    // б там мертвим рядком.
    if (e.run) { e.run(); return; }
    remember({ label: e.label, sub: e.sub, href: e.href });
    window.location.hash = e.href;
  };
  const paint = () => {
    const q = input.value.trim().toLowerCase();
    if (!q) {
      shown = recent();
      list.innerHTML = shown.length
        ? `<div class="pal-h">Нещодавно</div>` + listHTML(shown, active)
        : `<div class="pal-none">Набери назву сторінки, папера чи рахунку</div>`;
      return;
    }
    const words = q.split(/\s+/).filter(Boolean);
    shown = all.map((e) => ({ e, s: score(e, words) })).filter((x) => x.s > 0)
      .sort((a, b) => b.s - a.s || a.e.label.localeCompare(b.e.label, "uk"))
      .slice(0, LIMIT).map((x) => x.e);
    list.innerHTML = listHTML(shown, active);
  };
  input.addEventListener("input", () => { active = 0; paint(); });
  input.addEventListener("keydown", (ev) => {
    if (ev.key === "ArrowDown") { active = Math.min(active + 1, shown.length - 1); paint(); ev.preventDefault(); }
    else if (ev.key === "ArrowUp") { active = Math.max(active - 1, 0); paint(); ev.preventDefault(); }
    else if (ev.key === "Enter" && shown[active]) { go(shown[active]); ev.preventDefault(); }
  });
  list.addEventListener("click", (ev) => {
    const row = ev.target.closest(".pal-row");
    if (!row) return;
    ev.preventDefault();
    go(shown[Number(row.dataset.i)]);
  });
  paint();
}
