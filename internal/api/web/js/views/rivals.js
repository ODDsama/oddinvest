import { esc, pct, uah2 as fmtUAH } from "../format.js";
import { infoBtn } from "../info.js";
import { svgLine, fluid, seriesLegend } from "../charts.js";
import { tile, empty } from "../components.js";
import { opsGrid } from "../grid.js";

// «Ціна моїх рішень»: мої гроші проти чотирьох механічних альтернатив.
//
// Замінює картку «А якби просто долари». Долар лишився — одним рядком із
// чотирьох, — а рахунок під ним той самий: /api/benchmark відколи існує ця
// сторінка є тонкою обгорткою над тим самим рушієм, тож двох арифметик
// одного числа тут немає.
//
// ДВА РІВНІ ПЕРЕМИКАЧЕМ, А НЕ ДВІ КАРТКИ ПОРУЧ. «Портфель» і «усі гроші» —
// це та сама відповідь на різні гроші, і поставлені поруч вони читались би
// як дві різні відповіді. Перемикач каже прямо: питання одне, а от що ти
// називаєш своїми грішми — вибір.
//
// АРИФМЕТИКИ ТУТ НЕМАЄ ЖОДНОЇ (CLAUDE.md §5): і термінали, і різниці, і
// відсотки приходять із /api/rivals готовими. Навіть «зайшло після
// відкриття» не віднімається тут — воно й не показується окремо, бо
// відповіді не додає.

const LEVEL_KEY = "oddinvest.rivalLevel";

function level() {
  try { return localStorage.getItem(LEVEL_KEY) === "all" ? "all" : "portfolio"; }
  catch (_) { return "portfolio"; }
}

// Кольори — з наявних рядів, п'ятої палітри не заводимо.
//
// Суперники не є шарами капіталу, тож семантика тутешніх токенів на них
// не переноситься — крім одного випадку, і він точний: «гривня під
// матрацом» бере --oi-series-neutral, при якому написано «присутній і
// мовчазний», а це буквально й є поведінка «нічого не робив».
const COLORS = {
  uah_cash: "var(--oi-series-neutral)",
  usd_cash: "var(--oi-series-invested)",
  eur_cash: "var(--oi-series-funds)",
  ovdp_market: "var(--oi-series-npf)",
};

/** Картка. d — відповідь /api/rivals, або null, коли бекенд не відповів. */
export function rivalsCard(ctx, d) {
  const lv = level();
  // aria-pressed, а не клас на НЕактивній: активний стан не має читатись
  // із заперечення — ані в розмітці, ані читачем екрана.
  const btn = (v, t) => `<button data-rivlevel="${v}" aria-pressed="${lv === v}">${t}</button>`;
  const head = `<h2 class="card-head">
    <span>Ціна моїх рішень ${infoBtn("rivals")}</span>
    <span class="seg">${btn("portfolio", "портфель")}${btn("all", "усі гроші")}</span></h2>`;

  if (!d) {
    return `<div class="card">${head}${empty("", "Бекенд не віддав порівняння.")}</div>`;
  }
  if (d.why) {
    return `<div class="card">${head}${empty("", esc(d.why))}</div>`;
  }
  const rows = (d.rivals || []).filter((r) => !r.why);
  const mute = (d.rivals || []).filter((r) => r.why);
  if (!rows.length) {
    return `<div class="card">${head}${empty("",
      "Жоден суперник не порахувався — нижче сказано, чому.")}${silentHTML(mute)}</div>`;
  }

  // Малюємо РІЗНИЦЮ, а не самі вартості, і це рішення з живої перевірки.
  //
  // Абсолютні криві на молодому портфелі — п'ять майже однакових
  // сходинок: форму їм задають внески, а не рішення, і різниці в кілька
  // сотень гривень на шкалі до сорока тисяч не видно взагалі. Тобто
  // графік малював те, що й так відоме («я вносив гроші»), і ховав те,
  // заради чого існує. Історія самого капіталу вже є поруч, на «Як
  // росте», тож другої її копії тут і не бракує.
  //
  // zero: false, попри те що нуль тут осмислений. zero: true в niceScale
  // означає «нуль ВНИЗУ шкали» — а всі ці числа бувають від'ємними, і
  // тоді криві йшли б за полотно (перевірено вживу: вісь показувала
  // 0…50 при значеннях до −3 000). Нуль однаково потрапляє в шкалу сам:
  // перша точка кожної кривої — рівно нуль, бо стартують усі з того
  // самого відкриття.
  const series = rows.map((r) => ({
    name: r.label, color: COLORS[r.key] || COLORS.uah_cash, values: r.points_diff || [],
  }));

  const chart = `${fluid((w, h) => svgLine(d.days || [], series, { W: w, H: h, zero: false }),
    { cls: "tall" })}
    <div class="lg">${seriesLegend(series)}</div>
    <div class="sub-xs muted">Наскільки я попереду кожного суперника, ₴.
      Вище нуля — попереду я. Середина кривої намальована з добових знімків
      і може відставати від внеску на день; кінець точний.</div>`;

  const table = opsGrid({
    cols: [
      { key: "label", label: "Якби я", cell: (r) => esc(r.label) },
      { key: "terminal", label: "Мав би сьогодні", num: true,
        cell: (r) => fmtUAH(r.terminal_uah) },
      { key: "diff", label: "Я проти нього", num: true, cell: diffCell },
    ],
    rows,
    caption: "Механічні альтернативи: що б я мав сьогодні й наскільки я попереду",
  });

  return `<div class="card">${head}
    <div class="tiles flush">
      ${tile("У мене зараз", fmtUAH(d.actual_uah))}
      ${tile("Зайшло в гру", fmtUAH(d.in_uah),
        `<div class="sub">за курсами тих днів</div>`)}
    </div>
    ${chart}
    ${table}
    ${proseHTML(d)}
    ${silentHTML(mute)}</div>`;
}

// Знак показується завжди — і в плюс, і в мінус: без нього «+419» і
// «−419» читаються однаково швидко, а різниця між ними тут і є вся
// відповідь.
function diffCell(r) {
  const won = (r.diff_uah || 0) >= 0;
  const tone = won ? "t-ok" : "t-danger";
  return `<span class="${tone}">${won ? "+" : ""}${fmtUAH(r.diff_uah)}</span>
    <span class="sub-xs ${tone}">${won ? "+" : ""}${pct(r.diff_pct)}</span>`;
}

// Проза під таблицею. Три речі, і кожна тут тому, що без неї число
// прочитали б не тим, чим воно є.
function proseHTML(d) {
  const bits = [];
  // Про відкриття говоримо, лише коли на руках справді щось було: «те,
  // що вже лежало (0,00 ₴)» — це речення ні про що, яке читач мусить
  // дочитати, щоб дізнатись, що воно ні про що.
  bits.push(d.open_uah
    ? `Кожен суперник дістав ТІ САМІ гроші в ТІ САМІ дні: те, що вже лежало
       на ${esc(d.first_day || "")} (${fmtUAH(d.open_uah)}), і кожен дальший рух.
       Оцінені всі на сьогодні.`
    : `Кожен суперник дістав ТІ САМІ гроші в ТІ САМІ дні — з
       ${esc(d.first_day || "")}, коли на руках не було ще нічого. Оцінені всі
       на сьогодні.`);
  bits.push(`Вікно починається на першому добовому знімку — раніше застосунок
    власного капіталу не знав, і намальована звідти лінія була б не історією,
    а нулем.`);
  if (d.young) {
    bits.push(`<b>${d.day_count} ${d.day_count === 1 ? "день" : d.day_count < 5 ? "дні" : "днів"}</b>
      — це ще про момент входу, а не про стратегію. Річних тут немає навмисно:
      помножене на рік, таке число стало б твердженням, якого ніхто не
      перевіряв.`);
  }
  bits.push(`Суперники нічого не приносять понад свою природу: валюта — лише
    курс, ринкова ОВДП — рівень розміщення Мінфіну на строк ${esc(d.ovdp_bucket || "")}
    (дохід за ОВДП не оподатковується). Купони, дивіденди й відсотки — це те,
    що ти отримав натомість, і супернику вони не нараховуються.`);
  if (d.note) bits.push(`<b>${esc(d.note)}.</b>`);
  return `<div class="muted fine">${bits.join(" ")}</div>`;
}

// Суперник, який не порахувався, називає причину й не показує нуля: нуль
// на цьому полотні читався б як «нічого не варте».
function silentHTML(mute) {
  if (!mute.length) return "";
  return `<div class="muted fine">${mute.map((r) =>
    `<b>${esc(r.label)}</b> — ${esc(r.why)}.`).join(" ")} Суперник, якому бракує
    ціни хоч на один день свого потоку, мовчить цілком: число з діркою
    виглядає так само переконливо, як ціле.</div>`;
}

export function wireRivals(ctx, main) {
  main.querySelectorAll("[data-rivlevel]").forEach((b) =>
    b.addEventListener("click", () => {
      try { localStorage.setItem(LEVEL_KEY, b.dataset.rivlevel); } catch (_) {}
      ctx.reload();
    }));
}

/** Рівень для запиту — щоб панель не знала про ключ у localStorage. */
export function rivalsPath() { return "rivals?level=" + level(); }
