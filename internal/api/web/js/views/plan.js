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

import {
  esc, today, money as fmtMoney, uah0 as fmtUAH, cur2, pct, dayMonth, humanMonths,
  monthYear, monthShort, plural,
} from "../format.js";
import { infoBtn } from "../info.js";
import { empty, legend } from "../components.js";
import { onSubmit, onDelete, openEdit, fillForm } from "../forms.js";
import { disclosure, section, wireDisclosures } from "../disclosure.js";
import { CONTRIB, contribTriad } from "../contrib.js";
import {
  fluid, svgInflowProfile, svgGrouped, wireChartTips, CAT_COLORS, EVENT_COLORS,
} from "../charts.js";
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

// PROVIDES_MONTHS — вікно, за яким бекенд усереднює «дає ₴/міс»
// (planProvidesMonths у state_plan.go). Тут воно потрібне ЛИШЕ для підписів:
// саме число рахується там і сюди приходить готовим. Розійтись вони можуть
// тільки в тексті, і саме тому текст стоїть в одному місці, а не в трьох.
const PROVIDES_MONTHS = 12;

// Разова стаття в колонці «дає ₴/міс» — це сума, поділена на 12, а не
// щомісячний платіж: вікно усереднення однакове для всіх рядків, інакше
// плитка «План дає» стрибала б на всю премію в місяць виплати й назад.
// Без мітки рядок читається як платіж, якого не існує, — і не сходиться з
// профілем, де та сама стаття стоїть чесним шпилем в один місяць.
const isOnceSpread = (f) => f.cadence === "once" && (f.provides_uah || 0) !== 0;

function onceTitle(f) {
  if (!isOnceSpread(f)) return "";
  const full = (f.gross_uah || 0) * PROVIDES_MONTHS;
  return ` title="разова виплата ${fmtUAH(full)} ${dayMonth(f.from_date)}, розкладена на ${
    PROVIDES_MONTHS} місяців"`;
}

const onceHint = (f) => (isOnceSpread(f)
  ? `<div class="fine-xs muted">разова ÷${PROVIDES_MONTHS} міс</div>` : "");

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
      <td class="num${(f.gross_uah || 0) < 0 ? " t-warn" : ""}">${fmtUAH(f.gross_uah || 0)}</td>
      <td class="num">${pct(f.invest_pct)}</td>
      <td class="num${(f.provides_uah || 0) < 0 ? " t-warn" : ""}"${onceTitle(f)}>${
  fmtUAH(f.provides_uah || 0)}${onceHint(f)}</td>
      <td>${CADENCE_LABEL[f.cadence] || esc(f.cadence)}</td>
      <td>${esc(dayMonth(f.from_date))}</td>
      <td>${f.until_date ? esc(dayMonth(f.until_date)) : "безстроково"}</td>
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
  // Порядок колонок — це арифметика зліва направо: 89 420 ₴ × 10.0% = 8 941 ₴.
  // Доти «повного» числа не було видно ніде, тож перевірити рядок очима було
  // нічим — а підсумок «Доходи» під таблицею складався саме з нього й через
  // це читався як помилка розрахунку (224 521 під колонкою, що дає 31 390).
  return `<div class="table-scroll"><table><thead><tr>
    <th scope="col">Назва</th><th scope="col">Тип</th><th scope="col" class="num">Сума</th>
    <th scope="col" class="num" title="до «частки в портфель», за сьогоднішнім курсом">повне ₴/міс</th>
    <th scope="col" class="num">У портфель</th>
    <th scope="col" class="num" title="середнє за найближчі ${PROVIDES_MONTHS} місяців">дає ₴/міс</th>
    <th scope="col">Період</th><th scope="col">З</th><th scope="col">До</th>
    <th scope="col"><span class="sr-only">Дії</span></th>
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
  //
  // Кожне число стоїть рівно під СВОЄЮ колонкою, і це не косметика: «Доходи»
  // рахуються з gross_uah, решта — з provides_uah, і поки обидва підсумки
  // стояли в одному стовпці, перший читався як помилка (224 521 ₴ під
  // колонкою, чиї рядки дають 31 390 ₴). Тепер кожен складається з того, що
  // видно над ним.
  const row = (label, val, cls = "") =>
    `<tr class="${cls}"><td colspan="5">${label}</td>
      <td class="num nowrap">${fmtUAH(val)}</td><td colspan="4"></td></tr>`;
  const grossRow = (label, val, cls = "") =>
    `<tr class="${cls}"><td colspan="3">${label}</td>
      <td class="num nowrap">${fmtUAH(val)}</td><td colspan="6"></td></tr>`;
  return `<tfoot>
    ${grossRow("Доходи", gross, "muted")}
    ${cut ? row("Не доходить до портфеля (частка)", -cut, "muted") : ""}
    ${exp ? row("Витратні потоки", exp, "muted") : ""}
    ${row("План дає", provides, "tot")}
  </tfoot>`;
}

// ---------- історія правок ----------
//
// Журнал, якого не прочитати, — це просто прихована таблиця. Те саме, з
// чого картка «План проти факту» читає минуле, показується й людині: коли
// що змінилось і на що саме.
//
// Порівняння робить браузер, а не бекенд: тут уже є і форматування грошей,
// і підписи періодичності, а другий їхній набір на сервері неминуче
// розійшовся б із цим.

const REV_OP = {
  seed: "заведено до появи журналу",
  create: "додано",
  update: "змінено",
  delete: "видалено",
};

// Поля, за якими має сенс питати «що змінилось», у порядку читання.
const REV_FIELDS = [
  ["amount", "сума", (r) => `${cur2(r.amount, r.currency)}`],
  ["cadence", "період", (r) => CADENCE_LABEL[r.cadence] || r.cadence],
  ["from_date", "з", (r) => dayMonth(r.from_date)],
  ["until_date", "до", (r) => (r.until_date ? dayMonth(r.until_date) : "безстроково")],
  ["invest_pct", "у портфель", (r) => pct(r.invest_pct)],
  ["growth_pct", "індексація", (r) => pct(r.growth_pct)],
];

// Що змінилось порівняно з попередньою ревізією ТОГО САМОГО потоку.
// Валюта порівнюється разом із сумою: «1 700 → 2 000» без неї брехало б,
// якби змінилась саме валюта.
function revDiff(cur, prev) {
  if (!prev) return "";
  return REV_FIELDS.map(([key, label, show]) => {
    const a = key === "amount" ? `${cur[key]}|${cur.currency}` : cur[key];
    const b = key === "amount" ? `${prev[key]}|${prev.currency}` : prev[key];
    return a === b ? "" : `${label}: ${esc(show(prev))} → <b>${esc(show(cur))}</b>`;
  }).filter(Boolean).join(" · ");
}

function revisionsHTML(revs) {
  if (!revs.length) return "";
  // Журнал приходить найновішими згори, а «попередня ревізія» — це та, що
  // йде в списку ПІСЛЯ поточної для того самого потоку.
  const rows = revs.map((r, i) => {
    // Попередня ревізія шукається за flow_id, а НЕ за назвою: після «⇗» два
    // рядки називаються однаково, і за назвою підвищення виглядало б як
    // правка старого рядка.
    const prev = revs.slice(i + 1).find((o) => o.flow_id === r.flow_id);
    const what = r.op === "update" ? revDiff(r.flow, prev && prev.flow) : "";
    return `<tr>
      <td class="nowrap">${esc(dayMonth(r.at.slice(0, 10)))}</td>
      <td>${esc(r.name)}</td>
      <td>${what || `<span class="muted">${REV_OP[r.op] || esc(r.op)}</span>`}</td>
    </tr>`;
  }).join("");
  const body = `<div class="table-scroll"><table><thead><tr>
    <th scope="col">Коли</th><th scope="col">Потік</th><th scope="col">Що змінилось</th>
    </tr></thead><tbody>${rows}</tbody></table></div>
    <div class="sub-xs">Саме з цього журналу картка «План проти факту» читає, скільки план
      давав у минулому місяці. Тому правка суми на місці більше не переписує минуле:
      місяці до неї лишаються з тією сумою, яка діяла тоді.</div>`;
  return disclosure("planRevisions", "Історія правок", body,
    `${revs.length} ${plural(revs.length, "запис", "записи", "записів")}`);
}

// ---------- профіль надходжень ----------
//
// Замінив Гант зі смугами «з…до»: той малював рівно те, що вже стоїть у
// таблиці колонками «З» і «До». Питання, на яке таблиця відповісти не
// може, — ФОРМА плану в часі: коли надходження підскочить, коли просяде,
// де його зжере разова витрата. Криву капіталу тут не малюємо — вона
// лишається власною карткою, де вже має і вісь, і лінію цілі.

// Дані підказки профілю. На рівні модуля з тієї ж причини, що й capTip у
// history.js: будує її onMount рамки, тобто момент, коли локальних змінних
// profileHTML уже немає.
let profTip = null;

// Назва події так, як її читають: ISIN сам по собі не каже, що це ОВДП, а
// назва фонду вже містить усе потрібне.
const eventName = (e) => (e.kind === "bond" ? `ОВДП ${e.label}` : e.label);

// Розмітка підказки. Нулі не показуємо: у профілі більшість рядів мовчить
// у більшості місяців (разова премія — в одинадцяти з дванадцяти), і
// список із п'яти нулів ховав би те єдине, що цього місяця сталося.
function profTipHTML(i) {
  if (!profTip || !profTip.points[i]) return "";
  const p = profTip.points[i];
  const r = (label, val, color, cls = "") =>
    `<div class="r${cls ? " " + cls : ""}"><span>${color
      ? `<i style="--oi-c:${color}"></i>` : ""}${esc(label)}</span><b>${fmtUAH(val)}</b></div>`;

  const rows = profTip.series.map((s, k) => {
    const v = p.values[k] || 0;
    return v === 0 ? "" : r(s.name, v, s.color);
  }).join("");
  const inc = p.income || 0;
  const evs = (profTip.byIdx[i] || []).map((e) =>
    r(`↩ ${eventName(e)}`, e.amount_uah, EVENT_COLORS[e.kind])).join("");
  const acts = (profTip.actsByIdx[i] || []).map((a) =>
    `<div class="r"><span>◆ ${esc(a.name || (a.type === "lock" ? "замок" : "зміна часток"))}</span>${
      a.amount_uah ? `<b>${fmtUAH(a.amount_uah)}</b>` : ""}</div>`).join("");

  return `<div><b>${esc(monthYear(p.date))}</b>${profTip.step > 1
    ? ` <span class="muted">· у середньому за ${profTip.step} міс.</span>` : ""}</div>
    ${rows}${r("План разом", p.net, "var(--oi-series-invested)", "tot")}
    ${inc ? r("дохід портфеля", inc, "var(--oi-series-nominal)") : ""}
    ${inc ? r("Усе разом", p.net + inc, "var(--oi-series-neutral)") : ""}
    ${evs || acts ? `<div class="r tot"><span class="muted">цього місяця</span></div>${evs}${acts}` : ""}`;
}

// Подія лягає в ту точку, чиє вікно [дата_i, дата_{i+1}) її містить;
// остання точка забирає все, що після неї. Рахується один раз тут, а не в
// підказці: інакше кожне наведення перебирало б усі події заново.
function bucketByPoint(pts, items) {
  const out = {};
  if (!pts.length) return out;
  for (const it of items || []) {
    if (!it.date || it.date < pts[0].date) continue;
    let idx = pts.length - 1;
    for (let i = 0; i < pts.length - 1; i++) {
      if (it.date < pts[i + 1].date) { idx = i; break; }
    }
    (out[idx] = out[idx] || []).push(it);
  }
  return out;
}

// Список повернень тіла — те, чого з картинки не прочитати: маркер каже
// «коли», а «скільки» й «чого саме» доти лишалось у нативному <title> на
// п'яти пікселях. Згорнутий, бо це довідка до графіка, а не сам графік.
function returnsHTML(events) {
  if (!events.length) return "";
  const total = events.reduce((a, e) => a + (e.amount_uah || 0), 0);
  // Рік дописує сам dayMonth — і лише там, де він не цьогорічний. Дописати
  // його ще раз тут означало б «18 листопада 2026 2026».
  const rows = events.map((e) => `<tr>
    <td class="nowrap">${esc(dayMonth(e.date))}</td>
    <td><i class="swatch" style="--oi-c:${EVENT_COLORS[e.kind] || "var(--oi-muted)"}"></i>${
  esc(eventName(e))}</td>
    <td class="num nowrap">${fmtUAH(e.amount_uah)}</td></tr>`).join("");
  const body = `<div class="table-scroll"><table><thead><tr>
    <th scope="col">Дата</th><th scope="col">Що</th><th scope="col" class="num">Сума</th>
    </tr></thead><tbody>${rows}</tbody><tfoot><tr class="tot">
    <td colspan="2">Разом</td><td class="num nowrap">${fmtUAH(total)}</td>
    </tr></tfoot></table></div>
    <div class="sub-xs">Це не дохід і не внесок: власне тіло виходить із паперу на рахунок,
      і питання воно ставить інше — що з ним робити далі.</div>`;
  return disclosure("planReturns", "Що повертається", body,
    `${events.length} ${plural(events.length, "подія", "події", "подій")} на ${fmtUAH(total)}`);
}

export function profileHTML(doc) {
  const profile = doc.profile;
  if (!profile || (profile.points || []).length < 2) {
    return `<div class="card"><h2 class="card-head"><span>Профіль надходжень ${
      infoBtn("planTimeline")}</span></h2>
      ${empty("", "Заведи перший потік — і тут з'явиться, як план виглядає в часі.")}</div>`;
  }
  const hasIncome = (profile.points || []).some((p) => (p.income || 0) > 0);
  const names = (profile.series || []).map((s, i) => ({
    color: s.kind === "expense" ? "var(--oi-warn)" : CAT_COLORS[i % CAT_COLORS.length],
    label: s.name,
  }));
  const extra = [
    hasIncome && { color: "var(--oi-series-nominal)", label: "дохід портфеля", faint: true },
    { color: "var(--oi-series-invested)", label: "план разом" },
    hasIncome && { color: "var(--oi-series-neutral)", label: "усе разом" },
  ].filter(Boolean);
  const step = profile.step_months > 1
    ? ` Крок ${profile.step_months} міс.` : "";
  const pts = profile.points || [];
  const events = profile.events || [];
  profTip = {
    points: pts, step: profile.step_months || 1,
    series: (profile.series || []).map((s, i) => ({ name: s.name, color: names[i].color })),
    byIdx: bucketByPoint(pts, events),
    actsByIdx: bucketByPoint(pts, doc.actions || []),
  };
  const frame = fluid((w, h) => svgInflowProfile(profile, doc.actions || [], doc.milestones || [],
    { W: w, H: h }),
  { cls: "tall", onMount: (box) => wireChartTips(box.closest(".chart-wrap"), profTipHTML) });
  return `<div class="card"><h2 class="card-head"><span>Профіль надходжень ${
    infoBtn("planTimeline")}</span></h2>
    <div class="chart-wrap">${frame}<div class="chart-tip" data-tip="profile"></div></div>
    ${legend(names.concat(extra))}
    <div class="sub-xs">Скільки ₴/міс заходить у портфель — уже після «частки в портфель».
      Наведи мишу на місяць — побачиш розклад по джерелах. Витрати йдуть униз від нуля,
      ромби на нулі — дії плану${events.length
    ? ", засічки під нулем — повернення тіла (погашення, закриття вкладу чи фонду)"
    : ""}.${step}</div>
    ${returnsHTML(events)}</div>`;
}

// ---------- план проти факту ----------
//
// Дзеркало профілю: той малює майбутнє, ця картка — минуле. Питання до неї
// одне й дуже конкретне: «план обіцяв 32 тисячі, а скільки вийшло?».
//
// Три числа з трьох РІЗНИХ джерел, і саме тому вони підписані по-різному.
// План читається з журналу ревізій — зі стану таблиці потоків на кінець
// того місяця, тож пізніша правка суми його не переписує. Факт — реальні
// поповнення, нетто зі зняттями. «Бракувало» береться зі знімка того
// місяця й НЕ перераховується: це те, що застосунок вважав нестачею тоді.
//
// Місяці, старші за журнал, план усе ж виводить із теперішньої таблиці —
// такі стовпчики малюються контуром і кажуть про це в підказці. Коли
// журнал накриє все вікно, застереження зникне з картки САМО, а не
// лишиться висіти вічним дисклеймером.

const PLAN_FACT_COLORS = [
  "var(--oi-series-plan)", "var(--oi-series-invested)", "var(--oi-series-neutral)",
];

let factTip = null;

function factTipHTML(i) {
  if (!factTip || !factTip.points[i]) return "";
  const p = factTip.points[i];
  const diff = (p.actual_uah || 0) - (p.plan_uah || 0);
  const r = (label, val, color, cls = "") =>
    `<div class="r${cls ? " " + cls : ""}"><span>${color
      ? `<i style="--oi-c:${color}"></i>` : ""}${esc(label)}</span><b>${fmtUAH(val)}</b></div>`;
  // Відсоток виконання лише коли план був: 45 000 із нуля — це не «нескінченно
  // добре», а просто місяць без плану.
  const done = p.plan_uah > 0
    ? `<div class="r"><span class="muted">виконано</span><b>${pct(
      (p.actual_uah / p.plan_uah) * 100, 0)}</b></div>` : "";
  return `<div><b>${esc(monthYear(p.month + "-01"))}</b></div>
    ${r("План", p.plan_uah, PLAN_FACT_COLORS[0])}
    ${r("Факт", p.actual_uah, PLAN_FACT_COLORS[1])}
    ${p.gap_uah ? r("Бракувало (як тоді)", p.gap_uah, PLAN_FACT_COLORS[2]) : ""}
    ${r(diff >= 0 ? "Понад план" : "Недобрано", Math.abs(diff), "", "tot")}${done}
    ${p.plan_derived
    ? `<div class="r tip-note"><span class="muted fine-xs">план виведено з теперішньої
        таблиці — журнал правок тоді ще не вівся</span></div>` : ""}`;
}

export function planVsFactHTML(doc, summary) {
  const pts = doc.history || [];
  const head = `<h2 class="card-head"><span>План проти факту ${infoBtn("planVsFact")}</span></h2>`;
  if (pts.length < 2) {
    return `<div class="card">${head}${empty("",
      `Тут з'явиться, як план розходився з фактом по місяцях — щойно набереться
       два місяці з потоками або поповненнями.`)}</div>`;
  }
  const hasGap = pts.some((p) => (p.gap_uah || 0) > 0);
  const groups = pts.map((p) => ({
    // Підпис — місяць без року: дванадцять «2026-07» злиплись би в сіру
    // смугу, а рік і так один-два на все вікно.
    label: monthShort(p.month),
    values: hasGap
      ? [p.plan_uah || 0, p.actual_uah || 0, p.gap_uah || 0]
      : [p.plan_uah || 0, p.actual_uah || 0],
    // Контуром малюється тільки план: факт і нестача записані завжди.
    derived: [!!p.plan_derived, false, false],
  }));
  const anyDerived = pts.some((p) => p.plan_derived);
  factTip = { points: pts };

  const frame = fluid((w, h) => svgGrouped(groups, {
    W: w, H: h, colors: PLAN_FACT_COLORS, fmt: fmtUAH, hits: true,
  }), { onMount: (box) => wireChartTips(box.closest(".chart-wrap"), factTipHTML) });

  const names = [
    { color: PLAN_FACT_COLORS[0], label: "план" },
    { color: PLAN_FACT_COLORS[1], label: "факт" },
    hasGap && { color: PLAN_FACT_COLORS[2], label: "бракувало (як тоді)" },
  ].filter(Boolean);

  // Підсумок береться по ТИХ САМИХ місяцях, що на графіку: середнє за
  // півроку на вікні з трьох місяців було б середнім за три.
  const win = pts.slice(-6);
  const avg = (key) => win.reduce((a, p) => a + (p[key] || 0), 0) / win.length;
  const plan = avg("plan_uah"), fact = avg("actual_uah");
  const verdict = plan > 0
    ? `За останні ${win.length} ${plural(win.length, "місяць", "місяці", "місяців")} план обіцяв
       <b>${fmtUAH(plan)}</b>/міс, зайшло <b>${fmtUAH(fact)}</b>/міс — це ${pct(
    (fact / plan) * 100, 0)} плану.`
    : `За ці місяці плану заведено не було, тож порівнювати факт нема з чим.`;

  // Поточний місяць у стовпчики не входить — він ще триває, і півмісяця
  // поповнень читались би як провалений план. Але сказати, скільки вже
  // зайшло, варто: число вже пораховане у зведенні.
  const now = (summary || {}).month_deposited_uah;
  const nowLine = now == null ? ""
    : `<div class="sub-xs">Поточний місяць ще триває й у стовпчики не входить — у ньому вже
       внесено <b>${fmtUAH(now)}</b>.</div>`;

  // Застереження про виведені місяці стоїть, лише поки такі місяці є: коли
  // журнал накриє все вікно, воно зникне САМО, а не лишиться вічним
  // дисклеймером, який усі навчились не читати.
  const derivedLine = anyDerived
    ? `<div class="sub-xs">Порожні стовпчики плану — місяці, старші за журнал правок: за них
       план не записаний, а виведений із теперішньої таблиці, тож давня правка суми на місці
       їх усе ще зачіпає. Кожен новий місяць уже записується, і позначка сходить сама.</div>`
    : "";

  return `<div class="card">${head}
    <div class="chart-wrap">${frame}<div class="chart-tip" data-tip="planfact"></div></div>
    ${legend(names)}
    <div class="sub">${verdict}</div>
    ${nowLine}${derivedLine}
    <div class="sub-xs">Факт — реальні поповнення, нетто зі зняттями (купівля паперів сюди не
      входить: вона лише переносить гроші з рахунку в папери). План береться з журналу правок —
      зі стану потоків на кінець того місяця, тож пізніше підвищення минулого не переписує.
      Валютні суми переведено сьогоднішнім курсом — обидва ряди в одних грошах, але це не ті
      гривні, що були тоді.</div></div>`;
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
    ${timeline ? planVsFactHTML(timeline, ctx.summary) : ""}
    ${section("inflow", "Що заходить", `
      <div class="card">
        <h2 class="card-head"><span>Джерела доходу й витрат</span></h2>
        <div class="note">Кожен потік — сума з датою, періодичністю й тим, яка його частка
          доходить до портфеля. Колонка «дає ₴/міс» показує внесок саме цього рядка в число
          вгорі; підсумок під таблицею розкладає його на складники.</div>
        ${planFlowsListHTML(flows, (ctx.summary || {}).plan_provides_uah || 0)}
        ${revisionsHTML((timeline || {}).flow_revisions || [])}
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
