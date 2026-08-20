// Картки «Плану», які лише показують: вердикт, важелі, профіль надходжень,
// план проти факту.
//
// Межа модуля проведена по тому, чи є в картки ФОРМА. Тут її немає в
// жодної: усе, що нижче, читає зведення й стрічку і повертає розмітку.
// Форми живуть у сусідніх модулях разом зі своєю проводкою, бо саме
// проводка й робить їх важкими — обробники, модалки правки, префіли.
//
// Практичний наслідок: цей файл потрібен КОЖНІЙ сторінці «Плану» (вердикт
// стоїть шапкою на всіх чотирьох), а решта — лише своїй.

import {
  esc, uah0 as fmtUAH, pct, dayMonth, humanMonths, monthYear, monthShort, plural,
} from "../format.js";
import { infoBtn } from "../info.js";
import { empty, legend } from "../components.js";
import { disclosure } from "../disclosure.js";
import { opsGrid } from "../grid.js";
import { CONTRIB, contribTriad } from "../contrib.js";
import {
  fluid, svgInflowProfile, svgGrouped, wireChartTips, CAT_COLORS, EVENT_COLORS,
} from "../charts.js";
// Вердикт домальовує підрядок про поточний місяць, а той рахується з
// відміток надходжень. Напрямок залежності саме такий і не інакший:
// підсумок спирається на журнал, не навпаки.
import { monthKeyOffset, amtOf } from "./plan-receipts.js";

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
// thisMonthHTML — підрядок про ПОТОЧНИЙ місяць: скільки з обіцяного вже
// відмічено.
//
// Саме підрядок, а не четверта плитка. Тріада Треба/План/Факт канонічна —
// ті самі три слова названі в CONTRIB і стоять ще в п'яти місцях
// застосунку, — і четверта плитка поруч читалася б як рівноправний
// показник, тоді як це зріз одного місяця, а не темп.
//
// Поточний місяць не входить у стовпчики «Плану проти факту» (він ще
// триває), тож без цього рядка про нього не сказано ніде.
function thisMonthHTML(doc) {
  const key = monthKeyOffset(0);
  const rows = (doc.expected || []).filter((e) => e.month === key);
  if (!rows.length) return "";
  const marked = rows.filter((e) => e.receipt);
  if (!marked.length) {
    return `<div class="sub-xs mt-xs">${esc(monthYear(key + "-01"))}: жодне з
      ${rows.length} ${plural(rows.length, "джерела", "джерел", "джерел")} ще не відмічене.</div>`;
  }
  const got = marked.reduce((a, e) => a + (e.receipt.gives_uah || 0), 0);
  const planned = rows.reduce((a, e) => a + (e.plan_uah || 0), 0);
  // Ті, що НЕ прийшли, називаються поіменно: «надійшло менше» без причини
  // — це половина відповіді, а причина вже записана в самій відмітці.
  const missed = marked.filter((e) => amtOf(e.receipt.amount) === 0)
    .map((e) => esc(e.name) + (e.receipt.note ? ` (${esc(e.receipt.note)})` : ""));
  return `<div class="sub-xs mt-xs">${esc(monthYear(key + "-01"))}: відмічено
    ${marked.length} із ${rows.length} — надійшло <b>${fmtUAH(got)}</b> із запланованих
    <b>${fmtUAH(planned)}</b>${missed.length
    ? ` · <span class="t-warn">не прийшло: ${missed.join(", ")}</span>` : ""}.</div>`;
}

export function planVerdictHTML(ctx, doc = null) {
  const t = contribTriad(ctx);
  const month = doc ? thisMonthHTML(doc) : "";

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
      <div class="sub-xs mt-sm">Задай ціль і дедлайн у «Налаштуваннях», щоб побачити, чи цього досить.</div>
      ${month}</div>`;
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
    ${month}${outlives}${leversHTML(ctx)}</div>`;
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
  const body = opsGrid({
    cols: [
      { key: "date", label: "Дата", cls: "nowrap", cell: (e) => esc(dayMonth(e.date)) },
      { key: "what", label: "Що", cell: (e) => `<i class="swatch" style="--oi-c:${
        EVENT_COLORS[e.kind] || "var(--oi-muted)"}"></i>${esc(eventName(e))}` },
      { key: "amount", label: "Сума", num: true, cls: "nowrap",
        cell: (e) => fmtUAH(e.amount_uah) },
    ],
    rows: events,
    caption: "Що повертається: дата, подія, сума",
    foot: [{ cell: "Разом", span: 2 }, { cell: fmtUAH(total), num: true }],
  }) + `<div class="sub-xs">Це не дохід і не внесок: власне тіло виходить із паперу на рахунок,
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

// Четвертий ряд — «надійшло»: скільки грошей ПРИЙШЛО за відмітками, тоді
// як «факт» поруч — скільки з них зрештою внесено в портфель. Різниця між
// ними і є те, що досі не було видно ніде: зарплата могла прийти вся, а
// доїхати до брокера — наполовину.
//
// Зелений (--oi-series-nominal) вільний і не збігається ні з планом, ні з
// поповненнями, ні з нестачею — нового токена заводити не довелось.
const PLAN_FACT_COLORS = [
  "var(--oi-series-plan)", "var(--oi-series-nominal)",
  "var(--oi-series-invested)", "var(--oi-series-neutral)",
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
  // Саме p.marked, а не p.received_uah: нуль тут означає «нічого не
  // прийшло» — записаний факт, — і показати його треба, а не сховати за
  // хибністю числа.
  const recv = p.marked
    ? r("Надійшло", p.received_uah || 0, PLAN_FACT_COLORS[1]) : "";
  return `<div><b>${esc(monthYear(p.month + "-01"))}</b></div>
    ${r("План", p.plan_uah, PLAN_FACT_COLORS[0])}
    ${recv}
    ${r("Внесено", p.actual_uah, PLAN_FACT_COLORS[2])}
    ${p.gap_uah ? r("Бракувало (як тоді)", p.gap_uah, PLAN_FACT_COLORS[3]) : ""}
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
  // Ряд «надійшло» вмикається лише тоді, коли є що показувати, — тим самим
  // прийомом, що вже вмикає «бракувало». Порожній четвертий стовпчик у
  // кожному місяці означав би «надійшло нуль», а не «ще не відмічали».
  const hasRecv = pts.some((p) => p.marked);
  // Місяць БЕЗ відмітки в цьому ряду отримує нуль, і намалюється він як
  // відсутній стовпчик — те саме, що показує сама відсутність запису.
  const recvOf = (p) => (p.marked ? p.received_uah || 0 : 0);
  const groups = pts.map((p) => ({
    // Підпис — місяць без року: дванадцять «2026-07» злиплись би в сіру
    // смугу, а рік і так один-два на все вікно.
    label: monthShort(p.month),
    values: [
      p.plan_uah || 0,
      ...(hasRecv ? [recvOf(p)] : []),
      p.actual_uah || 0,
      ...(hasGap ? [p.gap_uah || 0] : []),
    ],
    // Контуром малюється тільки план: решта рядів записана завжди.
    derived: [!!p.plan_derived, false, false, false],
  }));
  const anyDerived = pts.some((p) => p.plan_derived);
  factTip = { points: pts };

  const frame = fluid((w, h) => svgGrouped(groups, {
    W: w, H: h, colors: PLAN_FACT_COLORS, fmt: fmtUAH, hits: true,
  }), { onMount: (box) => wireChartTips(box.closest(".chart-wrap"), factTipHTML) });

  const names = [
    { color: PLAN_FACT_COLORS[0], label: "план" },
    hasRecv && { color: PLAN_FACT_COLORS[1], label: "надійшло" },
    { color: PLAN_FACT_COLORS[2], label: "внесено" },
    hasGap && { color: PLAN_FACT_COLORS[3], label: "бракувало (як тоді)" },
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
  // Різниця «надійшло → внесено» — це найцікавіше, що картка вміє сказати
  // після появи відміток, і сказати це варто числом, а не лишити читачеві
  // віднімати два стовпчики очима. Лише по відмічених місяцях: решта до
  // цього порівняння не входить узагалі.
  //
  // Частка «дійшло до портфеля» рахується ЛИШЕ коли внесено не більше, ніж
  // надійшло. Інакше вона виглядає як 1667%, і це не курйоз, а типовий стан
  // на початку: відмічено одне джерело з п'яти, тоді як поповнення в
  // місяці всі. Показувати відсоток там означало б видавати неповноту
  // відміток за дисципліну.
  const recvWin = win.filter((p) => p.marked);
  const recvLine = recvWin.length
    ? (() => {
      const r = recvWin.reduce((a, p) => a + (p.received_uah || 0), 0) / recvWin.length;
      const a = recvWin.reduce((x, p) => x + (p.actual_uah || 0), 0) / recvWin.length;
      const tail = r > 0 && a <= r
        ? ` — до портфеля дійшло ${pct((a / r) * 100, 0)} того, що прийшло`
        : ` — внесено більше, ніж відмічено: або в цих місяцях відмічені не всі
            джерела, або гроші прийшли не лише з плану`;
      return `<div class="sub-xs">За ${recvWin.length} ${plural(recvWin.length,
        "відмічений місяць", "відмічені місяці", "відмічених місяців")} надійшло
        <b>${fmtUAH(r)}</b>/міс, а внесено <b>${fmtUAH(a)}</b>/міс${tail}.</div>`;
    })()
    : "";

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
    ${recvLine}
    <div class="sub-xs">Внесено — реальні поповнення, нетто зі зняттями (купівля паперів сюди не
      входить: вона лише переносить гроші з рахунку в папери). План береться з журналу правок —
      зі стану потоків на кінець того місяця, тож пізніше підвищення минулого не переписує.
      Валютні суми переведено сьогоднішнім курсом — усі ряди в одних грошах, але це не ті
      гривні, що були тоді.</div></div>`;
}

