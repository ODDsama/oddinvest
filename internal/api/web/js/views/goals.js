// Ціль накопичення: авто, будинок, ремонт.
//
// ЧИМ ЦЕ НЕ РЕЗЕРВ І НЕ ЦІЛЬ КАПІТАЛУ. Три схожі слова живуть у застосунку
// поруч, і сплутати їх легко:
//
//   резерв        — гроші на чорний день, без суми й без дати; питання
//                   «чи вистачить прожити»;
//   ціль капіталу — куди виводить портфель разом («План → Ціль і
//                   прогноз»); питання «коли я стану незалежним»;
//   ціль тут      — названа сума на названу річ у названу дату; питання
//                   «чи встигну».
//
// Через це в цієї сутності є сума, дедлайн і намір ВИТРАТИТИ, яких немає в
// подушки. Гроші при цьому тієї самої природи — не інвестиція, — і
// купівельною спроможністю вони так само не стають.
//
// ЖОДНОГО ЧИСЛА ТУТ НЕ РАХУЄТЬСЯ. Прогрес, розрив, потрібний темп,
// фактичний темп і прогноз «коли збереться» приходять готовими зі
// /api/summary (doc.goals). Друга копія в браузері вже двічі закінчувалась
// різними числами на одному екрані — розбір лежить у CLAUDE.md.
//
// ВАЛЮТА — ГОЛОВНЕ, ЩО ТУТ ТРЕБА ПОКАЗАТИ ЧЕСНО. Ціль номінована у своїй
// валюті, а відкладати можна й гривнею; тоді курс рухає розрив без жодного
// руху в журналі, і прогрес може поїхати НАЗАД. Мовчати про це не можна:
// без пояснення це читається як помилка застосунку, а насправді це те, що
// з доларовою ціллю й гривневими заощадженнями відбувається насправді.

import { esc, curSym, plural, uah2 as fmtUAH, cur2 as fmtCur, money as fmtMoney } from "../format.js";
import { infoBtn } from "../info.js";
import { empty } from "../components.js";
import { opsGrid, actionsCol } from "../grid.js";
import {
  money as moneyField, text as textField, date as dateField,
  note as noteField, num as numField, formHTML,
} from "../fields.js";
import { refSelect, refValue } from "../refs.js";
import { routeFor } from "../routes.js";
import { wireCrud } from "../crud.js";
import { onSubmit } from "../forms.js";
import { wireRefs } from "../refs.js";

/** Ціль із документа за id рядка адреси («goal:3» → 3). */
export const goalOf = (ctx, key) =>
  ((ctx.summary || {}).goals || []).find((g) => String(g.id) === String(key));

// ---------- ПЛИТКИ ----------

/** Стан цілі: скільки зібрано, скільки лишилось, чи встигаєш.
 *
 *  ДВІ ОДИНИЦІ В КОЖНОМУ ЧИСЛІ, і гривня стоїть ПІД валютою цілі, а не
 *  замість неї. Головне питання — «чи вистачить на авто», а авто коштує
 *  $20 000; гривневий еквівалент потрібен лише щоб звести ціль із рештою
 *  застосунку, де все гривневе. */
export function goalTilesHTML(g) {
  if (!g) return "";
  const sym = curSym(g.currency);
  const uah = g.currency !== "UAH";
  const done = !!g.done_date;
  const fill = Math.min(100, Math.max(0, g.done_pct || 0));
  return `<div class="card">
    <h2 class="h-row">${esc(g.name)} ${infoBtn("goals")}</h2>
    <div class="note">Гроші, відкладені на названу річ у названу дату. Не інвестиція — і
      в купівельну спроможність не входять, як і резерв. Але від резерву відрізняються тим,
      що з ними буде: подушку тримають, щоб НЕ витратити, ціль — щоб витратити.</div>
    <div class="tiles flush">
      <div class="tile"><div class="lbl">Зібрано</div>
        <div class="val">${fmtCur(g.collected_native || 0, sym)}</div>
        ${uah ? `<div class="sub">${fmtUAH(g.collected_uah || 0)}</div>` : ""}</div>
      <div class="tile"><div class="lbl">Ціль</div>
        <div class="val">${fmtCur(g.target_native || 0, sym)}</div>
        ${uah ? `<div class="sub">${fmtUAH(g.target_uah || 0)} за сьогоднішнім курсом</div>` : ""}</div>
      <div class="tile"><div class="lbl">${done ? "Куплено" : "Лишилось"}</div>
        <div class="val">${done ? esc(g.done_date) : fmtCur(g.gap_native || 0, sym)}</div>
        <div class="sub">${done
    ? "ціль закрита — журнал під нею лишається історією"
    : uah ? fmtUAH(g.gap_uah || 0) : `${(g.done_pct || 0).toFixed(0)}% зібрано`}</div></div>
    </div>
    ${done ? "" : `<div class="progress mb-sm">
      <span style="--oi-fill:${fill}%;--oi-c:${g.behind ? "var(--oi-warn)" : "var(--oi-info)"}"></span></div>`}
    ${done ? "" : paceHTML(g, sym)}
    ${fxHTML(g, sym)}
    ${placesHTML(g)}
  </div>`;
}

/** Чи встигаю: потрібний темп проти фактичного.
 *
 *  Мовчить у цілі без дедлайну, і це не пропуск: питання «чи встигаю» їй
 *  не ставлять — вона нікуди не поспішає за визначенням. Прогноз «коли
 *  збереться» при цьому лишається: він не обіцянка встигнути, а наслідок
 *  нинішнього темпу. */
function paceHTML(g, sym) {
  const eta = g.eta_date
    ? `<div class="kv"><span class="muted">За нинішнім темпом збереться</span>
       <b>${esc(g.eta_date)}</b></div>` : "";
  if (!g.due_date) {
    return `${eta}<div class="sub">Дедлайну немає — застосунок не питає «чи встигаєш»
      і не ставить задач. Постав дату в «Записати», якщо хочеш темп і нагадування.</div>`;
  }
  const behind = !!g.behind;
  return `<div class="kv"><span class="muted">До ${esc(g.due_date)}</span>
      <b>${(g.months_left || 0).toFixed(1)} ${plural(Math.round(g.months_left || 0),
    "місяць", "місяці", "місяців")}</b></div>
    <div class="kv"><span class="muted">Треба відкладати</span>
      <b>${fmtCur(g.required_native || 0, sym)}/міс</b></div>
    <div class="kv"><span class="muted">Відкладається насправді</span>
      <b class="${behind ? "t-warn" : "t-ok"}">${fmtCur(g.actual_native || 0, sym)}/міс</b></div>
    ${eta}
    <div class="sub">${behind
    ? `<span class="t-warn">⚠ за нинішнім темпом до ${esc(g.due_date)} не збереться</span>`
    : `<span class="t-ok">темп тримається ✅</span>`}</div>
    ${g.short_month_uah ? `<div class="sub">Порада застосунку при цьому МЕНША за потрібний
      темп: стеля наповнення дає на ${fmtUAH(g.short_month_uah)} менше, ніж треба на місяць.
      Це не про твій темп — рядок вище саме про нього; це про те, скільки застосунок
      відріже сам, коли прийдуть гроші. Якщо покладаєшся на нього, підніми частку в
      <a class="lnk" href="${routeFor("policy/goals/main")}">Політиці → Цілі накопичення</a>
      або зсунь дату.</div>` : ""}`;
}

/** Курс працює проти цілі — сказати прямо.
 *
 *  Мовчить, коли гроші лежать у валюті цілі: там курсу нема куди
 *  втрутитись, і рядок був би шумом. */
function fxHTML(g, sym) {
  if (!g.fx_mixed) return "";
  const held = Object.entries(g.by_currency || {})
    .map(([c, v]) => fmtCur(v, curSym(c))).join(" · ");
  return `<div class="note">Ціль названа у ${esc(g.currency)}, а лежить це ${esc(held)}.
    Зібране міряється сьогоднішнім курсом — стільки ${sym}, скільки за ці гроші дають
    ЗАРАЗ. Через це прогрес може поїхати назад без жодного зняття: девальвація гривні
    відкидає від доларової цілі. Це не помилка розрахунку, а те, що справді відбувається.</div>`;
}

function placesHTML(g) {
  const places = Object.entries(g.places || {}).sort((a, b) => b[1] - a[1]);
  if (!places.length) return "";
  return `<div class="note">Де лежить: ${places.map(([p, v]) =>
    `${esc(p)} — ${fmtUAH(v)}`).join(" · ")}</div>`;
}

// ---------- ЩО ДАЛІ ----------

/** Скільки відкласти просто зараз. Порожньо — коли радити нема чого. */
export function goalFillHTML(g) {
  if (!g || g.done_date || !g.fill_now_uah) return "";
  return `<div class="card">
    <h2>Відкласти цього місяця — ${fmtUAH(g.fill_now_uah)}</h2>
    <div class="kv"><span class="muted">Стеля цього місяця</span>
      <b>${fmtUAH(g.fill_month_uah || 0)}</b></div>
    ${g.moved_uah ? `<div class="kv"><span class="muted">Цього місяця вже відкладено</span>
      <b>${fmtUAH(g.moved_uah)}</b></div>` : ""}
    <div class="sub">Стеля, а не черга: решта грошей далі йде в папери. Число спадає
      після ЗАПИСУ руху, а не після наміру — тож запиши, коли відклав.</div>
    ${g.short_month_uah ? `<div class="sub t-warn">Щоб устигнути, треба ще
      ${fmtUAH(g.short_month_uah)} на місяць понад стелю.</div>` : ""}
  </div>`;
}

// ---------- ЖУРНАЛ ----------

export function goalJournalHTML(ops, goalID) {
  const list = (ops || [])
    .filter((o) => String(o.goal_id) === String(goalID))
    .sort((a, b) => (a.date < b.date ? 1 : a.date > b.date ? -1 : b.id - a.id));
  return `<div class="card"><h2>Рухи цілі</h2>
    ${opsGrid({
    cols: [
      { key: "date", label: "Дата", cell: (o) => esc(o.date) },
      { key: "kind", label: "Рух",
        cell: (o) => (Number(o.amount.amount) >= 0 ? "Відклав" : "Узяв") },
      { key: "amount", label: "Сума", num: true, cell: (o) => fmtMoney(o.amount) },
      { key: "place", label: "Місце", cell: (o) => esc(o.place || "")
        + (o.note ? ` <span class="muted">${esc(o.note)}</span>` : "") },
      actionsCol("goal-ops", { label: (o) => "рух цілі від " + o.date }),
    ],
    rows: list,
    caption: "Рухи цілі: дата, напрям, сума, місце",
    empty: "Рухів ще немає — перший запис почне збирати на ціль.",
  })}
  </div>`;
}

// ---------- ФОРМИ ----------

/** Поля руху під ціллю. goalID приходить з адреси, тож поля вибору цілі
 *  тут немає: сторінка вже КАЖЕ, у яку ціль ідуть гроші, і випадайка
 *  дозволила б промахнутись на екрані, який про це й не питає. */
export const goalOpFields = (ctx, row = null) => [
  moneyField("amount", "Сума (+ відклав / − узяв)", {
    ph: "5000.00", required: true, value: row ? row.amount.amount : "",
  }),
  refSelect(ctx, { name: "currency", ref: "currency", value: row ? row.amount.currency : "UAH" }),
  textField("place", "Місце", {
    ph: "готівка / сейф / картка", value: row ? row.place || "" : "",
  }),
  dateField("date", "Дата", row ? { value: row.date } : {}),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

export const goalOpBody = (goalID) => (f) => ({
  goal_id: String(goalID),
  amount: f.amount.value.trim(),
  currency: refValue(f, "currency"),
  place: f.place.value.trim(),
  date: f.date.value,
  note: f.note.value.trim(),
});

/** Поля самої цілі — один список і для створення, і для правки. */
export const goalFields = (ctx, row = null) => [
  textField("name", "Назва", {
    ph: "Авто", required: true, value: row ? row.name || "" : "",
  }),
  moneyField("amount", "Скільки треба", {
    ph: "20000.00", required: true, value: row ? row.amount.amount : "",
  }),
  refSelect(ctx, { name: "currency", ref: "currency", value: row ? row.amount.currency : "UAH" }),
  dateField("due_date", "До коли", row ? { value: row.due_date || "" } : {}),
  numField("priority", "Порядок наповнення", {
    ph: "0 — першою", value: row ? String(row.priority || 0) : "",
  }),
  textField("place", "Місце", {
    ph: "готівка / сейф / картка", value: row ? row.place || "" : "",
  }),
  dateField("done_date", "Куплено (закрити ціль)",
    row ? { value: row.done_date || "" } : {}),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

export const goalBody = (f) => ({
  name: f.name.value.trim(),
  amount: f.amount.value.trim(),
  currency: refValue(f, "currency"),
  due_date: f.due_date.value,
  priority: f.priority.value.trim(),
  place: f.place.value.trim(),
  done_date: f.done_date.value,
  note: f.note.value.trim(),
});

/** Форма руху під конкретною ціллю. */
export function goalOpFormHTML(ctx, g, raw) {
  return `<div class="card"><h2 class="h-row">Рух цілі «${esc(g.name)}» ${infoBtn("goals")}</h2>
    ${formHTML({ id: "goalOpForm", fields: goalOpFields(ctx), submit: "Записати", cls: "mb" })}
    <div class="note">Переклав із рахунку? Запиши ще й зняття в «Гроші → Рухи» —
      інакше відкладене виглядатиме як втрата капіталу.</div>
    <h2 class="h-row mt-lg">Сама ціль</h2>
    ${formHTML({ id: "goalEditForm", fields: goalFields(ctx, rawOf(raw, g.id)), submit: "Зберегти" })}
    <div class="note">Куплено — постав дату в останньому полі: ціль закриється, але
      журнал під нею лишиться. Видаляти її не треба, та й не вийде, доки рухи є.</div>
  </div>`;
}

/** Сира ціль (те, що ВВІВ користувач) із /api/goals.
 *
 *  Документ стану сюди не годиться: у ньому суми ПОРАХОВАНІ — переведені
 *  за курсом, округлені, розкладені на дві одиниці, — а форма правки
 *  мусить показати введене. Той самий поділ, що між /api/goals і doc.goals
 *  на бекенді, і той самий довід. */
const rawOf = (raw, id) =>
  (raw || []).find((r) => String(r.id) === String(id)) || null;

/** Форма створення нової цілі — для панелі «Записати нове». */
export function goalCreateFormHTML(ctx) {
  return `<div class="card"><h2 class="h-row">Нова ціль накопичення ${infoBtn("goals")}</h2>
    ${formHTML({ id: "goalForm2", fields: goalFields(ctx), submit: "Завести" })}
    <div class="note">Сума задається В ТІЙ валюті, у якій названа ціль: авто коштує
      $20 000, і саме ця сума є ціллю — гривневий еквівалент застосунок порахує сам і
      перерахує щодня. Дедлайн необовʼязковий: без нього застосунок не питатиме «чи
      встигаєш» і не ставитиме задач.</div>
  </div>`;
}

/** Порожній стан — коли цілей немає жодної. */
export function goalsEmptyHTML() {
  return `<div class="card">${empty(
    "Цілей накопичення ще немає",
    "Ціль — це названа сума на названу річ: авто, будинок, ремонт. Від резерву "
    + "відрізняється тим, що її ВИТРАТЯТЬ у названу дату, тож у неї є сума й дедлайн.",
    { href: routeFor("portfolio/all/record"), label: "Завести ціль" })}</div>`;
}

// ---------- ПАНЕЛЬ ----------

/** Панель однієї цілі. Пʼять панелей, як у резерву, і з того самого
 *  доводу: «Що зробити» в неї немає ЗА ПРИРОДОЮ — /api/reinvest виду
 *  «ціль» не віддає, бо реальної дохідності в грошей не буває (nav.js).
 *
 *  ДВА РЕСУРСИ КРУДУ на одній панелі, і це не надлишок: рухи й сама ціль —
 *  різні сутності з різними ручками (шапка handlers_goals.go). wireCrud
 *  розводить їх селектором за data-res, тож кнопка сусіда не піде в чужий
 *  шлях. */
export async function goalPane(ctx, main) {
  const g = goalOf(ctx, ctx.key);
  if (!g) {
    // Закладка на ціль, якої вже немає, — звичайна річ. Порожній стан
    // мусить сказати це словами, а не показати нулі.
    main.innerHTML = goalsEmptyHTML();
    return;
  }
  const [raw, ops] = await Promise.all([
    ctx.soft("goals", []),
    ctx.soft("goal-ops", []),
  ]);
  main.innerHTML = goalPaneHTML(ctx, g, raw, ops);

  wireCrud(ctx, main, {
    resource: "goal-ops", form: "#goalOpForm", title: "Рух цілі", rows: ops,
    fields: goalOpFields, body: goalOpBody(g.id),
    msg: { add: "Рух записано", edit: "Рух виправлено", del: "Рух видалено" },
  });
  // Сама ціль правиться ТІЄЮ САМОЮ проводкою, лише без списку рядків:
  // форма одна, запис один, і кнопки видалення в неї немає — ціль із
  // рухами й не видалиться (goals.go), а закривають її датою «куплено».
  wireCrud(ctx, main, {
    resource: "goals", form: null, rows: raw,
    fields: goalFields, body: goalBody, title: "Ціль",
  });
  const edit = main.querySelector("#goalEditForm");
  if (edit) {
    onSubmit(ctx, edit, (f) => ({
      method: "PUT", path: `goals/${g.id}`, body: goalBody(f),
      msg: "Ціль збережено",
    }));
  }
  wireRefs(main);
}

function goalPaneHTML(ctx, g, raw, ops) {
  switch (ctx.pane) {
  case "state":
    return goalTilesHTML(g);
  case "have":
    return goalJournalHTML(ops, g.id);
  case "next":
    return goalFillHTML(g) || `<div class="card"><div class="sub">
      Відкладати зараз нічого: або ціль зібрана, або стеля наповнення не задана,
      або плану доходу немає — рахувати частку нема від чого. Задати стелю:
      <a class="lnk" href="${routeFor("policy/goals/main")}">Політика → Цілі накопичення</a>.
      </div></div>`;
  case "record":
    return goalOpFormHTML(ctx, g, raw);
  default:
    return `<div class="card"><div class="sub">Чим керується наповнення цілей:
      <a class="lnk" href="${routeFor("policy/goals/main")}">стеля й джерело</a>.
      Сама ціль — сума, дата й порядок — правиться в «Записати».</div></div>`;
  }
}
