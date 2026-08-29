// Потоки: джерела доходу й витрат — поля, список, історія правок, проводка.
//
// Найбільший із чотирьох модулів «Плану», і причина в тому, що потік —
// єдина сутність розділу з повним набором операцій: додати, виправити,
// закрити з дати, скопіювати, підставити з пресета. Кожна з них тягне свою
// форму, а форма правки й форма додавання навмисно малюються ОДНІЄЮ
// функцією (flowFields) — два списки полів розійшлися б на першій же
// зміні, і розійшлися б тихо, бо PUT тут повна заміна рядка.

import {
  esc, today, money as fmtMoney, uah0 as fmtUAH, cur2, pct, dayMonth, monthYear, plural,
} from "../format.js";
import { empty } from "../components.js";
import { onSubmit, onDelete, openEdit, fillForm } from "../forms.js";
import { disclosure } from "../disclosure.js";
import {
  money as moneyField, num as numField, text as textField, date as dateField,
  note as noteField, check as checkField, selectOf, formHTML, whenKind, wireKind,
} from "../fields.js";
import { PLAN_USES } from "../constants.js";
import { refSelect } from "../refs.js";
import { opsGrid } from "../grid.js";
import { npfDestOptions } from "../npf.js";

const CADENCE_LABEL = { month: "щомісяця", quarter: "щокварталу", year: "щороку", once: "разово" };

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
// Куди може йти потік. Порожнє призначення першим і завжди: типовий потік —
// звичайні гроші, і пенсійний рахунок тут виняток, а не норма.
function destOptions(accounts) {
  return [["", "ліквідний портфель"], ...npfDestOptions(accounts)];
}

// usesFields, usesPillsHTML і usesText експортуються: та сама розмітка
// дозволу потрібна формі позапланового надходження й чеклисту
// (plan-receipts.js). Другий набір галочок розійшовся б із цим на першому
// ж новому кошику — рівно так, як розійшлися б два списки полів потоку,
// проти чого написана шапка цього файла.

// Дозвіл ГАЛОЧКАМИ, а не випадайкою. Кошиків чотири, тобто законних
// поєднань п'ятнадцять, і перелічити їх у селекті означало б список, у
// якому шукають своє замість того, щоб його скласти. Галочка ж відповідає
// рівно на те питання, яке ставлять: «сюди можна?».
//
// УСІ ПОЗНАЧЕНІ ЗА ЗАМОВЧУВАННЯМ, бо типовий дохід не заборонений нікуди,
// а порожній набір бекенд читає як «без обмежень» — тобто те саме. Зняти
// всі чотири не заборонено окремою перевіркою свідомо: це той самий
// «дозволено всюди», і помилки в ньому немає.
export function usesFields(uses) {
  const on = new Set(uses && uses.length ? uses : PLAN_USES.map(([k]) => k));
  return `<div class="row-h" title="Куди цим грошам МОЖНА. Скільки саме туди піде,
    і далі вирішують стелі наповнення у «Політиці» — це дозвіл, а не поділ">
    <span class="sub-xs">Може йти в</span>
    ${PLAN_USES.map(([key, label]) =>
    checkField("use_" + key, label, { checked: on.has(key) })).join("")}</div>`;
}

// Дозвіл у вигляді, який розуміє fillForm: по булеану на галочку.
//
// Потрібен «копії», і без нього вона мовчки губила б заборону: fillForm
// ходить по ІМЕНАХ полів, а масив uses імені в формі не має — ключ просто
// пропускався б, лишаючи всі чотири галочки позначеними.
export function usesCheckValues(uses) {
  const on = new Set(usesNarrowed(uses) ? uses : PLAN_USES.map(([k]) => k));
  return Object.fromEntries(PLAN_USES.map(([k]) => ["use_" + k, on.has(k)]));
}

// Дозвіл звужений — тобто його варто показати. Бекенд віддає ПОВНИЙ
// перелік, коли обмежень немає, тож ознака одна: чи всі кошики на місці.
export function usesNarrowed(uses) {
  return Array.isArray(uses) && uses.length > 0 && uses.length < PLAN_USES.length;
}

// Пігулки дозволу — лише коли він звужений. Повний перелік під кожним
// рядком був би чотирма пігулками, які нічого не повідомляють: вони
// стояли б у всіх однаково.
export function usesPillsHTML(f) {
  if (f.kind !== "income" || !usesNarrowed(f.uses)) return "";
  const on = new Set(f.uses);
  const pills = PLAN_USES.filter(([k]) => on.has(k))
    .map(([, label, cls]) => `<span class="pill ${cls}">${label}</span>`).join(" ");
  return `<div class="fine-xs" title="решті кошиків ці гроші не дістаються">${
    pills} <span class="muted">— тільки сюди</span></div>`;
}

function flowFields(accounts, values = null) {
  const v = values || {};
  const val = (k, d = "") => (v[k] != null ? String(v[k]) : d);
  // Правка мусить показати те, що правиться. Якщо в потоці є щось, крім
  // типових значень, «Ще» розгортається одразу — інакше правка виглядала б
  // так, ніби кінцевої дати чи індексації в потоці немає, і перше ж
  // збереження мовчки лишило б їх незміненими, а другий читач вирішив би,
  // що форма їх з'їла.
  const nonDefault = values && (v.until_date || +v.growth_pct !== 0
    || +v.invest_pct !== 100 || v.dest || v.note || usesNarrowed(v.uses));
  return [
    textField("name", "Назва", { ph: "Зарплата", required: true, value: val("name") }),
    selectOf("kind", "Тип", [["income", "дохід"], ["expense", "витрата"]], val("kind", "income")),
    moneyField("amount", "Сума", { ph: "40000.00", required: true, value: val("amount") }),
    // Валюта тепер поле-посилання, як усюди: локальний список із трьох
    // літералів був тут восьмою копією тієї самої трійки.
    refSelect(null, { name: "currency", ref: "currency", value: val("currency", "UAH") }),
    selectOf("cadence", "Періодичність", [
      ["month", "щомісяця"], ["quarter", "щокварталу"], ["year", "щороку"], ["once", "разово"],
    ], val("cadence", "month")),
    dateField("from_date", "З дати", { required: true, value: val("from_date", today()) }),
    disclosure("planFlowMore", "Ще", [
      dateField("until_date", "До дати", { value: val("until_date") }),
      numField("growth_pct", "Індексація, %/рік", {
        step: "0.01", min: "-99.99", max: "100", value: val("growth_pct", "0"),
      }),
      numField("invest_pct", "Частка в портфель, %", {
        step: "0.01", min: "0", max: "100", value: val("invest_pct", "100"),
      }),
      // ЛИШЕ У ВИТРАТИ, і це не косметика форми. Проєкція читає dest
      // рівно для відʼємних місячних сум (state_projection.go), тож на
      // доході воно не робить нічого — а поле, яке нічого не робить,
      // читається як робоче: рядок «Бонус 250 $» діставав пігулку «переказ,
      // а не витрата», хоч жодного переказу в жодному числі не було.
      // ЛИШЕ В ДОХОДУ, і це дзеркало сусіднього поля, а не симетрія
      // заради симетрії: «на що ці гроші можуть піти» — питання про
      // гроші, які ПРИХОДЯТЬ. Витрата нікуди не йде, вона зникає, і
      // бекенд таке поєднання відхиляє (planFlowFromReq).
      whenKind(["income"], usesFields(v.uses)),
      whenKind(["expense"], selectOf("dest", "Призначення", destOptions(accounts), val("dest"), {
        title: "Витрата з призначенням у пенсійний — це не зʼїдені гроші, а переказ: "
          + "ліквідне худне, пенсійний капітал росте. Без призначення проєкція вважала б їх витраченими",
      })),
      noteField("note", "Нотатка", { value: val("note") }),
    ].join(""), "до дати, індексація, частка, дозвіл, призначення, нотатка", !!nonDefault),
  ].join("");
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
    dest: f.dest || "",
    // Перелік як є: порожній масив і повний однаково означають «без
    // обмежень», і зводити їх тут не можна — форма мусить показати
    // галочки, а не вгадувати.
    uses: Array.isArray(f.uses) ? f.uses : [],
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
    dest: String(v.dest || "").trim(),
    uses: Array.isArray(v.uses) ? v.uses : [],
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
    // Прихована група ПОЛЕ НЕ ПРИБИРАЄ — hidden ховає, а не видаляє, — тож
    // без перевірки виду форма надіслала б призначення, набране до
    // перемикання типу. Бекенд відхилив би це чотирьохсоткою, і людина
    // побачила б помилку про поле, якого вона на екрані не бачить.
    dest: f.kind.value === "expense" && f.dest ? f.dest.value : "",
    // Дозвіл — теж лише в доходу, і з тієї самої причини, що dest вище:
    // прихована група поле НЕ прибирає, тож без перевірки виду форма
    // надіслала б галочки, зняті до перемикання типу, а бекенд відповів би
    // помилкою про поле, якого на екрані не видно.
    uses: f.kind.value === "income"
      ? PLAN_USES.map(([k]) => k).filter((k) => f["use_" + k] && f["use_" + k].checked)
      : [],
    note: f.note.value,
  });
}

export function planFlowFormHTML(ctx) {
  return formHTML({
    id: "planFlowForm", submit: "Додати",
    fields: [flowFields((ctx || {}).npfAccounts)],
  });
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
  return dateField("date", "З дати", { value: firstOfNextMonth(), required: true })
    + moneyField("amount", "Нова сума", { ph: "порожньо — потік закінчується" })
    + `<div class="sub-xs">Старий рядок закриється напередодні, новий почнеться з цієї дати —
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

// У разової виплати немає «щомісяця» — і це не діра в даних, а властивість
// потоку: премія приходить один раз. Тому в колонці стоїть прочерк, а її
// справжній розмір видно у двох сусідніх — «повне ₴/міс» (уся сума за
// курсом) і в місяці, коли вона справді прийде.
//
// Доти в цій колонці стояла дванадцята частина премії, бо plan_provides_uah
// усереднює вікно у 12 місяців. Число правильне для ПЛИТКИ, але в рядку
// читалось як щомісячний платіж, якого не існує, — і ламало єдине, заради
// чого «повне ₴/міс» узагалі з'явилась: множення 250 USD × курс мало
// давати те, що поруч.
const isOnce = (f) => f.cadence === "once";

const DASH = "—";

// Назва найближчого місяця плану — місяця 1 моделі, тобто НАСТУПНОГО
// календарного. Саме назвою, а не «цього місяця»: сьогодні шістнадцяте
// серпня, і «цього» читалось би як серпень, тоді як число в колонці — про
// вересень.
function nextMonthLabel() {
  const n = new Date();
  // Через Date, а не «місяць + 1» рядком: у грудні додавання дало б
  // тринадцятий місяць, і в шапці стояло б «13».
  const d = new Date(n.getFullYear(), n.getMonth() + 1, 1);
  return monthYear(`${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-01`)
    .split(" ")[0];
}

// «повне ₴/міс» — для регулярних стала ставка до частки, для разової вся
// сума: це єдине число, яке говорить про премію правду.
const fullCell = (f) => (isOnce(f) ? (f.amount_uah || 0) : (f.monthly_gross_uah || 0));

// ЗАВЕРШЕНІ ПОТОКИ — ОКРЕМИМ, ЗГОРНУТИМ БЛОКОМ.
//
// Видалити їх не можна: за рядком тримається журнал ревізій, а за журналом
// — картка «План проти факту», тобто зникла б не одна назва, а половина
// історії. Але й у робочому списку їм не місце: закрита зарплата стоїть із
// нулями в обох гривневих колонках і читається як помилка розрахунку рівно
// доти, доки не звіриш дату «до».
//
// Хто саме завершений, каже БЕКЕНД (planFlowRow.expired). Перевірити це в
// браузері двома датами було б на рядок коротше й завело б друге означення
// «чи платить іще цей потік» — те, від чого застережений весь модуль.
export function planFlowsListHTML(flows, provides = 0) {
  if (!flows.length) {
    return `${empty("", "Джерел доходу й витрат ще немає — перше додасть форма нижче.")}
      <div class="form-actions">${FLOW_PRESETS.map(([label], i) =>
    `<button type="button" class="sm quiet" data-preset="${i}">${label}</button>`).join("")}</div>`;
  }
  const rows = flows.slice()
    // Сортуємо за СИРОЮ ISO-датою, а не за підписом: «10 березня» стало б
    // перед «2 січня», бо порівнювались би рядки, а не дні.
    .sort((a, b) => (a.from_date < b.from_date ? -1 : a.from_date > b.from_date ? 1 : 0));
  const live = rows.filter((f) => !f.expired);
  const done = rows.filter((f) => f.expired);
  // Порядок колонок — це арифметика зліва направо: 89 412 ₴ × 10.0% = 8 941 ₴.
  // Доти «повного» числа не було видно ніде, тож перевірити рядок очима було
  // нічим — а підсумок «Доходи» під таблицею складався саме з нього й через
  // це читався як помилка розрахунку (224 521 під колонкою, що дає 31 390).
  //
  // Дві гривневі колонки замість однієї — бо питань справді два, і разова
  // виплата відповідає на них по-різному: щомісяця вона не дає нічого,
  // а в свій місяць приносить усю суму.
  const cols = [
    { key: "name", label: "Назва", cell: (f) => esc(f.name)
      + (f.note ? ` <span class="muted fine-xs">${esc(f.note)}</span>` : "")
      + (f.kind === "expense" && f.dest
        ? `<div class="fine-xs"><span class="pill pill-npf">НПФ</span> переказ, а не витрата</div>` : "")
      + usesPillsHTML(f) },
    { key: "kind", label: "Тип", cell: (f) => `<span class="pill ${
      f.kind === "income" ? "coupon" : "redemption"}">${
      f.kind === "income" ? "дохід" : "витрата"}</span>` },
    { key: "amount", label: "Сума", num: true, cell: (f) => fmtMoney(f.amount) },
    { key: "full", label: "повне ₴/міс", num: true,
      cell: (f) => `<span class="${fullCell(f) < 0 ? "t-warn" : ""}"
        title="сума за сьогоднішнім курсом, до «частки в портфель»">${fmtUAH(fullCell(f))}</span>`
        + (isOnce(f) ? `<div class="fine-xs muted">разова</div>` : "") },
    { key: "invest", label: "У портфель", num: true, cell: (f) => pct(f.invest_pct) },
    { key: "monthly", label: "щомісяця", num: true,
      cell: (f) => `<span class="${(f.monthly_uah || 0) < 0 ? "t-warn" : ""}"${isOnce(f)
        ? ` title="разова виплата ${esc(dayMonth(f.from_date))} не дає нічого щомісяця — її внесок у план стоїть рядком «Разові» під таблицею"`
        : ` title="стала ставка: сума × частка ÷ період. Не залежить ні від дати початку, ні від вікна усереднення"`
      }>${isOnce(f) ? DASH : fmtUAH(f.monthly_uah || 0)}</span>` },
    { key: "next", label: nextMonthLabel(), num: true,
      cell: (f) => `<span class="${(f.next_month_uah || 0) < 0 ? "t-warn" : ""}"
        title="скільки заходить у найближчому місяці плану">${fmtUAH(f.next_month_uah || 0)}</span>` },
    { key: "cadence", label: "Період",
      cell: (f) => CADENCE_LABEL[f.cadence] || esc(f.cadence) },
    { key: "from", label: "З", cell: (f) => esc(dayMonth(f.from_date)) },
    { key: "until", label: "До",
      cell: (f) => (f.until_date ? esc(dayMonth(f.until_date)) : "безстроково") },
    // Кнопки свої, а не з actionsCol: у потоку ЧОТИРИ дії, і дві з них
    // (⇗ зміна з дати, ⧉ копія) не є ні правкою, ні видаленням.
    { key: "acts", label: "", cls: "row-actions nowrap", cell: (f) => `
      <button class="sm" data-editflow="${f.id}" aria-label="Змінити потік ${esc(f.name)}">✎</button>
      <button class="sm" data-changeflow="${f.id}"
        aria-label="Змінити потік ${esc(f.name)} з дати: підвищення або завершення">⇗</button>
      <button class="sm quiet" data-copyflow="${f.id}"
        aria-label="Скопіювати потік ${esc(f.name)} у форму">⧉</button>
      <button class="sm warn" data-delflow="${f.id}"
        aria-label="Видалити потік ${esc(f.name)}">✕</button>` },
  ];
  const grid = (list, foot) => opsGrid({
    cols,
    rows: list,
    caption: "Потоки плану: назва, тип, сума, внесок у місяць, період і строк",
    foot,
  });
  // Підсумок рахується по ВСІХ рядках, а не по видимих. Числа завершених у
  // ньому й так нульові, тож жодного разу не зрушить — зате знаменник у
  // підвалі лишається один, а два різні знаменники на одному екрані в цьому
  // застосунку вже коштували розбіжності між карткою й плиткою.
  const head = live.length
    ? grid(live, flowsFootHTML(flows, provides))
    : empty("", "Усі джерела вже завершені — діючих не лишилось.");
  if (!done.length) return head;
  return head + disclosure("planFlowsDone",
    `Завершені — ${done.length}`, grid(done),
    "більше не платять: дата «до» позаду або разова виплата вже пройшла");
}

// Підсумок, який показує подвійне віднімання ЧИСЛОМ, а не прозою.
//
// Пастка така: «частка в портфель» уже відрізала те, що з'їдають витрати,
// тож завести ЩЕ й витратний потік на ту саму суму означає відняти її
// двічі. Попереджати про це абзацом марно — його читають один раз, а
// помиляються потім. Тут обидва віднімання стоять сусідніми рядками зі
// своїми числами: побачивши −16 000 і −16 000, важко не помітити.
//
// ЧОМУ ПІДСУМКИ СКЛАДАЮТЬСЯ З ОКРУГЛЕНИХ РЯДКІВ. Доти вони бралися з
// точних чисел, а колонка показує округлені — і Σ видимого давала 224 520
// проти 224 521 у підсумку. Ланцюг теж не закривався: 224 521 − 193 130 =
// 31 391, а «План дає» казав 31 390. Число, поставлене під колонкою, це
// обіцянка, що воно і є її сума; тримати цю обіцянку важливіше за зайву
// точність у числі, яке однаково показується цілими гривнями.
//
// Гривня округлення мусить десь осісти, і осідає вона в «Не доходить до
// портфеля»: це єдине похідне число підвалу, своєї колонки в нього немає,
// тож скласти його очима нізвідки. А «У середньому за 12 міс» лишається з
// плитки — те саме число з тим самим іменем не має права мати два значення
// на одній сторінці.
function flowsFootHTML(flows, provides) {
  const r0 = (v) => Math.round(v || 0);
  const inc = flows.filter((f) => f.kind === "income");
  // Разові рахуються окремо від щомісячних, бо це різні за природою гроші:
  // 11 177 ₴ премії не можна складати з 89 412 ₴ зарплати й називати суму
  // місячним доходом.
  const regular = inc.filter((f) => !isOnce(f));
  const once = inc.filter(isOnce);

  const monthlyGross = regular.reduce((a, f) => a + r0(f.monthly_gross_uah), 0);
  const onceTotal = once.reduce((a, f) => a + r0(f.amount_uah), 0);
  const onceAvg = once.reduce((a, f) => a + r0(f.provides_uah), 0);
  const monthly = regular.reduce((a, f) => a + r0(f.monthly_uah), 0);
  const exp = flows.filter((f) => f.kind === "expense").reduce((a, f) => a + r0(f.monthly_uah), 0);
  const nextMon = flows.reduce((a, f) => a + r0(f.next_month_uah), 0);
  const avg12 = r0(provides);
  // Залишок: ланцюг «щомісячний дохід − не доходить = щомісяця» закривається
  // за побудовою, а не за щасливим збігом округлень.
  const cut = monthlyGross + exp - monthly;

  // nowrap на числах: колонки вузькі за вмістом рядків, і без нього
  // «49 563 ₴» переносило гривню на другий рядок — підсумок, заради якого
  // таблиця й має tfoot, читався найгірше з усього.
  const num = (v) => `<td class="num nowrap">${fmtUAH(v)}</td>`;
  const blank = (n) => (n > 0 ? `<td colspan="${n}"></td>` : "");
  // 11 колонок; число стоїть рівно під своєю. cols — індекси колонок із
  // числами (3 = «повне ₴/міс», 5 = «щомісяця», 6 = найближчий місяць).
  const at = (label, cells, cls = "") => {
    let html = `<tr class="${cls}"><td colspan="3">${label}</td>`, prev = 3;
    for (const [col, val] of cells) {
      html += blank(col - prev) + num(val);
      prev = col + 1;
    }
    return `${html}${blank(11 - prev)}</tr>`;
  };

  const onceNote = onceAvg
    ? ` <span class="muted fine-xs">→ +${fmtUAH(onceAvg)}/міс у середньому</span>`
    : ` <span class="muted fine-xs">поза вікном ${PROVIDES_MONTHS} міс</span>`;

  // Віддаємо самі рядки: обгортку <tfoot> ставить грід (grid.js).
  return `
    ${at("Щомісячний дохід", [[3, monthlyGross]], "muted")}
    ${once.length ? at(`Разові${onceNote}`, [[3, onceTotal]], "muted") : ""}
    ${cut ? at("Не доходить до портфеля (частка)", [[5, -cut]], "muted") : ""}
    ${exp ? at("Витратні потоки", [[5, exp]], "muted") : ""}
    ${at("План дає", [[5, monthly + exp], [6, nextMon]], "tot")}
    ${at(`У середньому за ${PROVIDES_MONTHS} міс`, [[5, avg12]], "muted")}`;
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
  // Дозвіл — саме тут, а не серед косметики: від нього залежить база
  // обох стель наповнення, тобто «нічого не змінилось» у цьому рядку
  // означало б сховати правку, яка рухає гроші.
  ["uses", "може йти", (r) => usesText(r.uses)],
  ["growth_pct", "індексація", (r) => pct(r.growth_pct)],
];

// Що змінилось порівняно з попередньою ревізією ТОГО САМОГО потоку.
// Валюта порівнюється разом із сумою: «1 700 → 2 000» без неї брехало б,
// якби змінилась саме валюта.
// Дозвіл рядком — і для порівняння ревізій, і для підпису. Порожній
// перелік у старій ревізії читається як «будь-куди»: до 0041 обмежень не
// було ні в кого.
export function usesText(uses) {
  if (!usesNarrowed(uses)) return "будь-куди";
  const on = new Set(uses);
  return PLAN_USES.filter(([k]) => on.has(k)).map(([, label]) => label).join(", ");
}

function revDiff(cur, prev) {
  if (!prev) return "";
  return REV_FIELDS.map(([key, label, show]) => {
    // Масив дозволу порівнюється ТЕКСТОМ: два різні масиви з тим самим
    // вмістом ніколи не рівні за ===, і рядок «може йти: резерв →
    // резерв» вилазив би на кожній правці суми.
    if (key === "uses") {
      const a2 = usesText(cur.uses), b2 = usesText(prev.uses);
      return a2 === b2 ? "" : `${label}: ${esc(b2)} → <b>${esc(a2)}</b>`;
    }
    const a = key === "amount" ? `${cur[key]}|${cur.currency}` : cur[key];
    const b = key === "amount" ? `${prev[key]}|${prev.currency}` : prev[key];
    return a === b ? "" : `${label}: ${esc(show(prev))} → <b>${esc(show(cur))}</b>`;
  }).filter(Boolean).join(" · ");
}

export function revisionsHTML(revs) {
  if (!revs.length) return "";
  // Журнал приходить найновішими згори, а «попередня ревізія» — це та, що
  // йде в списку ПІСЛЯ поточної для того самого потоку.
  const body = opsGrid({
    cols: [
      { key: "at", label: "Коли", cls: "nowrap",
        cell: (r) => esc(dayMonth(r.at.slice(0, 10))) },
      { key: "name", label: "Потік", cell: (r) => esc(r.name) },
      { key: "what", label: "Що змінилось", cell: (r, i) => {
        const prev = revs[i + 1];
        const what = r.op === "update" ? revDiff(r.flow, prev && prev.flow) : "";
        return what || `<span class="muted">${REV_OP[r.op] || esc(r.op)}</span>`;
      } },
    ],
    rows: revs,
    caption: "Історія правок плану: коли, який потік, що змінилось",
  }) + `<div class="sub-xs">Саме з цього журналу картка «План проти факту» читає, скільки план
      давав у минулому місяці. Тому правка суми на місці більше не переписує минуле:
      місяці до неї лишаються з тією сумою, яка діяла тоді.</div>`;
  return disclosure("planRevisions", "Історія правок", body,
    `${revs.length} ${plural(revs.length, "запис", "записи", "записів")}`);
}


export function wirePlanFlows(ctx, main, flows) {
  const byId = new Map(flows.map((f) => [String(f.id), f]));
  const form = main.querySelector("#planFlowForm");
  wireKind(form);

  onSubmit(ctx, form, (f) => ({ path: "plan/flows", body: flowBody(f), msg: "Потік додано" }));

  onDelete(ctx, main, "[data-delflow]", (b) => ({
    path: "plan/flows/" + b.dataset.delflow,
    confirm: "Видалити цей потік?",
    msg: "Потік видалено",
  }));

  main.querySelectorAll("[data-editflow]").forEach((b) => b.addEventListener("click", () => {
    const f = byId.get(b.dataset.editflow);
    if (!f) return;
    openEdit(ctx, {
      title: `Правка потоку «${esc(f.name)}»`,
      fields: flowFields(ctx.npfAccounts, flowFormValues(f)),
      wire: wireKind,
    },
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
    fillForm(form, { ...v, ...usesCheckValues(v.uses), from_date: today() });
    if (v.until_date || +v.growth_pct !== 0 || +v.invest_pct !== 100 || v.note
      || usesNarrowed(v.uses)) {
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
