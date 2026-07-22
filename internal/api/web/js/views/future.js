// Розділ «Майбутнє» — куди це йде.
//
// Календар майбутніх виплат зі статусами, пасивний дохід, проєкції
// капіталу й картка прогнозу під ціль. Спільне в них те, що жодне число
// тут ще не сталось.
//
// Картка прогнозу («скільки треба вносити») тепер малюється РІВНО ТУТ.
// Доти вона стояла і на «Огляді», і тут — два рендери одного й того
// самого, кожен зі своїм шансом відстати від іншого.

import { esc, today, humanMonths, uah2 as fmtUAH, money as fmtMoney } from "../format.js";
import { infoBtn } from "../info.js";
import { svgBars, svgLine } from "../charts.js";
import { PAY_TYPES, PAY_CLASS } from "../constants.js";
import { goalsHTML } from "./forecast.js";

// Дохід по місяцях: коли саме надійдуть купони й погашення на рік наперед.
export function income12mChartHTML(ctx) {
  const inc = (ctx.summary || {}).income_12m || [];
  if (!inc.some((m) => m.amount > 0)) return "";
  return `<div class="card"><h4>Дохід по місяцях ${infoBtn("income12m")}</h4>
    ${svgBars(inc.map((m) => ({ label: m.month.slice(5), value: m.amount, color: "var(--oi-series-nominal)" })))}
    <div class="sub">Купони + погашення на рік наперед (грн-екв.).</div></div>`;
}

// Крива капіталу: внески без відсотків проти внесків із реінвестом.
export function capitalChartHTML(ctx) {
  const proj = (ctx.summary || {}).projection || [];
  if (!proj.length) return "";
  return `<div class="card"><h4>Крива капіталу ${infoBtn("capital")}</h4>
    ${svgLine(proj.map((p) => p.years + "р"), [
      { color: "var(--oi-series-neutral)", values: proj.map((p) => p.contributed) },
      { color: "var(--oi-series-invested)", values: proj.map((p) => p.with_reinvest) },
    ])}
    <div class="lg"><span><i style="background:var(--oi-series-neutral)"></i>внесено</span>
      <span><i style="background:var(--oi-series-invested)"></i>з реінвестом</span></div></div>`;
}

// ---------- ПРОЄКЦІЇ (блок вкладки «Майбутнє») ----------
export function projectionHTML(ctx) {
  const s = ctx.summary || {};
  const st = s.settings || {};
  const P0 = (s.nominal_uah_eq || 0) + (s.account_uah || 0);
  const C = s.month_target_uah || 0;
  const rowsData = s.projection || [];
  const rate = s.projection_rate_pct || 0;
  // Ставка не вічна, тож у підписі показуємо ШЛЯХ, а не одну цифру:
  // інакше читач вважає, що воєнні 16-17% закладені на весь горизонт.
  const term = ((s.forecast || {}).rows || []).find((r) => r.key === "realistic") || {};
  const gy = (s.forecast || {}).glide_years || 0;
  const rateSrc = rate <= 0 ? "додай папери — і дохідність порахується сама"
    : term.rate_terminal_pct && gy > 0 && Math.abs(term.rate_terminal_pct - rate) > 0.05
      ? `за портфелем ${rate.toFixed(1)}% (YTM) сьогодні → ${term.rate_terminal_pct.toFixed(1)}% за ${humanMonths(Math.round(gy * 12))}`
      : `за портфелем ${rate.toFixed(1)}% (YTM до погашення)`;

  const hasActual = (s.actual_monthly_uah || 0) > 0;
  const rows = rowsData.length ? rowsData.map((r) =>
    `<tr><td>${r.years} р.</td><td class="num">${fmtUAH(r.contributed)}</td>
      <td class="num">${fmtUAH(r.with_reinvest)}</td>
      ${hasActual ? `<td class="num">${fmtUAH(r.with_reinvest_actual || 0)}</td>` : ""}
      <td class="num">${fmtUAH(r.with_reinvest - r.contributed)}</td></tr>`).join("")
    : `<tr><td colspan="${hasActual ? 5 : 4}" class="muted">Додай папери й ціль на місяць, щоб побачити проєкцію.</td></tr>`;
  const paceNote = hasActual
    ? `<div class="muted" style="margin-bottom:10px;font-size:13px">Фактичний темп поповнень: <b>${fmtUAH(s.actual_monthly_uah)}/міс</b> за ${s.actual_months} міс історії (план — ${fmtUAH(C)}/міс).</div>`
    : `<div class="muted" style="margin-bottom:10px;font-size:13px">Прогноз за фактичним темпом зʼявиться після першого поповнення.</div>`;

  return `
    <div class="card">
      <h2>Проєкції капіталу</h2>
      <div class="muted" style="margin-bottom:10px">Старт = капітал ${fmtUAH(P0)}, внесок = ${fmtUAH(C)}/міс, ставка = ${rateSrc}. Модель: реальні купони й погашення наявних паперів + внески, реінвест під ставку; готівка не працює до реінвесту. Обидві колонки — у гривні сьогоднішньої купівельної спроможності, тож «внесено» теж знецінюється: приріст показує, наскільки вкладати вигідніше, ніж просто відкладати. Це припущення, не гарантія.</div>
      ${paceNote}
      <table><thead><tr><th>Горизонт</th><th class="num">Внесено (без %)</th>
        <th class="num">За планом</th>${hasActual ? `<th class="num">За фактом</th>` : ""}
        <th class="num">Приріст</th></tr></thead>
        <tbody>${rows}</tbody></table>
    </div>`;
  // Картка цілей сюди НЕ входить: розділ малює її сам, першим блоком.
  // Доки вона висіла хвостом проєкцій, «Майбутнє» показувало її двічі —
  // саме той дубль, заради якого й затівалось перегрупування.
}

// ---------- КАЛЕНДАР ----------
// append=true дописує календар до вже намальованого розділу, а не
// затирає його: у «Майбутньому» він стоїть останнім блоком, після
// прогнозів.
export async function renderCalendar(ctx, main, { append = false } = {}) {
  const cal = await ctx.api("GET", "calendar?from=1970-01-01");
  const now = today();
  const rows = cal.slice().sort((a, b) => a.date.localeCompare(b.date));
  const html = `
    <div class="card">
      <h2>Виплати</h2>
      <div class="muted" style="margin-bottom:10px">Минулі виплати можна позначати отримано / перевкладено.</div>
      ${rows.length ? `<table><thead><tr>
        <th>Дата</th><th>ISIN</th><th>Тип</th><th class="num">Сума</th><th>Статус</th><th></th></tr></thead><tbody>
        ${rows.map((c) => {
          const past = c.date <= now;
          const st = c.status || "";
          const pill = st === "reinvested" ? `<span class="pill reinv">перевкладено</span>`
            : st === "received" ? `<span class="pill recv">отримано</span>` : `<span class="muted">—</span>`;
          return `<tr>
            <td>${esc(c.date)}</td><td>${esc(c.isin)}</td>
            <td><span class="pill ${PAY_CLASS[c.type] || ""}">${PAY_TYPES[c.type] || c.type}</span></td>
            <td class="num">${fmtMoney(c.amount)}</td><td>${pill}</td>
            <td class="row-actions">${past ? `
              <button class="sm" data-isin="${esc(c.isin)}" data-date="${esc(c.date)}" data-st="received">Отримано</button>
              <button class="sm" data-isin="${esc(c.isin)}" data-date="${esc(c.date)}" data-st="reinvested">Перевкл.</button>` : ""}</td>
          </tr>`;
        }).join("")}</tbody></table>` : `<div class="muted">Виплат немає.</div>`}
    </div>`;
  if (append) main.insertAdjacentHTML("beforeend", html);
  else main.innerHTML = html;
  main.querySelectorAll("[data-st]").forEach((b) =>
    b.addEventListener("click", async () => {
      try {
        await ctx.api("POST", "payments/status", { isin: b.dataset.isin, pay_date: b.dataset.date, status: b.dataset.st });
        ctx.toast("Статус збережено"); ctx.reload();
      } catch (err) { ctx.toast(String(err.message || err), false); }
    }));
}

// ---------- МАЙБУТНЄ: календар + проєкції + ціль ----------
// Порядок навмисний: спершу «скільки треба вносити» — питання, з яким
// сюди заходять, — потім потік, який уже забезпечено наявними паперами,
// далі модель на роки й аж наприкінці календар по датах.
export async function renderFuture(ctx, main) {
  main.innerHTML = `
    ${goalsHTML(ctx)}
    ${incomeHTML(ctx)}
    <div class="chart-grid">
      ${income12mChartHTML(ctx)}
      ${capitalChartHTML(ctx)}
    </div>
    ${projectionHTML(ctx)}`;
  await renderCalendar(ctx, main, { append: true });
}


// ---------- пасивний дохід ----------

// Пасивний дохід: скільки папери приноситимуть щомісяця. Саме КУПОННИЙ
// потік — погашення це повернення власного тіла, а не дохід, і плутати
// їх означало б завищувати відповідь удвічі на коротких горизонтах.
export function incomeHTML(ctx) {
  const s = ctx.summary || {};
  const rows = (s.projection || []).filter((r) => r.income_monthly > 0);
  const now = Number(s.income_monthly_now || 0);
  if (!rows.length && now <= 0) return "";
  // Без копійок, як і в решті планових чисел: дробова частина місячного
  // доходу на горизонті в роки — це точність, якої в оцінці немає.
  const inc = (v) => Math.round(v || 0).toLocaleString("uk-UA") + " ₴";
  const line = (label, v, extra = "") => `<div style="display:flex;justify-content:space-between;align-items:baseline;gap:8px;margin-bottom:6px">
    <span class="muted" style="font-size:13px">${label}</span>
    <span><b>${inc(v)}</b><span class="muted" style="font-size:12px">/міс</span>${extra}</span></div>`;
  const body = rows.map((r) => line(`через ${humanMonths(r.years * 12)}`, r.income_monthly,
    r.income_monthly_actual > 0 && Math.abs(r.income_monthly_actual - r.income_monthly) > 1
      ? ` <span class="muted" style="font-size:11px">· за фактом ${inc(r.income_monthly_actual)}</span>` : "")).join("");
  return `<div class="card"><h2 class="h-row" style="justify-content:space-between">
    <span>Пасивний дохід ${infoBtn("income")}</span></h2>
    <div class="muted" style="font-size:12px;margin-bottom:8px">скільки папери приноситимуть щомісяця, у сьогоднішніх гривнях</div>
    ${line("зараз", now)}
    <div style="border-top:1px solid var(--oi-border);padding-top:6px;margin-top:4px">${body}</div>
  </div>`;
}

