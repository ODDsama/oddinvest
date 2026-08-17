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

function flowFields(accounts, values = null) {
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
    || +v.invest_pct !== 100 || v.dest || v.note);
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
      <label title="Витрата з призначенням у пенсійний — це не зʼїдені гроші, а переказ:
        ліквідне худне, пенсійний капітал росте. Без призначення проєкція вважала б їх витраченими">
        Призначення<select name="dest">${sel("dest", destOptions(accounts), "")}</select></label>
      <label>Нотатка<input name="note" ${val("note")}></label>`,
    "до дати, індексація, частка, призначення, нотатка", !!nonDefault)}`;
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
    dest: f.dest ? f.dest.value : "",
    note: f.note.value,
  });
}

export function planFlowFormHTML(ctx) {
  return `<form id="planFlowForm">${flowFields((ctx || {}).npfAccounts)}
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
      <td>${esc(f.name)}${f.note ? ` <span class="muted fine-xs">${esc(f.note)}</span>` : ""}${
  f.dest ? `<div class="fine-xs"><span class="pill pill-npf">НПФ</span> переказ, а не витрата</div>` : ""}</td>
      <td><span class="pill ${f.kind === "income" ? "coupon" : "redemption"}">${
  f.kind === "income" ? "дохід" : "витрата"}</span></td>
      <td class="num">${fmtMoney(f.amount)}</td>
      <td class="num${fullCell(f) < 0 ? " t-warn" : ""}">${fmtUAH(fullCell(f))}${isOnce(f)
    ? `<div class="fine-xs muted">разова</div>` : ""}</td>
      <td class="num">${pct(f.invest_pct)}</td>
      <td class="num${(f.monthly_uah || 0) < 0 ? " t-warn" : ""}"${isOnce(f)
    ? ` title="разова виплата ${esc(dayMonth(f.from_date))} не дає нічого щомісяця — її внесок у план стоїть рядком «Разові» під таблицею"`
    : ""}>${isOnce(f) ? DASH : fmtUAH(f.monthly_uah || 0)}</td>
      <td class="num${(f.next_month_uah || 0) < 0 ? " t-warn" : ""}">${
  fmtUAH(f.next_month_uah || 0)}</td>
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
  // Порядок колонок — це арифметика зліва направо: 89 412 ₴ × 10.0% = 8 941 ₴.
  // Доти «повного» числа не було видно ніде, тож перевірити рядок очима було
  // нічим — а підсумок «Доходи» під таблицею складався саме з нього й через
  // це читався як помилка розрахунку (224 521 під колонкою, що дає 31 390).
  //
  // Дві гривневі колонки замість однієї — бо питань справді два, і разова
  // виплата відповідає на них по-різному: щомісяця вона не дає нічого,
  // а в свій місяць приносить усю суму.
  return `<div class="table-scroll"><table><thead><tr>
    <th scope="col">Назва</th><th scope="col">Тип</th><th scope="col" class="num">Сума</th>
    <th scope="col" class="num" title="сума за сьогоднішнім курсом, до «частки в портфель»">повне ₴/міс</th>
    <th scope="col" class="num">У портфель</th>
    <th scope="col" class="num" title="стала ставка: сума × частка ÷ період. Не залежить ні від дати початку, ні від вікна усереднення">щомісяця</th>
    <th scope="col" class="num" title="скільки заходить у найближчому місяці плану">${
  esc(nextMonthLabel())}</th>
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

  return `<tfoot>
    ${at("Щомісячний дохід", [[3, monthlyGross]], "muted")}
    ${once.length ? at(`Разові${onceNote}`, [[3, onceTotal]], "muted") : ""}
    ${cut ? at("Не доходить до портфеля (частка)", [[5, -cut]], "muted") : ""}
    ${exp ? at("Витратні потоки", [[5, exp]], "muted") : ""}
    ${at("План дає", [[5, monthly + exp], [6, nextMon]], "tot")}
    ${at(`У середньому за ${PROVIDES_MONTHS} міс`, [[5, avg12]], "muted")}
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

export function revisionsHTML(revs) {
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


export function wirePlanFlows(ctx, main, flows) {
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
    openEdit(ctx, { title: `Правка потоку «${esc(f.name)}»`, fields: flowFields(ctx.npfAccounts, flowFormValues(f)) },
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
