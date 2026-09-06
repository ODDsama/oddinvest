// Планові витрати: вирішені разові гроші з датою й станом «сплачено».
//
// Окремою сторінкою від «Що заходить», і довід той самий, що розводить
// таблиці (міграція 0056): потік — це РИТМ життя (комуналка, оренда),
// однаковий щомісяця й без стану виконання. Планова витрата — ПОДІЯ:
// «замінити котел у листопаді», «страховка до 14-го». Спільний список
// ховав би разові рішення серед постійних рядків саме тоді, коли по них
// приходять.
//
// ЖОДНОГО ВЛАСНОГО ЧИСЛА ПРО ГРОШІ. «Скільки тисне цього місяця» береться
// з month_plan.planned_uah у зведенні; складати суми рядків тут означало б
// друге означення того, що вже рахує buildMonthPlan, — і два числа на
// одному екрані розійшлися б на першій же простроченій витраті, бо правило
// «прострочена падає в поточний місяць» живе на бекенді.
//
// Дні до дати браузер таки рахує сам, і це не виняток із правила: календар
// — не арифметика грошей, і dayMonth поруч робить рівно те саме.

import { esc, today, dayMonth, uah0 as fmtUAH, money as fmtMoney, plural } from "../format.js";
import { empty } from "../components.js";
import { apply } from "../forms.js";
import { disclosure } from "../disclosure.js";
import {
  money as moneyField, text as textField, date as dateField,
  note as noteField, selectOf, formHTML,
} from "../fields.js";
import { refSelect } from "../refs.js";
import { opsGrid, actionsCol } from "../grid.js";
import { wireCrud } from "../crud.js";
import { routeFor } from "../routes.js";

const PAID_FROM = [
  ["card", "з картки — побут"],
  ["plan", "з портфельних — менше піде в папери"],
];
const PAID_FROM_SHORT = { card: "з картки", plan: "з портфельних" };

// ---------- поля форми ----------
//
// ОДНА функція малює і форму додавання, і тіло модалки правки — те саме
// правило, що в потоках: два списки полів розійшлися б на першій же зміні,
// і розійшлися б тихо, бо PUT тут повна заміна рядка.
function expenseFields(_ctx, row = null) {
  const v = row || {};
  const val = (k, d = "") => (v[k] != null ? String(v[k]) : d);
  const amount = (v.amount || {}).amount || "";
  const currency = (v.amount || {}).currency || "UAH";
  // Правка мусить показати те, що правиться: якщо у витраті є щось, крім
  // типових значень, «Ще» розгортається одразу — інакше здавалося б, що
  // позначки «сплачено» в ній немає, і перше ж збереження мовчки лишило б
  // її незміненою.
  const nonDefault = Boolean(row && (v.paid_date || v.place || v.note));
  return [
    textField("name", "Назва", { ph: "Котел", required: true, value: val("name") }),
    moneyField("amount", "Сума", { ph: "30000.00", required: true, value: amount }),
    refSelect(null, { name: "currency", ref: "currency", value: currency }),
    dateField("due_date", "Коли платити", {
      required: true, value: val("due_date", today()),
      title: "День, коли гроші мають піти. Дата обовʼязкова: без неї витрата "
        + "не тисне ні на який місяць",
    }),
    // ГОЛОВНЕ ПОЛЕ СТОРІНКИ, і воно не технічне. Гроші течуть двома
    // контурами, і рядок тисне рівно на один: віднявши його з обох,
    // застосунок забрав би вдвічі більше, ніж витрата коштує.
    selectOf("paid_from", "З яких грошей", PAID_FROM, val("paid_from", "card"), {
      title: "«З картки» зменшує стелю витрат і таблицю боргу на горизонті. "
        + "«З портфельних» зменшує гроші місяця, стелі подушки й цілей та ноги маршруту",
    }),
    disclosure("planExpenseMore", "Ще", [
      dateField("paid_date", "Сплачено", {
        value: val("paid_date"),
        title: "Порожньо = ще винна. Датою, а не галочкою: сплачене до звірки "
          + "картки вже сидить у її балансі",
      }),
      textField("place", "Де", { ph: "ПУМБ", value: val("place") }),
      noteField("note", "Нотатка", { value: val("note") }),
    ].join(""), "", nonDefault),
  ].join("");
}

function expenseBody(f) {
  return {
    name: f.name.value.trim(),
    amount: f.amount.value.trim(),
    currency: f.currency.value,
    due_date: f.due_date.value,
    paid_from: f.paid_from.value,
    paid_date: f.paid_date.value,
    place: f.place.value.trim(),
    note: f.note.value.trim(),
  };
}

// ---------- стан рядка ----------

/** Скільки днів між сьогодні й датою. Календар, а не гроші: рядок
 *  «YYYY-MM-DD» розбирається як UTC-північ, тож зсув цілими днями не
 *  залежить від часового поясу (той самий прийом, що shiftDays у потоках). */
function daysTo(iso) {
  const a = new Date(today() + "T00:00:00Z");
  const b = new Date(iso + "T00:00:00Z");
  return Math.round((b - a) / 86400000);
}

/** Пігулка стану: сплачено / прострочено / скільки лишилось.
 *
 *  Три різні твердження, а не одне з відтінками: сплачена вибула з усіх
 *  розрахунків, прострочена тисне на ПОТОЧНИЙ місяць замість свого, а
 *  майбутня чекає своєї черги. */
function stateCell(e) {
  if (e.paid_date) {
    return `<span class="pill coupon">сплачено ${esc(dayMonth(e.paid_date))}</span>`;
  }
  const d = daysTo(e.due_date);
  if (d < 0) {
    const n = -d;
    return `<span class="pill redemption t-danger"
      title="дата минула, а гроші не пішли — витрата тисне на ПОТОЧНИЙ місяць,
        а не зникає разом зі своєю датою">прострочено ${n} ${
  plural(n, "день", "дні", "днів")}</span>`;
  }
  if (d === 0) return `<span class="pill early">сьогодні</span>`;
  return `<span class="muted">через ${d} ${plural(d, "день", "дні", "днів")}</span>`;
}

// ---------- список ----------

export function planExpensesListHTML(exps) {
  if (!exps.length) {
    return empty("", "Планових витрат ще немає — першу додасть форма нижче. "
      + "Сюди йдуть разові рішення з датою («котел у листопаді», «страховка до 14-го»), "
      + "а не постійні платежі: ті живуть у «Що заходить» витратним потоком.");
  }
  // Несплачені попереду сплачених, далі за датою. Сплачена — вже історія,
  // і місце їй під тим, що ще вимагає дії.
  const rows = exps.slice().sort((a, b) => {
    const pa = a.paid_date ? 1 : 0;
    const pb = b.paid_date ? 1 : 0;
    if (pa !== pb) return pa - pb;
    return a.due_date < b.due_date ? -1 : a.due_date > b.due_date ? 1 : 0;
  });
  const cols = [
    { key: "name", label: "Назва", cell: (e) => esc(e.name)
      + (e.place ? ` <span class="muted fine-xs">${esc(e.place)}</span>` : "")
      + (e.note ? `<div class="fine-xs muted">${esc(e.note)}</div>` : "") },
    { key: "amount", label: "Сума", num: true, cell: (e) => fmtMoney(e.amount) },
    { key: "due", label: "Коли", cell: (e) => esc(dayMonth(e.due_date)) },
    { key: "from", label: "З яких грошей",
      cell: (e) => `<span class="pill ${e.paid_from === "plan" ? "early" : "recv"}">${
        esc(PAID_FROM_SHORT[e.paid_from] || e.paid_from)}</span>` },
    { key: "state", label: "Стан", cell: stateCell },
    // Кнопки свої, а не самий actionsCol: у витрати ТРИ дії, і «сплачено»
    // не є ні правкою, ні видаленням. Значок ставиться лише несплаченій —
    // на сплаченій він означав би «сплатити ще раз».
    { key: "acts", label: "", cls: "row-actions nowrap", cell: (e) =>
      (e.paid_date ? "" : `<button class="sm" data-paidexp="${e.id}"
        title="Позначити сплаченою сьогодні"
        aria-label="Позначити «${esc(e.name)}» сплаченою">₴</button>`)
      + actionsCol("plan-expenses", {
        label: (r) => "планову витрату «" + r.name + "»",
      }).cell(e) },
  ];
  return opsGrid({
    cols, rows, caption: "Планові витрати: назва, сума, дата, контур і стан",
  });
}

export function planExpenseFormHTML(ctx) {
  return formHTML({
    id: "planExpenseForm", submit: "Додати", fields: [expenseFields(ctx)],
  });
}

// ---------- підсумок ----------
//
// Число тут ОДНЕ й воно зі зведення: скільки планові витрати забирають у
// грошей місяця. Карткові в нього не входять і не мають — вони зменшують
// стелю витрат, і своє число показує сторінка «Борги».
function footHTML(ctx) {
  const mp = (ctx.summary || {}).month_plan || {};
  const planned = mp.planned_uah || 0;
  if (!planned) return "";
  return `<div class="note">Цього місяця планові витрати з <b>портфельних</b> грошей
    забирають <b>${fmtUAH(planned)}</b> — саме на стільки менший «план місяця»
    і саме на стільки худіші ноги в
    <a class="lnk" href="${routeFor("plan/route")}">Маршруті грошей</a>.
    Витрати з картки сюди не входять: вони зменшують «скільки можна витрачати» в
    <a class="lnk" href="${routeFor("plan/debts/main")}">Боргах</a>.</div>`;
}

// ЧОГО ЗАСТОСУНОК НЕ БАЧИТЬ — обовʼязковий блок, а не косметика. Усі
// чотири пастки ведуть до того самого: та сама витрата віднімається двічі,
// і жоден екран цього не назве.
function pitfallsHTML() {
  return disclosure("planExpensePitfalls", "Де тут можна відняти двічі", `
    <ul class="fine">
      <li><b>Постійний платіж, записаний ще й потоком.</b> Комуналка й оренда — це
        «Що заходить» витратним потоком. Записана ще й сюди, вона віднімається двічі.</li>
      <li><b>Платіж за боргом.</b> Обовʼязкові платежі за розстрочками й мінімалку
        застосунок віднімає сам — їх не треба заводити плановою витратою.</li>
      <li><b>Уже закладене в місячні витрати.</b> Якщо котел уже сидить у числі
        «витрати на місяць» у «Політиці», планова витрата відніме його вдруге.
        Довести це застосунок не може: він бачить лише підсумок.</li>
      <li><b>Щойно сплачена витрата з картки.</b> З розрахунку вона зникає тієї ж
        миті, але сама покупка ще тиждень-два «догорає» у ВИМІРЯНОМУ спаленні
        картки між двома звірками, тож стеля витрат просяде вдруге на один період.
        Це минається саме — але виглядає як помилка розрахунку, і краще знати
        заздалегідь.</li>
    </ul>`);
}

// ---------- сторінка ----------

export async function planExpenses(ctx, main) {
  const exps = await ctx.soft("plan/expenses", []);
  main.innerHTML = `
    <div class="card">
      <h2 class="card-head"><span>Планові витрати</span></h2>
      <div class="note">Гроші, які вже вирішено витратити, але ще не витрачено. Кожна
        витрата тисне рівно на ОДИН контур: «з картки» зменшує «скільки можна
        витрачати», «з портфельних» — гроші місяця й ноги маршруту. Прострочена не
        зникає разом зі своєю датою: вона переходить на поточний місяць і чекає
        позначки «сплачено».</div>
      ${planExpensesListHTML(exps)}
      ${footHTML(ctx)}
      ${pitfallsHTML()}
      ${planExpenseFormHTML(ctx)}
    </div>`;

  wireCrud(ctx, main, {
    resource: "plan-expenses",
    createPath: "plan/expenses",
    path: (id) => "plan/expenses/" + id,
    form: "#planExpenseForm",
    fields: expenseFields,
    body: expenseBody,
    rows: exps,
    title: "Планова витрата",
    msg: { add: "Планову витрату додано", edit: "Збережено" },
  });

  // «Сплачено» — ПРАВКА рядка, а не своя ручка на бекенді: PUT тут повна
  // заміна, тож тіло збирається з самого рядка й дістає лише дату. Своя
  // ручка була б другим способом написати те, що вже вміє правка, а
  // різниця між «не надіслали paid_date» і «спорожнили paid_date» у ній
  // зникла б — тобто зняти помилкову позначку стало б неможливо. Знімає
  // її та сама правка (✎), де поле «Сплачено» стоїть під «Ще».
  const byId = new Map(exps.map((e) => [String(e.id), e]));
  main.querySelectorAll("[data-paidexp]").forEach((b) => b.addEventListener("click", () => {
    const e = byId.get(b.dataset.paidexp);
    if (!e) { ctx.toast("Запис не знайдено — онови сторінку", false); return; }
    apply(ctx, {
      method: "PUT", path: "plan/expenses/" + e.id,
      body: {
        name: e.name, amount: (e.amount || {}).amount, currency: (e.amount || {}).currency,
        due_date: e.due_date, paid_from: e.paid_from, paid_date: today(),
        place: e.place, note: e.note,
      },
    }, `«${e.name}» — сплачено`);
  }));
}
