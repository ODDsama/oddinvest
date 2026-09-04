// «Ціна покупки»: що ця витрата коштує насправді.
//
// ЄДИНЕ МІСЦЕ, ДЕ ДВА КОНТУРИ ДАЮТЬ ОДНУ ВІДПОВІДЬ. Портфельний умів
// сказати, що буде, якщо КУПИТИ ПАПІР; борговий знав свою дату виходу з
// ліміту. «Скільки мені коштує цей холодильник» не належало жодному з
// них — і тому не питалось ніде.
//
// Жодного числа тут не рахується: усе приходить із POST /api/spend, тобто
// з того самого документа стану, тільки над портфелем, у якому витрата
// вже сталась. «Зараз» береться з ctx.summary, і обидві сторони міряні
// однією лінійкою — на це є тест, який вимагає, щоб ПОРОЖНЯ витрата дала
// документ, байт у байт рівний /api/summary.
//
// ЦЕ НЕ ЗАПИС. Сторінка нічого не зберігає й не пропонує зберегти:
// справді витрачені гроші заносять рухом у «Грошах» або в «Боргах». Якби
// відповідь ще й писалась, у застосунку зʼявилось би два журнали одних
// грошей — і питання «який справжній» не мало б відповіді.
//
// ЧОГО ТУТ СВІДОМО НЕМАЄ:
//
//   Вироку. Ні «дорого», ні «краще не бери». Застосунок не знає, навіщо
//   тобі ця річ, — він знає лише, від чого ці гроші відмовляються. Той
//   самий принцип, що біля порога перекладання й перцентиля курсу.
//
//   Очікуваної вартості покупки на картку. Вона залежить від того, чи
//   закриєш баланс до розрахункової дати, а цього застосунок знати не
//   може. Тому показується умовна пара: до дати — нуль, після — ставка.
//
//   Руху черги погашення й маршруту грошей. /api/payoff і /api/route
//   читають борги власними шляхами й гіпотези не бачать; обіцяти їхній
//   зсув означало б обіцяти те, чого немає.

import { esc, uah2 as fmtUAH } from "../format.js";
import { empty } from "../components.js";
import { infoBtn } from "../info.js";
import { money, date, note, selectOf, whenKind, wireKind, formHTML } from "../fields.js";
import { refSelect, wireRefs } from "../refs.js";
import { delta, dateDelta } from "./impact.js";

const asPct = (v) => `${(v || 0).toFixed(2)}%`;
const asMonths = (v) => `${(v || 0).toFixed(1)} міс.`;

const PAY = [
  ["cash", "з рахунку"],
  ["card", "на картку"],
  ["installment", "у розстрочку"],
];

const PREPAY = {
  card: "При достроковому комісія лишається на картці",
  cancel: "При достроковому комісія скасовується",
  keep: "При достроковому комісія лишається — банк її не скасовує",
  free: "Комісії за обслуговування тут немає",
  unknown: "Що буде з комісією при достроковому — в умовах не задано",
};

function formBlock(ctx, debts) {
  // Перемикач названий kind, а не pay, навмисно: whenKind/wireKind із
  // кита слухають саме це імʼя, і заводити другий перемикач заради іншої
  // назви поля означало б скопіювати проводку. У запит воно йде як pay.
  return formHTML({
    id: "spendForm",
    submit: "Порахувати",
    fields: [
      money("amount", "Скільки", { required: true }),
      refSelect(ctx, { name: "currency", ref: "currency", label: "Валюта", value: "UAH" }),
      date("date", "Коли", { value: "" }),
      selectOf("kind", "Як платиш", PAY, "cash"),
      whenKind(["cash"], refSelect(ctx, { name: "broker", ref: "broker", label: "З якого рахунку" })),
      whenKind(["card"], refSelect(ctx, {
        name: "card_id", ref: "debt-card", label: "На яку картку", items: debts,
      })),
      whenKind(["installment"], [
        money("principal", "Тіло розстрочки", { ph: "= сумі покупки" }),
        selectOf("payments_total", "Платежів", [
          ["3", "3"], ["4", "4"], ["6", "6"], ["10", "10"], ["12", "12"], ["24", "24"],
        ], "10"),
        money("fee_month_pct", "Комісія, %/міс", { ph: "1.99" }),
      ].join("")),
      note("note", "Що саме", { ph: "холодильник" }),
    ],
  });
}

/** Що ці гроші заробили б, якби пішли в найкраще доступне зараз. */
function alternativeBlock(cost) {
  if (!cost.alternative) {
    return cost.alternative_why
      ? `<div class="muted fine">${esc(cost.alternative_why)}.</div>` : "";
  }
  const a = cost.alternative;
  const basis = a.yield_basis === "estimate" ? "оцінка" : "обіцянка";
  return `<div class="sub mb-sm">Від чого ці гроші відмовляються</div>
    <div class="pv-row"><span>За рік у «${esc(a.label)}»</span>
      <span><b>${fmtUAH(a.year_uah)}</b>
        <span class="muted fine-xs">· ${asPct(a.real_pct)} реальних${
  a.nominal_pct ? ` · ${asPct(a.nominal_pct)} номінальних` : ""} · ${basis}</span></span></div>
    <div class="muted fine">Це верхній рядок «Що купити», який справді можна взяти
      зараз, і взятий він тим самим порядком, який ти задав сам. Ставка РЕАЛЬНА:
      після податку й знецінення, тобто в сьогоднішній купівельній
      спроможності.</div>`;
}

/** Чесна ціна кредиту — умовна для картки, порахована для розстрочки. */
function creditBlock(cost) {
  if (!cost.credit) {
    return cost.credit_why ? `<div class="muted fine">${esc(cost.credit_why)}.</div>` : "";
  }
  const c = cost.credit;
  if (c.basis === "compound") {
    const grace = c.due_date
      ? `Повернеш до <b>${esc(c.due_date)}</b>${
        c.days_to_due ? ` (${c.days_to_due} дн.)` : ""} — відсотків не буде.`
      : "Доки триває пільговий цикл, відсотків немає.";
    return `<div class="sub mb-sm mt-lg">Чого коштує сам борг</div>
      <div class="pv-row"><span>Якщо не повернеш до розрахункової дати</span>
        <span><b>${asPct(c.apr_pct)}</b>
          <span class="muted fine-xs">річних, місячна капіталізація</span></span></div>
      <div class="muted fine">${grace} Одного числа тут немає навмисно:
        застосунок не може знати, чи закриєш ти баланс вчасно, а вгадана
        ймовірність зробила б відповідь переконливою й хибною.</div>`;
  }
  return `<div class="sub mb-sm mt-lg">Чого коштує сам борг</div>
    <div class="pv-row"><span>Справжня ставка розстрочки</span>
      <span><b>${asPct(c.apr_pct)}</b>
        <span class="muted fine-xs">річних, за графіком платежів</span></span></div>
    ${c.extra_uah ? `<div class="pv-row"><span>Комісій за весь строк</span>
      <span><b>${fmtUAH(c.extra_uah)}</b></span></div>` : ""}
    <div class="muted fine">«0%» у вітрині означає нуль ВІДСОТКІВ, а не нуль
      грошей: комісія за обслуговування — теж ціна, і саме вона тут переведена
      в річну ставку. ${esc(PREPAY[c.prepay] || "")}.</div>`;
}

/** Наслідки — відніманням «стане» проти «зараз». */
function impactBlock(ctx, after) {
  const before = ctx.summary || {};
  const a = before.reserve || {}, b = after.reserve || {};
  const exitA = ((before.debt || {}).exit) || null;
  const exitB = ((after.debt || {}).exit) || null;
  const indA = before.independence || {}, indB = after.independence || {};

  const exitRows = exitA && exitB
    ? dateDelta("Вихід із ліміту", exitA.exit_by, exitB.exit_by)
      + delta("Треба звільняти на місяць", exitA.need_per_month_uah,
        exitB.need_per_month_uah, fmtUAH)
      + delta("Стеля витрат на місяць", exitA.spend_cap_uah, exitB.spend_cap_uah, fmtUAH)
    : `<div class="muted fine">Дати виходу з ліміту не задано жодній картці —
        рухати нічого. Задається вона в
        <a class="lnk" href="#/plan/debts/main">«Боргах»</a>.</div>`;

  return `<div class="sub mb-sm mt-lg">Що це робить із планом</div>
    ${delta("Капітал", before.capital_uah, after.capital_uah, fmtUAH)}
    ${delta("Чистий капітал", before.net_worth_uah, after.net_worth_uah, fmtUAH)}
    ${a.target_months || b.target_months
    ? delta("Подушка", a.months, b.months, asMonths,
      a.target_months ? ` <span class="muted fine-xs">· ціль ${a.target_months} міс.</span>` : "")
    : ""}
    ${indA.date || indB.date ? dateDelta("Точка незалежності", indA.date, indB.date) : ""}
    ${delta("Місячна ціль", before.month_target_uah, after.month_target_uah, fmtUAH)}
    ${exitRows}`;
}

/** Картка відповіді. res — тіло /api/spend. */
export function spendResultHTML(ctx, res) {
  if (!res) return "";
  const cost = res.cost || {};
  return `<div class="card">
    <h2>Що ця покупка коштує ${infoBtn("spendPrice")}</h2>
    ${alternativeBlock(cost)}
    ${creditBlock(cost)}
    ${impactBlock(ctx, res.after || {})}
    <div class="muted fine mt-lg">Нічого з цього не записано: це питання, а не
      операція. Витрачені гроші заносять рухом у
      <a class="lnk" href="#/money/all/balances">«Грошах»</a> або в
      <a class="lnk" href="#/plan/debts/main">«Боргах»</a>. Черга погашення й
      маршрут грошей тут не рухаються — вони читають борги власним шляхом і
      цієї гіпотези не бачать.</div>
  </div>`;
}

/** Запит. Через raw, а не через ctx.store: це ЧИТАННЯ, яке просто ходить
 *  методом POST, і скидати ним кеш усього застосунку означало б
 *  перемальовувати розділ на кожен натиск клавіші. Той самий довід і той
 *  самий прийом, що в fetchWhatIf (buy-plan.js). */
async function fetchSpend(ctx, body, signal) {
  const resp = await ctx.store.raw("spend", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    ...(signal ? { signal } : {}),
  });
  if (!resp.ok) throw new Error((await resp.text()).slice(0, 300));
  return resp.json();
}

function bodyOf(form) {
  const v = (n) => (form[n] ? String(form[n].value || "").trim() : "");
  const pay = v("kind") || "cash";
  const out = {
    amount: v("amount"), currency: v("currency") || "UAH",
    date: v("date"), pay, note: v("note"),
  };
  if (pay === "cash") out.broker = v("broker");
  if (pay === "card") out.card_id = Number(v("card_id")) || 0;
  if (pay === "installment") {
    out.installment = {
      name: v("note") || "розстрочка",
      kind: "installment",
      currency: out.currency,
      principal: v("principal") || v("amount"),
      payments_total: v("payments_total"),
      fee_month_pct: v("fee_month_pct"),
    };
  }
  return out;
}

export async function spend(ctx, main) {
  const debts = await ctx.soft("debts", []);
  main.innerHTML = `<div class="card">
      <h2>Ціна покупки ${infoBtn("spendPrice")}</h2>
      <div class="note">Назви суму — і побач, від чого ці гроші відмовляються,
        чого коштує сам борг і що це робить із твоїм планом. Нічого не
        записується: це питання, а не операція.</div>
      ${formBlock(ctx, debts)}
    </div>
    <div data-spend-out></div>`;

  const form = main.querySelector("#spendForm");
  const out = main.querySelector("[data-spend-out]");
  wireRefs(main);
  wireKind(form);

  let inflight = null;
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (inflight) inflight.abort();
    inflight = new AbortController();
    out.innerHTML = `<div class="card">${empty("", "Рахуємо…")}</div>`;
    try {
      const res = await fetchSpend(ctx, bodyOf(form), inflight.signal);
      out.innerHTML = spendResultHTML(ctx, res);
    } catch (err) {
      if (err && err.name === "AbortError") return;
      out.innerHTML = `<div class="card">${empty("", esc(String(err.message || err)))}</div>`;
    }
  });
}
