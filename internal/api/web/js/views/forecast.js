// Картка прогнозу: скільки треба вносити щомісяця, щоб дійти до цілі.
//
// Живе окремим модулем, бо потрібна двом розділам одразу — «Огляду»
// (коротко, як орієнтир) і «Майбутньому» (у повному оточенні проєкцій).
// Доки вона була методом, обидва розділи малювали її НЕЗАЛЕЖНО, і будь-яка
// правка формулювання мала шанс поїхати лише в одному з них.

import { esc, curSym, humanMonths, monthYear, monthYearGen, pct, uah2 as fmtUAH } from "../format.js";
import { infoBtn } from "../info.js";

// Віяло розкидає ПОТРІБНИЙ ВНЕСОК, а не суму на дедлайн: щойно внесок
// підбирається під ціль, сума на дедлайн у всіх сценаріях однакова —
// це і є ціль. Корисне натомість те, скільки ціль КОШТУЄ щомісяця за
// різного ринку.
//
// Верстка: спершу вилка «від і до» одним великим числом, бо саме вона
// відповідає на «наскільки це реально». Далі рядки з допущеннями, з
// яких прибрано все, що в них однакове: сьогоднішні ставки в усіх
// рядках ті самі, тож вони винесені в шапку один раз, а в рядках
// лишились тільки довгострокові. Смужки в ринкових рядках прибрано —
// 72/85/100% візуально майже не відрізнялись і лише додавали шуму.
//
// Старий бекенд required_monthly у рядках не надсилає — тоді лишаємо
// попередній вигляд (сума на дедлайн), щоб панель не показувала порожньо.
export function goalsHTML(ctx) {
  const s = ctx.summary || {};
  const f = s.forecast;
  if (!f || !(f.rows || []).length) {
    return `<div class="card" id="fcCard"><h2>Скільки треба вносити</h2><div class="muted">Задай ціль
      і дедлайн у «Налаштуваннях» — і тут зʼявиться, скільки треба відкладати щомісяця
      за песимістичного, реалістичного й оптимістичного сценаріїв.</div></div>`;
  }
  // Доларового вигляду тут немає навмисно. Внески ти платиш гривнею —
  // про це каже й підказка картки, — а суми вже приведені до
  // сьогоднішньої купівельної спроможності. Друге перетворення поверх
  // першого давало число, з яким нічого не зробиш. Питання «скільки це
  // в доларах» лишилось, але воно про НАЯВНИЙ капітал, і на нього
  // відповідає плитка на «Огляді».
  const money = (v) => fmtUAH(v);
  const goal = f.goal_amount || 0;
  const hist = Number(s.actual_months || 0);
  const asPayment = f.rows.some((r) => r.required_monthly > 0);
  // Внески лишаються в гривні навіть у доларовому вигляді: відкладаєш
  // ти гривні, і рішення про суму приймаєш теж у гривнях.
  const payOf = (r) => (r.key === "actual" ? r.contrib_monthly : r.required_monthly) || 0;
  const market = f.rows.filter((r) => r.key !== "actual");
  const actual = f.rows.find((r) => r.key === "actual");
  const real = market.find((r) => r.key === "realistic") || {};
  const need = payOf(real);
  const COLOR = { optimistic: "var(--oi-optimistic)", realistic: "var(--oi-realistic)",
    pessimistic: "var(--oi-pessimistic)" };
  // Планові суми — без копійок: у числі «96 973,50 ₴/міс» дробова
  // частина не несе рішення, а заважає порівнювати рядки поглядом.
  const pay = (v) => Math.round(v || 0).toLocaleString("uk-UA") + " ₴";
  const goalFmt = (v) => pay(v);

  // Вилка — головне число блока.
  let range = "";
  if (asPayment && market.length) {
    const vals = market.map(payOf).filter((v) => v > 0).sort((a, b) => a - b);
    if (vals.length) {
      range = `<div style="margin:2px 0 12px">
        <div style="font-size:22px;font-weight:600">${Math.round(vals[0]).toLocaleString("uk-UA")} — ${pay(vals[vals.length - 1])}<span style="font-size:14px;font-weight:400">/міс</span></div>
        ${need > 0 ? `<div class="muted" style="font-size:12px;margin-top:2px">найімовірніше ${pay(need)}/міс</div>` : ""}
      </div>`;
    }
  }

  // Довгострокові ставки — єдине, чим рядки відрізняються. Вони
  // НОМІНАЛЬНІ, а знецінення поруч — окремий множник сценарію: саме
  // тому обидва й показано в одному рядку.
  const termRates = (r) => (r.by_currency || []).map((c) =>
    c.rate_terminal_pct && Math.abs(c.rate_terminal_pct - c.rate_pct) > 0.05
      ? `${curSym(c.currency)} →${pct(c.rate_terminal_pct)}`
      : `${curSym(c.currency)} ${pct(c.rate_pct)}`).join(" · ");

  const marketRows = market.map((r) => {
    const val = asPayment ? payOf(r) : (r.amount || 0);
    // Позначки «найімовірніше» тут немає: її вже сказано у вилці вище,
    // а реалістичний рядок і так виділений кольором.
    return `<div style="margin-bottom:8px">
      <div style="display:flex;justify-content:space-between;align-items:baseline;gap:8px">
        <span style="color:${COLOR[r.key] || "inherit"}">${esc(r.label)}</span>
        <span><b>${asPayment ? pay(val) + "/міс" : money(val)}</b></span>
      </div>
      <div class="muted" style="font-size:11px;margin-top:1px">${termRates(r)} номінальних · гривня слабшає ${pct(r.devaluation_pct)}/рік</div>
    </div>`;
  }).join("");

  // Факт: головне тут не сума, а яку частку потрібного ти покриваєш.
  let actualBlock = "";
  if (actual) {
    const share = need > 0 ? Math.min(100, payOf(actual) / need * 100) : 0;
    const eta = actual.goal_months === -1 ? "вже досягнуто"
      : actual.goal_months > 0 ? `${monthYear(actual.goal_date)}`
      : "не досягається за 60 років";
    actualBlock = `<div style="border-top:1px solid var(--oi-border);padding-top:10px;margin-top:10px">
      <div style="display:flex;justify-content:space-between;align-items:baseline;gap:8px">
        <span>За фактом ${hist > 0 ? `<span class="muted" style="font-size:12px">за ${humanMonths(hist)} історії</span>` : ""}</span>
        <span><b>${pay(payOf(actual))}/міс</b>${asPayment && need > 0
          ? ` <span style="color:var(--oi-info)">— ${share.toFixed(0)}% від потрібного</span>` : ""}</span>
      </div>
      ${asPayment && need > 0 ? `<div class="progress" style="margin-top:6px"><span style="width:${share}%;background:var(--oi-info)"></span></div>` : ""}
      <div class="muted" style="font-size:11px;margin-top:4px">на дедлайн ${goalFmt(actual.amount)}${
        goal > 0 ? ` — ${(actual.goal_pct || 0).toFixed(1)}% цілі` : ""} · за цим темпом ціль ${eta}</div>
    </div>`;
  } else {
    actualBlock = `<div class="muted" style="font-size:12px;border-top:1px solid var(--oi-border);padding-top:10px;margin-top:10px">
      Прогноз за фактичним темпом зʼявиться після першого поповнення.</div>`;
  }

  // Сьогоднішні ставки однакові в усіх рядках — кажемо їх один раз тут.
  const nowRates = (real.by_currency || []).map((c) =>
    `${curSym(c.currency)} ${pct(c.rate_pct)}`).join(" · ");
  const head = `<div class="sub">${
    asPayment && goal > 0 ? `щоб дійти до ${goalFmt(goal)} до ${monthYearGen(f.date)}` : `на ${monthYear(f.date)}`
    } · через ${humanMonths(f.months)}</div>
    ${nowRates ? `<div class="sub-xs">сьогодні ставки ${nowRates} номінальних${
      f.glide_years > 0 ? ` → сповзають до довгострокових за ${humanMonths(Math.round(f.glide_years * 12))}` : ""}</div>` : ""}
    <div class="sub-xs">Суми — у гривні сьогоднішньої купівельної спроможності: знецінення вже
      враховане всередині моделі, тож із сьогоднішніми витратами їх можна порівнювати прямо.</div>`;
  return `<div class="card" id="fcCard"><h2 class="h-row">
    <span>${asPayment ? "Скільки треба вносити" : "Скільки буде на дедлайн"} ${infoBtn("forecast")}</span></h2>
    ${head}${range}${marketRows}${actualBlock}</div>`;
}

// ---------- «Що як»: який важіль наскільки зрушує ціль ----------
//
// Продовження картки прогнозу, а не її заміна. Та каже «за фактом
// покривається 9%» і зупиняється; ця відповідає на питання, з яким після
// цього лишається читач.
//
// Рядки НЕ сортуються «найкращий зверху» і не мають позначки
// «рекомендовано»: у коді записано «Це інструмент, не порада», і половина
// важелів (ставка, знецінення) від людини взагалі не залежить. Групи
// стоять у сталому порядку — спершу те, що людина рухає сама, потім
// погода, потім самі умови задачі.

// Підпис важеля будується ТУТ, а не в бекенді: документ несе числа й
// ключ, як RebalanceRow. Інакше форматування жило б у двох місцях.
const LEVER_GROUP = [
  ["contrib", "Внесок", "єдине, що ти рухаєш сам"],
  ["rate", "Ставка", "куди прийде довгострокова"],
  ["deval", "Знецінення", "як швидко слабшає гривня"],
  ["deadline", "Дедлайн", "коли саме ти чекаєш ціль"],
  ["goal", "Ціль", "скільки з неї вже покривається"],
];

// Як прочитати зсув рядка. Множник для внеску й цілі, п.п. для ринку,
// місяці для дедлайну — заповнене рівно одне.
function leverShift(r) {
  if (r.factor) return `×${String(r.factor).replace(".", ",")}`;
  if (r.delta_pp) return `${r.delta_pp > 0 ? "+" : "−"}${Math.abs(r.delta_pp)} п.п.`;
  if (r.delta_months) return `${r.delta_months > 0 ? "+" : "−"}${Math.abs(r.delta_months)} міс`;
  return "";
}

// Величина після зсуву — у своїй одиниці. Одиницю знає лише важіль, тож
// вибір тут, а не у форматері.
function leverValue(r) {
  const round = (v) => Math.round(v || 0).toLocaleString("uk-UA");
  switch (r.lever) {
    case "contrib": return `${round(r.value)} ₴/міс`;
    case "rate": return `${r.value > 0 ? "+" : ""}${r.value} п.п.`;
    case "deval": return `${pct(r.value)}/рік`;
    case "deadline": return humanMonths(r.value);
    case "goal": return `${round(r.value)} ₴`;
    default: return "";
  }
}

// Коли ціль буде досягнута. Нуль означає «не досягається за 60 років» —
// саме так, а не «0 місяців»: різниця між «нескоро» й «негайно»
// протилежна, і плутати їх не можна.
function goalWhen(months, date) {
  if (months === -1) return "вже";
  if (months > 0) return monthYear(date);
  return "не досягається";
}

export function sensitivityHTML(ctx) {
  const s = (ctx.summary || {}).sensitivity;
  if (!s || !(s.rows || []).length) return "";
  const round = (v) => Math.round(v || 0).toLocaleString("uk-UA");
  const baseWhen = goalWhen(s.base_goal_months, s.base_goal_date);
  const baseFrom = s.base_from === "actual"
    ? "від фактичного темпу" : "від планового внеску";

  const groups = LEVER_GROUP.map(([key, title, why]) => {
    const rows = s.rows.filter((r) => r.lever === key);
    if (!rows.length) return "";
    const items = rows.map((r) => {
      const when = goalWhen(r.goal_months, r.goal_date);
      // Стрілка лише там, де є що порівнювати: для дедлайну місяць
      // досягнення навмисно базовий, тож рухається сама сума.
      const moved = r.goal_months !== s.base_goal_months;
      return `<div style="display:flex;justify-content:space-between;align-items:baseline;gap:8px;margin-bottom:5px">
        <span><b>${esc(leverShift(r))}</b> <span class="muted" style="font-size:12px">${esc(leverValue(r))}</span></span>
        <span style="text-align:right">
          ${moved ? `<b>${esc(when)}</b>` : `<span class="muted">${esc(when)}</span>`}
          <span class="muted" style="font-size:11px"> · ${(r.goal_pct || 0).toFixed(0)}% цілі</span>
        </span>
      </div>`;
    }).join("");
    return `<div style="margin-bottom:12px">
      <div class="sub-xs" style="margin-bottom:4px"><b>${esc(title)}</b> · ${esc(why)}</div>
      ${items}</div>`;
  }).join("");

  return `<div class="card"><h2 class="h-row"><span>Що зрушить ціль ${infoBtn("sensitivity")}</span></h2>
    <div class="sub">Один вхід за раз, ${esc(baseFrom)} ${round(s.base_contrib_uah)} ₴/міс.
      Зараз ціль ${esc(baseWhen)} — ${(s.base_goal_pct || 0).toFixed(0)}% на дедлайн.</div>
    <div class="sub-xs" style="margin-bottom:10px">Це наслідки припущень, а не поради: рядки не
      відсортовані «найкращий зверху», і половина з них — ставка й знецінення — від тебе не
      залежить узагалі.</div>
    ${groups}</div>`;
}

