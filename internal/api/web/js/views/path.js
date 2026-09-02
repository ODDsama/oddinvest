// «Шлях» — як далеко я зайшов.
//
// МЕЖА, ЯКА РОБИТЬ ГРУ ПРИЙНЯТНОЮ, лишається та сама: кожне число тут
// виміряне, жодне не нараховане. Прибери гру — числа лишаться ті самі,
// просто читатимуться гірше. Повний довід і те, чого тут навмисно немає
// (очок за активність, докору за розрив серії, порівняння з чужими
// портфелями), записано в internal/api/state_progress.go: рахує все
// бекенд, і фронтенд не знає навіть, як складається рівень.
//
// ЧОМУ ЦЕ ПЕРЕЇХАЛО З «ОГЛЯДУ». Блок стояв там суцільною сіткою з
// чотирнадцяти карток однакової ваги — тобто картою без напрямку: жодна
// віха не була названа найближчою, і жодне число не казало, скільки
// лишилось. Питання «як далеко я зайшов» ставлять окремо й рідше за «що
// зробити сьогодні», і під чергою рішень ця сітка одночасно програвала
// сусідству з терміновим і відтісняла його вниз.
//
// НАПРЯМОК ДАЄ БЕКЕНД, А НЕ ЦЕЙ ФАЙЛ. Найближчу віху вибирає pickNext,
// «скільки лишилось» приходить готовою прозою в полі left. Тут
// лишається рівно одне власне знання — на якому екрані ця віха
// рухається (MOVES нижче), і воно навігаційне, а не доменне.
//
// ДАНІ БЕРУТЬСЯ З ctx.progress, а не читаються тут. Оболонка вантажить
// їх один раз на вкладку (app.js), бо те саме число показує підвал
// майстер-списку; чотири в'юшки з власним ctx.soft дали б два читання
// одного маршруту на кожному переході всередині «Шляху».

import { esc, monthShort, plural, uah0, signedUAH } from "../format.js";
import { empty, progressBar } from "../components.js";
import { routeFor } from "../routes.js";
import { heatmapHTML } from "./year.js";

/** Де рухається кожна віха.
 *
 *  Таблиця живе ТУТ, а не в бекенді, навмисно. Адреса екрана — знання
 *  фронтенду, і віддана рядком із сервера вона стала б невидимою для
 *  web-routes-check.mjs: мертвий перехід прожив би непоміченим рівно
 *  так, як прожили три дії черги (довід — у шапці того скрипта). Поле
 *  зветься `to` тому, що саме цю форму скрипт і знає.
 *
 *  Віхи без рядка тут не помилка: «Обіграв просто долари» рухається не
 *  однією дією, а часом, і кнопка «зробити щось» під нею обіцяла б
 *  важіль, якого немає. */
const MOVES = {
  first_bond: { to: "work/buy/main", label: "Що купити" },
  first_100k: { to: "work/buy/main", label: "Що купити" },
  first_1m: { to: "work/buy/main", label: "Що купити" },
  first_2m: { to: "work/buy/main", label: "Що купити" },
  four_kinds: { to: "work/buy/main", label: "Що купити" },
  half_year_streak: { to: "plan/goal/main", label: "Ціль і прогноз" },
  year_no_gaps: { to: "plan/goal/main", label: "Ціль і прогноз" },
  income_quarter: { to: "plan/goal/main", label: "Ціль і прогноз" },
  reserve_full: { to: "policy/reserve/main", label: "Налаштувати резерв" },
  ladder_full: { to: "policy/reserve/main", label: "Налаштувати резерв" },
  shares_aligned: { to: "portfolio/all/structure", label: "Структура" },
  currency_aligned: { to: "portfolio/all/structure", label: "Структура" },
  no_limit_breach: { to: "portfolio/all/limits", label: "Ліміти" },
  life_month: { to: "work/buy/main", label: "Що купити" },
  life_year: { to: "work/buy/main", label: "Що купити" },
  net_worth_positive: { to: "plan/debts/main", label: "Борги" },
  card_zero: { to: "plan/debts/main", label: "Борги" },
  exit_by_met: { to: "plan/debts/main", label: "Борги" },
  debt_covered: { to: "policy/reserve/main", label: "Налаштувати резерв" },
  installments_done: { to: "plan/debts/main", label: "Борги" },
};

/** «Портфель оплатив N днів життя» — один рядок, той самий на «Огляді»
 *  й у «Звичці». Лічильник, що росте, а не поріг: саме тому він стоїть
 *  окремо від віх і не має відсотка. */
function lifeLineHTML(p) {
  const l = p && p.life;
  if (!l) return "";
  const n = Math.floor(l.days);
  return `<div class="sub">Портфель уже оплатив <b>${n} ${dayWord(n)}</b> твого життя — ${
    uah0(l.income_uah)} заробленого при ${uah0(l.per_day_uah)} на день</div>`;
}

function dayWord(n) {
  const t = Math.abs(n) % 100;
  if (t >= 11 && t <= 14) return "днів";
  const o = t % 10;
  if (o === 1) return "день";
  if (o >= 2 && o <= 4) return "дні";
  return "днів";
}

/** Чи є що показувати взагалі. Порожньо означає, що маршрут не
 *  відповів або портфель ще порожній. */
function ready(p) {
  return !!(p && p.milestones && p.milestones.length);
}

/** Віха, названа найближчою. Ключ приходить із бекенда, а сама віха
 *  береться зі списку — другої її копії у відповіді немає навмисно. */
function nextOf(p) {
  if (!ready(p) || !p.next_key) return null;
  return p.milestones.find((m) => m.key === p.next_key) || null;
}

/** Порожня сторінка «Шляху».
 *
 *  На «Огляді» блок у такому разі просто зникав — половина прогресу
 *  гірша за його відсутність. Тут зникнути нема чому: сторінку відкрили
 *  саме заради нього, і порожнеча мусить бути названа словами. */
function noProgress() {
  return `<div class="card">${empty("Прогресу поки немає",
    "Віхи рахуються з портфеля, знімків і журналу рішень. Щойно з'явиться "
    + "перший папір, тут стане що показувати.",
    { href: routeFor("work/buy/main"), label: "Що купити" })}</div>`;
}

// ---------------------------------------------------------------------
// Найближче
// ---------------------------------------------------------------------

/** Герой сторінки: одна віха, до якої ближче за все.
 *
 *  «Лишилось» стоїть головним числом, а відсоток — смугою під ним, і це
 *  не оформлення. Відсоток каже, наскільки далеко зайшов, і не каже, що
 *  робити; гривні кажуть. */
function heroHTML(m) {
  const move = MOVES[m.key];
  return `<div class="mile-hero">
    <div class="mile-t">${esc(m.title)}</div>
    ${m.left ? `<div class="mile-l">${esc(m.left)}</div>` : ""}
    <div class="sub">${esc(m.note)}</div>
    ${m.progress_pct >= 0
    ? progressBar(m.progress_pct, { color: "var(--oi-accent)" }) : ""}
    ${etaHTML(m)}
    ${move ? `<a class="lnk mile-a" href="${routeFor(move.to)}">${
  esc(move.label)}</a>` : ""}
  </div>`;
}

/** «≈ коли · чий це темп». Основа стоїть поруч із датою завжди — два
 *  різні «коли» в застосунку не зводяться в одне число (state_progress.go,
 *  milestone.EtaBasis), і дата без основи читалась би як обіцянка. */
function etaHTML(m) {
  if (!m.eta_on) return "";
  return `<div class="sub-xs">≈ ${esc(m.eta_on)} · ${esc(m.eta_basis)}</div>`;
}

/** Що вже сталось: датовані віхи, найновіші згори.
 *
 *  Доти на «Огляді» стояв банер рівно з ОДНІЄЮ останньою датованою — і
 *  саме тому нічого не змінювалось місяцями: подія лишалась та сама,
 *  доки не траплялась наступна. Список показує ту саму правду як
 *  послідовність, і кожна нова віха робить його довшим.
 *
 *  Недатовані сюди не потрапляють ніколи. Довід записаний у бекенді при
 *  milestone.EarnedOn: у частини віх дати немає НІДЕ, і вигадувати її
 *  заради стрічки — рівно те, чого ця сторінка не робить. Ціна названа
 *  підписом під списком, а не змовчана.
 *
 *  ПРИМІТКИ ВІХИ В РЯДКУ НЕМАЄ, і це не економія. У датованої віхи note
 *  і Є дата («куплено 2026-02-16», «пройдено 2026-04-30»), тож поруч із
 *  колонкою дати вона читалась би як дві різні дати в одному рядку — той
 *  самий довід, через який дата не дублювалась у банері, на місце якого
 *  ця стрічка стала. Що саме сталось, каже назва; коли — колонка. */
function timelineHTML(p) {
  const dated = p.milestones.filter((m) => m.earned && m.earned_on)
    .sort((a, b) => (a.earned_on < b.earned_on ? 1 : -1));
  const blind = p.milestones.filter((m) => m.earned && !m.earned_on).length;

  if (!dated.length) {
    return `<div class="card"><h2>Що вже сталось</h2>${empty(
      "Датованих віх ще немає",
      "Тут стануть віхи, у яких є день: перша покупка, перехід порога "
      + "капіталу. Стан, який став правдою до першого знімка, дати не має "
      + "ніде, і вигадувати її застосунок не буде.")}</div>`;
  }

  const tail = blind
    ? `<div class="sub mt-lg">Ще ${blind} ${plural(blind, "віха", "віхи", "віх")
    } зібрано без дати: це стан, який став правдою до того, як застосунок `
      + `почав вести знімки, і дня в нього немає ніде.</div>`
    : "";

  return `<div class="card"><h2>Що вже сталось</h2>
    <div class="tl">${dated.map((m) => `<div class="tl-row">
      <span class="tl-d">${esc(m.earned_on)}</span>
      <span class="tl-n">${esc(m.title)}</span>
    </div>`).join("")}</div>${tail}</div>`;
}

/** Рядок про найближчу віху для «Огляду».
 *
 *  Експортується звідси, а не пишеться там удруге, з того ж доводу, що
 *  «Огляд» уже бере monthTile з «Роботи» й rebalanceCard із «Портфеля»:
 *  друга копія розійшлася б із першою тихо.
 *
 *  Порожньо, коли найближчої немає. Це той самий випадок, що й доти:
 *  на «Огляді» половина блока гірша за його відсутність, а сказати
 *  словами, чому порожньо, — робота сторінки «Шляху», не головної. */
export function nextLineHTML(p) {
  const m = nextOf(p);
  if (!m) return "";
  return `<div class="card">
    <h2 class="card-head"><span>Найближча віха</span>
      <a class="lnk" href="${routeFor("path/next/main")}">увесь шлях</a></h2>
    <div class="nx-row">
      <span class="nx-t">${esc(m.title)}</span>
      <span class="nx-l">${esc(m.left)}</span>
    </div>
    ${m.progress_pct >= 0
    ? progressBar(m.progress_pct, { color: "var(--oi-accent)" }) : ""}
    <div class="sub-xs">${esc(m.note)}</div>
    ${etaHTML(m)}
    ${lifeLineHTML(p)}
  </div>`;
}

/** «Найближче»: куди ближче за все й чим це міряється. */
export function next(ctx, main) {
  const p = ctx.progress;
  if (!ready(p)) {
    main.innerHTML = noProgress();
    return;
  }
  const m = nextOf(p);
  const all = p.level >= p.level_of;

  // Порожньо буває двома різними способами, і сплутати їх не можна:
  // «усі зібрані» — це кінець, а «ні до чого не відома відстань» — це
  // брак чисел, який лікується налаштуваннями, а не внесками.
  const body = m ? heroHTML(m) : empty(
    all ? "Усі віхи зібрані" : "Попереду немає нічого вимірного",
    all
      ? "Далі йде те саме, що й було: внески за планом і папери в межах."
      : "Решта віх чекає на числа, яких застосунок ще не знає — ціль резерву, "
      + "цілі часток, місячні витрати. Задай їх, і відстань стане видною.",
    all ? null : { href: routeFor("policy/mix/main"), label: "Частки й межі" });

  main.innerHTML = `<div class="card">
    <h2 class="card-head"><span>Найближча віха</span>
      <span class="sub-xs">${p.level} із ${p.level_of} зібрано</span></h2>
    ${body}
  </div>${timelineHTML(p)}`;
}

// ---------------------------------------------------------------------
// Віхи
// ---------------------------------------------------------------------

/** Сітка віх.
 *
 *  Замкнені показані НЕ як докір, а як карта: у кожної стоїть її власне
 *  число, тобто видно не «ти не зміг», а «стільки лишилось і чим це
 *  міряється». Підрядком іде саме `left` — «лишилось 24 636 ₴» відповідає
 *  на питання, з яким сюди й заходять; `note` («75 364 ₴ із 100 000 ₴»)
 *  лишається зібраним віхам, де лишатись нема чому. */
function badgesHTML(list) {
  return `<div class="badges">${list.map((m) => `
    <div class="badge${m.earned ? " on" : ""}">
      <span class="badge-i" aria-hidden="true">${m.earned ? "✓" : "○"}</span>
      <span class="badge-t">
        <span class="badge-n">${esc(m.title)}</span>
        <span class="badge-s">${esc(m.left || m.note)}${
  m.eta_on ? ` · ≈ ${esc(m.eta_on)} ${esc(m.eta_basis)}` : ""}</span>
      </span>
      ${!m.earned && m.progress_pct >= 0
    ? `<span class="badge-p">${m.progress_pct}%</span>` : ""}
    </div>`).join("")}</div>`;
}

/** «Віхи»: зібране й попереду.
 *
 *  ПОПЕРЕДУ ВПОРЯДКОВАНЕ ЗА БЛИЗЬКІСТЮ, і саме це робить сторінку
 *  живою: список перебудовується тоді й лише тоді, коли змінилось
 *  виміряне. Невимірні («порівнювати ще нема з чим») ідуть у хвіст самі
 *  собою — у них -1, — і це правильний хвіст: віха без відомої відстані
 *  не може стояти попереду тієї, до якої лишилось десять відсотків.
 *
 *  Зібране лишається в порядку бекенда: він тематичний, а сортувати вже
 *  пройдене нема за чим. */
export function milestones(ctx, main) {
  const p = ctx.progress;
  if (!ready(p)) {
    main.innerHTML = noProgress();
    return;
  }
  const done = p.milestones.filter((m) => m.earned);
  const ahead = p.milestones.filter((m) => !m.earned)
    .sort((a, b) => b.progress_pct - a.progress_pct);

  main.innerHTML = `<div class="card">
    <h2 class="card-head"><span>Зібрано</span>
      <span class="sub-xs">${p.level} із ${p.level_of}</span></h2>
    <div class="lvl">
      <div class="lvl-n">${p.level}</div>
      <div class="lvl-l">віх зібрано з ${p.level_of}</div>
    </div>
    ${done.length ? badgesHTML(done)
    : `<div class="sub">Жодної поки що. Перша — «Перший папір».</div>`}
  </div>
  <div class="card"><h2>Попереду</h2>
    ${ahead.length ? badgesHTML(ahead)
    : `<div class="sub">Попереду порожньо: усі ${p.level_of} зібрані.</div>`}
  </div>`;
}

// ---------------------------------------------------------------------
// Звичка
// ---------------------------------------------------------------------

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
    ? track("Постійність", `${st.months} ${monthWord(st.months)}`,
      streakNote || "місяців поспіль за планом", null)
    : track("Постійність", "—", streakNote, null);

  const coll = c.of
    ? track("Колекція", `${c.filled} / ${c.of}`,
      "клітинок поля «роки погашень × валюти»",
      c.filled / c.of * 100, "var(--oi-kind-fund)")
    : track("Колекція", "—", "драбини погашень ще немає", null);

  // Оплачені дні: лічильник без смуги — стелі в нього немає, росте він
  // завжди. Знаменник названий: без нього «12 днів» не перевірити.
  const l = p.life;
  const life = l
    ? track("Оплачені дні", `${Math.floor(l.days)} ${dayWord(Math.floor(l.days))}`,
      `${uah0(l.income_uah)} заробленого (купони, дивіденди, відсотки) при ${
        uah0(l.per_day_uah)} на день${l.since ? ` · з ${l.since}` : ""}`, null)
    : track("Оплачені дні", "—",
      "місячні витрати не задані — ділити нема на що", null);

  return disc + cons + coll + life;
}

// Місяць українською. Своя, бо format.js:plural просить три форми, а тут
// потрібна рівно одна пара слів і жодного числа перед ними.
function monthWord(n) {
  const t = Math.abs(n) % 100;
  if (t >= 11 && t <= 14) return "місяців";
  const o = t % 10;
  if (o === 1) return "місяць";
  if (o >= 2 && o <= 4) return "місяці";
  return "місяців";
}

/** Смужка місяців: серія, розкладена на клітинки.
 *
 *  ТРИ СТАНИ, А НЕ ДВА, і третій — не відтінок другого. «Повз» означає
 *  «ціль була, внеску не вистачило», «невідомо» — «знімка за той місяць
 *  немає, судити нічим». Розрізняє їх ФОРМА (пунктир проти суцільної), а
 *  не яскравість: приглушений відтінок читався б як слабший зрив, тобто
 *  як докір, а докору за власну сліпоту застосунку не належить.
 *
 *  Легенда зроблена тими самими клітинками, що й смужка: другий опис
 *  того самого розійшовся б із першим при першій же правці кольору. */
function stripHTML(st) {
  const marks = st.marks;
  // Класу на «повз» немає навмисно: гола клітинка і Є пропущений місяць.
  // Окремий .miss, який нічого не малює, був би класом без правила —
  // css-tokens-check.mjs ловить саме такі, і правильно.
  const cls = (m) => (!m.known ? " unk" : m.hit ? " hit" : "");
  const title = (m) => (m.known
    ? `${m.month} · внесено ${uah0(m.contrib_uah || 0)} із ${uah0(m.target_uah)}`
    : `${m.month} · цілі того місяця застосунок не знає`);

  return `<div class="strip" role="presentation">${marks.map((m) =>
    `<span class="strip-c${cls(m)}" title="${esc(title(m))}"></span>`)
    .join("")}</div>
    <div class="strip-x">
      <span>${esc(monthShort(marks[0].month))}</span>
      <span>${esc(monthShort(marks[marks.length - 1].month))}</span>
    </div>
    <div class="strip-lg">
      <span><span class="strip-c hit"></span>за планом</span>
      <span><span class="strip-c"></span>повз</span>
      <span><span class="strip-c unk"></span>невідомо</span>
    </div>`;
}

/** Смужка «попереду долара»: ті самі клітинки, що в серії внесків, і
 *  ДВА стани, а не три — ряд суперників починається з першого знімка, і
 *  місяців «невідомо» у ньому немає за побудовою (vsDoc у бекенді).
 *  Питання в цієї смужки інше: та — про дисципліну, ця — про результат. */
function vsStripHTML(vs) {
  const marks = vs.marks;
  const title = (m) => `${m.month} · ${signedUAH(m.diff_uah)} проти «просто доларів»`;
  return `<div class="strip" role="presentation">${marks.map((m) =>
    `<span class="strip-c${m.ahead ? " hit" : ""}" title="${esc(title(m))}"></span>`)
    .join("")}</div>
    <div class="strip-x">
      <span>${esc(monthShort(marks[0].month))}</span>
      <span>${esc(monthShort(marks[marks.length - 1].month))}</span>
    </div>
    <div class="strip-lg">
      <span><span class="strip-c hit"></span>попереду</span>
      <span><span class="strip-c"></span>позаду</span>
    </div>`;
}

/** «Звичка»: постійність, дисципліна, результат проти долара й дні з
 *  рухом грошей за поточний рік (хітмап — із «Року в цифрах», той самий
 *  рендер над тією самою відповіддю). */
export async function habit(ctx, main) {
  const p = ctx.progress;
  if (!ready(p)) {
    main.innerHTML = noProgress();
    return;
  }
  const y = await ctx.soft("year", null);
  const heat = y && y.days ? `<div class="card">
      <h2 class="card-head"><span>Дні з рухом грошей</span>
        <a class="lnk" href="${routeFor("portfolio/all/year")}">рік у цифрах</a></h2>
      ${heatmapHTML(y)}</div>` : "";
  const st = p.streak || {};
  const strip = st.marks && st.marks.length ? stripHTML(st) : empty(
    "Судити ще нічого",
    "Місяць зараховується, коли внесено не менше, ніж ціль ТОГО місяця, а "
    + "ціль минулого місяця відома лише зі знімка, зробленого тоді. Знімок "
    + "кладеться щодня автоматично — смужка почнеться з першого з них.");

  const vs = p.vs_usd;
  const vsNote = [
    vs && vs.best ? `найдовше — ${vs.best}` : "",
    vs && vs.since ? `попереду з ${vs.since}` : "",
    // Застереження те саме, що в «Ціні рішень»: знімок кладеться зранку,
    // тож внесок того самого дня видно з добовою затримкою.
    "останній день кожного місяця з ряду «Ціни рішень»; знімок ранковий, тож день внеску може відстати на добу",
  ].filter(Boolean).join(" · ");
  const vsCard = vs && vs.marks && vs.marks.length
    ? `<div class="card">
        <h2 class="card-head"><span>Проти долара</span>
          <span class="sub-xs">${vs.months} ${monthWord(vs.months)} поспіль попереду</span></h2>
        ${vsStripHTML(vs)}
        <div class="sub-xs">${esc(vsNote)}</div>
      </div>`
    : "";

  main.innerHTML = `<div class="card"><h2>Чотири доріжки</h2>
      <div class="tracks">${tracksHTML(p)}</div></div>
    <div class="card"><h2>Місяць за місяцем</h2>${strip}</div>${vsCard}${heat}`;
}

// ---------------------------------------------------------------------
// Колекція
// ---------------------------------------------------------------------

/** Поле колекції: роки погашень × валюти.
 *
 *  Підпис під полем — з бекенда, а не звідси: він пояснює, чому
 *  заповнене поле НЕ є ціллю саме по собі, і це та сама проза, яку мусив
 *  би побачити другий споживач, якби він з'явився. */
export function collection(ctx, main) {
  const p = ctx.progress;
  if (!ready(p)) {
    main.innerHTML = noProgress();
    return;
  }
  const c = p.collection || {};
  if (!c.rows || !c.rows.length) {
    main.innerHTML = `<div class="card"><h2>Поле колекції</h2>${empty(
      "Драбини погашень ще немає",
      "Поле показує, у якому році й у якій валюті до тебе повертається тіло. "
      + "Доки паперів із датою погашення немає, показувати нічого.",
      { href: routeFor("work/buy/main"), label: "Що купити" })}</div>`;
    return;
  }
  main.innerHTML = `<div class="card">
    <h2 class="card-head"><span>Поле колекції</span>
      <span class="sub-xs">${c.filled} / ${c.of}</span></h2>
    <div class="matrix" role="presentation">
      <div class="mx-row mx-head"><span></span>${c.currencies
    .map((x) => `<span class="mx-c">${esc(x)}</span>`).join("")}</div>
      ${c.rows.map((r) => `<div class="mx-row">
        <span class="mx-y">${r.year}</span>
        ${r.cells.map((on, i) => `<span class="mx-cell${on ? " on" : ""}"
          title="${r.year} · ${esc(c.currencies[i])}"></span>`).join("")}
      </div>`).join("")}
    </div>
    <div class="sub mt-lg">${esc(c.note)}</div>
  </div>`;
}
