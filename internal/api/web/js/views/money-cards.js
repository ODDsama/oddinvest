// Грошові картки: резерв, рух грошей за період, податок, імпорт виписки,
// курс серед історії.
//
// Бібліотека, а не розділ. Вкладки «Гроші» більше немає (ревізія
// 2026-10-03: рахунків застосунок не веде), а її картки, що лишились
// потрібними, живуть у «Портфелі» — «Записати нове», «Період», «Податки»,
// «Виписка», «Валютний шок» — і на сторінці резерву.
//
// Резерв через це розколотий на три частини (плитки, журнал, форма): він
// єдиний, чиї числа читають в «Активах», а рухи записують у «Записати».
// Розкол зроблено функціями, а не прапорцем-режимом: аргумент «покажи
// мені половину себе» читається гірше, ніж три імені.

import {
  esc, curSym, dayMonth, plural, pct,
  uah2 as fmtUAH, cur2 as fmtCur, money as fmtMoney,
} from "../format.js";
import { infoBtn } from "../info.js";
import { opsGrid, rowActions, actionsCol } from "../grid.js";
import {
  money as moneyField, text as textField, date as dateField,
  note as noteField,
  pct as pctField, check as checkField,
  formHTML,
} from "../fields.js";
import { refSelect, refValue } from "../refs.js";
import { routeFor } from "../routes.js";
import { disclosure } from "../disclosure.js";
import { pref } from "../uistate.js";

// ---------- РЕЗЕРВ («МАТРАЦ») ----------
// Гроші, які вкладати не збираються: запропонувати купити за них папір
// означало б порадити витратити аварійні гроші.
// «1.1 місяць» — двічі неправильно: дробові в українській вимагають
// родового («1,1 місяця»), якого в plural() немає, а крапка суперечить
// решті чисел на екрані. Скорочення «міс.» знімає обидва питання.
const monthsNum = (m) => m.toFixed(1).replace(".", ",");

/** Плитки резерву: скільки відкладено, на скільки місяців вистачить, яку
 *  частку капіталу з'їло. Смужка йде до ЦІЛІ, а не до 100% капіталу:
 *  питання «чи вистачить прожити», а не «яку частку портфеля з'їв
 *  матрац» — на друге відповідає окрема плитка поруч. */
export function reserveTilesHTML(ctx) {
  const r = (ctx.summary || {}).reserve;
  if (!r || !r.uah) return "";
  const months = r.months || 0;
  const target = r.target_months || 0;
  const set = (ctx.summary || {}).settings || {};
  const fill = target > 0 ? Math.min(100, (months / target) * 100) : 0;
  const enough = target > 0 && months >= target;
  const places = r.places ? Object.entries(r.places).sort((a, b) => b[1] - a[1]) : [];
  const byCur = r.by_currency ? Object.entries(r.by_currency).sort() : [];
  return `<div class="card">
    <h2 class="h-row">Резерв ${infoBtn("reserve")}</h2>
    <div class="note">Гроші на чорний день. Не інвестиція — але саме тому вони й доступні миттєво, без продажу паперу й розірвання вкладу. У купівельну спроможність не входять.</div>
    <div class="tiles flush">
      <div class="tile"><div class="lbl">Відкладено</div>
        <div class="val">${fmtUAH(r.uah || 0)}</div>
        ${byCur.length > 1 ? `<div class="sub">${byCur.map(([c, v]) =>
    fmtCur(v, c)).join(" · ")}</div>` : ""}</div>
      <div class="tile"><div class="lbl">Вистачить на</div>
        <div class="val">${months ? `${monthsNum(months)} міс.` : "—"}</div>
        <div class="sub">${months
    ? (target ? `ціль — ${target} ${plural(target, "місяць", "місяці", "місяців")}` : "ціль не задана")
    : (target
      // Ціль СТОЇТЬ, але порахувати її нема з чого. Доти цей стан
      // склеювався з «цілі теж немає» — обидва писали «постав місячні
      // витрати», і людина, яка щойно застосувала набір із ціллю в 12
      // місяців, не мала звідки дізнатись, що число вже записане й мовчить.
      ? "ціль стоїть, але порахувати нема з чого"
      : "постав місячні витрати в «Політиці»")}</div></div>
      <div class="tile"><div class="lbl">Частка капіталу</div>
        <div class="val">${r.share_pct ? pct(r.share_pct) : "—"}</div>
        <div class="sub">не інвестиція, але капітал</div></div>
    </div>
    ${target > 0 && !months ? `<div class="note">Ціль резерву задана — ${target} ${
    plural(target, "місяць", "місяці", "місяців")} витрат, — але самі «місячні витрати» порожні,
      тож ні цілі в гривнях, ні розриву тут не буде: ділити нема на що. Це не «цілі немає»:
      число стоїть і чекає на друге. Задай витрати в
      <a class="lnk" href="${routeFor("policy/money/main")}">Політиці → Резерв</a> — і смужка
      з розривом стануть на місце самі.</div>` : ""}
    ${expensesFXHTML(set, r)}
    ${target > 0 && months ? `<div class="progress mb-sm">
      <span style="--oi-fill:${fill}%;--oi-c:${enough ? "var(--oi-ok)" : "var(--oi-info)"}"></span></div>
      <div class="note">${enough
    ? `резерв зібраний${r.uah > r.target_uah ? ` — з перевищенням на ${fmtUAH(r.uah - r.target_uah)}` : ""}`
    : `до цілі ще ${fmtUAH(r.gap_uah || 0)} · ціль ${fmtUAH(r.target_uah || 0)}`}</div>` : ""}
    ${debtCoverHTML(r)}
    ${reserveLoansHTML(r)}
    ${places.length ? `<div class="note">Де лежить: ${places.map(([p, v]) =>
    `${esc(p)} — ${fmtUAH(v)}`).join(" · ")}</div>` : ""}
    ${accessHTML(r)}
  </div>`;
}

/** Борг перед подушкою: узяв із неї — повертаєш із відсотком (0057).
 *
 *  ЧОМУ ЦІЛЬ РОЗКЛАДАЄТЬСЯ ВГОЛОС. Ціль, яка сама собою підросла на 186 ₴,
 *  читається як помилка застосунку — рівно те, чого уникає пара
 *  debt_capped/full_target_uah поруч. Тому тут завжди видно обидва числа:
 *  базову ціль і надбавку, і що саме її породило.
 *
 *  ТІЛО В НАДБАВКУ НЕ ВХОДИТЬ, і це сказано словами, бо інакше перше
 *  питання читача буде «а чому ціль виросла лише на 186, я ж узяв 12 000».
 *  Відповідь: подушка вже впала на 12 000 самим зняттям, і розрив їх уже
 *  вимагає. */
function reserveLoansHTML(r) {
  const loans = r.loans || [];
  if (!loans.length) return "";
  const owed = r.owed_uah || 0;
  const interest = r.owed_interest_uah || 0;
  const base = r.base_target_uah || 0;
  const overdue = loans.filter((l) => l.overdue).length;
  const rows = loans.map((l) => {
    const taken = l.currency
      ? `${fmtCur(l.taken_native, l.currency)} (${fmtUAH(l.taken_uah)})`
      : fmtUAH(l.taken_uah);
    const due = l.due_date
      ? (l.overdue
        ? ` · <b class="t-danger">мав повернути до ${dayMonth(l.due_date)}</b>`
        : ` · повернути до ${dayMonth(l.due_date)}`)
      : "";
    return `<div class="kv"><span>${taken} узято ${dayMonth(l.date)}${
      l.note ? ` — ${esc(l.note)}` : ""}${due}</span>`
      + `<span>${fmtUAH(l.owed_uah)} · ${l.days} ${
        plural(l.days, "день", "дні", "днів")} під ${pct(l.rate_pct)} → ${
        fmtUAH(l.interest_uah)} ${rowActions("reserve-loans", l.id,
        { label: "позику від " + l.date })}</span></div>`;
  }).join("");
  return `<div class="note">
    <b${overdue ? ' class="t-danger"' : ""}>Винен резерву ${fmtUAH(owed)}</b> —
    ${loans.length === 1 ? "одна позика" : `${loans.length} ${
    plural(loans.length, "позика", "позики", "позик")}`} в самого себе,
    з них ${fmtUAH(interest)} відсотка. Саме на нього піднята ціль:
    ${fmtUAH(base)} базової + ${fmtUAH(interest)} = ${fmtUAH(r.target_uah || 0)}.
    Тіло ціль не піднімає — резерв уже впав на нього, коли ти його брав,
    і розрив його вже вимагає. Повернеш усе — ціль стане такою, якою була.
    </div>
    ${rows}`;
}

/** Перший рубіж подушки: чи є чим закрити кредити.
 *
 *  ОКРЕМИЙ РЯДОК, А НЕ ЧАСТИНА ЦІЛІ, і це не оформлення. Ціль у місяцях
 *  витрат відповідає на питання «скільки протримаюсь без доходу»; цей
 *  рубіж — на інше: «чи є чим закрити кредити». Він майже завжди ближчий
 *  за ціль, і саме тому його видно окремо: на живих даних власника ціль
 *  стояла на 400 954 ₴, борг — на 73 953 ₴, а подушка на 37 140 ₴, тобто
 *  найближчий рубіж не був названий узагалі.
 *
 *  Рахуються МАЙБУТНІ ПЛАТЕЖІ, а не залишок тіла: візьмуть тіло разом із
 *  комісіями (state_debts.go). І показується він у ОБОХ станах — «взято»
 *  теж відповідь, а рядок, що зникає при успіху, читається як поломка. */
function debtCoverHTML(r) {
  const cover = r.debt_cover_uah || 0;
  if (!cover) return "";
  const gap = r.debt_cover_gap_uah || 0;
  return gap > 0
    ? `<div class="note"><b class="t-danger">Резерв не перекриває борг.</b>
      Закрити його коштувало б ${fmtUAH(cover)} — це майбутні платежі разом із
      комісіями, — а бракує ${fmtUAH(gap)}. Рубіж ближчий за ціль у місяцях витрат
      і важливіший за неї: доки він не взятий, будь-яка пауза в доході робить борг
      простроченим.</div>`
    : `<div class="note">Борг перекрито: закрити його коштувало б ${fmtUAH(cover)},
      і ці гроші в подушці вже є.</div>`;
}

/** Ціль подушки, коли витрати мисляться не в гривні.
 *
 *  Мовчить на гривневих витратах, і це не економія рядка: там обидва
 *  числа збігаються до копійки, і показати їх поруч означало б написати
 *  «6 × 25 000 ₴ = 150 000 ₴ ≈ 150 000 ₴».
 *
 *  А от у доларах сказати треба обовʼязково, бо інакше гривнева ціль
 *  виглядає числом, яке хтось вписав, — і незрозуміло, чому вона їде сама.
 *  Їде вона за курсом, і саме це тут і названо. */
function expensesFXHTML(set, r) {
  const cur = set.monthly_expenses_currency || "";
  const native = set.monthly_expenses || 0;
  const months = r.target_months || 0;
  if (!cur || cur === "UAH" || !native || !months) return "";
  const sym = curSym(cur);
  return `<div class="note">Витрати задані у ${esc(cur)}: ${fmtCur(native, sym)} на місяць.
    Ціль резерву — ${months} × ${fmtCur(native, sym)} = <b>${fmtCur(native * months, sym)}</b>,
    тобто ${fmtUAH(r.target_uah || 0)} за сьогоднішнім курсом. Гривнева ціль їде за курсом сама:
    девальвація піднімає її, і резерв, який учора був зібраний, сьогодні може мати розрив.</div>`;
}

/** Доступ до подушки: коли я до цього дістануся.
 *
 *  ОКРЕМЕ ПИТАННЯ ВІД «НА СКІЛЬКИ ВИСТАЧИТЬ», і плитки вище на нього не
 *  відповідають. Подушка на 600 000 ₴ при витратах 50 000 ₴ дає рівно
 *  12 місяців і готівкою, і одним річним вкладом; у другому випадку на
 *  третій місяць у руках не буде нічого.
 *
 *  ТРИ СТАНИ, І ЧЕРВОНИЙ ЛИШЕ ОДИН. Недобрана голова — справжня вада:
 *  аварія не витрачається помісячно, і драбина її не покриває в принципі.
 *  Хвіст, який сам себе не тягне, вадою НЕ є: якщо договір дозволяє
 *  забрати достроково, це розмін — гроші будуть, ціна відсотки. Діра
 *  лишається дірою тільки тоді, коли не дотягує навіть із розірванням. */
function accessHTML(r) {
  if (!r.ladder || !r.ladder.length) return "";
  const target = r.target_months || 0;
  const covers = r.ladder_covers_months || 0;
  const reach = r.ladder_reach_months || 0;
  const headShort = r.liquid_target_uah > 0 && (r.liquid_uah || 0) < r.liquid_target_uah;
  // Розгортання проти діри — саме та пара станів, яку найлегше злити в
  // один «не гаразд». Драбина, що ще набирається, показує рівно те саме
  // неповне покриття, що й недосяжний хвіст, і без цієї гілки обидва
  // читались би як помилка.
  //
  // При СПРАВЖНІЙ дірі цей рядок мовчить, і це не дрібниця: «неповне
  // покриття тут очікуване» під червоним «грошей не буде ніяк» пом'якшувало
  // б рівно те твердження, заради якого червоний і лишили одному стану.
  // Спіймано живцем на екрані, а не тестом: обидві гілки поодинці правильні.
  const building = !r.ladder_gap_month
    && r.ladder_rungs_target > 0 && r.ladder_rungs < r.ladder_rungs_target;
  const line = r.ladder_gap_month
    ? `<span class="t-warn">⚠ на ${r.ladder_gap_month}-й місяць бракує
       ${fmtUAH(r.ladder_gap_uah || 0)} навіть із розірванням</span>`
    : reach > covers
      ? `далі — розірвання вкладу: тіло повернуть, відсотки згорять.
         Скільки саме коштує, знає банк`
      : `<span class="t-ok">покриття повне ✅</span>`;
  return `<div class="mt-sm">
    <div class="kv"><span class="muted">Доступно сьогодні</span>
      <b class="${headShort ? "t-warn" : ""}">${fmtUAH(r.liquid_uah || 0)}</b></div>
    ${r.liquid_target_uah > 0 ? `<div class="sub">${headShort
    ? `<span class="t-warn">⚠ голова не добрана: треба ${fmtUAH(r.liquid_target_uah)}</span>
       — аварія не витрачається помісячно, і драбина її не покриває`
    : `голова добрана (треба ${fmtUAH(r.liquid_target_uah)})`}</div>` : ""}
    <div class="kv mt-xs"><span class="muted">Драбина тягне сама</span>
      <b>${monthsNum(covers)} із ${target} міс.</b></div>
    ${reach > covers ? `<div class="kv"><span class="muted">з розірванням</span>
      <b>${monthsNum(reach)} із ${target} міс.</b></div>` : ""}
    <div class="sub">${line}</div>
    ${building ? `<div class="sub">драбина набирається: ${r.ladder_rungs} ${
    plural(r.ladder_rungs, "сходинка", "сходинки", "сходинок")} з ${r.ladder_rungs_target}
      — неповне покриття тут очікуване, а не помилка</div>` : ""}
    ${r.next_rung_months ? `<div class="sub">наступна сходинка — на ${r.next_rung_months}
      ${plural(r.next_rung_months, "місяць", "місяці", "місяців")}; якщо банк такого строку
      не дає, бери довший</div>` : ""}
    ${r.ladder_earns_uah ? `<div class="sub">сходинки приносять
      ${fmtUAH(r.ladder_earns_uah)} за рік після податку — стільки коштує тримати це готівкою</div>` : ""}
  </div>`;
}

/** Де лежать гроші руху резерву чи цілі: установа й підпис поруч.
 *
 *  ДВА ПОЛЯ, А НЕ ОДНЕ (0069). Доти місце було єдиним вільним текстом із
 *  доводом «за ним нічого не рахується». Тепер рахується: картка «В одній
 *  установі» питає, скільки я втрачу, якщо банк зникне, і євро подушки на
 *  картці банку стоїть за ним так само, як строковий вклад. Тому установа —
 *  посилання на той самий довідник, що й у вкладі, а «інший…» заводить нову.
 *
 *  Готівка й сейф — не установа: за ними немає контрагента, тож порожня
 *  установа означає саме їх, а «Де саме» лишається вільним підписом
 *  («готівка», «сейф», «картка дружини»). Заводити для них брокера означало
 *  б вигадати сутність, якої в житті немає.
 *
 *  Спільне для резерву й цілей (goals.js): поля в них однакові дослівно. */
export const placeFields = (ctx, row = null) => [
  refSelect(ctx, {
    name: "bank", ref: "broker", label: "Установа", blank: "— без установи (готівка, сейф) —",
    value: row ? row.bank || "" : "",
  }),
  textField("place", "Де саме", {
    ph: "готівка / сейф / картка", value: row ? row.place || "" : "",
  }),
];

/** Клітинка «Місце» журналу: установа, а підпис — сірим поруч. */
export const placeCell = (o) => (o.bank
  ? esc(o.bank) + (o.place ? ` <span class="muted">${esc(o.place)}</span>` : "")
  : esc(o.place || ""));

/** Поля руху резерву — один список і для запису, і для правки.
 *
 *  Правки резерву доти не було, хоча PUT /api/reserve/{id} існував. */
export const reserveFields = (ctx, row = null) => [
  moneyField("amount", "Сума (+ відклав / − узяв)", {
    ph: "5000.00", required: true, value: row ? row.amount.amount : "",
  }),
  refSelect(ctx, { name: "currency", ref: "currency", value: row ? row.amount.currency : "UAH" }),
  ...placeFields(ctx, row),
  dateField("date", "Дата", row ? { value: row.date } : {}),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
  ...reserveLoanFields(ctx, row),
];

/** Поля позики в самого себе (0057).
 *
 *  ТИПОВО УВІМКНЕНО на новому русі, і це не самовпевненість форми, а
 *  прямо названий намір власника: «якщо я беру щось із резерву, то хочу
 *  повертати цю суму з відсотком». Виняток тут — витрата подушки за
 *  призначенням, і зняти галочку дешевше, ніж щоразу її ставити.
 *
 *  Ставка порожня НЕ означає нуль: порожнє поле бере reserve_loan_rate_pct,
 *  а явний 0 лишається нулем («поверну ту саму суму» — теж обіцянка).
 *  Різницю тримає бекенд, тут вона лише не затирається значенням.
 *
 *  На ПРАВЦІ полів немає: умови позики міняє власний ресурс
 *  /api/reserve/loans, і другий шлях до них розійшовся б із першим —
 *  форма руху не знає ні id позики, ні того, чи вона взагалі є. */
function reserveLoanFields(ctx, row) {
  if (row) return [];
  const rate = ((ctx.summary || {}).settings || {}).reserve_loan_rate_pct;
  return [
    checkField("loan", "Зняття — це позика: поверну з відсотком", { checked: true }),
    pctField("loan_rate_pct", "Ставка позики, % річних", {
      ph: rate != null ? String(rate) : "12",
    }),
    // Порожньо, а не «сьогодні»: позика, яку треба повернути в день
    // зняття, — не позика, а помилка в типовому значенні.
    dateField("loan_due", "Повернути до", { value: "" }),
  ];
}

/** Правка позики: ставка, строк і нотатка — усе, що в неї можна змінити.
 *
 *  Суми й дати тут немає, і це не пропуск: вони належать РУХУ, з якого
 *  позика виросла, і правляться в журналі рухів. Позика лише каже, під
 *  який відсоток і до коли цей рух треба повернути (reserveLoanReq).
 *
 *  Видалення знімає статус позики, а не рух: «це була витрата за
 *  призначенням» — законне виправлення, і журнал подушки від нього не
 *  страждає (handleDeleteReserveLoan). */
export const reserveLoanEditFields = (ctx, row) => [
  pctField("rate_pct", "Ставка позики, % річних", { value: String(row.rate_pct ?? "") }),
  dateField("due_date", "Повернути до", { value: row.due_date || "" }),
  noteField("note", "Нотатка", { value: row.note || "" }),
];

export const reserveLoanBody = (f) => ({
  rate_pct: f.rate_pct.value.trim(),
  due_date: f.due_date.value,
  note: f.note.value.trim(),
});

// row — рух на правці. Позику, яку гасить поповнення (repays_loan_id),
// форма не показує, а PUT замінює рух цілком: без неї виправлена сума
// відв'язувала б повернення від позики, і та знову «висіла» б.
export const reserveBody = (f, row = null) => {
  const body = {
    amount: f.amount.value.trim(),
    currency: refValue(f, "currency"),
    bank: refValue(f, "bank"),
    place: f.place.value.trim(),
    date: f.date.value,
    note: f.note.value.trim(),
    loan_id: row ? row.repays_loan_id || 0 : 0,
  };
  // Позика — лише на ЗНЯТТІ. «Позичити, кладучи гроші в подушку» не
  // означає нічого, і бекенд це теж відкидає; тут воно ще й не долітає,
  // щоб форма не обіцяла того, чого не станеться.
  if (f.loan && f.loan.checked && Number(body.amount) < 0) {
    body.loan = true;
    body.loan_rate_pct = f.loan_rate_pct.value.trim();
    body.loan_due = f.loan_due.value;
  }
  return body;
};

export function reserveJournalHTML(ops) {
  const list = (ops || []).slice()
    .sort((a, b) => (a.date < b.date ? 1 : a.date > b.date ? -1 : b.id - a.id));
  return `<div class="card"><h2>Рухи резерву</h2>
    ${opsGrid({
    cols: [
      { key: "date", label: "Дата", cell: (o) => esc(o.date) },
      { key: "kind", label: "Рух",
        cell: (o) => (Number(o.amount.amount) >= 0
          ? (o.repays_loan_id ? "Повернув" : "Відклав")
          : (o.loan_id ? `Позичив (${pct(o.loan_rate_pct || 0)})` : "Узяв")) },
      { key: "amount", label: "Сума", num: true, cell: (o) => fmtMoney(o.amount) },
      { key: "place", label: "Місце", cell: (o) => placeCell(o)
        + (o.note ? ` <span class="muted">${esc(o.note)}</span>` : "") },
      actionsCol("reserve", { label: (o) => "рух резерву від " + o.date }),
    ],
    rows: list,
    caption: "Рухи резерву: дата, напрям, сума, місце",
    empty: "Рухів резерву ще немає — перший запис заведе резерв і покаже, на скільки місяців його вистачає.",
  })}
  </div>`;
}

/** Форма руху резерву. */
export function reserveFormHTML(ctx) {
  return `<div class="card"><h2 class="h-row">Рух резерву ${infoBtn("reserve")}</h2>
    ${formHTML({ id: "resForm", fields: reserveFields(ctx), submit: "Записати", cls: "mb" })}
    <div class="note">Зняття-позика піднімає ціль резерву на відсоток, доки її не
      повернуто. Поповнення гасить найстарішу відкриту позику саме собою — окремо
      його відмічати не треба, і ноги «Маршруту грошей» так само її гасять.</div>
  </div>`;
}

// ---------- ВАЛЮТНЕ ВІКНО ----------

/** Де стоїть сьогоднішній курс серед власної історії — три вікна на валюту.
 *
 *  Стоїть на «Портфель → Валютний шок»: питання «купувати валюту зараз чи
 *  почекати» — той самий курс, що й «що зробив би з портфелем рух, який уже
 *  був». Доти картка жила біля форми конвертації в «Грошах»; конвертацій
 *  більше немає, а питання лишилось.
 *
 *  ПОРАДИ ТУТ НЕМАЄ Й НЕ БУДЕ. Ані підсвітки «дорого», ані порога, за яким
 *  щось червоніє: гривня падає стрибками, і найвищий за десять років курс
 *  був найвищим рівно до наступного тижня. Довгий аргумент — у шапці
 *  internal/domain/fxwindow.go; тут він повторений коротко, бо саме на
 *  екрані спокуса дописати колір найбільша.
 *
 *  Жодної арифметики (CLAUDE.md §5): перцентиль, медіану й різницю до неї
 *  рахує buildFXWindow, картка їх лише малює. */
export function fxWindowHTML(ctx) {
  const rows = (ctx.summary || {}).fx_window || [];
  if (!rows.length) return "";
  const byCur = new Map();
  for (const r of rows) {
    if (!byCur.has(r.currency)) byCur.set(r.currency, []);
    byCur.get(r.currency).push(r);
  }
  const blocks = [...byCur.entries()].map(([c, list]) => {
    const sym = curSym(c);
    return `<div class="mb-lg">
      <div class="mb-xs"><b>${esc(c)}</b> · зараз ${fmtRate(list[0].now_rate)} ₴</div>
      ${opsGrid({
    cols: [
      { key: "years", label: "Вікно",
        cell: (r) => `${r.years} ${plural(r.years, "рік", "роки", "років")}` },
      { key: "pct", label: "Перцентиль", num: true,
        cell: (r) => pct(r.percentile, 0) },
      { key: "median", label: "Медіана", num: true,
        cell: (r) => fmtRate(r.median_rate) },
      { key: "range", label: "Розмах", num: true,
        cell: (r) => `${fmtRate(r.min_rate)} – ${fmtRate(r.max_rate)}` },
      { key: "vs", label: "Дефіцит дає", num: true, cls: "muted",
        cell: (r) => (r.vs_median_native
          ? `${r.vs_median_native > 0 ? "+" : "−"}${fmtCur(Math.abs(r.vs_median_native), sym)}`
          : "—") },
      { key: "points", label: "Точок", num: true, cls: "muted", prio: 2,
        cell: (r) => String(r.points) },
    ],
    rows: list,
    caption: `Курс ${c}: вікно, перцентиль, медіана, розмах, різниця до медіани, точок історії`,
  })}
    </div>`;
  }).join("");
  return `<div class="card"><h2 class="card-head">
    <span>Курс серед історії ${infoBtn("fxwindow")}</span></h2>
    ${blocks}</div>`;
}

// Курс має чотири знаки за визначенням НБУ, і жоден із наявних
// форматувальників його не показує: uah2/cur2 округлюють до копійки, а
// різниця в третьому знаку — це вже сотні гривень на конвертації.
const fmtRate = (v) => (Number(v) || 0).toLocaleString("uk",
  { minimumFractionDigits: 4, maximumFractionDigits: 4 });

// Імпорт виписки Inzhur. Два кроки навмисно: спершу показати, що буде
// зроблено, і лише потім писати. Ціна помилки тут — подвоєний баланс,
// а він знаходиться не одразу.
//
// Вибору «звідки виписка» більше немає: профілі інших виписок (і разом
// із ними виписка картки для боргів) прибрані в ревізії 2026-10-03 — за
// весь час не завели жодного.
export function importHTML() {
  return `<div class="card"><h2 class="card-head">
    <span>Імпорт виписки Inzhur ${infoBtn("import")}</span></h2>
    <div class="muted fine mb-sm">Спершу перегляд — нічого не записується.</div>
    <div class="row-h">
      <input type="file" id="impFile" accept=".xlsx" aria-label="Файл виписки">
      <button id="impPreview">Переглянути</button>
    </div>
    <div class="muted fine mt-sm row-h">
      Враховувати зміни від <input type="date" id="impSince" class="w-md" aria-label="Враховувати зміни від">
      <span>рухається сама після кожного імпорту</span>
    </div>
    <div id="impOut" class="mt"></div></div>`;
}

// Результат справжнього імпорту — до наступного малювання панелі.
//
// Після запису сторінка перемальовується (ctx.reload: змінились лоти,
// фонди, зведення), і разом із нею зникав блок «Записано N» — лишався
// тільки тост, який гасне за кілька секунд. Тепер відповідь переживає
// перемальовку рівно один раз: wireImport показує її й забуває.
let lastImport = null;

const KIND = { fund_buy: "купівля", fund_sell: "продаж", dividend: "дивіденд",
  deposit: "поповнення", withdrawal: "виведення", bond_buy: "купівля ОВДП", coupon: "купон ОВДП" };

export function wireImport(ctx, main) {
  const file = main.querySelector("#impFile");
  const out = main.querySelector("#impOut");
  if (!file || !out) return;

  // Водяний знак: показуємо поточний і даємо посунути руками — інакше
  // «перезавантажити позаминулий місяць» стало б неможливим узагалі.
  const since = main.querySelector("#impSince");
  if (since) {
    ctx.api("GET", "import/since")
      .then((s) => { since.value = (s && s.since) || ""; })
      .catch(() => {});
    since.addEventListener("change", async () => {
      try { await ctx.api("PUT", "import/since", { since: since.value }); ctx.toast("Дату змінено"); }
      catch (err) { ctx.toast(String(err.message || err), false); }
    });
  }

  const send = async (dry) => {
    if (!file.files || !file.files[0]) { ctx.toast("Обери файл", false); return null; }
    const fd = new FormData();
    fd.append("file", file.files[0]);
    const resp = await ctx.store.raw("import" + (dry ? "?dry=1" : ""), { method: "POST", body: fd });
    if (!resp.ok) throw new Error(`${resp.status}: ${(await resp.text()).slice(0, 300)}`);
    // Справжній імпорт міняє геть усе — лоти, рухи, фонди, зведення.
    // Перегляд (dry) не міняє нічого, тож і кеш чіпати нема за що.
    if (!dry) ctx.store.invalidate();
    return resp.json();
  };

  const render = (res, dry) => {
    const rows = (res.rows || []).map((r) => {
      const tag = r.conflict
        ? `<div class="t-danger fine-xs">⚠ ${esc(r.conflict)}</div>`
        : "";
      return `<div class="mb-sm">
        <div class="kv">
          <span>${dayMonth(r.date)} · ${KIND[r.kind] || r.kind}${
            r.fund ? ` <span class="muted">${esc(r.fund)}</span>` : ""}${
            r.qty ? ` <span class="muted">${r.qty} серт.</span>` : ""}</span>
          <span><b>${esc(r.amount)}</b>${r.tax && r.tax !== "0.00" ? ` <span class="muted fine-xs">податок ${esc(r.tax)}</span>` : ""} ${r.exists && !r.conflict ? `<span class="muted fine-xs">вже є</span>` : ""}</span>
        </div>${tag}</div>`;
    }).join("");
    const skipped = (res.skipped || []).map((s) =>
      `<div class="sub-xs">${dayMonth(s.Date || s.date)} · ${esc(s.Op || s.op)} — ${esc(s.Reason || s.reason)}</div>`).join("");
    const conflicts = (res.rows || []).filter((r) => r.conflict).length;
    if (!dry && res.since && since) since.value = res.since;
    out.innerHTML = `
      <div class="mb-sm">Знайдено ${(res.rows || []).length} операцій · <b>${res.new}</b> нових${
        conflicts ? ` · <span class="t-danger">${conflicts} з конфліктом</span>` : ""}</div>
      ${res.before ? `<div class="sub-xs t-warn mb-sm">${res.before} ${
        plural(res.before, "рядок", "рядки", "рядків")}${
        res.before_from ? ` за ${dayMonth(res.before_from)} → ${dayMonth(res.before_to)}` : ""
      } не розглядались: водяний знак стоїть на ${dayMonth(res.since)}.
        Посунь дату нижче, якщо потрібна давніша історія.</div>` : ""}
      ${rows}
      ${skipped ? `<div class="rule-top tight">
        <div class="muted fine mb-xs">пропущено:</div>${skipped}</div>` : ""}
      ${dry && res.new > 0 ? `<button id="impGo" class="mt">Імпортувати ${res.new}</button>` : ""}
      ${!dry ? `<div class="mt-sm t-ok">Записано ${res.imported}</div>` : ""}`;
    const go = out.querySelector("#impGo");
    if (go) {
      go.addEventListener("click", async () => {
        go.disabled = true;
        try {
          lastImport = await send(false);
          ctx.toast("Імпортовано");
          await ctx.reload();
        } catch (err) { ctx.toast(String(err.message || err), false); go.disabled = false; }
      });
    }
  };

  if (lastImport) {
    render(lastImport, false);
    lastImport = null;
  }

  main.querySelector("#impPreview")?.addEventListener("click", async (e) => {
    e.target.disabled = true;
    try { const res = await send(true); if (res) render(res, true); }
    catch (err) { ctx.toast(String(err.message || err), false); }
    finally { e.target.disabled = false; }
  });
}

// Рух грошей за період — що зайшло в інструменти й що з них вийшло.
//
// Питання «по операціях не видно, як і куди я перевклав» — це запит на
// звіт про рух, а не на прив'язку купона до покупки. Рахунків застосунок
// не веде (ревізія 2026-10-03), тож тотожність тепер на межі інструмента:
// куплено − виплати й виходи = внесено в інструменти; плюс подушка й цілі
// — своїх разом. Те саме означення, що в плитки «Цей місяць».
//
// Таблиця — виписка (.ledger): дві колонки, підпис і сума; на всю ширину
// main сума відʼїжджала б від статті на пів монітора.
export function flowHTML(f) {
  if (!f) return "";
  // Рядки виписки — дані, а не розмітка: підпис, число і знак перед ним.
  // Знак тут не арифметика, а НАПРЯМ.
  const lines = [
    { label: "Куплено (мінус продажі)", uah: f.purchased_uah, sign: "" },
    { label: "− надійшло виплат", uah: f.income_uah, sign: "−" },
    { label: "= внесено в інструменти", uah: f.contributed_uah, sign: f.contributed_uah < 0 ? "−" : "" },
    f.outside_uah
      ? { label: "± у резерв і цілі", uah: f.outside_uah, sign: f.outside_uah > 0 ? "+" : "−" }
      : null,
  ].filter(Boolean);
  const detail = (f.rows || []).filter((r) => r.kind === "purchase" && r.uah < 0);
  return `<div class="card">
    <h2 class="h-row">Рух грошей ${infoBtn("cashflow")}</h2>
    <div class="note">${esc(f.from)} → ${esc(f.to)}</div>
    ${opsGrid({
    head: false,
    cls: "ledger",
    cols: [
      { key: "label", label: "Стаття", cell: (r) => r.label },
      { key: "uah", label: "Сума", num: true,
        cell: (r) => r.sign + fmtUAH(Math.abs(r.uah || 0)) },
    ],
    rows: lines,
    caption: `Рух грошей ${esc(f.from)} — ${esc(f.to)}: стаття й сума`,
    foot: [{ cell: "= своїх разом" }, {
      cell: (f.own_uah < 0 ? "−" : "") + fmtUAH(Math.abs(f.own_uah || 0)), num: true }],
  })}
    ${detail.length ? `<details class="disclosure" data-fold="flowbuys">
      <summary>Куди пішли<span class="hint">${detail.length} ${
  plural(detail.length, "операція", "операції", "операцій")}</span></summary>
      <div class="disclosure-body">${opsGrid({
    head: false,
    cls: "ledger",
    cols: [
      { key: "date", label: "Дата", cls: "muted", cell: (r) => esc(r.date) },
      { key: "label", label: "Що", cell: (r) => esc(r.label) },
      { key: "uah", label: "Сума", num: true, cell: (r) => fmtUAH(Math.abs(r.uah)) },
    ],
    rows: detail,
    caption: "Куди пішли гроші: дата, операція, сума",
  })}</div>
    </details>` : ""}
  </div>`;
}

// Скільки з доходу забрала держава — грошима, а не ставкою.
//
// Асиметрія між інструментами вже зашита в реальну дохідність, але
// відсотком її не відчуваєш. Вклад під 16% і папір під 16% — це різні
// гроші, і рядок «податок з'їв стільки-то» каже це пряміше.
// Рік звітності. Живе в localStorage, як і решта перемикачів періоду:
// декларацію заповнюють не в той самий день, коли дивляться картку, і
// повертатись до того самого року щоразу руками — зайва робота.
const TAX_KEY = "oddinvest.taxYear";
export function taxYear() {
  const now = new Date().getFullYear();
  const v = parseInt(pref(TAX_KEY, null, ""), 10);
  // Межа знизу та сама, що й у бекенді: сміття в сховищі не має
  // перетворюватись на запит, який упаде чотирисоткою.
  return v >= 1990 && v <= now ? v : now;
}

// taxRowAttrs — рядок НКД належить купонам НАД ним, а не сусідить із
// ними. Ключ bond_accrued приходить із бекенда саме для цього, тож умова
// одна на обидві сторінки, де картку малюють (year.js бере її звідси).
export const taxRowAttrs = (l) => ({ class: l.kind === "bond_accrued" ? "sub-row" : "" });

// gapsHTML — місяці, за які виплата фонду мала бути, а запису немає.
//
// Це НЕ оцінка доходу й не рядок таблиці: сума наверху лишається тим, що
// справді заведено. Це зізнання картки в межах власного знання — купони
// приходять із довідника НБУ самі, а дивіденди лише з виписки, тож
// мовчання тут читалось би як «доходу не було».
function gapsHTML(gaps) {
  if (!(gaps || []).length) return "";
  return `<div class="sub-xs t-warn">Дивіденд мав бути, запису немає: ${
    gaps.map((g) => `${esc(g.fund)} — ${g.months.map(esc).join(", ")}`).join("; ")
  }. Заведіть виписку за ці місяці, інакше картка занижує дохід.</div>`;
}

export function taxHTML(x) {
  if (!x) return "";
  const now = new Date().getFullYear();
  const sel = x.year || taxYear();
  const years = Array.from({ length: 5 }, (_, i) => now - i);
  const picker = `<select data-tax-year aria-label="Податковий рік">${years.map((y) =>
    `<option value="${y}"${y === sel ? " selected" : ""}>${y}</option>`).join("")}</select>`;
  // Податок — ЗАВЖДИ в гривні, хоч би в чому звітував документ: платиться
  // він у гривні за курсом на дату події, і звіт для декларації бекенд у
  // валюту звітності не перекладає (єдиний такий маршрут). Символ тому не
  // з currency.js, а з власного поля відповіді; старіший бекенд поля не
  // шле — тоді гривня.
  const taxCur = x.currency || "UAH";
  const tax = (v) => fmtCur(v, taxCur);
  // Порожній рік — не привід ховати картку: «за 2023-й податків не було»
  // це відповідь, а зникла картка читається як поломка.
  const body = opsGrid({
    cols: [
      { key: "label", label: "Джерело", cell: (l) => esc(l.label) },
      { key: "gross", label: "Нараховано", num: true, cell: (l) => tax(l.gross_uah) },
      { key: "tax", label: "Податок", num: true,
        cell: (l) => (l.tax_uah ? "−" + tax(l.tax_uah) : "—") },
      { key: "net", label: "Чистими", num: true, cell: (l) => tax(l.net_uah) },
      // Ставку показуємо лише на ДОДАТНОМУ нарахованому. Рядок НКД
      // відʼємний, тобто істинний, і без цієї умови в колонці стояло б
      // «0,0%» — ставка на поверненні власних грошей, тобто не мале
      // число, а помилка категорії.
      { key: "rate", label: "Ставка", num: true,
        cell: (l) => (l.gross_uah > 0 ? pct(l.rate_pct) : "—") },
    ],
    rows: x.by_kind || [],
    rowAttrs: taxRowAttrs,
    caption: `Податок на дохід за ${esc(String(sel))}: джерело, нараховано, податок, чистими, ставка`,
    foot: [
      { cell: "Разом" },
      { cell: tax(x.gross_uah), num: true },
      { cell: "−" + tax(x.tax_uah), num: true },
      { cell: tax(x.net_uah), num: true },
      { cell: pct(x.rate_pct), num: true },
    ],
    // Порожній рік — не привід ховати картку: «за 2023-й податків не було»
    // це відповідь, а зникла картка читається як поломка.
    empty: "За цей рік оподаткованого доходу не було.",
  });
  // Знижка — ОКРЕМОЮ таблицею під основною, а не рядком у ній: там
  // арифметика «нараховано − податок = чистими», і від'ємний рядок ламає
  // всі три числа разом зі ставкою. І в «Разом» вона не входить навмисно:
  // те число відповідає на «скільки з мене взяли».
  const credits = (x.credits || []).length
    ? `<h4 class="mt">Держава повертає</h4>
       ${opsGrid({
    cols: [
      { key: "label", label: "Підстава", cell: (l) => esc(l.label) },
      { key: "back", label: "Повернення", num: true, cls: "t-ok",
        cell: (l) => "+" + tax(l.net_uah) },
    ],
    rows: x.credits,
    caption: "Що держава повертає: підстава й сума",
  })}
       <div class="sub-xs t-warn">Це ОЦІНКА, а не факт, і в «Разом» вище вона не входить.
         Знижку треба отримати декларацією до 31 грудня наступного року; вона працює лише
         проти зарплати й не переноситься на інші роки.</div>`
    : "";
  return `<div class="card">${disclosure("tax", "Податок на дохід",
    `<div class="sub card-head">
       <span>${esc(x.from)} → ${esc(x.to)}</span><span>рік: ${picker}</span></div>
     ${body}
     ${credits}
     <div class="sub card-head mt-sm">
       <span>Купон ОВДП звільнений від податку, дивіденд фонду й відсотки вкладу — ні.
         Ставки не зашиті: у фонду береться фактично утримане з виписки, у вкладу —
         ставка самого вкладу. З нарахованого віднято накопичений купон, сплачений при купівлі:
         у брудній ціні вже сидів купон попереднього власника, і його повернення —
         не дохід. Віднімається на дату того купона, який його повернув.
         База продажу сертифікатів — прибуток за FIFO, а не виручка; конвертація
         між фондами продажем не вважається.</span>
       <button class="sm" data-tax-csv="${sel}">Завантажити CSV</button></div>
     ${gapsHTML(x.fund_gaps)}
     ${x.fx_basis ? `<div class="sub-xs">Валютні суми: ${esc(x.fx_basis)}${
        x.fx_max_lag_days > 1 ? `; найбільше відставання ${x.fx_max_lag_days} ${
          plural(x.fx_max_lag_days, "день", "дні", "днів")}` : ""}.${
        x.note ? ` ${esc(x.note)}.` : ""}</div>` : ""}`,
    pct(x.rate_pct))}</div>`;
}
