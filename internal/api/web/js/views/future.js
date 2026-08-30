// Картки «куди це йде»: календар майбутніх виплат зі статусами, пасивний
// дохід, проєкції капіталу, крива й декумуляція. Спільне в них те, що
// жодне число тут ще не сталось.
//
// Окремої вкладки «Майбутнє» більше НЕМАЄ — розділ злився з «Планом»
// (див. пояснення нижче, при calendarPlaceholderHTML). Файл лишається
// бібліотекою карток; складає їх views/plan-view.js.

import {
  esc, today, humanMonths, monthYear, pct, capitalUAH, outsideUAH,
  uah2 as fmtUAH, money as fmtMoney,
} from "../format.js";
import { infoBtn } from "../info.js";
import { needsSetting, empty, legend } from "../components.js";
import { routeFor } from "../routes.js";
import { svgBars, svgLine, svgBandLine, fluid } from "../charts.js";
import { PAY_TYPES, PAY_CLASS } from "../constants.js";
import { CONTRIB, contribTriad } from "../contrib.js";
import { opsGrid } from "../grid.js";

// Дохід по місяцях: коли саме надійдуть купони й погашення на рік наперед.
export function income12mChartHTML(ctx) {
  const inc = (ctx.summary || {}).income_12m || [];
  if (!inc.some((m) => m.amount > 0)) return "";
  return `<div class="card"><h4>Дохід по місяцях ${infoBtn("income12m")}</h4>
    ${fluid((w, h) => svgBars(
      inc.map((m) => ({ label: m.month.slice(5), value: m.amount, color: "var(--oi-series-nominal)" })),
      { W: w, H: h }))}
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
// картка не мусить показувати порожньо.
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
      ${fluid((w, h) => svgBandLine(labels, bands, lines, curve.goal_uah || 0, { W: w, H: h }))}
      ${legend([
        { color: "var(--oi-series-invested)", label: "за планом" },
        has("actual") && { color: "var(--oi-series-neutral)", label: "за фактом" },
        bands.lo && { color: "var(--oi-series-invested)", label: "коридор ринку", faint: true },
        curve.goal_uah > 0 && { color: "var(--oi-muted)", label: "ціль" },
      ])}
      <div class="sub-xs">У сьогоднішніх гривнях, до дедлайну цілі. Крок ${curve.step_months} міс.</div></div>`;
  }
  // Запасний вигляд для старого бекенда: ті самі чотири точки.
  const proj = s.projection || [];
  if (!proj.length) return "";
  return `<div class="card"><h4>Крива капіталу ${infoBtn("capitalCurve")}</h4>
    ${fluid((w, h) => svgLine(proj.map((p) => p.years + "р"), [
      { color: "var(--oi-series-neutral)", values: proj.map((p) => p.contributed) },
      { color: "var(--oi-series-invested)", values: proj.map((p) => p.with_reinvest) },
    ], { W: w, H: h, zero: true, label: "Крива капіталу" }))}
    ${legend([
      { color: "var(--oi-series-neutral)", label: "внесено" },
      { color: "var(--oi-series-invested)", label: "з реінвестом" },
    ])}</div>`;
}

// ---------- ПРОЄКЦІЇ (блок вкладки «Майбутнє») ----------
export function projectionHTML(ctx) {
  const s = ctx.summary || {};
  // Старт моделі — увесь капітал МІНУС резерв І МІНУС цілі накопичення. Не
  // «номінал + рахунок», як тут стояло: це була четверта, власна відповідь
  // на питання «що таке капітал», і вона не збігалась ні з плиткою
  // «Капітал», ні з тим, від чого модель насправді відштовхується.
  //
  // Обидва віднімаються явно й з одного доводу: вони в капіталі є, але не
  // інвестуються й не компаундяться. Довід дослівно той самий, з якого їх
  // віднімає й сам бекенд (p0 у state_projection.go) — без цілей клієнт
  // малював би старт вище за той, з якого модель насправді рахує.
  const outside = outsideUAH(s);
  const P0 = capitalUAH(s) - outside;
  // Скільки з ЦЬОГО місяця піде повз портфель. Стелі, а не потреби, і те
  // саме число, що віднімає модель (spendOutside у state_projection.go) на
  // першому місяці. Далі воно спадає разом із розривами — тому в підписі
  // сказано «цього місяця», а не «щомісяця».
  const outsideMonth = ((s.reserve && s.reserve.fill_month_uah) || 0)
    + (s.goals || []).filter((g) => !g.done_date)
      .reduce((a, g) => a + (g.fill_month_uah || 0), 0);
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
  // Колонка «За фактом» з'являється лише тоді, коли факт є: порожня
  // колонка з прочерками читалась би як «факт нульовий».
  const projTable = opsGrid({
    cols: [
      { key: "years", label: "Горизонт", cell: (r) => `${r.years} р.` },
      { key: "contributed", label: "Внесено (без %)", num: true,
        cell: (r) => fmtUAH(r.contributed) },
      { key: "plan", label: "За планом", num: true, cell: (r) => fmtUAH(r.with_reinvest) },
      hasActual ? { key: "actual", label: "За фактом", num: true,
        cell: (r) => fmtUAH(r.with_reinvest_actual || 0) } : null,
      { key: "gain", label: "Приріст", num: true,
        cell: (r) => fmtUAH(r.with_reinvest - r.contributed) },
    ].filter(Boolean),
    rows: rowsData,
    caption: "Проєкції капіталу: горизонт, внесено, за планом, приріст",
  }) || empty("",
    "Проєкція будується з капіталу й місячної цілі — щойно буде і те, і те, таблиця заповниться сама.",
    { href: "#/settings", label: "Задати ціль" });
  // Усі три показники названо своїми іменами й в одному рядку. Доти тут
  // стояло «(план — C/міс)», де C — це month_target_uah, тобто НЕСТАЧА
  // понад план: слово «план» позначало число, яке планом не є.
  const t = contribTriad(ctx);
  const paceBits = [
    t.hasActual ? `${CONTRIB.actual.label} <b>${fmtUAH(t.actual)}/міс</b>${
      t.actualMonths ? ` за ${t.actualMonths} міс історії` : ""}` : "",
    t.hasPlan ? `${CONTRIB.plan.label} ${fmtUAH(t.plan)}/міс` : "",
    t.hasGoal ? `${CONTRIB.need.label} ${fmtUAH(t.need)}/міс` : "",
  ].filter(Boolean);
  const paceNote = hasActual
    ? `<div class="muted mb fine">${paceBits.join(" · ")}.</div>`
    : `<div class="muted mb fine">Прогноз за фактичним темпом зʼявиться після першого поповнення.${
      paceBits.length ? " " + paceBits.join(" · ") + "." : ""}</div>`;

  return `
    <div class="card">
      <h2>Проєкції капіталу</h2>
      <div class="note">Старт = капітал ${fmtUAH(P0)}${
        outside > 0 ? ` <b>без резерву й цілей накопичення</b> (${fmtUAH(outside)} не інвестуються, тож і не ростуть — крива стартує нижче за плитку «Капітал» рівно на цю суму)` : ""
      }, внесок = ${t.hasPlan
    ? `${fmtUAH(t.plan)}/міс з плану + ${fmtUAH(C)}/міс від тебе`
    : `${fmtUAH(C)}/міс`}, ставка = ${rateSrc}.${outsideMonth > 0
    ? ` <b>Але в папери доходить не весь внесок</b>: подушка й цілі беруть своє раніше — цього місяця ${fmtUAH(outsideMonth)}, і в кривій ці гроші не ростуть. Модель віднімає їх щомісяця й перестає, щойно розрив закривається: зібрана подушка й закрита ціль стелі більше не мають, і внесок повертається повним сам. Число «${fmtUAH(t.plan)}/міс з плану» лишається БРУТТО навмисно — це те, що план дає, а не те, що доходить до паперів, і зводити два питання до одного числа означало б утратити обидва.` : ""
  } Модель: справжні купони й погашення наявних паперів + внески, реінвест під ставку; готівка не працює до реінвесту. Тіло вкладів і сертифікати фондів входять у старт нарівні з номіналом ОВДП: вклад повертається за графіком, сертифікат лежить безстроково й платить дивідендами. Подорожчання сертифіката модель не малює — його ніхто не обіцяв. <b>Ставка номінальна, суми реальні</b>: знецінення застосовується всередині моделі, окремо до кожного валютного рукава, тож усі колонки — у гривні сьогоднішньої купівельної спроможності. «Внесено» через це теж знецінюється, і приріст показує, наскільки вкладати вигідніше, ніж просто відкладати. Це припущення, не гарантія.</div>
      ${paceNote}
      ${projTable}
    </div>`;
  // Картка цілей сюди НЕ входить: розділ малює її сам, першим блоком.
  // Доки вона висіла хвостом проєкцій, «Майбутнє» показувало її двічі —
  // саме той дубль, заради якого й затівалось перегрупування.
}

// ---------- КАЛЕНДАР ----------

// Два НЕПЕРЕСІЧНІ види замість одного столу на всю історію.
//
// Доти картка завжди просила `from=1970-01-01` і малювала весь архів
// разом із майбутнім — сотні рядків без фільтра, сортування й пагінації,
// хоч вкладка називається «Майбутнє». Помічник реінвесту відмовляється
// обмежувати свій перелік саме тому, що в його таблиці все це є; тут
// нічого з того не було, тож правильна відповідь — не курсор в API, а
// поділ на два питання.
//
// Межі підібрані так, щоб види не накладались: «Попереду» починається
// сьогодні, «Архів» закінчується вчора. Кожен бере рівно ту межу, якої
// потребує, — саме заради архіву в /api/calendar і зʼявився `to`.
const CAL_KEY = "oddinvest.calRange";
const calRange = () => {
  try { return localStorage.getItem(CAL_KEY) === "past" ? "past" : "ahead"; }
  catch (_) { return "ahead"; }
};
const calQuery = (mode) => {
  const now = today();
  if (mode !== "past") return "from=" + now;
  const d = new Date(now);
  d.setDate(d.getDate() - 1);
  return "from=1970-01-01&to=" + d.toISOString().slice(0, 10);
};


// append=true дописує календар до вже намальованого розділу, а не
// затирає його: у «Майбутньому» він стоїть останнім блоком, після
// прогнозів.
// Куди лягає готовий календар: на місце заглушки, якщо вона є, інакше
// просто в кінець. Заглушка тримає висоту, поки триває запит, — інакше
// сторінка підстрибує на цілу картку рівно тоді, коли її вже читають.
function place(main, html) {
  const hold = main.querySelector("#calHold");
  if (hold) hold.outerHTML = html;
  else main.insertAdjacentHTML("beforeend", html);
}

export async function renderCalendar(ctx, main, { append = false } = {}) {
  const mode = calRange();
  let cal;
  try {
    cal = await ctx.api("GET", "calendar?" + calQuery(mode));
  } catch (err) {
    // append означає, що вище вже намальовані прогнози. Кинути звідси
    // помилку — стерти їх усі заради однієї таблиці, якої не вистачило:
    // розділ ловить виняток і замінює main карткою з текстом помилки.
    // Тож своя помилка лишається своєю.
    const card = `<div class="card"><h2>Виплати</h2>
      <div class="muted">Календар не завантажився: ${esc(err.message || err)}</div></div>`;
    if (append) place(main, card);
    else main.innerHTML = card;
    return;
  }
  const now = today();
  // Архів читається від найновішого: у минулому цікавить те, що щойно
  // сталось, а не перша виплата за всю історію.
  const rows = cal.slice().sort((a, b) => (mode === "past"
    ? b.date.localeCompare(a.date) : a.date.localeCompare(b.date)));
  // aria-pressed, а не клас .quiet на НЕактивній: доти активний стан
  // читався із заперечення — і в розмітці, і читачем екрана, який про
  // нього не дізнавався взагалі.
  const btn = (v, t) => `<button data-cal="${v}" aria-pressed="${mode === v}">${t}</button>`;
  const html = `
    <div class="card">
      <h2 class="card-head">
        <span>Виплати</span>
        <span class="seg">${btn("ahead", "попереду")}${btn("past", "архів")}</span></h2>
      <div class="note">Виплату, датовану сьогодні чи наперед, можна
        позначити <b>отриманою</b> — тоді вона ляже на рахунок, не чекаючи опівночі. Окремої позначки
        «перевкладено» більше немає: чи пішли гроші в діло, застосунок бачить сам із твоїх покупок.</div>
      ${opsGrid({
    cols: [
      { key: "date", label: "Дата", cell: (c) => esc(c.date) },
      // Вклади мають синтетичний ISIN "deposit:<id>" — показуємо «вклад»,
      // а не внутрішній ключ.
      { key: "isin", label: "ISIN",
        cell: (c) => esc(String(c.isin).startsWith("deposit:") ? "вклад" : c.isin) },
      { key: "type", label: "Тип", cell: (c) => `<span class="pill ${
        PAY_CLASS[c.type] || ""}">${PAY_TYPES[c.type] || c.type}</span>` },
      { key: "amount", label: "Сума", num: true, cell: (c) => fmtMoney(c.amount) },
      // Старі бази можуть іще нести «reinvested» (міграція 0017 зводить
      // його до «received», але дамп, зроблений до неї, — ні), тож читаємо
      // обидва як одне: гроші надійшли.
      { key: "status", label: "Статус", cell: (c) => (c.status
        ? `<span class="pill recv">отримано</span>` : `<span class="muted">—</span>`) },
      { key: "acts", label: "", cls: "row-actions nowrap", cell: (c) => {
        if (c.date > now) return "";
        // Уже позначено — лишається одна дія: зняти позначку. Раз вона
        // рухає гроші, помилковий клік має бути оборотним.
        const attrs = `data-isin="${esc(c.isin)}" data-date="${esc(c.date)}"`;
        return c.status
          ? `<button class="sm quiet" ${attrs} data-st="none">Скасувати</button>`
          : `<button class="sm" ${attrs} data-st="received">Отримано</button>`;
      } },
    ],
    rows,
    caption: "Виплати: дата, папір, тип, сума, статус",
    empty: mode === "past" ? "Виплат у минулому ще не було." : "Попереду виплат немає.",
  })}
      <div class="sub-xs">Куди піде кожне надходження —
        <a class="lnk" href="${routeFor("plan/route")}">Маршрут грошей</a>.</div>
    </div>`;
  if (append) place(main, html);
  else main.innerHTML = html;
  main.querySelectorAll("[data-cal]").forEach((b) =>
    b.addEventListener("click", () => {
      try { localStorage.setItem(CAL_KEY, b.dataset.cal); } catch (_) { /* приватний режим */ }
      ctx.reload();
    }));
  main.querySelectorAll("[data-st]").forEach((b) =>
    b.addEventListener("click", async () => {
      try {
        await ctx.api("POST", "payments/status", { isin: b.dataset.isin, pay_date: b.dataset.date, status: b.dataset.st });
        ctx.toast(b.dataset.st === "none" ? "Позначку знято" : "Статус збережено"); ctx.reload();
      } catch (err) { ctx.toast(String(err.message || err), false); }
    }));
}

// ---------- блоки «куди це йде» ----------
//
// Власного renderFuture тут більше НЕМАЄ, як і вкладки «Майбутнє»: розділ
// злився з «Планом». Причина не в зручності, а в тому, що обидва малювали
// ті самі числа. `summary.month_target_uah` (тутешнє «внесок = X/міс») і
// `forecast.contrib_plan` («До цілі бракує ще» в «Плані») — це одне
// out.ContribM зі state_projection.go, тобто одна відповідь у двох
// вкладках; а крива капіталу взагалі малювалась двічі — карткою тут і
// нижньою панеллю стрічки там.
//
// Файл лишається бібліотекою карток: складає їх тепер views/plan-view.js.
// Вливати шістсот рядків у нього не стали — від переїзду тексту в чужий
// файл нічого не покращується, а розділ, який і так зібрався з чотирьох
// джерел, став би нечитним.
//
// Порядок, у якому вони йдуть, задає plan-view.js; той, що був тут, зберігся:
// спершу «скільки треба вносити», потім потік із наявних паперів, далі
// модель на роки й аж наприкінці календар по датах.

/** Заглушка висоти під календар: він доїжджає окремим запитом, помітно
 *  пізніше за решту, і без неї сторінка підстрибувала рівно тоді, коли на
 *  ній уже почали читати. */
export function calendarPlaceholderHTML() {
  return `<div class="card" id="calHold"><h2>Виплати</h2>
    <div class="skel" aria-hidden="true"><div class="skel-rows">${
  Array.from({ length: 5 }, () => `<div class="skel-row"></div>`).join("")}</div></div></div>`;
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
  const line = (label, v, extra = "") => `<div class="kv mb-sm">
    <span class="muted fine">${label}</span>
    <span><b>${inc(v)}</b><span class="muted fine">/міс</span>${extra}</span></div>`;
  const body = rows.map((r) => line(`через ${humanMonths(r.years * 12)}`, r.income_monthly,
    r.income_monthly_actual > 0 && Math.abs(r.income_monthly_actual - r.income_monthly) > 1
      ? ` <span class="muted fine-xs">· за фактом ${inc(r.income_monthly_actual)}</span>` : "")).join("");
  return `<div class="card"><h2 class="card-head">
    <span>Пасивний дохід ${infoBtn("income")}</span></h2>
    <div class="muted fine mb-sm">скільки портфель приноситиме щомісяця, у сьогоднішніх гривнях</div>
    <div class="sub-xs mb-sm">купони ОВДП і відсотки вкладів — за графіком; дивіденди фондів — оцінка</div>
    ${line("зараз", now)}
    <div class="rule-top tight">${body}</div>
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
  if (!d || !d.withdraw_uah) {
    return needsSetting(`На скільки вистачить ${infoBtn("drawdown")}`,
      "Скільки знімати — застосунок сам не виводить: зняття з рахунку це і покупка "
      + "холодильника, і переказ у резерв, тож міряти від них «місяць життя» означало б "
      + "рахувати від випадкового числа. Задай «місячні витрати» або «скільки знімати» "
      + "в «Політиці → Резерв».",
    routeFor("policy/reserve"));
  }
  const inc = (v) => Math.round(v || 0).toLocaleString("uk-UA") + " ₴";
  const from = d.withdraw_from === "expenses"
    ? "стільки коштує місяць життя" : "задано в налаштуваннях";
  // Три різні відповіді, і жодну не можна показати замість іншої.
  // −1 — портфель живе з потоку; 0 неможливий (перший непокритий місяць
  // це вже 1); 1 — не вистачає навіть на місяць.
  let head, note;
  if (d.months === -1) {
    head = `<span class="t-ok">не вичерпується</span>`;
    note = "потоки покривають зняття — тіло лишається на місці";
  } else if (d.months <= 1) {
    head = `<span class="t-warn">не вистачить і на місяць</span>`;
    note = "ліквідної частини менше за одне зняття; замкнене не продається достроково";
  } else {
    head = `<b>${humanMonths(d.months)}</b>`;
    note = `до ${monthYear(d.until)}`;
  }
  return `<div class="card"><h2 class="h-row"><span>На скільки вистачить ${infoBtn("drawdown")}</span></h2>
    <div class="sub">Якщо перестати вносити й знімати ${inc(d.withdraw_uah)}/міс
      <span class="muted">· ${esc(from)}</span></div>
    <div class="kv mt-sm">
      <span class="muted fine">вистачить</span>
      <span>${head} <span class="muted fine">${esc(note)}</span></span>
    </div>
    <div class="progress mt-sm"><span style="--oi-fill:${
      Math.min(100, d.covered_pct || 0)}%;--oi-c:var(--oi-info)"></span></div>
    <div class="sub-xs mt-xs">Сьогоднішній дохід покриває
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
  // Тут не needsSetting: блок живе ВСЕРЕДИНІ картки «Пасивний дохід», і
  // картка в картці виглядала б поломкою. Той самий зміст, та сама
  // адреса налаштування — тільки рядком, а не окремою плиткою.
  if (!ind || !ind.target_uah) {
    return `<div class="rule-top">
      <div class="sub-xs">Щоб побачити, коли дохід покриє життя, задай «цільовий дохід»
        або «місячні витрати» в «Налаштуваннях».</div></div>`;
  }
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
  return `<div class="rule-top">
    <div class="sub-xs mb-xs">Коли дохід покриє ${inc(ind.target_uah)}/міс
      <span class="muted">· ${esc(from)}</span></div>
    <div class="kv">
      <span class="muted fine">за планом</span>
      <span><b>${esc(when(ind.plan_months, ind.plan_date))}</b></span>
    </div>
    ${showActual ? `<div class="kv mt-xs">
      <span class="muted fine">за фактичним темпом</span>
      <span>${esc(when(ind.actual_months, ind.actual_date))}</span>
    </div>` : ""}
    ${ind.capital_uah > 0 ? `<div class="sub-xs mt-xs">на той момент за цим стоятиме
      ${fmtUAH(ind.capital_uah)} капіталу</div>` : ""}
  </div>`;
}

