// Розкладка надходження: прийшли гроші — ось кроки, і ось кнопка.
//
// Питання, на яке відповідає ця модалка, ставиться щомісяця й досі
// вирішувалось головою: «зайшло 32 000 — куди їх». Усі складники відповіді
// в застосунку вже були (рейтинг «Що купити», поділ «Куди йдуть гроші
// місяця», кошик «Що заплановано»), і бракувало рівно одного — місця, де
// вони зводяться до однієї суми з ЦІЛИМИ квитками.
//
// ЖОДНОГО ЧИСЛА ТУТ НЕ РАХУЄТЬСЯ: усе приходить із POST /api/allocate. Той
// самий припис, що в buy-plan.js, і з тієї ж причини — «скільки паперів
// влізе в 32 000» у браузері було б другою ціною того самого паперу на
// сусідніх екранах.
//
// ПРЕВʼЮ НЕ РЕДАГУЄТЬСЯ, і це не економія. Правити рядки треба в самому
// кошику, де КРУД повний (plan-buys.js): поля кількості тут були б другим
// набором тих самих полів, а два набори розходяться — саме про це шапка
// fields.js. Тому вибір рівно один і бінарний: класти це в план чи ні.

import { esc, uah0 as fmtUAH, cur2 as fmtCur, curSym, pct } from "../format.js";
import { check as checkField, selectOf } from "../fields.js";
import { openEdit } from "../forms.js";
import { opsGrid } from "../grid.js";
import { kindPill } from "../components.js";
import { routeFor } from "../routes.js";

// Чиї це гроші. Від відповіді залежить, чи візьме з них своє подушка —
// налаштування «Подушку наповнювати» («Політика → Резерв») може лишити їй
// лише плановий дохід.
//
// ПОЛЕ ВИДИМЕ, ХОЧ ЖОДНЕ З ТРЬОХ МІСЦЬ, ЗВІДКИ РОЗКЛАДКУ ВІДКРИВАЮТЬ, НЕ
// ПИТАЄ ПРО НЬОГО ЛЮДИНУ: залишок місяця й відмітка надходження — це план,
// кнопка «Прийшло» на маршруті — портфель. Але саме тому, що джерело тепер
// щось вирішує, воно мусить бути видним і виправним: підставлене значення,
// про яке не можна дізнатись, — це рівно та тиша, від якої рядок вирізки
// зникав би без причини.
const SOURCES = [
  ["plan", "планове надходження"],
  ["portfolio", "виплата з портфеля"],
];

// Куди веде рядок, який у кошик не кладеться. Вклад — єдиний такий вид:
// порада про нього це ПОПОВНЕННЯ наявного, а рядок плану купівель описує
// НОВИЙ вклад і вимагає строку, якого в пораді немає (див. allocLine в
// handlers_allocate.go).
const WHERE = { deposit: "instr/deposits" };

function linesHTML(res) {
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
              ? " ≈" + esc(fmtCur(l.convert_native, curSym(res.amount.currency))) : ""}</div>`
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
        key: "where", label: "",
        cell: (l) => (l.addable
          ? `<span class="muted fine-xs">у план</span>`
          : `<a class="lnk fine-xs" href="${routeFor(WHERE[l.kind] || "now/buys")}"
              >зробити вручну</a>`),
      },
    ],
    rows: (res.lines || []).map((l, i) => ({ ...l, id: i })),
    caption: "Розкладка надходження: інструмент, кількість, сума, дохідність",
  });
}

// Рядок подушки — ПРАПОРЕЦЬ, а не текст, і від нього залежить правильність
// наступної розкладки, а не лише цієї.
//
// reserve.fill_now_uah спадає тільки після ЗАПИСУ руху в резерв. Без
// прапорця дві відмітки за місяць запропонували б відкласти ту саму суму
// двічі, і людина відклала б удвічі більше, ніж сама собі поставила стелею.
// Позначений — рух іде в ту саму пачку запитів, і друга відмітка бачить уже
// правильний хвіст.
function reserveHTML(res) {
  const r = res.reserve;
  if (!r || !(r.amount_uah > 0)) return "";
  return `<div class="mb-sm">
    ${checkField("reserve", `Спершу в резерв — ${fmtUAH(r.amount_uah)}`, { checked: true })}
    <div class="sub-xs">${esc(r.why)}. Знявши галочку, ти лишиш ці гроші на папери —
      але тоді наступна відмітка місяця запропонує ту саму суму ще раз.</div>
  </div>`;
}

// Чому подушка НЕ взяла своєї частки. Причину називає бекенд — тут лише
// місце для неї, і воно поруч із тим рядком, якого через цю причину немає.
function reserveSkipHTML(res) {
  return res.reserve_skip_why
    ? `<div class="sub-xs t-warn mb-sm">${esc(res.reserve_skip_why)}</div>`
    : "";
}

// Борг — між подушкою й цілями, рядком без прапорця.
//
// Без прапорця не з лінощів: вирізка несе суму й причину, але не борг, у
// який її класти (allocDebtCut), а платіж у борг записують зі звіркою на
// сторінці боргу, де відомо, з якої картки й на яку розстрочку. Тут лише
// сказати, що розкладка ці гроші відклала — доти рядок мовчав узагалі, і
// підсумок «в інструменти» виходив меншим за суму без пояснення.
function debtHTML(res) {
  const d = res.debt;
  if (!d || !(d.amount_uah > 0)) return "";
  return `<div class="mb-sm">${kindPill("debt")} <b>${fmtUAH(d.amount_uah)}</b>
    <div class="sub-xs">${esc(d.why)}. Платіж запиши на сторінці боргу — сюди він не пишеться.</div>
  </div>`;
}

function debtSkipHTML(res) {
  return res.debt_skip_why
    ? `<div class="sub-xs t-warn mb-sm">${esc(res.debt_skip_why)}</div>`
    : "";
}

// Цілі накопичення — ТРЕТІМИ, за подушкою й боргом, і теж прапорцями.
//
// Порядок не оформлення: у самій розкладці цілі ріжуть після подушки й лише
// з того, що після неї лишилось (handlers_allocate.go), і той самий порядок
// стоїть на сторінці маршруту. Третій порядок на третьому екрані читався б
// як третє правило.
//
// ПРАПОРЕЦЬ НА КОЖНУ ОКРЕМО, а не один на всі: цілі незалежні одна від
// одної — «в авто цього місяця так, у ремонт ні» це звичайне рішення, — і
// спільна галочка змусила б вибирати між усіма й жодною.
//
// Довід, чому взагалі прапорець, той самий, що в подушки: goal.fill_now_uah
// спадає тільки після ЗАПИСУ руху, тож без нього дві відмітки за місяць
// запропонували б відкласти ту саму суму двічі.
//
// Доти цього рядка тут не було зовсім, і ціна була не косметична: підсумок
// нижче складався без вирізки цілей, тобто числа на екрані не сходились
// рівно на неї.
function goalsHTML(res) {
  const cuts = (res.goals || []).filter((g) => g.amount_uah > 0);
  if (!cuts.length) return "";
  return cuts.map((g) => `<div class="mb-sm">
    ${checkField(`goal_${g.id}`, `У ціль «${g.name}» — ${fmtUAH(g.amount_uah)}`,
    { checked: true })}
    <div class="sub-xs">${esc(g.why)}. Знявши галочку, ти лишиш ці гроші на папери —
      але тоді наступна відмітка місяця запропонує ту саму суму ще раз.</div>
  </div>`).join("");
}

// Чому цілі НЕ взяли своєї частки — те саме місце й той самий довід, що в
// подушки. Причин у цього поля три (політика, дозвіл надходження, поріг
// призначення), і жодну з них тут не переказують: усі три формулює бекенд.
function goalsSkipHTML(res) {
  return res.goals_skip_why
    ? `<div class="sub-xs t-warn mb-sm">${esc(res.goals_skip_why)}</div>`
    : "";
}

function summaryHTML(res) {
  const parts = [];
  if (res.reserve && res.reserve.amount_uah > 0) {
    parts.push(`в резерв ${fmtUAH(res.reserve.amount_uah)}`);
  }
  // Цілі одним числом, а не по одній: підсумок відповідає на питання
  // «куди пішли гроші», і поіменний перелік у ньому повторив би рядки
  // вище. Скільки саме взяла кожна — видно там, де стоїть її галочка.
  if (res.debt_uah > 0) parts.push(`у борг ${fmtUAH(res.debt_uah)}`);
  if (res.goals_uah > 0) parts.push(`у цілі ${fmtUAH(res.goals_uah)}`);
  const spent = (res.lines || []).reduce((a, l) => a + (l.total_uah || 0), 0);
  if (spent > 0) parts.push(`в інструменти ${fmtUAH(spent)}`);
  if (res.rest_uah > 0) parts.push(`лишається ${fmtUAH(res.rest_uah)}`);
  return `<div class="sub">З ${fmtUAH(res.amount_uah)}: ${parts.join(" · ") || "нічого"}.
    ${res.rest_why ? esc(res.rest_why[0].toUpperCase() + res.rest_why.slice(1)) + "." : ""}</div>`;
}

// Тіло рядка плану купівель. Поля, яких цей вид не має, не надсилаються
// зовсім — бекенд відхиляє зайве (planBuyFromReq), і мовчки підкладати нулі
// означало б обходити його перевірку. Та сама межа, що в planBuyBody.
//
// Експортується, бо читачів двоє: ця модалка й «Закріпити» на сторінці
// маршруту (route.js). Однойменного planBuyBody з plan-buys.js тут не
// досить — той бере ФОРМУ, а це рядок розкладки, і поля в них різні.
export function buyBody(l) {
  if (l.kind === "npf") return { kind: "npf", ref: l.ref, amount: l.amount.amount };
  return { kind: l.kind, ref: l.ref, qty: l.qty };
}

// Рядок про те, що маршрут ВІВ ЦІ ГРОШІ кудись — і чи збіглось.
//
// Маршрут будується наперед, розкладка рахується щойно; між ними могла
// минути доба, змінитись стеля місяця або зʼявитись новий папір у
// довіднику. Показати обидва числа й назвати різницю — усе, що тут
// доречно. Кольору й слова-вироку немає навмисно: те саме правило, за яким
// підсумок місяця показує «було → стало» БЕЗ оцінок. Розбіжність — факт, а
// не помилка, і чия вона, вирішує людина.
function routedHTML(leg) {
  if (!leg) return "";
  const parts = [];
  if (leg.reserve && leg.reserve.amount_uah > 0) {
    parts.push(`резерв ${fmtUAH(leg.reserve.amount_uah)}`);
  }
  if (leg.debt && leg.debt.amount_uah > 0) parts.push(`борг ${fmtUAH(leg.debt.amount_uah)}`);
  for (const g of leg.goals || []) parts.push(`${g.name} ${fmtUAH(g.amount_uah)}`);
  for (const l of leg.lines || []) parts.push(`${l.label} ${fmtUAH(l.total_uah)}`);
  if (leg.rest_uah > 0) parts.push(`чекає ${fmtUAH(leg.rest_uah)}`);
  return `<div class="sub">Маршрут вів: ${parts.join(" · ") || "нікуди — на цілий крок не набиралось"}.
    Нижче — розкладка, порахована щойно.</div>`;
}

/** Модалка розкладки. amount/currency — сума, яку розкладаємо (валова сума
 *  відмітки); title — чиї це гроші, щоб у діалозі було видно, звідки він
 *  узявся.
 *
 *  routed — нога маршруту, якщо модалку відкрито з неї.
 *
 *  source — чиї це гроші (див. SOURCES); principal — скільки з суми є
 *  поверненням власного тіла, у ТІЙ САМІЙ валюті. Обидва потрібні бекенду,
 *  щоб вирішити, чи має подушка право на ці гроші.
 *
 *  sourceRef — ЯКЕ САМЕ це надходження ("flow:<id>" або "receipt:<id>").
 *  Третє питання поруч із двома попередніми, і воно окреме: source каже
 *  про рівень політики («планові гроші чи портфельні»), а sourceRef — про
 *  дозвіл самого джерела («цю оренду в подушку не клади»). Порожньо =
 *  обмежень немає, тобто ручна сума, набрана руками.
 *
 *  Полем воно тут НЕ стає, на відміну від «чиї це гроші»: посилання не
 *  вибирають — його ставить рядок, з якого модалку відкрили, і випадайка
 *  «яке надходження» посеред розкладки цього надходження питала б про вже
 *  відоме.
 *
 *  pick — ISIN, який людина обрала для цієї ноги на сторінці маршруту
 *  (route.js). Теж не поле, і з того самого доводу: вибір зроблено там, де
 *  видно всю ногу, а тут він лише мусить дати ту саму відповідь.
 *
 *  → Promise<boolean>: чи щось записалось. */
export async function openAllocate(ctx, opts) {
  const {
    amount, currency = "UAH", title = "", routed = null,
    source = "plan", principal = 0, sourceRef = "", pick = "",
  } = opts;
  let res;
  try {
    const resp = await ctx.store.raw("allocate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        amount: String(amount), currency, source,
        principal: principal ? String(principal) : "",
        source_ref: sourceRef || "",
        pick_isin: pick || "",
      }),
    });
    if (!resp.ok) throw new Error(`${resp.status}: ${(await resp.text()).slice(0, 200)}`);
    res = await resp.json();
  } catch (err) {
    ctx.toast(err.message || String(err), false);
    return false;
  }

  const addable = (res.lines || []).filter((l) => l.addable);
  const goalCuts = (res.goals || []).filter((g) => g.amount_uah > 0);
  const nothing = !addable.length && !goalCuts.length
    && !(res.reserve && res.reserve.amount_uah > 0);
  const fields = routedHTML(routed)
    + selectOf("source", "Чиї це гроші", SOURCES, source)
    + reserveHTML(res)
    + reserveSkipHTML(res)
    + debtHTML(res)
    + debtSkipHTML(res)
    + goalsHTML(res)
    + goalsSkipHTML(res)
    + linesHTML(res)
    + summaryHTML(res)
    + (res.note ? `<div class="note">${esc(res.note)}</div>` : "")
    + `<div class="sub-xs">Поділ між видами — той самий, що в картці «Куди йдуть гроші
       місяця»: скільки кожному виду бракує до його цілі. Порядок усередині виду —
       твій, із налаштування «Порядок у ‹Що купити›». Ціна кроку тут «номінал + НКД»,
       у брокера може бути інша.</div>`;

  // Зміна джерела ПЕРЕПИТУЄ розкладку, а не править її в браузері: з іншим
  // джерелом інші і вирізка подушки, і бюджети видів, і залишок, а рахує їх
  // бекенд — той самий, що будує маршрут. Порахувати різницю тут означало б
  // завести другу арифметику рівно там, де шапка файлу її забороняє.
  let again = "";
  const wire = (form) => {
    const sel = form.elements.source;
    const cancel = form.querySelector("[data-editcancel]");
    if (!sel || !cancel) return;
    sel.addEventListener("change", () => {
      if (sel.value === source) return;
      again = sel.value;
      // Через «Скасувати», а НЕ через dialog.close(). Кнопка кличе
      // завершення openEdit напряму й одразу; close() лише просить подію
      // close, а вона доходить не в кожному рушії — у панелі перегляду не
      // доходить узагалі, і модалка лишалась зачиненою назавжди, не
      // перепитавши нічого. Шлях, який працює всюди, вартий одного рядка.
      cancel.click();
    });
  };

  const done = await openEdit(ctx, {
    title: title ? `Розкласти: ${title}` : "Розкласти надходження",
    fields,
    wire,
    // Кнопка називає, що саме станеться. «Зберегти» тут не годиться: рухів
    // може бути два види, і зробити їх вона може обидва.
    submit: nothing ? "Закрити" : "Записати",
  }, (f) => {
    const requests = [];
    if (f.reserve && f.reserve.checked && res.reserve) {
      // Резерв ПЕРШИМ: applyAll спиняється на першій помилці, і з двох
      // половинчастих результатів ця чесніша — у «Резерві» лишається
      // підписаний рух, який видно й можна зняти однією кнопкою. Той самий
      // порядок, що в onSubmitFunded (forms.js).
      requests.push({
        path: "reserve",
        body: {
          amount: res.reserve.amount_uah.toFixed(2),
          currency: "UAH",
          note: "розкладка надходження" + (title ? ": " + title : ""),
        },
      });
    }
    // Цілі — ДРУГИМИ, тим самим порядком, що й у розкладці: applyAll
    // спиняється на першій помилці, і з половинчастих результатів чесніші
    // ті, де записане видно поіменно й знімається однією кнопкою.
    goalCuts.forEach((g) => {
      const box = f.elements[`goal_${g.id}`];
      if (!box || !box.checked) return;
      requests.push({
        path: "goal-ops",
        body: {
          goal_id: String(g.id),
          amount: g.amount_uah.toFixed(2),
          currency: "UAH",
          note: "розкладка надходження" + (title ? ": " + title : ""),
        },
      });
    });
    addable.forEach((l) => requests.push({ path: "plan/buys", body: buyBody(l) }));
    if (!requests.length) return null;
    return { requests, msg: "Записано: резерв, цілі й план купівель" };
  });

  // Джерело змінили — та сама модалка, порахована заново. Тіло при цьому
  // їде далі незмінним: скільки з суми є поверненням власного, від того,
  // чиїми ці гроші назвати, не залежить.
  if (again) return openAllocate(ctx, { ...opts, source: again });
  return done;
}
