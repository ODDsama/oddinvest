// Чим добрати: план місяця ще обіцяє гроші — ось куди їх, щоб частки
// вирівнялись.
//
// ПИТАННЯ, НА ЯКЕ ЦЕ ВІДПОВІДАЄ, І ЧОМУ ВОНО НЕ ТЕ САМЕ, ЩО В РОЗКЛАДКИ.
// Модалка розкладки (allocate.js) відповідає на «ось ЦІ гроші прийшли» — у
// неї одне надходження, своє джерело й свій дозвіл. Тут інше: «місяць
// обіцяв стільки, стільки з того вже розписано планом — чим добрати
// решту». Джерел у цієї суми десяток, і одного слова про них не буває.
//
// ЧОМУ КАРТКА ЖИВЕ САМЕ НА «ПЛАНІ КУПІВЕЛЬ». Відповідь мусить знати, що
// вже заплановано, інакше вона радить купити те, що в плані стоїть, —
// саме цим і хибувала кнопка «Розкласти» на «Що купити»: POST /api/allocate
// будує стан БЕЗ plan_buys. Тут же обидва входи поруч: /api/whatif віддає
// і повний документ після плану, і сам план у грошах.
//
// ЖОДНОГО ЧИСЛА ТУТ НЕ РАХУЄТЬСЯ — усе приходить готовим у res.topup. Той
// самий припис, що в buy-plan.js і allocate.js, і з тієї ж причини:
// «скільки паперів влізе в решту» у браузері було б другою ціною того
// самого паперу на сусідніх екранах.
//
// ВИРІЗКИ ПОДУШКИ, БОРГУ Й ЦІЛЕЙ ТУТ БЕЗ ПРАПОРЦІВ, на відміну від
// модалки. Різниця не в оформленні: там прапорець ЗАПИСУЄ рух, і без нього
// друга відмітка місяця запропонувала б ту саму суму двічі. Тут не
// записується нічого, крім рядків плану купівель, — картка лише пояснює,
// чому в інструменти йде менше, ніж обіцяв місяць. Прапорець, який нічого
// не робить, гірший за текст.

import { esc, uah2 as fmtUAH, cur2 as fmtCur, curSym, pct } from "../format.js";
import { opsGrid } from "../grid.js";
import { kindPill } from "../components.js";
import { infoBtn } from "../info.js";
import { routeFor } from "../routes.js";
import { apply } from "../forms.js";
import { buyBody } from "./allocate.js";

// Куди веде рядок, який у план не кладеться. Вклад — єдиний такий вид:
// порада про нього це ПОПОВНЕННЯ наявного, а рядок плану купівель описує
// НОВИЙ вклад і вимагає строку, якого в пораді немає (allocLine в
// handlers_allocate.go). Той самий перелік, що в allocate.js, і поки в
// ньому один ключ, спільного місця він не вартий: винести його — означало
// б завести абстракцію з одним значенням.
const WHERE = { deposit: "instr/deposits" };

// Три числа шапки, і всі три обовʼязкові.
//
// Показати саму лише різницю означало б показати результат віднімання, не
// показавши віднімання: питання «чому пропонують так мало» виникає рівно
// на ньому, і відповідь «бо тисячу вже розписано» мусить стояти поруч, а
// не в іншій картці.
function headHTML(res) {
  const planUAH = res.topup_plan_uah || 0;
  const leftUAH = res.topup_left_uah || 0;
  const takenUAH = Math.max(0, planUAH - leftUAH);
  const parts = [`План місяця ще обіцяє <b>${fmtUAH(planUAH)}</b>`];
  if (takenUAH > 0.005) {
    parts.push(`з них уже розписано планом купівель ${fmtUAH(takenUAH)}`);
  }
  parts.push(`лишається <b>${fmtUAH(leftUAH)}</b>`);
  return `<div class="sub">${parts.join(" · ")}.</div>`;
}

// Вирізки, що йдуть ПЕРЕД інструментами, у тому самому порядку, у якому їх
// ріже бекенд: подушка → борг → цілі. Третій порядок на третьому екрані
// читався б як третє правило.
//
// Ховати їх не можна навіть заради компактності: без них сума картки не
// сходиться з тим, що обіцяв місяць, і бракує рівно цих грошей — тобто
// найгіршим способом, мовчки.
function cutsHTML(t) {
  const out = [];
  const line = (pill, amount, why) => `<div class="mb-sm">${kindPill(pill)}
    <b>${fmtUAH(amount)}</b><div class="sub-xs">${esc(why)}</div></div>`;
  if (t.reserve && t.reserve.amount_uah > 0) {
    out.push(line("reserve", t.reserve.amount_uah, t.reserve.why));
  }
  if (t.reserve_skip_why) {
    out.push(`<div class="sub-xs t-warn mb-sm">${esc(t.reserve_skip_why)}</div>`);
  }
  if (t.debt && t.debt.amount_uah > 0) {
    out.push(line("debt", t.debt.amount_uah, t.debt.why));
  }
  if (t.debt_skip_why) {
    out.push(`<div class="sub-xs t-warn mb-sm">${esc(t.debt_skip_why)}</div>`);
  }
  for (const g of (t.goals || []).filter((x) => x.amount_uah > 0)) {
    out.push(`<div class="mb-sm">${kindPill("goal")} <b>${g.name}</b>
      — <b>${fmtUAH(g.amount_uah)}</b><div class="sub-xs">${esc(g.why)}</div></div>`);
  }
  if (t.goals_skip_why) {
    out.push(`<div class="sub-xs t-warn mb-sm">${esc(t.goals_skip_why)}</div>`);
  }
  if (!out.length) return "";
  return `${out.join("")}<div class="sub-xs">Ці гроші забирають своє ПЕРЕД видами —
    саме тому нижче розкладається менше, ніж обіцяв місяць. Записуються вони не
    звідси: подушка й цілі — рухом на своїй сторінці, платіж у борг — на сторінці
    боргу.</div>`;
}

// Рядки покупок. Індекс рядка їде в data-атрибут, а сам рядок лишається в
// пам'яті проводки (wireTopup): класти тіло запиту в розмітку означало б
// або екранувати JSON у атрибуті, або зібрати його вдруге з того, що
// намальовано, — тобто прочитати власний HTML замість даних.
function linesHTML(t) {
  const rows = (t.lines || []).map((l, i) => ({ ...l, id: i }));
  if (!rows.length) return "";
  return opsGrid({
    cols: [
      {
        key: "what", label: "Що",
        cell: (l) => kindPill(l.kind) + " " + esc(l.label)
          + (l.why ? `<div class="fine-xs muted">${esc(l.why)}</div>` : "")
          // Конвертація називається сумою, а не самим фактом: «треба
          // конвертувати» без числа не каже, скільки саме міняти.
          + (l.convert
            ? `<div class="fine-xs t-warn">треба конвертувати${l.convert_native
              ? " ≈" + esc(fmtCur(l.convert_native, curSym(t.amount.currency))) : ""}</div>`
            : ""),
      },
      {
        key: "howmuch", label: "Скільки", num: true,
        cell: (l) => (l.qty
          ? `${l.qty} <span class="muted fine-xs">× ${esc(fmtCur(Number(l.unit.amount),
            curSym(l.currency)))}</span>`
          : esc(l.amount ? fmtCur(Number(l.amount.amount), curSym(l.currency)) : "—")),
      },
      { key: "total", label: "Разом", num: true, cell: (l) => fmtUAH(l.total_uah) },
      { key: "real", label: "Реальних", num: true, cell: (l) => pct(l.real_pct) },
      {
        key: "add", label: "",
        cell: (l) => (l.addable
          ? `<button type="button" class="sm" data-topupadd="${l.id}"
              title="Додати цей рядок у план купівель">+</button>`
          : `<a class="lnk fine-xs" href="${routeFor(WHERE[l.kind] || "now/buys")}"
              >зробити вручну</a>`),
      },
    ],
    rows,
    caption: "Чим добрати решту місяця: інструмент, кількість, сума, дохідність",
  });
}

// Хвіст: що не склалось у цілі квитки й чому. Формулювання — бекендові:
// «бракує 730 ₴» і «інструментів немає взагалі» вимагають різних дій, і
// розрізняє їх той, хто рахував.
function restHTML(t) {
  const parts = [];
  if (t.rest_uah > 0) {
    parts.push(`<div class="sub">Лишається <b>${fmtUAH(t.rest_uah)}</b>${
      t.rest_why ? ` — ${esc(t.rest_why)}` : ""}. Ці гроші не зникли:
      вони чекають на наступне надходження.</div>`);
  }
  if (t.note) parts.push(`<div class="note">${esc(t.note)}</div>`);
  return parts.join("");
}

/** Картка «Чим добрати» для сторінки «План купівель». res — відповідь
 *  /api/whatif.
 *
 *  Порожньо, коли добирати нема чого: плану доходу немає, місяць закритий
 *  або весь залишок уже розписаний. Рамка з написом «нічого» вчила б не
 *  читати картку — те саме правило, за яким мовчить картка наслідків. */
export function topupHTML(ctx, res) {
  const t = (res || {}).topup;
  if (!t) return "";
  const addable = (t.lines || []).filter((l) => l.addable).length;
  // «Додати все» — лише коли рядків справді кілька. На одному рядку кнопка
  // дублювала б «+» поруч із собою, і вибір між двома однаковими діями
  // доводилось би робити щоразу.
  const all = addable > 1
    ? `<div class="sub-xs"><button type="button" class="sm" data-topupall="1"
        >Додати все в план</button> — усі ${addable} рядки одним рухом; наслідки
        перерахуються.</div>`
    : "";
  return `<div class="card">
    <h2 class="h-row">Чим добрати ${infoBtn("allocate")}</h2>
    <div class="note">Скільки з решти місяця куди, щоб частки вирівнялись. Порядок
      той самий, що в самому обчисленні: подушка → борг → цілі → види, і бюджет
      виду — це «скільки йому бракує до його частки» (та сама колонка «на
      вирівнювання» в <a class="lnk" href="${routeFor("now/buy")}">Що купити</a>).
      Частки міряються від портфеля, ЯКИМ ВІН СТАНЕ з рядками плану, — тож те, що
      вже заплановано, вдруге не пропонується. Ціна кроку тут «номінал + НКД», у
      брокера може бути інша.</div>
    ${headHTML(res)}
    ${cutsHTML(t)}
    ${linesHTML(t)}
    ${restHTML(t)}
    ${all}
  </div>`;
}

/** Проводка картки: «+» на рядку й «Додати все».
 *
 *  res передається аргументом, а не читається з розмітки: тіло запиту
 *  збирає buyBody із самого рядка відповіді, і другий його екземпляр,
 *  відновлений із HTML, розійшовся б із першим на першому ж новому полі. */
export function wireTopup(ctx, main, res) {
  const lines = ((res || {}).topup || {}).lines || [];
  main.querySelectorAll("[data-topupadd]").forEach((b) =>
    b.addEventListener("click", () => {
      const l = lines[Number(b.dataset.topupadd)];
      if (l) addPlanLine(ctx, l);
    }));
  main.querySelectorAll("[data-topupall]").forEach((b) =>
    b.addEventListener("click", () => addAll(ctx, lines.filter((l) => l.addable))));
}

// Один рядок — через apply(), тобто з тостом, скиданням кеша й
// перемальовуванням. Те саме, що робить addToPlan із «Що купити»; окремою
// функцією тут тому, що тіло збирає buyBody (рядок розкладки), а не форма.
function addPlanLine(ctx, l) {
  return apply(ctx, { path: "plan/buys", body: buyBody(l) }, "Додано в план купівель");
}

// Кілька рядків — ПОСЛІДОВНО, а не Promise.all.
//
// Не заради ввічливості до сервера: кожен запис міняє стан, від якого
// залежить наступний (той самий брокер, та сама готівка), і паралельні
// записи дали б порядок, різний від запуску до запуску. Зупиняємось на
// першій же невдачі — доливати решту в план, коли один рядок не ліг,
// означало б лишити план у стані, якого людина не бачила.
async function addAll(ctx, lines) {
  for (const l of lines) {
    if (!await addPlanLine(ctx, l)) return;
  }
}
