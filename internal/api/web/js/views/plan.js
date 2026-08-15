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

import { esc, today, money as fmtMoney, uah0 as fmtUAH, pct, dayMonth, humanMonths } from "../format.js";
import { infoBtn } from "../info.js";
import { empty, legend } from "../components.js";
import { onSubmit, onDelete, openEdit, fillForm } from "../forms.js";
import { disclosure, section, wireDisclosures } from "../disclosure.js";
import { CONTRIB, contribTriad } from "../contrib.js";
import { fluid, svgInflowProfile, CAT_COLORS } from "../charts.js";
import {
  income12mChartHTML, capitalChartHTML, projectionHTML, incomeHTML, drawdownHTML,
  renderCalendar, calendarPlaceholderHTML,
} from "./future.js";
import { goalsHTML, sensitivityHTML } from "./forecast.js";

const CADENCE_LABEL = { month: "щомісяця", quarter: "щокварталу", year: "щороку", once: "разово" };

// ---------- вердикт ----------
//
// Три показники на ОДНІЙ основі, і саме тому їх можна ставити поруч:
// скільки має заходити щомісяця, скільки дає план, скільки заходить
// насправді. Підписи беруться з CONTRIB — спільного словника, бо ті самі
// три слова потрібні ще пʼятьом місцям застосунку.
//
// «Бракує» тут НЕ плитка, а підрядок: це різниця двох чисел вище, а не
// четвертий показник. Доти воно стояло плиткою поруч із планом і читалось
// як рівноправне — звідси й бралася плутанина, бо те саме значення
// водночас звалось «скільки треба вносити» в сусідній картці.
export function planVerdictHTML(ctx) {
  const t = contribTriad(ctx);

  if (!t.hasPlan && !t.hasGoal) {
    return `<div class="card"><h2 class="card-head"><span>План ${infoBtn("planFlows")}</span></h2>
      ${empty("", "Заведи перше джерело доходу нижче — і побачиш, скільки план реально дає щомісяця.")}</div>`;
  }

  const tile = (label, val, cls = "", hero = false) =>
    `<div class="tile${hero ? " hero" : ""}"><div class="lbl">${label}</div>
      <div class="val${cls ? " " + cls : ""}">${fmtUAH(val)}<span class="muted fine">/міс</span></div></div>`;

  // Без цілі «треба» не існує — і це не порожнє місце, а чесна відповідь:
  // не задавши цілі, ти не питав, скільки треба.
  if (!t.hasGoal) {
    return `<div class="card"><h2 class="card-head"><span>План ${infoBtn("planFlows")}</span></h2>
      <div class="tiles flush">${tile(CONTRIB.plan.label, t.plan, "", true)}${
  t.hasActual ? tile(CONTRIB.actual.label, t.actual) : ""}</div>
      <div class="sub-xs mt-sm">Задай ціль і дедлайн у «Налаштуваннях», щоб побачити, чи цього досить.</div></div>`;
  }

  const tiles = tile(CONTRIB.need.label, t.need, "", true)
    + (t.hasPlan ? tile(CONTRIB.plan.label, t.plan) : "")
    + (t.hasActual ? tile(CONTRIB.actual.label, t.actual) : "");

  // gap === 0 — законна відповідь «вистачає», а не «немає даних», тож
  // перевіряємо саме на null, а не на істинність.
  let verdict = "";
  if (t.gap != null && t.gap > 0) {
    verdict = `<span class="t-warn">${CONTRIB.gap.label.toLowerCase()} ${fmtUAH(t.gap)}/міс</span>`;
  } else if (t.gap != null) {
    verdict = `<span class="t-ok">план сам виводить на ціль</span>`;
  }

  // Пастка, у яку легко втрапити після «⇗»: план, що закінчується раніше
  // за дедлайн, дає велике 12-місячне середнє й майже нічого на весь
  // горизонт. Тоді ПЛАН на екрані більший за ТРЕБА, а БРАКУЄ все одно
  // додатне — і без цього рядка це читається як помилка розрахунку.
  const outlives = t.hasPlan && t.hasGoal && t.gap > 0 && t.plan >= t.need
    ? `<div class="sub-xs mt-xs t-warn">План більший за потрібне лише поки триває:
        до дедлайну він не дотягує, тож на весь горизонт його все одно бракує.</div>`
    : "";

  return `<div class="card"><h2 class="card-head"><span>План ${infoBtn("planFlows")}</span></h2>
    <div class="tiles flush">${tiles}</div>
    <div class="sub-xs mt-sm">Ціль ${fmtUAH(t.goal)} до ${esc(t.date)}${
  verdict ? " · " + verdict : ""}.</div>
    ${outlives}${leversHTML(ctx)}</div>`;
}

// ---------- важелі ----------
//
// Вердикт довго вмів лише констатувати розрив («бракує 85 734 ₴/міс») і
// замовкати — а це найменш корисна з трьох відповідей, бо вона єдина, яку
// не можна виконати. Дві інші вже пораховані й лежать у тому самому
// зведенні: коли план доходить до цілі САМ (sensitivity.base_goal_*) і
// скільки буде на дедлайн, якщо нічого не міняти (кінець кривої).
//
// Це арифметика на власних числах користувача, а не порада: застосунок
// показує, що дає кожен важіль, вибір лишається за людиною.
function leversHTML(ctx) {
  const s = ctx.summary || {};
  const f = s.forecast || {};
  const { gap } = contribTriad(ctx);
  if (gap == null || gap <= 0) return ""; // ціль і так береться — важелі ні до чого

  const bits = [];
  // 1. Посунути дедлайн: наскільки пізніше план дійде сам.
  const sens = s.sensitivity || {};
  const selfMonths = sens.base_goal_months || 0;
  const planMonths = f.months || 0;
  if (selfMonths > 0 && planMonths > 0 && selfMonths > planMonths) {
    bits.push(`посунути дедлайн на ${esc(humanMonths(selfMonths - planMonths))}` +
      (sens.base_goal_date ? ` (до ${esc(dayMonth(sens.base_goal_date))})` : ""));
  }
  // 2. Знизити ціль до того, що виходить само. Остання точка кривої — це
  //    капітал на дедлайн у сьогоднішніх гривнях за реалістичним сценарієм.
  const pts = (f.curve || {}).points || [];
  const last = pts.length ? pts[pts.length - 1] : null;
  if (last && last.plan > 0 && f.goal_amount > 0 && last.plan < f.goal_amount) {
    bits.push(`знизити ціль до ${esc(fmtUAH(last.plan))}`);
  }
  if (!bits.length) return "";
  return `<div class="sub-xs mt-sm">Те саме іншими важелями: ${bits.join(" · ")}.</div>`;
}

// ---------- потоки: поля форми ----------
//
// ОДНА функція малює і форму додавання, і тіло модалки правки. Два списки
// полів розійшлися б на першій же зміні, і розійшлися б тихо: форма
// правки просто перестала б надсилати щось, чого PUT вимагає, а PUT тут —
// повна заміна рядка, не часткове оновлення.
//
// values — уже готові значення полів (flowFormValues), а не сирий рядок
// API: там валюта лежить усередині amount, нульова індексація взагалі
// відсутня, і кожен викликач мусив би пам'ятати це сам.
function flowFields(values = null) {
  const v = values || {};
  const val = (k, d = "") => `value="${esc(v[k] != null ? v[k] : d)}"`;
  const sel = (k, opts, d) => opts.map(([o, label]) =>
    `<option value="${o}"${(v[k] != null ? v[k] : d) === o ? " selected" : ""}>${label}</option>`).join("");
  // Правка мусить показати те, що правиться. Якщо в потоці є щось, крім
  // типових значень, «Ще» розгортається одразу — інакше правка виглядала б
  // так, ніби кінцевої дати чи індексації в потоці немає, і перше ж
  // збереження мовчки лишило б їх незміненими, а другий читач вирішив би,
  // що форма їх з'їла.
  const nonDefault = values && (v.until_date || +v.growth_pct !== 0
    || +v.invest_pct !== 100 || v.note);
  return `
    <label>Назва<input name="name" placeholder="Зарплата" ${val("name")} required></label>
    <label>Тип<select name="kind">${sel("kind", [["income", "дохід"], ["expense", "витрата"]], "income")}</select></label>
    <label>Сума<input name="amount" inputmode="decimal" placeholder="40000.00" ${val("amount")} required></label>
    <label>Валюта<select name="currency">${
      sel("currency", [["UAH", "UAH"], ["USD", "USD"], ["EUR", "EUR"]], "UAH")}</select></label>
    <label>Періодичність<select name="cadence">${sel("cadence", [
      ["month", "щомісяця"], ["quarter", "щокварталу"], ["year", "щороку"], ["once", "разово"],
    ], "month")}</select></label>
    <label>З дати<input name="from_date" type="date" ${val("from_date", today())} required></label>
    ${disclosure("planFlowMore", "Ще", `
      <label>До дати<input name="until_date" type="date" ${val("until_date")}></label>
      <label>Індексація, %/рік<input name="growth_pct" type="number" step="0.01"
        min="-99.99" max="100" ${val("growth_pct", "0")}></label>
      <label>Частка в портфель, %<input name="invest_pct" type="number" step="0.01"
        min="0" max="100" ${val("invest_pct", "100")}></label>
      <label>Нотатка<input name="note" ${val("note")}></label>`,
    "до дати, індексація, частка, нотатка", !!nonDefault)}`;
}

// Значення полів із рядка API. Тут зібрані всі чотири місця, де форма
// відрізняється від відповіді, — щоб їх не довелось згадувати тричі:
//   • валюти на верхньому рівні немає, вона всередині amount;
//   • сума для поля — сирий рядок «4500.00», а не «4 500,00 UAH»;
//   • growth_pct має omitempty, тож нульова індексація ВІДСУТНЯ в JSON, і
//     наївний префіл написав би в поле «undefined»;
//   • порожня частка в портфель на бекенді означає 100, а не 0.
function flowFormValues(f) {
  return {
    name: f.name || "",
    kind: f.kind || "income",
    amount: (f.amount || {}).amount || "",
    currency: (f.amount || {}).currency || "UAH",
    cadence: f.cadence || "month",
    from_date: f.from_date || today(),
    until_date: f.until_date || "",
    growth_pct: f.growth_pct != null ? f.growth_pct : 0,
    invest_pct: f.invest_pct != null ? f.invest_pct : 100,
    note: f.note || "",
  };
}

// Тіло запиту з набору значень — ЄДИНЕ місце, де описана форма запиту.
// PUT замінює рядок цілком, тож будь-яке поле, забуте тут, не «лишиться як
// було», а зникне.
//
// Порожні відсоткові поля дописуються ЯВНО тими значеннями, які підставив
// би бекенд: інакше стерта «частка в портфель» тихо означала б 100% —
// тобто протилежне тому, навіщо її стирали.
function flowBodyFromValues(v) {
  return {
    name: String(v.name || "").trim(), kind: v.kind,
    amount: String(v.amount || "").trim(), currency: v.currency,
    cadence: v.cadence, from_date: v.from_date,
    until_date: v.until_date || "",
    growth_pct: String(v.growth_pct ?? 0).trim() || "0",
    invest_pct: String(v.invest_pct ?? 100).trim() || "100",
    note: String(v.note || "").trim(),
  };
}

// Те саме з живої форми: знімаємо значення, далі спільний збирач.
function flowBody(f) {
  return flowBodyFromValues({
    name: f.name.value, kind: f.kind.value, amount: f.amount.value,
    currency: f.currency.value, cadence: f.cadence.value,
    from_date: f.from_date.value, until_date: f.until_date.value,
    growth_pct: f.growth_pct.value, invest_pct: f.invest_pct.value,
    note: f.note.value,
  });
}

export function planFlowFormHTML() {
  return `<form id="planFlowForm">${flowFields()}
    <div class="form-actions"><button type="submit">Додати</button></div>
  </form>`;
}

// ---------- зміна потоку з дати ----------
//
// Дві найчастіші зміни плану — «зарплата виросла» і «цей дохід
// закінчився» — це та сама операція з різним значенням одного поля, тож і
// дія одна. Порожня сума означає, що потік просто закінчується.
//
// Робити це правкою суми НА МІСЦІ не можна: модель тоді вважає, що так
// було завжди. Профіль не покаже сходинки, а колонка «дає ₴/міс» видасть
// нову суму так, ніби вона діяла весь рік. Тому старий рядок закривається
// датою, а новий починається наступного дня — два записи, одна дія.

/** iso ± n днів. Своя арифметика, бо в format.js її немає, а Date тут
 *  безпечний: рядок «YYYY-MM-DD» розбирається як UTC-північ, тож зсув
 *  цілими днями не залежить від часового поясу. */
function shiftDays(iso, n) {
  const d = new Date(iso + "T00:00:00Z");
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
}

/** Перше число наступного місяця — типова дата, з якої починає діяти нова
 *  зарплата. */
function firstOfNextMonth() {
  const t = today().split("-");
  const y = +t[0], m = +t[1];
  return m === 12 ? `${y + 1}-01-01` : `${y}-${String(m + 1).padStart(2, "0")}-01`;
}

function changeFields(f) {
  return `
    <label>З дати<input name="date" type="date" value="${firstOfNextMonth()}" required></label>
    <label>Нова сума<input name="amount" inputmode="decimal"
      placeholder="порожньо — потік закінчується"></label>
    <div class="sub-xs">Старий рядок закриється напередодні, новий почнеться з цієї дати —
      тож на профілі буде сходинка, а не переписана історія. Порожня сума означає, що
      «${esc(f.name)}» просто більше не надходить.</div>`;
}

// ---------- потоки: список ----------

// Заготовки для порожнього стану: назва, тип і періодичність — усе, що в
// цих випадках і так відоме. Сума лишається порожньою й дістає курсор,
// бо саме вона тут єдина справжня новина.
const FLOW_PRESETS = [
  ["Зарплата", { name: "Зарплата", kind: "income", cadence: "month" }],
  ["Оренда", { name: "Оренда", kind: "income", cadence: "month" }],
  ["Комуналка", { name: "Комуналка", kind: "expense", cadence: "month" }],
];

export function planFlowsListHTML(flows, provides = 0) {
  if (!flows.length) {
    return `${empty("", "Джерел доходу й витрат ще немає — перше додасть форма нижче.")}
      <div class="form-actions">${FLOW_PRESETS.map(([label], i) =>
    `<button type="button" class="sm quiet" data-preset="${i}">${label}</button>`).join("")}</div>`;
  }
  const rows = flows.slice()
    // Сортуємо за СИРОЮ ISO-датою, а не за підписом: «10 березня» стало б
    // перед «2 січня», бо порівнювались би рядки, а не дні.
    .sort((a, b) => a.from_date < b.from_date ? -1 : a.from_date > b.from_date ? 1 : 0)
    .map((f) => `<tr>
      <td>${esc(f.name)}${f.note ? ` <span class="muted fine-xs">${esc(f.note)}</span>` : ""}</td>
      <td><span class="pill ${f.kind === "income" ? "coupon" : "redemption"}">${
  f.kind === "income" ? "дохід" : "витрата"}</span></td>
      <td class="num">${fmtMoney(f.amount)}</td>
      <td class="num${(f.provides_uah || 0) < 0 ? " t-warn" : ""}">${fmtUAH(f.provides_uah || 0)}</td>
      <td>${CADENCE_LABEL[f.cadence] || esc(f.cadence)}</td>
      <td>${esc(dayMonth(f.from_date))}</td>
      <td>${f.until_date ? esc(dayMonth(f.until_date)) : "безстроково"}</td>
      <td class="num">${pct(f.invest_pct)}</td>
      <td class="row-actions">
        <button class="sm" data-editflow="${f.id}" aria-label="Змінити потік ${esc(f.name)}">✎</button>
        <button class="sm" data-changeflow="${f.id}"
          aria-label="Змінити потік ${esc(f.name)} з дати: підвищення або завершення">⇗</button>
        <button class="sm quiet" data-copyflow="${f.id}"
          aria-label="Скопіювати потік ${esc(f.name)} у форму">⧉</button>
        <button class="sm warn" data-delflow="${f.id}"
          aria-label="Видалити потік ${esc(f.name)}">✕</button>
      </td>
    </tr>`).join("");
  return `<div class="table-scroll"><table><thead><tr>
    <th scope="col">Назва</th><th scope="col">Тип</th><th scope="col" class="num">Сума</th>
    <th scope="col" class="num" title="середнє за найближчі 12 місяців">дає ₴/міс</th>
    <th scope="col">Період</th><th scope="col">З</th><th scope="col">До</th>
    <th scope="col" class="num">У портфель</th><th scope="col"><span class="sr-only">Дії</span></th>
    </tr></thead><tbody>${rows}</tbody>${flowsFootHTML(flows, provides)}</table></div>`;
}

// Підсумок, який показує подвійне віднімання ЧИСЛОМ, а не прозою.
//
// Пастка така: «частка в портфель» уже відрізала те, що з'їдають витрати,
// тож завести ЩЕ й витратний потік на ту саму суму означає відняти її
// двічі. Попереджати про це абзацом марно — його читають один раз, а
// помиляються потім. Тут обидва віднімання стоять сусідніми рядками зі
// своїми числами: побачивши −16 000 і −16 000, важко не помітити.
//
// Останній рядок береться з плитки, а не складається з колонки: Σ
// округлених рядків ≠ округлена Σ, і підсумок таблиці розійшовся б із
// числом угорі на копійки — що читалось би як помилка розрахунку.
function flowsFootHTML(flows, provides) {
  const inc = flows.filter((f) => f.kind === "income");
  const gross = inc.reduce((a, f) => a + (f.gross_uah || 0), 0);
  const cut = inc.reduce((a, f) => a + ((f.gross_uah || 0) - (f.provides_uah || 0)), 0);
  const exp = flows.filter((f) => f.kind === "expense")
    .reduce((a, f) => a + (f.provides_uah || 0), 0);
  // nowrap на числі: колонка «дає ₴/міс» вузька за вмістом рядків, і без
  // нього «49 563 ₴» переносило гривню на другий рядок — підсумок,
  // заради якого таблиця й має tfoot, читався найгірше з усього.
  const row = (label, val, cls = "") =>
    `<tr class="${cls}"><td colspan="3">${label}</td>
      <td class="num nowrap">${fmtUAH(val)}</td><td colspan="5"></td></tr>`;
  return `<tfoot>
    ${row("Доходи", gross, "muted")}
    ${cut ? row("Не доходить до портфеля (частка)", -cut, "muted") : ""}
    ${exp ? row("Витратні потоки", exp, "muted") : ""}
    ${row("План дає", provides, "tot")}
  </tfoot>`;
}

// ---------- профіль надходжень ----------
//
// Замінив Гант зі смугами «з…до»: той малював рівно те, що вже стоїть у
// таблиці колонками «З» і «До». Питання, на яке таблиця відповісти не
// може, — ФОРМА плану в часі: коли надходження підскочить, коли просяде,
// де його зжере разова витрата. Криву капіталу тут не малюємо — вона
// лишається власною карткою, де вже має і вісь, і лінію цілі.
export function profileHTML(doc) {
  const profile = doc.profile;
  if (!profile || (profile.points || []).length < 2) {
    return `<div class="card"><h2 class="card-head"><span>Профіль надходжень ${
      infoBtn("planTimeline")}</span></h2>
      ${empty("", "Заведи перший потік — і тут з'явиться, як план виглядає в часі.")}</div>`;
  }
  const names = (profile.series || []).map((s, i) => ({
    color: s.kind === "expense" ? "var(--oi-warn)" : CAT_COLORS[i % CAT_COLORS.length],
    label: s.name,
  }));
  const step = profile.step_months > 1
    ? ` Крок ${profile.step_months} міс.` : "";
  return `<div class="card"><h2 class="card-head"><span>Профіль надходжень ${
    infoBtn("planTimeline")}</span></h2>
    ${fluid((w, h) => svgInflowProfile(profile, doc.actions || [], doc.milestones || [],
    { W: w, H: h }), { cls: "tall" })}
    ${legend(names.concat([{ color: "var(--oi-series-invested)", label: "разом" }]))}
    <div class="sub-xs">Скільки ₴/міс заходить у портфель — уже після «частки в портфель».
      Витрати йдуть униз від нуля, ромби на нулі — дії плану.${step}</div></div>`;
}

// ---------- дії: список ----------

// Деталь рядка — те, що різнить дію: частки для set_shares, сума й строк
// для lock. Спільної форми в них немає, тож і колонка не вдає, що є.
function planActionDetail(a) {
  if (a.type === "set_shares") {
    const bits = [
      a.usd_share_pct != null ? `USD ${pct(a.usd_share_pct, 0)}` : null,
      a.eur_share_pct != null ? `EUR ${pct(a.eur_share_pct, 0)}` : null,
    ].filter(Boolean);
    return bits.join(", ") || "—";
  }
  const term = a.months > 0 ? humanMonths(a.months) : "безстроково";
  return `${fmtMoney(a.amount)} під ${pct(a.rate_pct)} · ${esc(term)}`;
}

export function planActionsListHTML(actions) {
  if (!actions.length) {
    return empty("", "Дій ще немає — дві форми нижче: зміна валютних часток і замок під ставку.");
  }
  const rows = actions.slice()
    .sort((a, b) => a.date < b.date ? -1 : a.date > b.date ? 1 : 0)
    .map((a) => `<tr>
      <td>${esc(dayMonth(a.date))}</td>
      <td><span class="pill ${a.type === "lock" ? "coupon" : "early"}">${
  a.type === "lock" ? "замок" : "частки"}</span></td>
      <td>${esc(a.name || "—")}</td>
      <td>${planActionDetail(a)}</td>
      <td class="row-actions">
        <button class="sm" data-editaction="${a.id}"
          aria-label="Змінити дію від ${esc(a.date)}">✎</button>
        <button class="sm warn" data-delaction="${a.id}"
          aria-label="Видалити дію від ${esc(a.date)}">✕</button>
      </td>
    </tr>`).join("");
  return `<div class="table-scroll"><table><thead><tr>
    <th scope="col">Дата</th><th scope="col">Тип</th><th scope="col">Назва</th>
    <th scope="col">Деталі</th><th scope="col"><span class="sr-only">Дії</span></th>
    </tr></thead><tbody>${rows}</tbody></table></div>`;
}

// ---------- дії: форми ----------
//
// Дві форми, а не одна з перемикачем: set_shares і lock не мають
// спільного поля, крім дати, і об'єднана форма лише ховала б, які поля
// стосуються якого типу.

// Підказка під полем частки: скільки її В КАПІТАЛІ зараз і яка ціль.
// Доти частка заводилась наосліп — сторінка знала обидва числа й мовчала.
function shareHint(ctx, cur) {
  const s = ctx.summary || {};
  const now = cur === "USD" ? s.usd_share_pct : s.eur_share_pct;
  const set = s.settings || {};
  const target = cur === "USD" ? set.usd_target_share_pct : set.eur_target_share_pct;
  const bits = [];
  if (now != null) bits.push(`зараз ${pct(now)}`);
  if (target != null) bits.push(`ціль ${pct(target)}`);
  return bits.length ? `<div class="sub-xs">${bits.join(" · ")}</div>` : "";
}

function sharesFields(ctx, values = null) {
  const v = values || {};
  const val = (k, d = "") => `value="${esc(v[k] != null ? v[k] : d)}"`;
  return `
    <label>З дати<input name="date" type="date" ${val("date", today())} required></label>
    <label>Частка USD, %<input name="usd_share_pct" type="number" step="0.01" min="0" max="100"
      placeholder="залишити як є" ${val("usd_share_pct")}>${shareHint(ctx, "USD")}</label>
    <label>Частка EUR, %<input name="eur_share_pct" type="number" step="0.01" min="0" max="100"
      placeholder="залишити як є" ${val("eur_share_pct")}>${shareHint(ctx, "EUR")}</label>
    <label>Нотатка<input name="note" ${val("note")}></label>`;
}

export function planSetSharesFormHTML(ctx) {
  return `<form id="planSetSharesForm">${sharesFields(ctx)}
    <div class="form-actions"><button type="submit">Змінити частки</button></div>
  </form>`;
}

function lockFields(values = null) {
  const v = values || {};
  const val = (k, d = "") => `value="${esc(v[k] != null ? v[k] : d)}"`;
  const cur = v.currency || "UAH";
  return `
    <label>Назва<input name="name" placeholder="MilTech" ${val("name")} required></label>
    <label>Дата<input name="date" type="date" ${val("date", today())} required></label>
    <label>Сума<input name="amount" inputmode="decimal" placeholder="50000.00" ${val("amount")} required></label>
    <label>Валюта<select name="currency">${["UAH", "USD", "EUR"].map((c) =>
    `<option${c === cur ? " selected" : ""}>${c}</option>`).join("")}</select></label>
    <label>Ставка, %<input name="rate_pct" type="number" step="0.01" min="0" max="100"
      placeholder="20" ${val("rate_pct")} required></label>
    <label>Строк, міс. (0 = безстроково)<input name="months" type="number" min="0"
      placeholder="24" ${val("months")}></label>
    <label>Нотатка<input name="note" ${val("note")}></label>`;
}

// Підказку про капітал на дату несе лише форма ДОДАВАННЯ: у модалці
// правки їй нема з чого рахуватись (стрічка туди не передається), а
// порожній рядок під полями читався б як «даних немає».
export function planLockFormHTML() {
  return `<form id="planLockForm">${lockFields()}
    <div class="sub-xs" data-lockhint></div>
    <div class="form-actions"><button type="submit">Замкнути</button></div>
  </form>`;
}

// ---------- капітал на дату ----------

/** Капітал за планом на дату iso, ₴ у сьогоднішніх грошах. Лінійна
 *  інтерполяція між сусідніми точками кривої; null — кривої немає або
 *  дата поза її межами.
 *
 *  Інтерполяція, а не найближча точка: крок кривої — дедлайн/12, тобто на
 *  десятирічному горизонті точка раз на десять місяців, і «найближча»
 *  помилялась би на пів року внесків. */
export function capitalAt(curve, iso) {
  const pts = (curve || []).filter((p) => p && p.date && p.plan != null);
  if (pts.length < 2 || !iso) return null;
  if (iso < pts[0].date || iso > pts[pts.length - 1].date) return null;
  for (let i = 1; i < pts.length; i++) {
    if (iso > pts[i].date) continue;
    const a = pts[i - 1], b = pts[i];
    const span = Date.parse(b.date) - Date.parse(a.date);
    if (!span) return b.plan;
    const t = (Date.parse(iso) - Date.parse(a.date)) / span;
    return a.plan + (b.plan - a.plan) * t;
  }
  return null;
}

// ---------- розділ цілком ----------

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
    ${planVerdictHTML(ctx)}
    ${timeline ? profileHTML(timeline) : ""}
    ${section("inflow", "Що заходить", `
      <div class="card">
        <h2 class="card-head"><span>Джерела доходу й витрат</span></h2>
        <div class="note">Кожен потік — сума з датою, періодичністю й тим, яка його частка
          доходить до портфеля. Колонка «дає ₴/міс» показує внесок саме цього рядка в число
          вгорі; підсумок під таблицею розкладає його на складники.</div>
        ${planFlowsListHTML(flows, (ctx.summary || {}).plan_provides_uah || 0)}
        ${planFlowFormHTML()}
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
  // Без цього «Ще» й самі секції згортались би після кожного збереження:
  // ctx.reload() переписує main, а пам'ять розкриття живе саме тут.
  // «План» був єдиним розділом, який цього не робив.
  wireDisclosures(main);
  // Календар — окремим запитом, тож після решти розмітки; власний
  // try/catch усередині лишає прогнози на місці, якщо він упаде.
  await renderCalendar(ctx, main, { append: true });
}

function wirePlanFlows(ctx, main, flows) {
  const byId = new Map(flows.map((f) => [String(f.id), f]));
  const form = main.querySelector("#planFlowForm");

  onSubmit(ctx, form, (f) => ({ path: "plan/flows", body: flowBody(f), msg: "Потік додано" }));

  onDelete(ctx, main, "[data-delflow]", (b) => ({
    path: "plan/flows/" + b.dataset.delflow,
    confirm: "Видалити цей потік?",
    msg: "Потік видалено",
  }));

  main.querySelectorAll("[data-editflow]").forEach((b) => b.addEventListener("click", () => {
    const f = byId.get(b.dataset.editflow);
    if (!f) return;
    openEdit(ctx, { title: `Правка потоку «${esc(f.name)}»`, fields: flowFields(flowFormValues(f)) },
      (form2) => ({
        method: "PUT", path: "plan/flows/" + f.id, body: flowBody(form2), msg: "Потік змінено",
      }));
  }));

  main.querySelectorAll("[data-changeflow]").forEach((b) => b.addEventListener("click", () => {
    const f = byId.get(b.dataset.changeflow);
    if (!f) return;
    openEdit(ctx, {
      title: `«${esc(f.name)}» з дати`,
      fields: changeFields(f),
      submit: "Застосувати",
    }, (form2) => {
      const date = form2.date.value;
      if (!date) return null;
      const v = flowFormValues(f);
      // Старий рядок закривається НАПЕРЕДОДНІ, а не тією ж датою: інакше
      // місяць зміни оплатили б обидва рядки.
      const closed = { ...flowBodyFromValues(v), until_date: shiftDays(date, -1) };
      const requests = [{ method: "PUT", path: "plan/flows/" + f.id, body: closed }];
      const amount = form2.amount.value.trim();
      if (amount) {
        requests.push({
          method: "POST", path: "plan/flows",
          body: { ...flowBodyFromValues(v), amount, from_date: date, until_date: "" },
        });
      }
      return { requests, msg: amount ? "Потік змінено з дати" : "Потік закрито" };
    });
  }));

  // «Копія» — префіл форми, а не мутація: жодного запиту не йде. Цим
  // закривається найчастіший випадок, під який у моделі немає
  // періодичності, — один оклад, що приходить двічі на місяць двома
  // датами. Дата лишається сьогоднішньою й дістає курсор, бо саме її й
  // міняють.
  main.querySelectorAll("[data-copyflow]").forEach((b) => b.addEventListener("click", () => {
    const f = byId.get(b.dataset.copyflow);
    if (!f || !form) return;
    const v = flowFormValues(f);
    fillForm(form, { ...v, from_date: today() });
    if (v.until_date || +v.growth_pct !== 0 || +v.invest_pct !== 100 || v.note) {
      const more = form.querySelector("details[data-fold]");
      if (more) more.open = true;
    }
    form.scrollIntoView({ block: "center" });
    form.from_date.focus();
  }));

  main.querySelectorAll("[data-preset]").forEach((b) => b.addEventListener("click", () => {
    const preset = FLOW_PRESETS[+b.dataset.preset];
    if (!preset || !form) return;
    fillForm(form, { ...preset[1], amount: "", from_date: today() });
    form.scrollIntoView({ block: "center" });
    form.amount.focus();
  }));
}

function wirePlanActions(ctx, main, actions, timeline) {
  const byId = new Map(actions.map((a) => [String(a.id), a]));

  const sharesBody = (f) => ({
    date: f.date.value, type: "set_shares",
    usd_share_pct: f.usd_share_pct.value.trim(), eur_share_pct: f.eur_share_pct.value.trim(),
    note: f.note.value.trim(),
  });
  const lockBody = (f) => ({
    date: f.date.value, type: "lock", name: f.name.value.trim(),
    amount: f.amount.value.trim(), currency: f.currency.value,
    rate_pct: f.rate_pct.value.trim(), months: f.months.value ? parseInt(f.months.value, 10) : 0,
    note: f.note.value.trim(),
  });

  onSubmit(ctx, main.querySelector("#planSetSharesForm"),
    (f) => ({ path: "plan/actions", body: sharesBody(f), msg: "Дію додано" }));
  onSubmit(ctx, main.querySelector("#planLockForm"),
    (f) => ({ path: "plan/actions", body: lockBody(f), msg: "Замок додано" }));

  onDelete(ctx, main, "[data-delaction]", (b) => ({
    path: "plan/actions/" + b.dataset.delaction,
    confirm: "Видалити цю дію?",
    msg: "Дію видалено",
  }));

  main.querySelectorAll("[data-editaction]").forEach((b) => b.addEventListener("click", () => {
    const a = byId.get(b.dataset.editaction);
    if (!a) return;
    const shares = a.type === "set_shares";
    // usd_share_pct/eur_share_pct — покажчики на бекенді: відсутнє поле
    // означає «не задано», а 0 — задану частку «долара не лишається зовсім».
    // `|| ""` перетворив би нуль на «не чіпати», тобто мовчки змінив би
    // сенс дії, тож перевіряємо саме на null.
    const values = shares
      ? {
        date: a.date, note: a.note || "",
        usd_share_pct: a.usd_share_pct != null ? a.usd_share_pct : "",
        eur_share_pct: a.eur_share_pct != null ? a.eur_share_pct : "",
      }
      : {
        date: a.date, name: a.name || "", note: a.note || "",
        amount: (a.amount || {}).amount || "",
        currency: (a.amount || {}).currency || "UAH",
        rate_pct: a.rate_pct != null ? a.rate_pct : "",
        months: a.months != null ? a.months : 0,
      };
    openEdit(ctx, {
      title: shares ? "Правка зміни часток" : `Правка замка «${esc(a.name || "")}»`,
      fields: shares ? sharesFields(ctx, values) : lockFields(values),
    }, (form2) => ({
      method: "PUT", path: "plan/actions/" + a.id,
      body: shares ? sharesBody(form2) : lockBody(form2), msg: "Дію змінено",
    }));
  }));

  wireLockHint(main.querySelector("#planLockForm"), timeline);
}

// Скільки буде на рахунку до цієї дати — щоб «замкнути 50 000» не
// заводилось наосліп. Число вже приїхало разом зі стрічкою, тож
// додаткового запиту не треба.
function wireLockHint(form, timeline) {
  if (!form) return;
  const hint = form.querySelector("[data-lockhint]");
  if (!hint) return;
  const curve = (timeline || {}).curve || [];
  const update = () => {
    const v = capitalAt(curve, form.date.value);
    if (v == null) {
      // Крива існує лише коли задано ціль і дедлайн, і обривається на
      // дедлайні. Свого числа замість неї не вигадуємо: «капітал сьогодні
      // + план × місяці» було б четвертим означенням росту капіталу,
      // порахованим у браузері.
      hint.textContent = curve.length
        ? "Поза межами прогнозу — він рахується до дедлайну цілі."
        : "Скільки буде на цю дату, видно, коли задано ціль і дедлайн.";
      hint.classList.remove("t-warn");
      return;
    }
    const amount = parseFloat(String(form.amount.value).replace(",", ".")) || 0;
    const over = form.currency.value === "UAH" && amount > v;
    hint.textContent = `За планом на цю дату капітал ≈ ${fmtUAH(v)}`
      + (over ? " — замок більший за нього." : "");
    hint.classList.toggle("t-warn", over);
  };
  for (const el of [form.date, form.amount, form.currency]) {
    el.addEventListener("change", update);
    el.addEventListener("input", update);
  }
  update();
}
