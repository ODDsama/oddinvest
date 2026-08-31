// Борги: картка з пільговим циклом, розстрочки й коли це скінчиться.
//
// ЧОМУ ЦЕ ОКРЕМА СТОРІНКА, А НЕ РЯДОК «ПОРТФЕЛЯ». Портфель — це капітал, а
// борг має протилежний знак. Поставити його рядком поруч із ОВДП означало б
// або порахувати зобовʼязання майном, або завести всередині списку виняток,
// який кожен наступний читач мусив би памʼятати.
//
// ГОЛОВНЕ ЧИСЛО СТОРІНКИ — «ВІЛЬНО», а не борг і не ліміт. Питання, з яким
// сюди приходять, звучить так: «на картці плюс — скільки з нього можна
// витратити, не потрапивши на відсотки». Відповідь = баланс − сума до
// сплати − найближчі частини розстрочок, і вона буває відʼємною тоді, коли
// картка ще виглядає повною.
//
// ДВА ПОРОГИ ПОРУЧ, І ЖОДЕН НЕ ГОЛОВНІШИЙ. Мінімалка рятує від штрафу й
// підвищеної ставки; повна сума — від відсотків узагалі. Показати один
// означало б або лякати там, де можна заплатити менше, або мовчати про те,
// що менше коштує грошей.
//
// ЖОДНОГО ЧИСЛА ТУТ НЕ РАХУЄТЬСЯ. Ефективна ставка, пороги, «вільно», дата
// свободи й порівняння стратегій приходять готовими з /api/payoff.
// Друга копія в браузері вже двічі закінчувалась різними числами на одному
// екрані (CLAUDE.md §5).

import { esc, uah2 as fmtUAH, money as fmtMoney, pct, plural, monthYear } from "../format.js";
import { infoBtn } from "../info.js";
import { empty } from "../components.js";
import { opsGrid, actionsCol } from "../grid.js";
import {
  money as moneyField, text as textField, date as dateField, note as noteField,
  num as numField, pct as pctField, selectOf, formHTML, whenKind, wireKind,
} from "../fields.js";
import { refSelect, refValue, wireRefs } from "../refs.js";
import { routeFor } from "../routes.js";
import { wireCrud } from "../crud.js";
import { wireDisclosures, disclosure } from "../disclosure.js";

const KIND_LABEL = { card: "Картка", installment: "Розстрочка" };

/** Питання до плану погашення: скільки кидаю понад обовʼязкове й у якому
 *  порядку. Живе в модулі, а не в адресі, і це свідомо: це не місце, куди
 *  ставлять закладку, а ручка, якою крутять тут і зараз. Порожній extra
 *  означає «візьми число застосунку» — стелю дострокового погашення. */
let ask = { extra: "", strategy: "avalanche" };

/** Керування планом: одна форма на два питання. */
function askHTML(p) {
  const from = p && p.extra_from ? p.extra_from : "";
  return `<div class="card"><h2 class="h-row">Скільки кидаю на борг ${infoBtn("payoff")}</h2>
    ${formHTML({
    id: "payoffAskForm",
    fields: [
      moneyField("extra", "Понад обовʼязкове, ₴/міс", {
        ph: p && p.extra ? p.extra.amount : "5000.00", value: ask.extra,
      }),
      selectOf("strategy", "Порядок", [
        ["avalanche", "Лавина — спершу найдорожчий"],
        ["snowball", "Сніжок — спершу найменший"],
        ["minimum", "Лише мінімалки"],
      ], ask.strategy),
    ],
    submit: "Порахувати",
  })}
    <div class="note">Порожньо = ${esc(from || "число застосунку")}${p && p.extra
    ? `, зараз ${fmtMoney(p.extra)}` : ""}. Обовʼязкові платежі сюди не входять —
      вони не вибір, і застосунок відняв їх від грошей місяця сам.</div>
  </div>`;
}

/** Основа ставки словами. Рядок пише бекенд лише для машини («graph»),
 *  бо підпис для людини мусить помістити ще й ЦІНУ довіри до числа. */
const BASIS_TEXT = {
  graph: "виведено з графіка платежів",
  compound: "заявлена банком, з місячною капіталізацією",
  none: "рахувати нема з чого",
};

// ---------- КАРТКА ----------

/** Пільговий цикл однієї картки: «вільно», два пороги й ціна помилки. */
function graceHTML(g) {
  if (!g.known) {
    return `<div class="card"><h2 class="h-row">${esc(g.name)} ${infoBtn("card")}</h2>
      ${empty("Звірки ще немає",
    "Забий два числа з додатка банку — баланс і суму до сплати. Доти застосунок "
    + "не може сказати ні скільки внести, ні скільки ще можна витратити.")}
    </div>`;
  }
  const free = Number(g.free.amount);
  const short = free < 0;
  // Проза залежить від того, чи є на картці СВОЇ гроші. Один текст на
  // обидва стани був би неправдою для одного з них: «плюс уже обіцяний
  // виписці» нічого не означає, коли плюса немає взагалі.
  const inPlus = Number((g.free_own || g.free).amount) > 0 || free > 0;
  return `<div class="card">
    <h2 class="h-row">${esc(g.name)} ${infoBtn("card")}</h2>
    <div class="note">Пільговий оборот — це побут, а не борг: доки виписку закривають
      вчасно, він не коштує нічого й у чергу погашення не входить. Ціна помилки —
      нижче, двома числами.</div>
    <div class="tiles flush">
      <div class="tile hero"><div class="lbl">Вільно</div>
        <div class="val ${short ? "t-danger" : "t-ok"}">${fmtMoney(g.free)}</div>
        <div class="sub">${!short
    ? "стільки своїх грошей на картці лишається вільними"
    : inPlus
      ? `плюс на картці вже обіцяний виписці — стільки ще треба принести до ${
        esc(g.due_date || "розрахункової дати")}`
      : `стільки треба принести до ${esc(g.due_date || "розрахункової дати")},
         інакше з цієї дати почнуть нараховувати`}</div></div>
      <div class="tile"><div class="lbl">До сплати ${g.due_date ? "до " + esc(g.due_date) : ""}</div>
        <div class="val">${fmtMoney(g.full_due)}</div>
        <div class="sub">внести стільки — і відсотків не буде взагалі</div></div>
      <div class="tile"><div class="lbl">Мінімум</div>
        <div class="val">${fmtMoney(g.min_due)}</div>
        <div class="sub">менше — штраф і підвищена ставка на весь борг</div></div>
    </div>
    ${g.days_to_due ? `<div class="kv"><span class="muted">До розрахункової дати</span>
      <b>${g.days_to_due} ${plural(g.days_to_due, "день", "дні", "днів")}</b></div>` : ""}
    <div class="kv"><span class="muted">Не закрити виписку — коштуватиме за місяць</span>
      <b>${fmtMoney(g.miss_full_cost)}</b></div>
    <div class="kv"><span class="muted">Пропустити мінімалку — коштуватиме</span>
      <b class="t-danger">${fmtMoney(g.miss_min_cost)}</b></div>
    <div class="sub">Звірка від ${esc(g.mark_date || "—")}${g.mark_age_days > 14
    ? ` <span class="t-warn">— числам уже ${g.mark_age_days} ${plural(g.mark_age_days,
      "день", "дні", "днів")}, а баланс кредитки рухається щодня</span>`
    : ""}</div>
  </div>`;
}

/** Вихід із кредитного ліміту: скільки можна витрачати на місяць.
 *
 *  ГОЛОВНЕ ЧИСЛО — стеля витрат, а не борг і не дата. Питання, з яким сюди
 *  приходять, звучить так: «скільки я можу піти в мінус на місяць, щоб за
 *  два місяці вибратись». Борг і дата — це вхід, стеля — відповідь.
 *
 *  РОЗБІЖНІСТЬ ЗАЯВЛЕНИХ І ВИМІРЯНИХ ВИТРАТ НАЗИВАЄТЬСЯ ВГОЛОС (рішення
 *  власника). Заявлені — намір, виміряні — те, що сталося; сховати
 *  різницю означало б рахувати стелю від наміру й називати це фактом. */
function exitHTML(g) {
  const e = g.exit;
  if (!e) return "";
  return `<div class="card">
    <h2 class="h-row">Вихід із ліміту до ${esc(e.exit_by)} ${infoBtn("cardExit")}</h2>
    <div class="tiles flush">
      <div class="tile hero"><div class="lbl">Можна витрачати</div>
        <div class="val ${e.feasible ? "t-ok" : "t-danger"}">${e.feasible
    ? fmtMoney(e.spend_cap) : "не встигнути"}</div>
        <div class="sub">${e.feasible
    ? `на місяць — і до ${esc(e.exit_by)} картка вийде в нуль`
    : `навіть при нульових витратах: за ${e.months.toFixed(1)} міс треба звільняти
       ${fmtMoney(e.need_per_month)}, а на картці лишається менше`}</div></div>
      <div class="tile"><div class="lbl">Треба звільняти</div>
        <div class="val">${fmtMoney(e.need_per_month)}</div>
        <div class="sub">на місяць, щоб устигнути</div></div>
      <div class="tile"><div class="lbl">За твоїм темпом</div>
        <div class="val">${e.eta_date ? esc(e.eta_date) : "—"}</div>
        <div class="sub">${e.eta_date
    ? "якщо витрачати стільки ж, скільки зараз"
    : "борг не меншає: витрати зʼїдають усе, що приходить"}</div></div>
    </div>
    ${Number(e.short_per_month.amount) > 0 ? `<div class="kv">
      <span class="muted">Щоб устигнути, врізати витрати на</span>
      <b class="t-warn">${fmtMoney(e.short_per_month)}/міс</b></div>` : ""}
    <div class="kv"><span class="muted">Приходить усього, у середньому</span>
      <b>${fmtMoney(e.gross)}/міс</b></div>
    <div class="kv"><span class="muted">З них виводиться в інструменти</span>
      <b>${fmtMoney(e.invest)}/міс</b></div>
    <div class="kv"><span class="muted">Лишається на картці — це «все інше»</span>
      <b>${fmtMoney(e.on_card)}/міс</b></div>
    <div class="sub">Три числа вище — СЕРЕДНІ за місяці, які ще попереду. Місяць,
      що вже прожитий на дату звірки, у розрахунок не входить: його дохід прийшов,
      витрати сталися, і результат уже сидить у самому боргу. Місяці при цьому різні —
      одна зарплата закінчується, інша починається, — і таблиця нижче показує кожен
      окремо.</div>
    <div class="kv"><span class="muted">Витрати, з якими рахували (${esc(e.spend_basis)})</span>
      <b>${fmtMoney(e.spend_used)}/міс</b></div>
    ${spendGapHTML(e)}
    ${exitWalkHTML(e)}
    <div class="sub">Якщо докинути на картку й інвестиційну частку —
      витрачати можна <b>${fmtMoney(e.with_invest_spend_cap)}/міс</b>${e.with_invest_eta_date
    ? `, а за нинішніми витратами вихід зсунеться на ${esc(e.with_invest_eta_date)}` : ""}.
      Ціна цього — купівель за цей час не буде. Вирішувати щомісяця, застосунок лише
      ставить обидва числа поруч.</div>
  </div>`;
}

/** Прохід балансу картки вперед.
 *
 *  Місяці НЕ однакові, і саме тому це таблиця, а не одне число: у власника
 *  одна зарплата скінчилась у серпні, а дві починаються у вересні, тож
 *  перший крок помітно менший за решту. Середнє цього не показує. */
function exitWalkHTML(e) {
  if (!(e.schedule || []).length) return "";
  return disclosure("card-exit-walk", "Помісячно до нуля", opsGrid({
    cols: [
      { key: "m", label: "Місяць", cell: (r) => esc(monthYear(r.month + "-01")) },
      { key: "gross", label: "Приходить", num: true, cell: (r) => fmtMoney(r.gross) },
      { key: "invest", label: "У портфель", num: true, cell: (r) => fmtMoney(r.invest) },
      { key: "spend", label: "Витрати", num: true, cell: (r) => fmtMoney(r.spend) },
      { key: "left", label: "Лишиться боргу", num: true, cell: (r) => fmtMoney(r.left) },
    ],
    rows: e.schedule,
    caption: "Баланс картки місяць за місяцем до нуля",
    empty: "",
  }));
}

/** Заявлені витрати проти виміряних. Мовчазне «взяли заявлені» перетворило
 *  б намір на факт — а на живих даних вони розходяться в рази. */
function spendGapHTML(e) {
  if (!e.spend_measured) {
    return `<div class="sub">Виміряти витрати ще не вийшло: ${esc(e.burn_why || "")}
      Доти стеля стоїть на заявлених ${fmtMoney(e.spend_declared)}/міс — це намір,
      а не факт.</div>`;
  }
  const diff = Number(e.spend_measured.amount) - Number(e.spend_declared.amount);
  return `<div class="sub">Заявлено ${fmtMoney(e.spend_declared)}/міс, виміряно
    ${fmtMoney(e.spend_measured)}/міс за ${esc(e.burn_from)} — ${esc(e.burn_to)}.
    ${Math.abs(diff) < 1 ? "Сходиться."
    : diff > 0
      ? `<span class="t-warn">Витрачається на ${fmtUAH(diff)} більше, ніж заявлено —
         і саме ця різниця тримає ліміт на дні.</span>`
      : `Витрачається менше, ніж заявлено; стеля рахується з виміряного.`}</div>`;
}

// ---------- ЧЕРГА ПОГАШЕННЯ ----------

/** Борги, упорядковані чергою погашення обраної стратегії.
 *
 *  Порожня черга мусить сказати ЧОМУ вона порожня, і причин три: боргів
 *  немає взагалі, вони позначені погашеними, або в картки немає ставки.
 *  Мовчазна порожнеча читається як «застосунок не взяв дані» — саме так
 *  вона й прочиталась на живих даних, коли дата в полі «Погашено»
 *  прибрала борг з усіх чисел одразу. */
function queueHTML(p, list) {
  if (!p || !(p.debts || []).length) {
    const closed = (list || []).filter((d) => d.closed_date);
    const noRate = (list || []).filter((d) => d.kind === "card" && !d.closed_date && !d.apr_pct);
    let why = "У черзі стоїть лише те, на що нараховують: розстрочки й готівка з ліміту. "
      + "Пільговий оборот картки сюди не входить — він нічого не коштує, доки його "
      + "закривають вчасно.";
    if (closed.length) {
      why = `Позначено погашеними: ${closed.map((d) => esc(d.name) + " (" + esc(d.closed_date) + ")")
        .join(", ")}. Закритий борг не рахується НІДЕ — ні в черзі, ні в обовʼязкових `
        + "платежах місяця. Якщо борг ще живий, прибери дату в полі «Погашено»: вона про те, "
        + "коли борг ЗАКРИТО, а не про те, коли платити.";
    } else if (noRate.length) {
      why = `Без ставки: ${noRate.map((d) => esc(d.name)).join(", ")}. `
        + "Постав річну ставку після пільгового періоду — без неї немає ні мінімального "
        + "платежу, ні ціни помилки, ні місця в черзі.";
    }
    return `<div class="card"><h2 class="h-row">Черга погашення ${infoBtn("payoff")}</h2>
      ${empty(closed.length || noRate.length
    ? "Черга порожня, і це не тому, що боргів немає" : "Боргів під ставкою немає", why)}</div>`;
  }
  return `<div class="card"><h2 class="h-row">Черга погашення ${infoBtn("payoff")}</h2>
    <div class="note">${esc(p.note || "")}</div>
    ${opsGrid({
    cols: [
      { key: "name", label: "Борг", cell: (d) => esc(d.name) },
      { key: "kind", label: "Вид", cell: (d) => KIND_LABEL[d.kind] || d.kind },
      { key: "left", label: "Лишилось", num: true, cell: (d) => fmtMoney(d.left) },
      { key: "rate", label: "Ставка", num: true,
        cell: (d) => `<b class="t-danger">${pct(d.rate_pct)}</b>` },
      { key: "real", label: "Реальна", num: true, cell: (d) => pct(d.real_pct) },
      { key: "basis", label: "Звідки", cell: (d) => esc(BASIS_TEXT[d.rate_basis] || "") },
      { key: "close", label: "Закриється", cell: (d) => esc(d.close_date || "—") },
    ],
    rows: p.debts,
    caption: "Черга погашення: борг, залишок, ставка й дата закриття",
    empty: "",
  })}
    ${p.invest_instead_pct ? `<div class="sub">Портфель заробляє
      ${pct(p.invest_instead_pct)} реальних. Рядки вище — гарантовані: погашення не
      має ні податку, ні ризику ціни. Порівняння робиш ти, застосунок лише ставить
      числа в одну колонку.</div>` : ""}
  </div>`;
}

/** Три стратегії поруч: у місяцях і в гривнях. */
function strategiesHTML(p) {
  if (!p || !(p.compare || []).length) return "";
  const label = {
    avalanche: "Лавина — спершу найдорожчий",
    snowball: "Сніжок — спершу найменший",
    minimum: "Лише мінімалки",
  };
  return `<div class="card"><h2 class="h-row">Що дає вибір ${infoBtn("payoff")}</h2>
    <div class="note">Лавина завжди платить менше — це арифметика, а не думка. Сніжок
      обирають заради іншого: перший борг закривається раніше, і рядків меншає
      швидше. «Лише мінімалки» — не варіант, а лінійка, проти якої міряються обидва.</div>
    ${opsGrid({
    cols: [
      { key: "s", label: "Стратегія",
        cell: (r) => `${esc(label[r.strategy] || r.strategy)}${
          r.strategy === p.strategy ? " <b>· обрана</b>" : ""}` },
      { key: "months", label: "Місяців", num: true,
        cell: (r) => (r.unfunded ? "ніколи" : String(r.months)) },
      { key: "free", label: "Свобода", cell: (r) => esc(r.free_date ? monthYear(r.free_date + "-01") : "—") },
      // У стратегії, яка борг не гасить, «віддано банку» — не факт, а
      // артефакт стелі проходу в пʼятдесят років. Показати те число
      // означало б назвати сумою те, що насправді «нескінченно».
      { key: "cost", label: "Віддано банку", num: true,
        cell: (r) => (r.unfunded ? "нескінченно" : fmtMoney(r.cost)) },
    ],
    rows: p.compare,
    caption: "Порівняння стратегій погашення",
    empty: "",
  })}
    ${sensitivityHTML(p)}
  </div>`;
}

/** Що дасть іще тисяча.
 *
 *  Рядки без ефекту ВІДКИДАЮТЬСЯ: «на 0 місяців швидше й на 0,00 ₴
 *  дешевше» — не відповідь, а шум, і читається як поломка. Коли всі
 *  порожні, це змістовний стан: борг і так закривається за нинішнім
 *  темпом, і кидати більше нема куди. */
function sensitivityHTML(p) {
  const rows = (p.sensitivity || []).filter((s) => s.months_saved > 0
    || Number((s.cost_saved || {}).amount || 0) > 0);
  if (!rows.length) {
    return `<div class="sub">Кидати більше нема куди: за нинішнім темпом борг
      закривається, і додаткові гроші вже нічого не пришвидшують.</div>`;
  }
  return `<div class="sub">Якщо кидати більше:
    ${rows.map((s) => `<b>+${fmtMoney(s.extra)}/міс</b> — на ${s.months_saved}
    ${plural(s.months_saved, "місяць", "місяці", "місяців")} швидше й на
    ${fmtMoney(s.cost_saved)} дешевше`).join("; ")}.</div>`;
}

/** Помісячний графік — скільки віддано, скільки з того банку, скільки ще. */
function scheduleHTML(p) {
  if (!p || !(p.schedule || []).length) return "";
  return disclosure("payoff-schedule", "Помісячно", `
    ${opsGrid({
    cols: [
      { key: "m", label: "Місяць", cell: (r) => esc(monthYear(r.month + "-01")) },
      { key: "paid", label: "Платіж", num: true, cell: (r) => fmtMoney(r.paid) },
      { key: "cost", label: "З них банку", num: true, cell: (r) => fmtMoney(r.cost) },
      { key: "left", label: "Лишиться", num: true, cell: (r) => fmtMoney(r.left) },
    ],
    rows: p.schedule,
    caption: "Помісячний графік погашення",
    empty: "",
  })}`);
}

// ---------- ФОРМИ ----------

const debtFields = (ctx, row = null) => [
  textField("name", "Назва", {
    ph: "ПУМБ ВсеМожу", required: true, value: row ? row.name || "" : "",
  }),
  selectOf("kind", "Вид", [
    ["card", "Картка з пільговим періодом"],
    ["installment", "Розстрочка"],
  ], row ? row.kind : "card"),
  refSelect(ctx, { name: "currency", ref: "currency", value: row ? row.currency : "UAH" }),

  whenKind(["card"], moneyField("limit", "Кредитний ліміт", {
    ph: "200000.00", value: row && row.limit ? row.limit.amount : "",
  })),
  whenKind(["card"], numField("statement_day", "Розрахункова дата (число місяця)", {
    ph: "30", value: row ? String(row.statement_day || "") : "",
  })),
  whenKind(["card"], pctField("apr_pct", "Ставка після пільгового, % річних", {
    ph: "47.88", value: row ? String(row.apr_pct || "") : "",
  })),
  whenKind(["card"], pctField("apr_overdue_pct", "Підвищена за прострочення, %", {
    ph: "62", value: row ? String(row.apr_overdue_pct || "") : "",
  })),
  whenKind(["card"], pctField("min_payment_pct", "Мінімальний платіж, % боргу", {
    ph: "3", value: row ? String(row.min_payment_pct || "") : "",
  })),
  whenKind(["card"], moneyField("min_payment_floor", "…але не менше ніж", {
    ph: "100.00", value: row && row.min_payment_floor ? row.min_payment_floor.amount : "",
  })),
  whenKind(["card"], moneyField("late_fee", "Штраф за прострочення", {
    ph: "100.00", value: row && row.late_fee ? row.late_fee.amount : "",
  })),
  // Ціль виходу — ДАТА, а не кількість місяців: «за два місяці» щомісяця
  // означає іншу дату, і відставання не зʼявилось би ніколи.
  whenKind(["card"], dateField("exit_by", "Вийти в нуль до (порожньо = без цілі)",
    row ? { value: row.exit_by || "" } : {})),

  whenKind(["installment"], refSelect(ctx, {
    name: "card_id", ref: "debt-card", label: "Списується з картки",
    items: ctx.debtList, value: row ? String(row.card_id || "") : "",
  })),
  whenKind(["installment"], moneyField("principal", "Сума, яку розтягнули", {
    ph: "30000.00", value: row && row.principal ? row.principal.amount : "",
  })),
  whenKind(["installment"], numField("payments_total", "Скільки платежів", {
    ph: "9", value: row ? String(row.payments_total || "") : "",
  })),
  whenKind(["installment"], dateField("first_payment_date", "Перший платіж",
    row ? { value: row.first_payment_date || "" } : {})),
  whenKind(["installment"], pctField("fee_month_pct", "Комісія за місяць, % від ПОЧАТКОВОЇ суми", {
    ph: "1.99", value: row ? String(row.fee_month_pct || "") : "",
  })),
  whenKind(["installment"], numField("fee_free_months", "Місяців без комісії", {
    ph: "3", value: row ? String(row.fee_free_months || "") : "",
  })),

  dateField("opened_date", "Відкрито", row ? { value: row.opened_date || "" } : {}),
  // Підпис довгий навмисно. Коротке «Погашено (закрити борг)» власник
  // прочитав як «коли треба погасити» — і не міг прочитати інакше: іншого
  // поля з датою на картці немає, бо дату платежу застосунок виводить із
  // розрахункового числа. Борг миттєво зник з усіх чисел.
  dateField("closed_date", "Погашено — коли борг ЗАКРИТО (не дата платежу)",
    row ? { value: row.closed_date || "" } : {}),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

/** Тіло запиту — ЛИШЕ поля обраного виду.
 *
 *  whenKind ховає чужі поля, але форма надсилає їх однаково, і на живих
 *  даних картка поїхала на бекенд із датою першого платежу розстрочки.
 *  Шкоди від мертвої колонки немає, але вона перестає бути мертвою, щойно
 *  вид перемкнуть: запис успадкував би числа, яких ніхто не вводив для
 *  цього виду. */
const debtBody = (f) => {
  const card = f.kind.value === "card";
  return {
    name: f.name.value.trim(),
    kind: f.kind.value,
    currency: refValue(f, "currency"),
    limit: card ? f.limit.value.trim() : "",
    statement_day: card ? f.statement_day.value.trim() : "",
    apr_pct: card ? f.apr_pct.value.trim() : "",
    apr_overdue_pct: card ? f.apr_overdue_pct.value.trim() : "",
    min_payment_pct: card ? f.min_payment_pct.value.trim() : "",
    min_payment_floor: card ? f.min_payment_floor.value.trim() : "",
    late_fee: card ? f.late_fee.value.trim() : "",
    exit_by: card ? f.exit_by.value : "",
    card_id: card ? "" : refValue(f, "card_id"),
    principal: card ? "" : f.principal.value.trim(),
    payments_total: card ? "" : f.payments_total.value.trim(),
    first_payment_date: card ? "" : f.first_payment_date.value,
    fee_month_pct: card ? "" : f.fee_month_pct.value.trim(),
    fee_free_months: card ? "" : f.fee_free_months.value.trim(),
    opened_date: f.opened_date.value,
    closed_date: f.closed_date.value,
    note: f.note.value.trim(),
  };
};

/** Поля звірки. ТРИ ЧИСЛА В ОДНІЙ ФОРМІ — так само, як вони стоять на
 *  одному екрані додатка банку: розведені по трьох формах, вони
 *  розійшлися б у часі. */
const markFields = (ctx, row = null) => [
  refSelect(ctx, {
    name: "debt_id", ref: "debt-card", label: "Картка",
    items: ctx.debtList, required: true, value: row ? String(row.debt_id || "") : "",
  }),
  moneyField("balance", "Баланс (мінус — використаний ліміт)", {
    ph: "-3000.00", required: true, value: row ? row.balance.amount : "",
  }),
  moneyField("statement_due", "До сплати за випискою", {
    ph: "18400.00", value: row && row.statement_due ? row.statement_due.amount : "",
  }),
  moneyField("non_grace", "З них поза пільговим (готівка, перекази)", {
    ph: "0.00", value: row && row.non_grace ? row.non_grace.amount : "",
  }),
  dateField("date", "Дата звірки", row ? { value: row.date } : {}),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

const markBody = (f) => ({
  debt_id: refValue(f, "debt_id"),
  balance: f.balance.value.trim(),
  statement_due: f.statement_due.value.trim(),
  non_grace: f.non_grace.value.trim(),
  date: f.date.value,
  note: f.note.value.trim(),
});

const opFields = (ctx, row = null) => [
  refSelect(ctx, {
    name: "debt_id", ref: "debt", label: "Борг",
    items: ctx.debtList, required: true, value: row ? String(row.debt_id || "") : "",
  }),
  selectOf("kind", "Рух", [
    ["payment", "Унесено на картку / сплачено"],
    ["draw", "Покупка карткою"],
    ["cash", "Готівка або переказ із ліміту"],
  ], row ? row.kind : "payment"),
  moneyField("amount", "Сума", {
    ph: "5000.00", required: true, value: row ? row.amount.amount : "",
  }),
  dateField("date", "Дата", row ? { value: row.date } : {}),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

const opBody = (f) => ({
  debt_id: refValue(f, "debt_id"),
  kind: f.kind.value,
  amount: f.amount.value.trim(),
  date: f.date.value,
  note: f.note.value.trim(),
});

function journalHTML(ops, debts) {
  const name = (id) => {
    const d = (debts || []).find((x) => String(x.id) === String(id));
    return d ? d.name : "—";
  };
  const kind = { payment: "Унесено", draw: "Покупка", cash: "Готівка" };
  return disclosure("debt-journal", "Рухи між звірками", opsGrid({
    cols: [
      { key: "date", label: "Дата", cell: (o) => esc(o.date) },
      { key: "debt", label: "Борг", cell: (o) => esc(name(o.debt_id)) },
      { key: "kind", label: "Рух", cell: (o) => kind[o.kind] || o.kind },
      { key: "amount", label: "Сума", num: true, cell: (o) => fmtMoney(o.amount) },
      { key: "note", label: "Нотатка", cell: (o) => esc(o.note || "") },
      actionsCol("debt-ops", { label: (o) => "рух від " + o.date }),
    ],
    rows: (ops || []).slice().sort((a, b) => (a.date < b.date ? 1 : -1)),
    caption: "Рухи під боргами між звірками",
    empty: "Рухів немає. Між звірками сюди пишуть лише велике — зарплату на картку, "
      + "покупку в розстрочку, зняття готівки.",
  }));
}

function marksHTML(marks, debts) {
  const name = (id) => {
    const d = (debts || []).find((x) => String(x.id) === String(id));
    return d ? d.name : "—";
  };
  return disclosure("debt-marks", "Звірки з банком", opsGrid({
    cols: [
      { key: "date", label: "Дата", cell: (m) => esc(m.date) },
      { key: "debt", label: "Картка", cell: (m) => esc(name(m.debt_id)) },
      { key: "balance", label: "Баланс", num: true, cell: (m) => fmtMoney(m.balance) },
      { key: "due", label: "До сплати", num: true, cell: (m) => fmtMoney(m.statement_due) },
      { key: "ng", label: "Поза пільговим", num: true, cell: (m) => fmtMoney(m.non_grace) },
      actionsCol("debt-marks", { label: (m) => "звірку від " + m.date }),
    ],
    rows: (marks || []).slice().sort((a, b) => (a.date < b.date ? 1 : -1)),
    caption: "Звірки балансу картки з додатком банку",
    empty: "Звірок немає — без них пороги й «вільно» рахувати нема з чого.",
  }));
}

// ---------- СТОРІНКА ----------

/** «План → Борги»: нагляд за карткою, черга погашення й дата свободи. */
export async function debts(ctx, main) {
  const q = "payoff?strategy=" + encodeURIComponent(ask.strategy)
    + (ask.extra ? "&extra=" + encodeURIComponent(ask.extra) : "");
  const [list, ops, marks, plan] = await Promise.all([
    ctx.soft("debts", []),
    ctx.soft("debt-ops", []),
    ctx.soft("debt-marks", []),
    ctx.soft(q, null),
  ]);

  // Списки полів читають борги з ctx: refSelect бачить лише те, що йому
  // передали, а форми малюються до того, як сторінка встигне щось знати
  // про свій же список.
  ctx.debtList = list || [];
  const cards = (list || []).filter((d) => d.kind === "card" && !d.closed_date);
  main.innerHTML = `
    ${(plan && plan.grace || []).map((g) => graceHTML(g) + exitHTML(g)).join("")}
    ${cards.length ? "" : `<div class="card"><h2 class="h-row">Борги ${infoBtn("debts")}</h2>
      <div class="note">Заведи картку або розстрочку внизу — і застосунок почне рахувати
        чесну ставку, чергу погашення й дату, коли це скінчиться.</div></div>`}
    ${queueHTML(plan, list)}
    ${askHTML(plan)}
    ${strategiesHTML(plan)}
    ${scheduleHTML(plan)}
    <div class="card"><h2 class="h-row">Звірити картку ${infoBtn("card")}</h2>
      ${formHTML({ id: "debtMarkForm", fields: markFields(ctx), submit: "Записати звірку" })}
      <div class="note">Два числа з додатка банку: скільки на картці зараз і скільки
        банк просить до розрахункової дати. Третє — лише якщо знімав готівку або робив
        переказ із ліміту: на них пільговий не діє ніколи.</div>
    </div>
    ${marksHTML(marks, list)}
    <div class="card"><h2 class="h-row">Записати рух ${infoBtn("debts")}</h2>
      ${formHTML({ id: "debtOpForm", fields: opFields(ctx), submit: "Записати" })}
      <div class="note">Між звірками — лише велике: зарплата на картку, покупка в
        розстрочку, зняття готівки. Кожну каву сюди писати не треба, для цього і є звірка.</div>
    </div>
    ${journalHTML(ops, list)}
    <div class="card"><h2 class="h-row">Завести борг ${infoBtn("setDebt")}</h2>
      ${formHTML({ id: "debtForm", fields: debtFields(ctx), submit: "Зберегти" })}
      <div class="note">Комісія розстрочки береться від ПОЧАТКОВОЇ суми — так її беруть
        банки, і саме тому «1,99% на місяць» коштує близько 50% річних, а не 24%.
        Обовʼязкові платежі за боргами застосунок віднімає від грошей місяця сам, тож у
        <a class="lnk" href="${routeFor("policy/reserve/main")}">місячні витрати</a>
        їх вписувати НЕ треба — інакше віднімуться двічі.</div>
    </div>
    ${listHTML(list)}`;

  wireDisclosures(main);
  const askForm = main.querySelector("#payoffAskForm");
  if (askForm) {
    askForm.addEventListener("submit", (e) => {
      e.preventDefault();
      ask = {
        extra: askForm.elements.extra.value.trim(),
        strategy: askForm.elements.strategy.value,
      };
      // Перемальовуємо саму сторінку, а не сховище: питання змінює лише
      // ПРОЄКЦІЮ, і скидати кеш даних заради нього означало б
      // перечитувати борги, які нікуди не поділись.
      debts(ctx, main);
    });
  }
  wireCrud(ctx, main, {
    resource: "debt-marks", form: "#debtMarkForm", title: "Звірка", rows: marks,
    fields: markFields, body: markBody,
    msg: { add: "Звірку записано", edit: "Звірку виправлено", del: "Звірку видалено" },
  });
  wireCrud(ctx, main, {
    resource: "debt-ops", form: "#debtOpForm", title: "Рух боргу", rows: ops,
    fields: opFields, body: opBody,
    msg: { add: "Рух записано", edit: "Рух виправлено", del: "Рух видалено" },
  });
  wireCrud(ctx, main, {
    resource: "debts", form: "#debtForm", title: "Борг", rows: list,
    fields: debtFields, body: debtBody,
    msg: { add: "Борг заведено", edit: "Борг збережено", del: "Борг видалено" },
  });
  wireKind(main.querySelector("#debtForm"));
  wireRefs(main);
}

/** Самі борги — довідник із умовами, для правки й видалення. */
function listHTML(list) {
  return disclosure("debt-list", "Заведені борги", opsGrid({
    cols: [
      { key: "name", label: "Назва", cell: (d) => esc(d.name) },
      { key: "kind", label: "Вид", cell: (d) => KIND_LABEL[d.kind] || d.kind },
      { key: "terms", label: "Умови", cell: (d) => esc(termsOf(d)) },
      { key: "closed", label: "Погашено", cell: (d) => esc(d.closed_date || "—") },
      actionsCol("debts", { label: (d) => "борг «" + d.name + "»" }),
    ],
    rows: list || [],
    caption: "Заведені борги та їхні умови",
    empty: "Боргів ще немає.",
  }));
}

/** Умови одним рядком — рівно ті, що визначають гроші. */
function termsOf(d) {
  if (d.kind === "card") {
    return [
      d.statement_day ? `розрахунок ${d.statement_day} числа` : "",
      // Відсутню ставку називаємо вголос: без неї рядок виглядає повним, а
      // застосунок за ним не рахує нічого.
      d.apr_pct ? `${d.apr_pct}% річних` : "⚠ ставки немає",
      d.min_payment_pct ? `мінімалка ${d.min_payment_pct}%` : "",
    ].filter(Boolean).join(" · ");
  }
  return [
    d.payments_total ? `${d.payments_total} платежів` : "",
    d.fee_month_pct ? `комісія ${d.fee_month_pct}%/міс` : "",
    d.fee_free_months ? `перші ${d.fee_free_months} без комісії` : "",
  ].filter(Boolean).join(" · ");
}

/** Скільки боргу під ставкою — для смуги стану й «Огляду». Експортується,
 *  бо читачів двоє, а число одне (doc.debt). */
export const debtTotalUAH = (s) => ((s || {}).debt || {}).total_uah || 0;

/** Рядок «Огляду»: борг як перше, що з'їдає гроші місяця. */
export function debtOverviewHTML(s) {
  const d = (s || {}).debt;
  if (!d || (!d.total_uah && !d.cards_watched)) return "";
  return `<div class="card"><h2 class="h-row">Борг ${infoBtn("debts")}</h2>
    <div class="tiles flush">
      <div class="tile"><div class="lbl">Під ставкою</div>
        <div class="val ${d.total_uah ? "t-danger" : "t-ok"}">${fmtUAH(d.total_uah || 0)}</div>
        <div class="sub">${d.top_name
    ? `найдорожчий — ${esc(d.top_name)} під ${pct(d.top_rate_pct)}`
    : "пільговий оборот не рахується: він нічого не коштує, доки закритий вчасно"}</div></div>
      <div class="tile"><div class="lbl">Обовʼязкове цього місяця</div>
        <div class="val">${fmtUAH(d.due_this_month_uah || 0)}</div>
        <div class="sub">уже відняте від грошей місяця</div></div>
      ${d.fill_now_uah ? `<div class="tile"><div class="lbl">Достроково ще</div>
        <div class="val">${fmtUAH(d.fill_now_uah)}</div>
        <div class="sub">із ${fmtUAH(d.fill_month_uah || 0)} місячної частки</div></div>` : ""}
    </div>
    <div class="sub"><a class="lnk" href="${routeFor("plan/debts/main")}">План → Борги</a>
      — чесна ставка кожного, черга погашення й дата, коли це скінчиться.</div>
  </div>`;
}
