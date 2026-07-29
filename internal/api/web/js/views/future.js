// Розділ «Майбутнє» — куди це йде.
//
// Календар майбутніх виплат зі статусами, пасивний дохід, проєкції
// капіталу й картка прогнозу під ціль. Спільне в них те, що жодне число
// тут ще не сталось.
//
// Картка прогнозу («скільки треба вносити») тепер малюється РІВНО ТУТ.
// Доти вона стояла і на «Огляді», і тут — два рендери одного й того
// самого, кожен зі своїм шансом відстати від іншого.

import {
  esc, today, humanMonths, monthYear, pct, capitalUAH,
  uah2 as fmtUAH, money as fmtMoney,
} from "../format.js";
import { infoBtn } from "../info.js";
import { svgBars, svgLine, svgBandLine } from "../charts.js";
import { PAY_TYPES, PAY_CLASS } from "../constants.js";
import { goalsHTML, sensitivityHTML } from "./forecast.js";

// Дохід по місяцях: коли саме надійдуть купони й погашення на рік наперед.
export function income12mChartHTML(ctx) {
  const inc = (ctx.summary || {}).income_12m || [];
  if (!inc.some((m) => m.amount > 0)) return "";
  return `<div class="card"><h4>Дохід по місяцях ${infoBtn("income12m")}</h4>
    ${svgBars(inc.map((m) => ({ label: m.month.slice(5), value: m.amount, color: "var(--oi-series-nominal)" })))}
    <div class="sub">Купони + погашення на рік наперед (грн-екв.).</div></div>`;
}

// Крива капіталу до дедлайну.
//
// Доти тут була лінія по чотирьох точках (1/3/5/10 років) — тих самих, що
// в таблиці нижче. Вона казала, ЧИМ усе скінчиться, і мовчала про те, коли
// саме траєкторія розходиться з планом. А розходиться вона не рівномірно:
// перші роки будуються з фактичного календаря виплат, тож мають форму,
// якої в сухому складному відсотку немає.
//
// Тепер помісячна крива з бекенда: коридор між песимістичним і
// оптимістичним, план посередині, пунктиром — фактичний темп, і
// горизонтальна лінія цілі. Розрив між планом і фактом тут видно як
// відстань, а не як два числа в різних рядках.
//
// Запасний варіант лишається: старі бекенди кривої не надсилають, і
// панель не мусить показувати порожньо.
export function capitalChartHTML(ctx) {
  const s = ctx.summary || {};
  const curve = (s.forecast || {}).curve;
  if (curve && (curve.points || []).length > 1) {
    const pts = curve.points;
    const has = (k) => pts.some((p) => (p[k] || 0) > 0);
    // Мітки лише на роках: 14 підписів на 320 пікселів злипаються в
    // сіру смугу, а рік — та одиниця, у якій читають горизонт.
    const labels = pts.map((p) => p.month % 12 === 0 ? `${p.month / 12}р` : "");
    const lines = [{ color: "var(--oi-series-invested)", values: pts.map((p) => p.plan) }];
    if (has("actual")) {
      lines.push({ color: "var(--oi-series-neutral)", values: pts.map((p) => p.actual), dash: "5 3" });
    }
    const bands = has("optimistic") && has("pessimistic")
      ? { lo: pts.map((p) => p.pessimistic), hi: pts.map((p) => p.optimistic) } : {};
    return `<div class="card"><h4>Крива капіталу ${infoBtn("capitalCurve")}</h4>
      ${svgBandLine(labels, bands, lines, curve.goal_uah || 0)}
      <div class="lg"><span><i style="background:var(--oi-series-invested)"></i>за планом</span>
        ${has("actual") ? `<span><i style="background:var(--oi-series-neutral)"></i>за фактом</span>` : ""}
        ${bands.lo ? `<span><i style="background:var(--oi-series-invested);opacity:.3"></i>коридор ринку</span>` : ""}
        ${curve.goal_uah > 0 ? `<span><i style="background:var(--oi-muted)"></i>ціль</span>` : ""}</div>
      <div class="sub-xs">У сьогоднішніх гривнях, до дедлайну цілі. Крок ${curve.step_months} міс.</div></div>`;
  }
  // Запасний вигляд для старого бекенда: ті самі чотири точки.
  const proj = s.projection || [];
  if (!proj.length) return "";
  return `<div class="card"><h4>Крива капіталу ${infoBtn("capitalCurve")}</h4>
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
  // Старт моделі — увесь капітал МІНУС резерв. Не «номінал + рахунок», як
  // тут стояло: це була четверта, власна відповідь на питання «що таке
  // капітал», і вона не збігалась ні з плиткою «Капітал», ні з тим, від
  // чого модель насправді відштовхується. Резерв віднімається явно — він
  // не інвестується й не компаундиться, тож крива й мусить стартувати
  // нижче за плитку рівно на суму матраца.
  const P0 = capitalUAH(s) - (s.reserve_uah || 0);
  const C = s.month_target_uah || 0;
  const rowsData = s.projection || [];
  const rate = s.projection_rate_pct || 0;
  // Ставка не вічна, тож у підписі показуємо ШЛЯХ, а не одну цифру:
  // інакше читач вважає, що воєнні 16-17% закладені на весь горизонт.
  const term = ((s.forecast || {}).rows || []).find((r) => r.key === "realistic") || {};
  const gy = (s.forecast || {}).glide_years || 0;
  // Ставка тут НОМІНАЛЬНА, а суми в таблиці — реальні: знецінення
  // застосовується всередині моделі, до кожного гривневого рукава
  // окремо. Без цього слова читач вважав би, що ставку вже приведено, і
  // приріст здавався б удвічі меншим, ніж модель насправді рахує.
  const rateSrc = rate <= 0 ? "додай папери — і дохідність порахується сама"
    : term.rate_terminal_pct && gy > 0 && Math.abs(term.rate_terminal_pct - rate) > 0.05
      ? `за портфелем ${pct(rate)} номінальних (YTM) сьогодні → ${pct(term.rate_terminal_pct)} за ${humanMonths(Math.round(gy * 12))}`
      : `за портфелем ${pct(rate)} номінальних (YTM до погашення)`;

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
      <div class="muted" style="margin-bottom:10px">Старт = капітал ${fmtUAH(P0)}${
        s.reserve_uah > 0 ? ` <b>без резерву</b> (${fmtUAH(s.reserve_uah)} у матраці не інвестуються, тож і не ростуть — крива стартує нижче за плитку «Капітал» рівно на цю суму)` : ""
      }, внесок = ${fmtUAH(C)}/міс, ставка = ${rateSrc}. Модель: справжні купони й погашення наявних паперів + внески, реінвест під ставку; готівка не працює до реінвесту. Тіло вкладів і сертифікати фондів входять у старт нарівні з номіналом ОВДП: вклад повертається за графіком, сертифікат лежить безстроково й платить дивідендами. Подорожчання сертифіката модель не малює — його ніхто не обіцяв. <b>Ставка номінальна, суми реальні</b>: знецінення застосовується всередині моделі, окремо до кожного валютного рукава, тож усі колонки — у гривні сьогоднішньої купівельної спроможності. «Внесено» через це теж знецінюється, і приріст показує, наскільки вкладати вигідніше, ніж просто відкладати. Це припущення, не гарантія.</div>
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
      <div class="muted" style="margin-bottom:10px">Виплату, датовану сьогодні чи наперед, можна
        позначити <b>отриманою</b> — тоді вона ляже на рахунок, не чекаючи опівночі. Окремої позначки
        «перевкладено» більше немає: чи пішли гроші в діло, застосунок бачить сам із твоїх покупок.</div>
      ${rows.length ? `<table><thead><tr>
        <th>Дата</th><th>ISIN</th><th>Тип</th><th class="num">Сума</th><th>Статус</th><th></th></tr></thead><tbody>
        ${rows.map((c) => {
          const past = c.date <= now;
          const st = c.status || "";
          // Старі бази можуть іще нести «reinvested» (міграція 0017
          // зводить його до «received», але дамп зроблений до неї — ні),
          // тож читаємо обидва як одне: гроші надійшли.
          const pill = st ? `<span class="pill recv">отримано</span>` : `<span class="muted">—</span>`;
          // Вклади мають синтетичний ISIN "deposit:<id>" — показуємо
          // «вклад», а не внутрішній ключ.
          const label = String(c.isin).startsWith("deposit:") ? "вклад" : c.isin;
          return `<tr>
            <td>${esc(c.date)}</td><td>${esc(label)}</td>
            <td><span class="pill ${PAY_CLASS[c.type] || ""}">${PAY_TYPES[c.type] || c.type}</span></td>
            <td class="num">${fmtMoney(c.amount)}</td><td>${pill}</td>
            <td class="row-actions">${past ? (st
              // Уже позначено — лишається одна дія: зняти позначку. Раз
              // вона рухає гроші, помилковий клік має бути оборотним.
              ? `<button class="sm quiet" data-isin="${esc(c.isin)}" data-date="${esc(c.date)}" data-st="none">Скасувати</button>`
              : `<button class="sm" data-isin="${esc(c.isin)}" data-date="${esc(c.date)}" data-st="received">Отримано</button>`) : ""}</td>
          </tr>`;
        }).join("")}</tbody></table>` : `<div class="muted">Виплат немає.</div>`}
    </div>`;
  if (append) main.insertAdjacentHTML("beforeend", html);
  else main.innerHTML = html;
  main.querySelectorAll("[data-st]").forEach((b) =>
    b.addEventListener("click", async () => {
      try {
        await ctx.api("POST", "payments/status", { isin: b.dataset.isin, pay_date: b.dataset.date, status: b.dataset.st });
        ctx.toast(b.dataset.st === "none" ? "Позначку знято" : "Статус збережено"); ctx.reload();
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
    ${sensitivityHTML(ctx)}
    ${incomeHTML(ctx)}
    <div class="chart-grid">
      ${income12mChartHTML(ctx)}
      ${capitalChartHTML(ctx)}
    </div>
    ${projectionHTML(ctx)}
    ${drawdownHTML(ctx)}`;
  await renderCalendar(ctx, main, { append: true });
}


// ---------- пасивний дохід ----------

// Пасивний дохід: скільки ПОРТФЕЛЬ приноситиме щомісяця. Саме дохідний
// потік — погашення це повернення власного тіла, а не дохід, і плутати
// їх означало б завищувати відповідь удвічі на коротких горизонтах.
//
// Інструменти всі три, і підпис мусить це казати. Доти він казав
// «папери», хоч у числі вже сиділи й відсотки вкладів, і оцінені
// дивіденди фондів: на живих даних із 32 виплат календаря 12 були
// фондові. Різниця не косметична — купон і відсоток вкладу це
// зобовʼязання за графіком, а дивіденд фонду ОЦІНКА, і читач має право
// знати, що частина числа саме така.
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
    <div class="muted" style="font-size:12px;margin-bottom:8px">скільки портфель приноситиме щомісяця, у сьогоднішніх гривнях</div>
    <div class="sub-xs" style="margin-bottom:8px">купони ОВДП і відсотки вкладів — за графіком; дивіденди фондів — оцінка</div>
    ${line("зараз", now)}
    <div style="border-top:1px solid var(--oi-border);padding-top:6px;margin-top:4px">${body}</div>
    ${independenceHTML(ctx)}
  </div>`;
}

// ---------- декумуляція: на скільки вистачить ----------
//
// Питання, зворотне до всієї решти вкладки: не «скільки накопичу», а «на
// скільки цього вистачить, якщо перестати вносити». Тому й стоїть
// останньою, після проєкцій.
//
// Головне тут — не саме число, а припущення за ним, і картка називає їх
// вголос. Найважливіше: замкнене (номінал ОВДП, тіло вкладів,
// сертифікати) достроково НЕ продається. Це найконсервативніша з розумних
// угод — продати ОВДП на вторинці можна, але за ціною, якої застосунок не
// знає, а вклад — із втратою відсотків.
export function drawdownHTML(ctx) {
  const d = (ctx.summary || {}).drawdown;
  if (!d || !d.withdraw_uah) return "";
  const inc = (v) => Math.round(v || 0).toLocaleString("uk-UA") + " ₴";
  const from = d.withdraw_from === "expenses"
    ? "стільки коштує місяць життя" : "задано в налаштуваннях";
  // Три різні відповіді, і жодну не можна показати замість іншої.
  // −1 — портфель живе з потоку; 0 неможливий (перший непокритий місяць
  // це вже 1); 1 — не вистачає навіть на місяць.
  let head, note;
  if (d.months === -1) {
    head = `<span class="ok-t">не вичерпується</span>`;
    note = "потоки покривають зняття — тіло лишається на місці";
  } else if (d.months <= 1) {
    head = `<span class="warn-t">не вистачить і на місяць</span>`;
    note = "ліквідної частини менше за одне зняття; замкнене не продається достроково";
  } else {
    head = `<b>${humanMonths(d.months)}</b>`;
    note = `до ${monthYear(d.until)}`;
  }
  return `<div class="card"><h2 class="h-row"><span>На скільки вистачить ${infoBtn("drawdown")}</span></h2>
    <div class="sub">Якщо перестати вносити й знімати ${inc(d.withdraw_uah)}/міс
      <span class="muted">· ${esc(from)}</span></div>
    <div style="display:flex;justify-content:space-between;align-items:baseline;gap:8px;margin-top:8px">
      <span class="muted" style="font-size:13px">вистачить</span>
      <span>${head} <span class="muted" style="font-size:12px">${esc(note)}</span></span>
    </div>
    <div class="progress" style="margin-top:8px"><span style="width:${
      Math.min(100, d.covered_pct || 0)}%;background:var(--oi-info)"></span></div>
    <div class="sub-xs" style="margin-top:4px">Сьогоднішній дохід покриває
      ${(d.covered_pct || 0).toFixed(0)}% зняття. Решту доводиться брати з тіла — і саме
      тому число таке.</div>
    <div class="sub-xs">Замкнене (номінал ОВДП, тіло вкладів, сертифікати) достроково
      не продається: воно лише віддає купони й погашення за графіком.</div>
  </div>`;
}

// ---------- точка незалежності ----------
//
// Висновок картки вище, а не окреме питання: та каже, скільки портфель
// приноситиме, ця — коли цього стане ДОСИТЬ. Тому живе всередині тієї
// самої картки, під горизонтами, а не окремим блоком.
//
// Дві дати навмисно. За планом — якщо вносити стільки, скільки виходить
// із цілі; за фактом — скільки виходить насправді. Одна без другої або
// лестить, або лякає, а різниця між ними це ціна дисципліни, не ринку.
function independenceHTML(ctx) {
  const ind = (ctx.summary || {}).independence;
  if (!ind || !ind.target_uah) return "";
  const inc = (v) => Math.round(v || 0).toLocaleString("uk-UA") + " ₴";
  // Нуль означає «не досягається за 60 років», −1 — «уже». Різниця між
  // ними протилежна за змістом, тож жодного спільного «немає даних».
  const when = (m, d) => m === -1 ? "вже покриває"
    : m > 0 ? `${monthYear(d)} · через ${humanMonths(m)}`
    : "не досягається за 60 років";
  const from = ind.target_from === "expenses"
    ? "стільки коштує місяць життя" : "задано в налаштуваннях";
  // Рядок «за фактом» лише тоді, коли темп відомий і відрізняється:
  // дві однакові дати поруч читаються як помилка, а не як збіг.
  const showActual = ind.actual_months !== undefined
    && ind.actual_months !== ind.plan_months;
  return `<div style="border-top:1px solid var(--oi-border);padding-top:10px;margin-top:10px">
    <div class="sub-xs" style="margin-bottom:4px">Коли дохід покриє ${inc(ind.target_uah)}/міс
      <span class="muted">· ${esc(from)}</span></div>
    <div style="display:flex;justify-content:space-between;align-items:baseline;gap:8px">
      <span class="muted" style="font-size:13px">за планом</span>
      <span><b>${esc(when(ind.plan_months, ind.plan_date))}</b></span>
    </div>
    ${showActual ? `<div style="display:flex;justify-content:space-between;align-items:baseline;gap:8px;margin-top:3px">
      <span class="muted" style="font-size:13px">за фактичним темпом</span>
      <span>${esc(when(ind.actual_months, ind.actual_date))}</span>
    </div>` : ""}
    ${ind.capital_uah > 0 ? `<div class="sub-xs" style="margin-top:4px">на той момент за цим стоятиме
      ${fmtUAH(ind.capital_uah)} капіталу</div>` : ""}
  </div>`;
}

