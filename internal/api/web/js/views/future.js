// Картки «куди це йде»: календар майбутніх виплат зі статусами, пасивний
// дохід, проєкції капіталу, крива й декумуляція. Спільне в них те, що
// жодне число тут ще не сталось.
//
// Окремої вкладки «Майбутнє» більше НЕМАЄ — розділ злився з «Планом»
// (див. пояснення нижче, при calendarPlaceholderHTML). Файл лишається
// бібліотекою карток; складає їх views/plan-view.js.

import {
  esc, today, dayShift, humanMonths, monthYear,
  uah0, uah2 as fmtUAH, money as fmtMoney, TODAY_FX,
} from "../format.js";
import { eq } from "../currency.js";
import { infoBtn } from "../info.js";
import { needsSetting, legend } from "../components.js";
import { routeFor } from "../routes.js";
import { svgBars, svgLine, svgBandLine, fluid } from "../charts.js";
import { PAY_TYPES, PAY_CLASS } from "../constants.js";
import { opsGrid } from "../grid.js";
import { pref, wirePrefs } from "../uistate.js";
import { openAllocate } from "./allocate.js";

// Дохід по місяцях: коли саме надійдуть купони й погашення на рік наперед.
export function income12mChartHTML(ctx) {
  const inc = (ctx.summary || {}).income_12m || [];
  if (!inc.some((m) => m.amount > 0)) return "";
  return `<div class="card"><h4>Дохід по місяцях ${infoBtn("income12m")}</h4>
    ${fluid((w, h) => svgBars(
      inc.map((m) => ({ label: m.month.slice(5), value: m.amount, color: "var(--oi-series-nominal)" })),
      { W: w, H: h }))}
    <div class="sub">Купони + погашення на рік наперед (${eq()}).</div></div>`;
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
        curve.goal_uah > 0 && { color: "var(--oi-muted)", label: "мета" },
      ])}
      <div class="sub-xs">Суми ${TODAY_FX}, до дедлайну мети. Крок ${curve.step_months} міс.</div></div>`;
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

// ---------- ПРОЄКЦІЇ ----------
//
// Таблиці «Проєкції капіталу» (1 / 3 / 5 / 10 років: внесено, за планом, за
// фактом, приріст) тут більше НЕМАЄ. Вона відповідала на чотири горизонти
// з нескінченних, а модель уміє будь-який місяць; тепер це картка
// «Прогноз капіталу» з повзунком (views/outlook.js, GET /api/outlook) —
// ті самі серії, та сама модель, і на роках старої таблиці ті самі числа
// (engine/outlook_test.go це тримає).
//
// Поле projection у документі стану лишилось: його читають картка доходу
// нижче й запасний вигляд «Кривої капіталу» вище.

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
//
// ОДИН ВИНЯТОК З НЕПЕРЕСІЧНОСТІ: минула виплата БЕЗ ВІДМІТКИ показується
// і в «Попереду», нагорі. Задача «N виплат без відмітки» веде саме сюди,
// а «Попереду» доти починалось сьогоднішнім днем — тобто ховало рівно ті
// рядки, по які людину привели, і щоб їх знайти, треба було знати, що
// вони в архіві. Вікно те саме, що в задачі (taskPastDays у
// engine/state_tasks.go, 90 днів): старша прогалина — не задача, а
// архів.
const CAL_KEY = "oddinvest.calRange";
const UNCONFIRMED_DAYS = 90;
const calRange = () => pref(CAL_KEY, ["ahead", "past"], "ahead");
const calQuery = (mode) => {
  const now = today();
  if (mode !== "past") return "from=" + dayShift(now, -UNCONFIRMED_DAYS);
  return "from=1970-01-01&to=" + dayShift(now, -1);
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
  // У «Попереду» з минулого лишаються тільки непідтверджені (довід — при
  // CAL_KEY); за датою вони й так стають першими.
  const rows = cal.filter((c) => mode === "past" || c.date >= now || !c.status)
    .sort((a, b) => (mode === "past"
      ? b.date.localeCompare(a.date) : a.date.localeCompare(b.date)));
  // aria-pressed, а не клас .quiet на НЕактивній: доти активний стан
  // читався із заперечення — і в розмітці, і читачем екрана, який про
  // нього не дізнавався взагалі.
  const btn = (v, t) => `<button data-pref="${CAL_KEY}" value="${v}" aria-pressed="${mode === v}">${t}</button>`;
  const html = `
    <div class="card">
      <h2 class="card-head">
        <span>Виплати</span>
        <span class="seg">${btn("ahead", "попереду")}${btn("past", "архів")}</span></h2>
      <div class="note">Виплату, датовану сьогодні чи наперед, можна
        позначити <b>отриманою</b> — тоді вона рахується, не чекаючи опівночі. Окремої позначки
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
        const m = typeof c.amount === "object" && c.amount
          ? c.amount : { amount: c.amount, currency: c.currency };
        const attrs = `data-isin="${esc(c.isin)}" data-date="${esc(c.date)}"
          data-amt="${esc(String(m.amount || ""))}" data-cur="${esc(m.currency || "UAH")}"
          data-type="${esc(c.type || "")}"`;
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
        <a class="lnk" href="${routeFor("plan/route/main")}">Маршрут грошей</a>.</div>
    </div>`;
  if (append) place(main, html);
  else main.innerHTML = html;
  wirePrefs(main, ctx, CAL_KEY);
  // «Отримано» — ОДНА поведінка всюди (ревізія 2026-10-03): позначка, а
  // за нею — пропозиція розкласти суму, так само як у маршруті. Доти
  // календар лише ставив позначку, маршрут ще й відкривав розкладку, а
  // чеклист плану мав свою кнопку поруч — три різні наслідки одного слова.
  main.querySelectorAll("[data-st]").forEach((b) =>
    b.addEventListener("click", async () => {
      const d = b.dataset;
      try {
        await ctx.api("POST", "payments/status", { isin: d.isin, pay_date: d.date, status: d.st });
      } catch (err) { ctx.toast(String(err.message || err), false); return; }
      if (d.st === "none" || !d.amt) {
        ctx.toast(d.st === "none" ? "Позначку знято" : "Позначено отриманим");
        ctx.reload();
        return;
      }
      // Тіло погашення — окремо: розкладка не віддає подушці курс і знає,
      // що це повернення власних грошей, а не дохід.
      await openAllocate(ctx, {
        amount: d.amt, currency: d.cur, title: d.isin, source: "portfolio",
        principal: d.type === "redemption" ? d.amt : 0,
      });
      ctx.reload();
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
//
// chart — крива доходу на кожен місяць із повзунком (views/outlook.js).
// Коли вона є, рядки «через 1 / 3 / 5 / 10 років» не малюються: це ті самі
// числа на чотирьох точках тієї самої кривої (engine/outlook_test.go тримає
// збіг). Рядки лишаються запасним виглядом — прогноз доїжджає окремим
// запитом, і без нього картка не мусить порожніти.
export function incomeHTML(ctx, chart = "") {
  const s = ctx.summary || {};
  const rows = (s.projection || []).filter((r) => r.income_monthly > 0);
  const now = Number(s.income_monthly_now || 0);
  if (!rows.length && now <= 0 && !chart) return "";
  // Без копійок, як і в решті планових чисел: дробова частина місячного
  // доходу на горизонті в роки — це точність, якої в оцінці немає.
  const inc = (v) => uah0(v);
  const line = (label, v, extra = "") => `<div class="kv mb-sm">
    <span class="muted fine">${label}</span>
    <span><b>${inc(v)}</b><span class="muted fine">/міс</span>${extra}</span></div>`;
  const body = rows.map((r) => line(`через ${humanMonths(r.years * 12)}`, r.income_monthly,
    r.income_monthly_actual > 0 && Math.abs(r.income_monthly_actual - r.income_monthly) > 1
      ? ` <span class="muted fine-xs">· за фактом ${inc(r.income_monthly_actual)}</span>` : "")).join("");
  return `<div class="card"><h2 class="card-head">
    <span>Пасивний дохід ${infoBtn("income")}</span></h2>
    <div class="muted fine mb-sm">скільки портфель приноситиме щомісяця, ${TODAY_FX}</div>
    <div class="sub-xs mb-sm">купони ОВДП і відсотки вкладів — за графіком; дивіденди фондів — оцінка.
      «Зараз» — усе, що прийде за графіком; далі — реальний дохід: гривнева частина за
      вирахуванням знецінення, тобто те, що можна забирати, не проїдаючи капітал</div>
    ${line("зараз", now)}
    <div class="rule-top tight">${chart || body}</div>
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
      "Скільки знімати — застосунок сам не виводить: зняття грошей — це і покупка "
      + "холодильника, і переказ у резерв, тож міряти від них «місяць життя» означало б "
      + "рахувати від випадкового числа. Задай «місячні витрати» або «скільки знімати» "
      + "в «Політиці → Резерв».",
    routeFor("policy/money/main"));
  }
  const inc = (v) => uah0(v);
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
      <div class="sub-xs">Щоб побачити, коли дохід покриє життя, задай
        <a class="lnk" href="${routeFor("policy/assumptions/main")}">цільовий дохід</a> або
        <a class="lnk" href="${routeFor("policy/money/main")}">місячні витрати</a>
        у «Політиці».</div></div>`;
  }
  const inc = (v) => uah0(v);
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
    <div class="sub-xs mb-xs">Коли реальний дохід покриє ${inc(ind.target_uah)}/міс
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

