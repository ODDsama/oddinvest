// Рядки майстер-списку: що показує ліва колонка на кожній вкладці.
//
// Окремим модулем, а не всередині app.js, з тієї ж причини, з якої
// окремі nav.js і routes.js: оболонка має РОЗКЛАДАТИ, а не знати домен.
// Тут єдине місце, де застосунок вирішує, що «рядок Портфеля» — це папір,
// фонд, пенсійний рахунок, вклад або резерв, і як він зветься.
//
// ЖОДНОЇ АРИФМЕТИКИ. Кожне число рядка приходить готовим: номінал
// позиції, ринкова вартість фонду, тіло вкладу, залишок рахунку. Скласти
// їх «на місці» було б на кілька рядків коротше й завело б чергову копію
// зведення — рівно ту помилку, проти якої стоїть handlers_whatif.go.
// Підсумок у підвалі теж не сума рядків: він береться з того самого
// зведення, з якого й число в шапці.
//
// Порожнє число показується прочерком, а не нулем. Вид, у якого немає
// дохідності (резерв), і папір, якого немає в довіднику НБУ, — це
// «нема чого міряти», а не «нуль»; нуль тут невідрізнимий від справжнього.

import { esc, cur2, uah0, pct, plural, capitalUAH } from "./format.js";
import { seg } from "./routes.js";
import { panesFor } from "./nav.js";

/** Підпис виду в пігулці інспектора й у чипах. Множина й однина
 *  розходяться не випадково — так само, як у KINDS старої воронки:
 *  чип називає ГРУПУ, пігулка — одну сутність. */
export const KIND_ONE = {
  bond: "ОВДП", fund: "Фонд", npf: "НПФ", deposit: "Вклад", reserve: "Резерв",
  goal: "Ціль",
};
const KIND_MANY = {
  bond: "ОВДП", fund: "Фонди", npf: "НПФ", deposit: "Вклади", reserve: "Резерв",
  goal: "Цілі",
};
// Цілі ОСТАННІМИ, після резерву: список іде від того, що працює, до того,
// що чекає свого дня. Подушка чекає аварії, ціль — названої дати.
const KIND_ORDER = ["bond", "fund", "npf", "deposit", "reserve", "goal"];

/** Число з відмінюваним словом: nOf(3, …) → «3 рахунки».
 *
 *  plural() віддає саме СЛОВО, без числа, — і саме тому потрібна ця
 *  обгортка: `plural(n, …)` у шаблоні мовчки давав «рахунки» без трійки
 *  перед ними, тобто підпис без числа там, де питали про кількість. */
const nOf = (n, one, few, many) => `${n} ${plural(n, one, few, many)}`;

/** Вид сутності з id рядка: "bond:UA4000227656" → "bond". */
export const kindOfItem = (id) =>
  id === "reserve" ? "reserve" : String(id).split(":")[0];

/** Колір виду. Токеном, а не значенням: літералів кольору в JS немає.
 *
 *  Мапа з ПОВНИМИ іменами, а не `var(--oi-kind-${k})` підстановкою, і це
 *  не багатослівність. css-tokens-check.mjs звіряє кожен ужитий токен із
 *  оголошеними, а зібране підстановкою ім'я приїжджає до нього як
 *  «--oi-kind-» і не звіряється ні з чим — тобто описка у ВИДІ пройшла б
 *  повз перевірку й дала б рядок без кольору, який ні на що не
 *  скаржиться. Саме такий випадок цей скрипт і ловить. */
export const KIND_COLOR = {
  bond: "var(--oi-kind-bond)",
  fund: "var(--oi-kind-fund)",
  npf: "var(--oi-kind-npf)",
  deposit: "var(--oi-kind-deposit)",
  reserve: "var(--oi-kind-reserve)",
  goal: "var(--oi-kind-goal)",
  all: "var(--oi-accent)",
};

/** Залишок вкладу: balance там, де він порахований, інакше тіло.
 *  Обидва вже пораховані бекендом — вибирається джерело, а не
 *  складається число. */
const bal = (t) => t.balance || t.principal;

// Тон правої примітки. Три стани, а не довільний колір: добре, тривожно,
// погано. Класом це зробити не можна — колір їде в --oi-c, який читає
// правило в base.css.
const TONE = {
  ok: "var(--oi-ok)",
  warn: "var(--oi-warn)",
  danger: "var(--oi-danger)",
};

/** Рядок списку. value/meta — те, що вже пораховане бекендом. */
const row = (id, name, sub, value, meta, kind, metaTone = "") => ({
  id, name, sub, value, meta, kind, metaTone,
});

/** Рядки «Портфеля»: зведення плюс по рядку на кожну живу позицію.
 *
 *  Порядок видів сталий (KIND_ORDER), а не за вартістю: список, який
 *  перетасовується після кожної покупки, перестає бути списком — у ньому
 *  не можна запам'ятати, де що лежить. */
export function portfolioRows(ctx, d) {
  const s = ctx.summary || {};
  const out = [row(
    "all", "Портфель цілком", "як росте · місяць · структура · ліміти",
    uah0(capitalUAH(s)),
    s.blended_yield_real_pct ? pct(s.blended_yield_real_pct) : "—",
    "all", s.blended_yield_real_pct > 0 ? "ok" : "",
  )];

  for (const p of d.positions || []) {
    const lots = (d.lots || []).filter((l) => l.isin === p.isin);
    const who = [...new Set(lots.map((l) => l.channel).filter(Boolean))].join(", ");
    out.push(row(
      `bond:${p.isin}`, p.isin,
      [KIND_ONE.bond, `${p.qty} шт`, who].filter(Boolean).join(" · "),
      cur2(p.nominal.amount, p.nominal.currency), pct(p.real_pct), "bond",
      p.real_pct > 0 ? "ok" : "",
    ));
  }

  for (const f of s.funds || []) {
    out.push(row(
      `fund:${f.fund}`, f.fund,
      [KIND_ONE.fund, `${f.qty} сертифікатів`].join(" · "),
      cur2(f.market_value, f.currency),
      pct(f.real_pct), "fund", f.real_pct > 0 ? "ok" : "",
    ));
  }

  for (const n of s.npf || []) {
    out.push(row(
      `npf:${n.name}`, n.name,
      [KIND_ONE.npf, n.administrator].filter(Boolean).join(" · "),
      uah0(n.value_uah), pct(n.real_pct), "npf",
      n.real_pct > 0 ? "ok" : "",
    ));
  }

  // Резервні вклади в списку окремими рядками не стоять: вони частина
  // резерву, і показати їх двічі означало б показати гроші двічі.
  for (const t of d.deposits || []) {
    if (t.closed_date || t.is_reserve) continue;
    out.push(row(
      `deposit:${t.id}`, t.bank,
      [KIND_ONE.deposit, t.maturity_date && `до ${t.maturity_date}`]
        .filter(Boolean).join(" · "),
      cur2(bal(t).amount, bal(t).currency), pct(t.rate_pct), "deposit",
    ));
  }

  const res = s.reserve;
  if (res || s.reserve_uah) {
    out.push(row(
      "reserve", "Резерв",
      res && res.months
        ? `${KIND_ONE.reserve} · ${res.months.toFixed(1)} місяця витрат`
        : KIND_ONE.reserve,
      uah0(s.reserve_uah || 0),
      // Дохідності в резерву немає ЗА ПРИРОДОЮ — не «поки що немає».
      // Довід цілком лежить у nav.js при PANE_SETS.
      "—", "reserve",
    ));
  }

  // Цілі накопичення — по рядку на ціль. Закриті теж: журнал під ними і є
  // історія того, як на них збирали, а ціна, яку за це заплатили,
  // лишається частиною картини. Помітка «куплено» відрізняє їх від живих.
  for (const g of s.goals || []) {
    const done = !!g.done_date;
    out.push(row(
      `goal:${g.id}`, g.name,
      [KIND_ONE.goal, done ? "куплено" : g.due_date && `до ${g.due_date}`]
        .filter(Boolean).join(" · "),
      uah0(g.collected_uah || 0),
      // Праворуч — ПРОГРЕС, а не дохідність: дохідності в цілі немає за
      // природою (довід у nav.js), а «скільки вже зібрано» і є те число,
      // заради якого на неї дивляться.
      done ? "—" : `${(g.done_pct || 0).toFixed(0)}%`, "goal",
      done ? "" : g.behind ? "warn" : "",
    ));
  }
  return out;
}

/** Рядки «Грошей»: зведення плюс по рядку на брокерський рахунок.
 *
 *  Брокери беруться зі зведення, а не з довідника: довідник каже, кого
 *  ти завів, а зведення — де справді лежать гроші, і питання цієї
 *  вкладки саме друге.
 *
 *  ЗАЛИШОК БЕРЕТЬСЯ З brokers, А НЕ З accounts. Обидві мапи звуться
 *  схоже й тримають float — і саме тому їх легко сплутати: accounts
 *  keyed ВАЛЮТОЮ (скільки всього гривень, скільки доларів), brokers —
 *  брокером і вже в ньому валютою. Узявши accounts[ім'я брокера], рядок
 *  діставав undefined і показував рівний нуль при 241 220 ₴ у зведенні
 *  над ним. Нуль тут невідрізнимий від справжнього нуля — саме та тиша,
 *  проти якої в цьому файлі й написано «прочерк, а не нуль».
 *
 *  МУЛЬТИВАЛЮТНИЙ РАХУНОК не зводиться в одне число, і це навмисно:
 *  скласти гривні з доларами можна лише за курсом, тобто порахувавши
 *  тут те, що рахує бекенд. Показуємо найбільший залишок і кажемо, що є
 *  ще; повний розклад по валютах лежить у «Звірці рахунку», де він і
 *  потрібен. Вибрати найбільший — це порівняння, а не обчислення: нове
 *  число з нього не з'являється. */
export function moneyRows(ctx) {
  const s = ctx.summary || {};
  const out = [row(
    "all", "Усі рахунки", "баланси · рухи · податки · виписка",
    uah0(s.account_uah || 0),
    nOf(Object.keys(s.brokers || {}).length, "рахунок", "рахунки", "рахунків"),
    "all",
  )];
  for (const [name, byCur] of Object.entries(s.brokers || {})) {
    const held = Object.entries(byCur || {}).filter(([, v]) => v);
    held.sort((a, b) => Math.abs(b[1]) - Math.abs(a[1]));
    const [top] = held;
    // Виду в рахунку немає, тож немає й кольорової смуги. Дати йому
    // колір «якогось» виду було б найгіршим варіантом: смуга виглядає
    // осмисленою і не означає нічого. Розрізняє рахунки те, що в них
    // справді різне, — ім'я, валюти й залишок.
    out.push(row(
      `acct:${name}`, name,
      held.length ? held.map(([c]) => c).join(", ") : "порожній",
      top ? cur2(top[1], top[0]) : "—",
      held.length > 1 ? `ще ${held.length - 1}` : "", "",
    ));
  }
  return out;
}

/** Статичні рядки вкладки — з дерева навігації плюс число, якщо воно
 *  вже пораховане.
 *
 *  ЧИСЛО Є НЕ В КОЖНОГО РЯДКА, і порожнє місце тут краще за прочерк.
 *  Прочерк каже «тут мало б бути число, і його немає»; у «Важелів» і
 *  «Резервної копії» числа немає ЗА ПРИРОДОЮ, і питати про нього нема
 *  сенсу. Прочерк лишається за тим, що виміряти можна, але поки нічим —
 *  так він і стоїть у дохідності резерву.
 *
 *  Береться виключно зі зведення, яке оболонка вже завантажила. Кожен
 *  рядок, що потребував би СВОГО запиту (скільки порад у помічника,
 *  скільки рядків у маршруті), лишається без числа: платити обходом
 *  маршрутів за підпис у списку — не та ціна, і саме так з'являються
 *  сторінки, які довго відкриваються ні за що. */
export function staticRows(tab, ctx) {
  const s = (ctx && ctx.summary) || {};
  const num = {
    todo: () => {
      const n = (s.tasks || []).filter((t) => t.sev === "now").length;
      return n ? [String(n), "зараз", "warn"] : null;
    },
    inflow: () => (s.plan_provides_uah
      ? [`${uah0(s.plan_provides_uah)}`, "/міс", ""] : null),
    goal: () => {
      const ind = s.independence || {};
      const d = ind.actual_date || ind.plan_date;
      return d ? [d.slice(0, 4), "незалежність", ""] : null;
    },
    mix: () => {
      const n = overLimit(s).length;
      return n ? [String(n), "понад ліміт", "danger"] : null;
    },
    reserve: () => {
      const r = s.reserve;
      return r && r.months
        ? [r.months.toFixed(1).replace(".", ","),
          r.target_months ? `з ${r.target_months} міс` : "міс",
          r.target_months && r.months < r.target_months ? "warn" : "ok"]
        : null;
    },
  };
  return (tab.items || []).map((it) => {
    const [value, meta, tone] = (num[it.id] && num[it.id]()) || ["", "", ""];
    return row(it.id, it.name, it.sub, value, meta, "", tone);
  });
}

/** Виміри, що вийшли за свій ліміт.
 *
 *  Саме concentration, а не rebalance: ліміт — це стеля частки (ISIN,
 *  брокер, рік погашення), і перевищення в документі зветься over_uah.
 *  У rebalance ліміту немає взагалі — там ЦІЛЬ і відхилення від неї, а
 *  «нижче цілі» і «понад ліміт» це різні новини. */
export const overLimit = (s) =>
  (s.concentration || []).filter((c) => c.over_uah > 0);

/** Чипи-зрізи «Портфеля». Заміняють колишні сторінки виду: вибір паперу
 *  і є вибором виду, а зріз по виду лишається тут.
 *
 *  Лічильник у підписі — з тих самих рядків, з яких малюється список, а
 *  не з окремого підрахунку: два способи порахувати те саме розійшлися б. */
export function chipsOf(rows) {
  const n = {};
  for (const r of rows) if (r.kind !== "all") n[r.kind] = (n[r.kind] || 0) + 1;
  const total = rows.filter((r) => r.kind !== "all").length;
  return [{ key: "", label: `Усі ${total}` }].concat(
    KIND_ORDER.filter((k) => n[k])
      .map((k) => ({ key: k, label: `${KIND_MANY[k]} ${n[k]}` })));
}

/** Розмітка одного рядка.
 *
 *  Веде на ПЕРШУ панель рядка, а не на ту, що зараз відкрита. Спокуса
 *  зберігати панель при переході між рядками є, і вона хибна: набори
 *  панелей у рядків різні (у зведення сім, у резерву п'ять), тож
 *  «лишись на тій самій» половину переходів усе одно доводилось би
 *  підміняти — і саме та половина виглядала б випадковою. */
export function rowHTML(tabKey, r, current) {
  // Порожній вид — порожня смуга, а не смуга «якогось» кольору. Місце
  // під неї лишається: без нього рядки зі смугою й без неї вишикувались
  // би по різних вертикалях в одному списку.
  const c = KIND_COLOR[r.kind] || "transparent";
  const tone = TONE[r.metaTone] || "var(--oi-muted)";
  const href = `#/${tabKey}/${seg(r.id)}/${panesFor(tabKey, r.id)[0].key}`;
  return `<li><a class="m-row" href="${href}" data-kind="${esc(r.kind)}"${
    r.id === current ? ` aria-current="true"` : ""}>
    <span class="m-bar" style="--oi-c:${c}"></span>
    <span class="m-t">
      <span class="m-n">${esc(r.name)}</span>
      <span class="m-s">${esc(r.sub)}</span>
    </span>
    <span class="m-v">
      ${r.value ? `<span class="m-val">${esc(r.value)}</span>` : ""}
      ${r.meta ? `<span class="m-meta" style="--oi-c:${tone}">${esc(r.meta)}</span>` : ""}
    </span>
  </a></li>`;
}

/** Підсумок у підвалі списку. Береться зі зведення, а не складається з
 *  рядків: число, показане у двох місцях, мусить бути одним числом. */
export function footValue(tabKey, ctx, rows) {
  const s = ctx.summary || {};
  if (tabKey === "portfolio") {
    return `${uah0(capitalUAH(s))}${s.blended_yield_real_pct
      ? ` · ${pct(s.blended_yield_real_pct)}` : ""}`;
  }
  if (tabKey === "money") return uah0(s.account_uah || 0);
  if (tabKey === "work") {
    const t = s.tasks || [];
    const now = t.filter((x) => x.sev === "now").length;
    const soon = t.filter((x) => x.sev === "soon").length;
    return t.length ? `${now} зараз · ${soon} скоро` : "нічого";
  }
  if (tabKey === "plan") {
    return s.plan_provides_uah ? `${uah0(s.plan_provides_uah)}/міс` : "—";
  }
  if (tabKey === "path") {
    // Рівень, а не кількість рядків. Без цієї гілки підвал упав би в
    // rows.length і сказав під написом «Зібрано» число «4» — рівно та
    // мовчазна неправда, проти якої написаний абзац про over_limit
    // нижче. Прочерк, коли прогрес не приїхав: половини тут не буває.
    const p = ctx.progress;
    return p ? `${p.level} із ${p.level_of}` : "—";
  }
  if (tabKey === "policy") {
    // Доти тут стояло rebalance[].over_limit — поля, якого в документі
    // стану НЕМАЄ. Фільтр мовчки давав порожній список, і підвал завжди
    // казав «у межах», хоч би скільки лімітів було перевищено. Рівно та
    // тиша, проти якої в цьому застосунку й написані всі перевірки.
    const over = overLimit(s).length;
    return over ? `${over} понад ліміт` : "у межах";
  }
  if (tabKey === "settings") {
    // Довідники, а не «база»: розміру бази застосунок не знає, і писати
    // під підписом «База» кількість рядків меню означало б відповідати
    // числом не на те питання. Обидва числа вже завантажені оболонкою.
    const b = (ctx.brokers || []).length;
    const f = (ctx.fundCatalog || []).length;
    return `${nOf(b, "брокер", "брокери", "брокерів")} · ${
      nOf(f, "фонд", "фонди", "фондів")}`;
  }
  return `${rows.length}`;
}
