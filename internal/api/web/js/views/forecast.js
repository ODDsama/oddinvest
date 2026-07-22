// Картка прогнозу: скільки треба вносити щомісяця, щоб дійти до цілі.
//
// Живе окремим модулем, бо потрібна двом розділам одразу — «Огляду»
// (коротко, як орієнтир) і «Майбутньому» (у повному оточенні проєкцій).
// Доки вона була методом, обидва розділи малювали її НЕЗАЛЕЖНО, і будь-яка
// правка формулювання мала шанс поїхати лише в одному з них.

import { esc, curSym, humanMonths, monthYear, monthYearGen, uah2 as fmtUAH } from "../format.js";
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
  const rate0 = f.rate0_usd || 0;
  const usd = ctx.fcUnit === "USD" && rate0 > 0;
  const money = (v) => usd
    ? "$" + Math.round((v || 0) / rate0).toLocaleString("uk-UA")
    : fmtUAH(v);
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
  const goalFmt = (v) => usd ? money(v) : pay(v);

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

  // Довгострокові ставки — єдине, чим рядки відрізняються.
  const termRates = (r) => (r.by_currency || []).map((c) =>
    c.rate_terminal_pct && Math.abs(c.rate_terminal_pct - c.rate_pct) > 0.05
      ? `${curSym(c.currency)} →${c.rate_terminal_pct.toFixed(1)}%`
      : `${curSym(c.currency)} ${(c.rate_pct || 0).toFixed(1)}%`).join(" · ");

  const marketRows = market.map((r) => {
    const val = asPayment ? payOf(r) : (r.amount || 0);
    // Позначки «найімовірніше» тут немає: її вже сказано у вилці вище,
    // а реалістичний рядок і так виділений кольором.
    return `<div style="margin-bottom:8px">
      <div style="display:flex;justify-content:space-between;align-items:baseline;gap:8px">
        <span style="color:${COLOR[r.key] || "inherit"}">${esc(r.label)}</span>
        <span><b>${asPayment ? pay(val) + "/міс" : money(val)}</b></span>
      </div>
      <div class="muted" style="font-size:11px;margin-top:1px">${termRates(r)} · гривня слабшає ${(r.devaluation_pct || 0).toFixed(1)}%/рік</div>
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
    `${curSym(c.currency)} ${(c.rate_pct || 0).toFixed(1)}%`).join(" · ");
  const unitBtn = (u, lbl) => `<button class="unit${usd === (u === "USD") ? " on" : ""}" data-fcunit="${u}">${lbl}</button>`;
  const toggle = rate0 > 0 ? `<span class="unitbox">${unitBtn("UAH", "₴")}${unitBtn("USD", "$")}</span>` : "";
  const head = `<div class="sub">${
    asPayment && goal > 0 ? `щоб дійти до ${goalFmt(goal)} до ${monthYearGen(f.date)}` : `на ${monthYear(f.date)}`
    } · через ${humanMonths(f.months)}</div>
    ${nowRates ? `<div class="sub-xs">сьогодні ставки ${nowRates}${
      f.glide_years > 0 ? ` → сповзають до довгострокових за ${humanMonths(Math.round(f.glide_years * 12))}` : ""}</div>` : ""}`;
  return `<div class="card" id="fcCard"><h2 class="h-row" style="justify-content:space-between">
    <span>${asPayment ? "Скільки треба вносити" : "Скільки буде на дедлайн"} ${infoBtn("forecast")}</span>${toggle}</h2>
    ${head}${range}${marketRows}${actualBlock}</div>`;
}

