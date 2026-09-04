// Валютний шок: рух курсу, який УЖЕ був, накладений на сьогоднішній
// портфель.
//
// Жодного числа тут не рахується. Епізод шукає сервер у власній історії
// fx_rates, а «стане» — це той самий документ стану, тільки зібраний за
// зрушеними курсами (GET /api/fx-shock). Тому «зараз» і «стане» завжди
// міряні однією лінійкою, і віднімання між ними законне — на це є тест,
// який вимагає, щоб підміна ТИМИ САМИМИ курсами дала байт у байт
// /api/summary.
//
// ЧОГО ТУТ СВІДОМО НЕМАЄ — і чому, бо інакше наступний автор допише це
// заново:
//
//   Ціни ОВДП. Застосунок їх не моделює навмисно (README), тож «папери
//   подешевшали на X%» тут не буде ніколи. Ціновий ризик має власний
//   вимір — дюрацію, — і вона стоїть у «Лімітах», названа тим, чим є.
//
//   Ставок. Шок суто валютний, бо виміряної історії ставок за 2014 чи
//   2022 в базі немає: аукціони тримаються близько року. Сценарії
//   «ставка ±п.п.» живуть у «Важелях» — сюди на них посилаємось, а не
//   дублюємо.
//
//   Перцентиля курсу. Під шоком він показав би 100 — і це читалось би як
//   сигнал «дорого», тобто рівно той крок, який заборонено абзацом у
//   domain/fxwindow.go. Факт про минуле не має ставати порадою на завтра.
//
//   Дзеркального боку («а якби гривня зміцнилась»). Питання не ставилось,
//   а показаний поруч він читався б як половина поради.
//
// ЧОМУ ВІКНО ЗАПАМʼЯТОВУЄТЬСЯ В LOCALSTORAGE. Той самий прийом і той
// самий довід, що в «Ціні моїх рішень» (rivals.js): це не частина плану
// й не політика, а зручність читача на цьому пристрої. У базі йому місця
// немає — там воно стало б другим джерелом правди про те, чого людина не
// задавала.

import { esc, uah2 as fmtUAH } from "../format.js";
import { empty } from "../components.js";
import { infoBtn } from "../info.js";
import { delta, dateDelta, targetTail } from "./impact.js";

const WIN_KEY = "oi.shock.window";
const WINDOWS = [
  { v: 1, t: "місяць" },
  { v: 3, t: "квартал" },
  { v: 12, t: "рік" },
];

function window0() {
  try {
    const v = Number(localStorage.getItem(WIN_KEY));
    if (WINDOWS.some((w) => w.v === v)) return v;
  } catch (_) { /* приватне вікно чи заблоковані дані сайту */ }
  return 12;
}

/** Шлях запиту — щоб панель не знала про ключ у сховищі браузера. */
export function shockPath() { return "fx-shock?window=" + window0(); }

const asPct = (v) => `${(v || 0).toFixed(2)}%`;
const asMonths = (v) => `${(v || 0).toFixed(1)} міс.`;

// Курс пишеться чотирма знаками: НБУ публікує саме стільки, і округлення
// до копійок ховало б рух дрібних валют.
const asRate = (v) => (v || 0).toFixed(4);

/** Шапка з перемикачем вікна. Кнопки, а не форма: вибір з трьох
 *  значень — це сегмент, а не поле, яке кудись подається. */
function head(d) {
  const cur = window0();
  const have = new Set(d && d.windows ? d.windows : []);
  const btn = (w) => {
    const dead = have.size > 0 && !have.has(w.v);
    return `<button data-shockwin="${w.v}" aria-pressed="${cur === w.v}"
      ${dead ? "disabled title=\"на наявній історії це вікно не міряється\"" : ""}
      >${w.t}</button>`;
  };
  return `<h2 class="card-head">
    <span>Валютний шок ${infoBtn("fxShock")}</span>
    <span class="seg">${WINDOWS.map(btn).join("")}</span></h2>`;
}

/** Підпис, який називає межу даних просто на екрані.
 *
 *  Не в «як читати», а тут: скільки місяців стоїть за словом «найгірше» —
 *  це частина самого числа, а не довідка про нього. Той самий довід, що
 *  при полі Points у domain/fxwindow.go. */
function basis(d) {
  const m = (d.measured || {});
  if (!m.months) return "";
  return `<div class="muted fine">Міряно по <b>${m.months}</b> міс. історії
    ${m.from ? `(${esc(m.from)} — ${esc(m.to)})` : ""}, якір — ${esc(m.anchor)}.
    Історія курсів помісячна, тож рух УСЕРЕДИНІ місяця в неї не потрапляє:
    у лютому 2022 курс пройшов далі, ніж покаже будь-яка пара перших чисел.</div>`;
}

/** Сам епізод: які дати, який рух, у що перетворюється сьогоднішній курс. */
function episode(ep) {
  const rows = (ep.moves || []).map((m) => {
    if (m.why) {
      return `<div class="pv-row"><span>${esc(m.currency)}</span>
        <span class="muted">${esc(m.why)}</span></div>`;
    }
    return `<div class="pv-row"><span>${esc(m.currency)}
      <span class="muted fine-xs">${esc(m.from)} → ${esc(m.to)}:
        ${asRate(m.from_rate)} → ${asRate(m.to_rate)}</span></span>
      <span><span class="muted">${asRate(m.rate_now)} →</span>
        <b>${asRate(m.rate_then)}</b>
        <span class="muted fine-xs">· ${m.move_pct > 0 ? "+" : ""}${asPct(m.move_pct)}</span></span></div>`;
  }).join("");
  return `<div class="sub mb-sm">Що саме програємо</div>
    <div class="note">Найгірший виміряний рух за ${ep.window_months} міс.:
      <b>${esc(ep.from)} → ${esc(ep.to)}</b>. Переносимо не тодішні рівні, а
      той самий ВІДСОТОК на сьогоднішній курс.</div>
    ${rows}`;
}

/** Наслідки — відніманням «стане» проти «зараз». */
function consequences(ctx, after) {
  const before = ctx.summary || {};
  const st = before.settings || {};
  const a = before.reserve || {}, b = after.reserve || {};
  const indA = before.independence || {}, indB = after.independence || {};
  const debtA = before.debt || {}, debtB = after.debt || {};

  const goals = (after.goals || []).map((g) => {
    const was = (before.goals || []).find((x) => x.id === g.id);
    // Лише валютні цілі: гривнева під валютним шоком не рухається за
    // побудовою, і рядок «без змін» на ній привчав би не читати картку.
    if (!was || !g.currency || g.currency === "UAH") return "";
    return delta(`Ціль «${esc(g.name)}»`, was.done_pct, g.done_pct, asPct);
  }).join("");

  return `<div class="sub mb-sm mt-lg">Що це робить із портфелем</div>
    ${delta("Капітал", before.capital_uah, after.capital_uah, fmtUAH)}
    ${delta("Чистий капітал", before.net_worth_uah, after.net_worth_uah, fmtUAH)}
    ${delta("Частка USD", before.usd_share_pct, after.usd_share_pct, asPct,
    targetTail(st.usd_target_share_pct))}
    ${delta("Частка EUR", before.eur_share_pct, after.eur_share_pct, asPct,
    targetTail(st.eur_target_share_pct))}
    ${a.target_months || b.target_months
    ? delta("Подушка", a.months, b.months, asMonths,
      a.target_months ? ` <span class="muted fine-xs">· ціль ${a.target_months} міс.</span>` : "")
    : ""}
    ${goals}
    ${debtA.total_uah || debtB.total_uah
    ? delta("Борг", debtA.total_uah, debtB.total_uah, fmtUAH)
      + `<div class="muted fine">Борг гривневий, тож у гривнях він не рухається —
        рухається те, скільки він важить проти капіталу.</div>`
    : ""}
    ${indA.date || indB.date ? dateDelta("Точка незалежності", indA.date, indB.date) : ""}
    ${delta("Місячна ціль", before.month_target_uah, after.month_target_uah, fmtUAH)}`;
}

/** Про що шок мовчить — просто на екрані, а не лише в «як читати».
 *
 *  Названа відсутність тримає читача від висновку, якого дані не несуть:
 *  без цього абзацу «капітал майже не зрушив» прочиталося б як «портфель
 *  стійкий», хоч про переоцінку паперів тут не сказано нічого. */
function silent() {
  return `<div class="muted fine mt-lg">Про що цей вимір мовчить:
    <b>ціна ОВДП</b> — застосунок її не моделює, а виміром ризику ціни
    служить дюрація в «Лімітах»; <b>ставки</b> — виміряної історії за
    2014 чи 2022 в базі немає, сценарії ±п.п. живуть у
    <a class="lnk" href="#/plan/levers/main">«Важелях»</a>;
    <b>інфляція</b> — ряду ІСЦ у застосунку немає, шок міряє лише курс;
    <b>ціна сертифікатів фондів і ЧВОПА</b> — як вони відгукуються на курс,
    застосунок не знає.</div>`;
}

/** Картка. d — відповідь /api/fx-shock, або null, коли бекенд не відповів. */
export function fxShockCard(ctx, d) {
  if (!d) {
    return `<div class="card">${head(null)}
      ${empty("", "Бекенд не віддав вимір.")}</div>`;
  }
  if (!d.episode) {
    return `<div class="card">${head(d)}
      ${empty("", esc(d.why || "Історії курсів замало, щоб щось міряти."))}
      ${basis(d)}</div>`;
  }
  return `<div class="card">${head(d)}
    <div class="note">Не сценарій і не прогноз: це рух, який СПРАВДІ був,
      покладений на портфель, який є зараз. Гривня падає стрибками, і
      найгірше за десять років було найгіршим рівно до наступного разу.</div>
    ${episode(d.episode)}
    ${consequences(ctx, d.after || {})}
    ${silent()}
    ${basis(d)}</div>`;
}

export function wireFXShock(ctx, main) {
  main.querySelectorAll("[data-shockwin]").forEach((b) =>
    b.addEventListener("click", () => {
      try { localStorage.setItem(WIN_KEY, b.dataset.shockwin); } catch (_) { /* дані сайту заблоковані */ }
      ctx.reload();
    }));
}
