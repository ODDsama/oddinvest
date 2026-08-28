// «Огляд» — головне одним екраном. Знак ODD у шапці веде сюди.
//
// Єдина вкладка без майстер-списку: у неї немає сутності, яку можна
// вибрати, — вона про портфель ЦІЛКОМ.
//
// ПОРЯДОК БЛОКІВ ВЕДЕ ПИТАННЯМ, А НЕ РОЗМІРОМ ЧИСЕЛ. Спершу скільки в
// мене, далі що потребує рішення, і аж тоді контекст: прогрес, місяць,
// що заходить, де розриви. Черга стоїть ВИЩЕ за плитки місяця навмисно:
// сторінка зветься «Огляд», але заходять на неї з питанням «що робити».
//
// ЖОДНОГО ВЛАСНОГО ЧИСЛА. Кожен блок або бере готове зі зведення, або
// кличе ту саму функцію, що малює це число на своїй вкладці: плитка
// місяця — monthTile з «Роботи», валютні розриви — rebalanceCard із
// «Портфеля». Друга копія будь-якого з них розійшлася б із першою тихо.

import { esc, uah0, pct, capitalUAH, uah2 as fmtUAH, curSym } from "../format.js";
import { tile, empty, progressBar } from "../components.js";
import { routeFor } from "../routes.js";
import { tasksOf, tasksHTML } from "./tasks.js";
import { monthTile } from "./now-view.js";
import { rebalanceCard } from "./risk.js";

/** Головне число й три поруч.
 *
 *  «За 30 днів» у цьому ряду НЕМАЄ, хоч макет його й показує. Такого
 *  числа в застосунку не існує: щоденний знімок тримає капітал, але
 *  різниці за вікно не рахує ніхто, а порахувати її тут означало б
 *  завести в браузері арифметику над історією — саме те, проти чого
 *  стоїть увесь цей файл. Коли зведення почне віддавати цю дельту, вона
 *  стане четвертим числом ряду й нічого більше не зачепить. */
function heroHTML(ctx) {
  const s = ctx.summary || {};
  const cap = capitalUAH(s);
  const usd = (s.rates || {}).USD || 0;
  const xirr = (s.xirr || {}).UAH;
  const sub = [
    usd > 0 ? `≈ ${(cap / usd).toLocaleString("uk", { maximumFractionDigits: 0 })} $` : "",
    s.reserve_uah > 0 ? `з них ${uah0(s.reserve_uah)} у резерві, який не працює` : "",
  ].filter(Boolean).map((t) => `<div class="sub">${esc(t)}</div>`).join("");

  return `<div class="tiles flush">
    ${tile("Капітал", fmtUAH(cap), sub, { hero: true })}
    ${tile("Реальна дохідність",
    s.blended_yield_real_pct ? pct(s.blended_yield_real_pct) : "—",
    s.blended_yield_real_pct
      ? `<div class="sub-xs">після податку й знецінення</div>` : "")}
    ${tile("XIRR", xirr ? pct(xirr) : "—",
    xirr ? `<div class="sub-xs">з урахуванням дат внесків</div>`
      : `<div class="sub-xs">гроші ще замолоді, щоб міряти</div>`)}
    ${tile("Вільні гроші", uah0(s.account_uah || 0),
    s.reinvest_min_uah > 0
      ? `<div class="sub-xs">поріг покупки ${uah0(s.reinvest_min_uah)}</div>` : "")}
  </div>`;
}

/** Черга рішень, розрізана на «зараз» і «скоро».
 *
 *  Два блоки, а не один список із підзаголовками: питання в них різні —
 *  перше «що зробити сьогодні», друге «про що пам'ятати». Розрізає їх
 *  сам бекенд полем sev, тож жодного власного правила тут немає. */
function queuesHTML(ctx) {
  const all = tasksOf(ctx);
  const now = all.filter((t) => t.sev === "now");
  const soon = all.filter((t) => t.sev === "soon");
  const block = (title, list, none) => `<div class="card"><h2>${esc(title)}</h2>${
    list.length ? tasksHTML(ctx, list) : `<div class="sub">${esc(none)}</div>`}</div>`;
  return block("Потребує рішення зараз", now, "Зараз нічого не чекає рішення.")
    + block("Скоро · 30 днів", soon, "У найближчі тридцять днів строків немає.");
}

// ---------------------------------------------------------------------
// Прогрес
// ---------------------------------------------------------------------

/** Блок прогресу.
 *
 *  МЕЖА, ЯКА РОБИТЬ ЦЕ ПРИЙНЯТНИМ: кожне число тут виміряне, жодне не
 *  нараховане. Прибери гру — числа лишаться ті самі, просто читатимуться
 *  гірше. Повний довід і те, чого тут навмисно немає (очок за
 *  активність, докору за розрив серії, порівняння з чужими портфелями),
 *  записано в internal/api/state_progress.go: рахує все бекенд, і
 *  фронтенд не знає навіть, як складається рівень.
 *
 *  Порожній p означає, що маршрут не відповів. Блок тоді не малюється
 *  зовсім: половина прогресу гірша за його відсутність. */
function progressHTML(p) {
  if (!p || !p.milestones || !p.milestones.length) return "";
  const earned = p.milestones.filter((m) => m.earned);
  // Банер бере ОСТАННЮ ДАТОВАНУ віху. Недатовані в нього не потрапляють
  // ніколи, і це названо в state_progress.go при milestone.EarnedOn: у
  // частини віх дати немає ніде, і вигадувати її заради святкування —
  // рівно те, чого цей блок не робить.
  // Дата в банері не повторюється: вона вже всередині note («куплено
  // 2026-01-15»), і другий раз поруч читався б як дві різні дати.
  const dated = earned.filter((m) => m.earned_on)
    .sort((a, b) => (a.earned_on < b.earned_on ? 1 : -1));
  const last = dated[0];

  return `<div class="card">
    <h2 class="card-head"><span>Прогрес</span>
      <span class="sub-xs">${p.level} із ${p.level_of}</span></h2>
    ${last ? `<div class="banner ok"><div class="b-ic" aria-hidden="true">✓</div>
      <div class="b-tx"><div class="b-t">${esc(last.title)}</div>
      <div class="b-s">${esc(last.note)}</div></div></div>` : ""}
    <div class="lvl-row">
      <div class="lvl">
        <div class="lvl-n">${p.level}</div>
        <div class="lvl-l">віх зібрано з ${p.level_of}</div>
      </div>
      <div class="tracks">${tracksHTML(p)}</div>
    </div>
    ${badgesHTML(p.milestones)}
    ${matrixHTML(p.collection)}
  </div>`;
}

/** Три доріжки. Кожна показує СВОЄ джерело, і в кожної є стан «міряти
 *  нічим» — окремий від нуля. */
function tracksHTML(p) {
  const d = p.discipline || {};
  const st = p.streak || {};
  const c = p.collection || {};
  const track = (name, value, note, fill, color) => `<div class="track">
    <div class="track-h"><span class="track-n">${esc(name)}</span>
      <span class="track-v">${esc(value)}</span></div>
    ${fill === null ? "" : progressBar(fill, { color })}
    <div class="sub-xs">${esc(note)}</div>
  </div>`;

  // Дисципліна: доки журнал закороткий, відсотка немає взагалі. Один
  // вдалий вибір з одного — це 100%, і показувати таке означає обіцяти
  // точність, якої немає; поріг той самий, що й у самому журналі.
  const disc = d.enough && d.total > 0
    ? track("Дисципліна", `${d.top_row} з ${d.total}`,
      "покупок узято з верхнього рядка помічника",
      d.top_row / d.total * 100, "var(--oi-ok)")
    : track("Дисципліна", "—",
      "журнал рішень ще закороткий, щоб із нього щось читати", null);

  // Постійність. Розрив показаний фактом рівно один раз і не оцінений
  // нічим — довід у state_progress.go.
  const streakNote = [
    st.best ? `найдовша — ${st.best}` : "",
    st.broken_on ? `серія почалась заново з ${st.broken_on}` : "",
    st.unknown_before || !st.months_measured
      ? "раніше судити нічим — знімків за ті місяці немає" : "",
  ].filter(Boolean).join(" · ");
  const cons = st.months_measured
    ? track("Постійність", `${st.months} ${plural(st.months)}`,
      streakNote || "місяців поспіль за планом", null)
    : track("Постійність", "—", streakNote, null);

  const coll = c.of
    ? track("Колекція", `${c.filled} / ${c.of}`,
      "клітинок поля «роки погашень × валюти»",
      c.filled / c.of * 100, "var(--oi-kind-fund)")
    : track("Колекція", "—", "драбини погашень ще немає", null);

  return disc + cons + coll;
}

// Місяць українською. Своя, бо format.js:plural просить три форми, а тут
// потрібна рівно одна пара слів і жодного числа перед ними.
function plural(n) {
  const t = Math.abs(n) % 100;
  if (t >= 11 && t <= 14) return "місяців";
  const o = t % 10;
  if (o === 1) return "місяць";
  if (o >= 2 && o <= 4) return "місяці";
  return "місяців";
}

/** Сітка віх: зібрані й замкнені разом.
 *
 *  Замкнені показані НЕ як докір, а як карта: у кожної стоїть її власне
 *  число («3,0 з 5,0 місяців витрат»), тобто видно не «ти не зміг», а
 *  «стільки лишилось і чим це міряється». */
function badgesHTML(list) {
  return `<h3 class="mt-lg">Віхи</h3>
    <div class="badges">${list.map((m) => `
      <div class="badge${m.earned ? " on" : ""}">
        <span class="badge-i" aria-hidden="true">${m.earned ? "✓" : "○"}</span>
        <span class="badge-t">
          <span class="badge-n">${esc(m.title)}</span>
          <span class="badge-s">${esc(m.note)}</span>
        </span>
        ${!m.earned && m.progress_pct >= 0
    ? `<span class="badge-p">${m.progress_pct}%</span>` : ""}
      </div>`).join("")}</div>`;
}

/** Поле колекції: роки погашень × валюти.
 *
 *  Підпис під полем — з бекенда, а не звідси: він пояснює, чому
 *  заповнене поле НЕ є ціллю саме по собі, і це та сама проза, яку мусив
 *  би побачити другий споживач, якби він з'явився. */
function matrixHTML(c) {
  if (!c || !c.rows || !c.rows.length) return "";
  return `<h3 class="mt-lg">Поле колекції</h3>
    <div class="matrix" role="presentation">
      <div class="mx-row mx-head"><span></span>${c.currencies
    .map((x) => `<span class="mx-c">${esc(x)}</span>`).join("")}</div>
      ${c.rows.map((r) => `<div class="mx-row">
        <span class="mx-y">${r.year}</span>
        ${r.cells.map((on, i) => `<span class="mx-cell${on ? " on" : ""}"
          title="${r.year} · ${esc(c.currencies[i])}"></span>`).join("")}
      </div>`).join("")}
    </div>
    <div class="sub">${esc(c.note)}</div>`;
}

// ---------------------------------------------------------------------

/** Про що застосунок мовчить навмисно.
 *
 *  БЛОК ОБОВ'ЯЗКОВИЙ, і це не скромність. Без нього порожні місця на
 *  екрані читаються як недоробка: людина шукає ринкову ціну паперу,
 *  не знаходить і вирішує, що застосунок її загубив. Названа
 *  відсутність — теж інформація, і вона утримує від того, щоб дописати
 *  правдоподібне число туди, де його не може бути. */
function silenceHTML() {
  const items = [
    ["Ринкової ціни ОВДП", "Продати на вторинному ринку можна, але ціни застосунок "
      + "не моделює: джерела котирувань немає в жодного зі ста вісімдесяти семи "
      + "паперів, і «позначити ціну» означало б для них не переписати відоме "
      + "число, а вигадати його."],
    ["Наступного аукціону", "Дата й обсяг розміщення відомі Мінфіну, а не нам. "
      + "Що ринок платив за строк РАНІШЕ — видно в «Портфель → Порівняння»."],
    ["Вердикту за курсом", "Чи час купувати долар, застосунок не каже. Він "
      + "показує, де сьогоднішній курс стоїть серед історії, і лишає висновок "
      + "тобі."],
    ["Історії частки євро", "Щоденний знімок тримає частку долара й не тримає "
      + "євро. Ціль задається, факт видно в структурі, а рух факту в часі — ні."],
  ];
  return `<div class="card"><h2>Про що застосунок мовчить навмисно</h2>
    <div class="sub">Порожнє місце тут не помилка. Кожен рядок — число, яке
      виглядало б виведеним, а насправді було б вигаданим.</div>
    ${items.map(([t, why]) => `<div class="rule-top mt-lg">
      <div class="empty-t">${esc(t)}</div>
      <div class="sub">${esc(why)}</div></div>`).join("")}
  </div>`;
}

/** Куди зараз піде найближче надходження.
 *
 *  Перші рядки маршруту, а не весь маршрут: питання «Огляду» — «чи є що
 *  розкладати», а не «як саме». Повний маршрут із колонкою основи
 *  (зобов'язання / намір / оцінка) живе у «Плані». */
function routePreviewHTML(rows) {
  if (!rows || !rows.length) {
    return `<div class="card"><h2>Що заходить найближчим часом</h2>${empty(
      "Надходжень попереду немає",
      "Тут стануть найближчі надходження й те, куди вони підуть.",
      { href: routeFor("plan/inflow"), label: "Додати джерело доходу" })}</div>`;
  }
  return `<div class="card">
    <h2 class="card-head"><span>Що заходить найближчим часом</span>
      <a class="lnk" href="${routeFor("plan/route")}">увесь маршрут</a></h2>
    ${rows.slice(0, 5).map((r) => `<div class="pv-row">
      <span class="muted">${esc(r.date || "")} · ${esc(r.label || r.name || "")}</span>
      <span>${r.amount ? esc(`${Number(r.amount.amount || r.amount)
    .toLocaleString("uk", { minimumFractionDigits: 2 })} ${
    curSym((r.amount && r.amount.currency) || "UAH")}`) : "—"}</span>
    </div>`).join("")}
  </div>`;
}

/** Сама сторінка. */
export async function overview(ctx, main) {
  // Три м'які читання паралельно. Кожне живить свій блок, і падіння
  // будь-якого прибирає рівно його — той самий прийом, що в «Порівнянні».
  const [progress, route] = await Promise.all([
    ctx.soft("progress", null),
    ctx.soft("route", null),
  ]);
  const s = ctx.summary || {};

  main.innerHTML = `
    ${heroHTML(ctx)}
    ${queuesHTML(ctx)}
    ${progressHTML(progress)}
    <div class="card"><h2>Цей місяць</h2>
      <div class="tiles flush">${monthTile(ctx, s)}</div></div>
    ${routePreviewHTML(route && (route.rows || route))}
    ${rebalanceCard(ctx)}
    ${silenceHTML()}`;
}
