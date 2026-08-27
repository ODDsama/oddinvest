// Пенсійні рахунки: деталі рядка, форми внеску й ЧВОПА.
//
// Модуль окремий, а не всередині views/positions.js, з тієї ж причини, що
// й fund-ops.js: сама таблиця позицій тримає ЛИШЕ спільні колонки, а все
// специфічне для інструмента живе поруч зі своїм інструментом.
//
// Що НПФ показує такого, чого не показує жоден інший інструмент:
//
//   * пару «обіцяли / фактично». Обіцянка стоїть у довіднику вічно й
//     ніколи сама не перевіряється — а тут поруч із нею зʼявляється
//     виміряне зростання ЧВОПА, і різницю видно з одного погляду;
//   * дві РІЗНІ фактичні дохідності. Зростання ЧВОПА — «як спрацював
//     фонд», XIRR — «скільки заробили мої гроші» з урахуванням дат
//     внесків. При нерівномірних внесках вони розходяться, і зводити їх в
//     одне число означало б відповідати на друге питання числом від
//     першого;
//   * оцінку податкової знижки, яка НІКУДИ не входить. Поруч із нею
//     мусить стояти власна арифметика й підпис про це — інакше вона
//     читалась би як гроші, що вже враховані.

import { esc, curSym, pct, uah2 as fmtUAH, money as fmtMoney } from "./format.js";
import { apply, onSubmit } from "./forms.js";
import {
  money as moneyField, date as dateField, note as noteField, textarea, formHTML,
} from "./fields.js";
import { refSelect, refValue, wireRefs } from "./refs.js";
import { wireCrud } from "./crud.js";
import { opsGrid, actionsCol } from "./grid.js";
import { dateCurve } from "./charts.js";
import { parseDatedNumbers } from "./paste.js";

// Довідник, журнал і точки ЧВОПА на час одного рендеру. Заповнює той, хто
// першим прийшов; читають і «Портфель», і «Гроші».
let accounts = [];
let ops = [];
let navPoints = [];

export function setNPF(data) {
  accounts = (data || {}).accounts || [];
  ops = (data || {}).ops || [];
  navPoints = (data || {}).nav || [];
}

export const npfAccounts = () => accounts;

/** Призначення для потоків плану: [[ключ, підпис], …].
 *
 *  Ключ — «npf:<id>», а НЕ «npf:<назва>»: план тримається за id, щоб
 *  виправлення описки в назві рахунку не відчіпляло внески (див.
 *  domain.NPFPlanDest). Підпис, навпаки, — назва: на екрані має стояти те,
 *  що людина вводила. */
export function npfDestOptions(list) {
  // Список приходить ПАРАМЕТРОМ, а не з кешу модуля: кеш заповнює «Портфель»
  // при рендері, а форму потоку малює «План» — інша вкладка, куди можна
  // зайти першою. Схована залежність від порядку вкладок дала б порожню
  // випадайку рівно тоді, коли внесок і заводять.
  return (list || accounts).map((a) => [`npf:${a.id}`, `внесок у ${a.name}`]);
}

/** Рахунок за назвою: рядок документа несе назву, а форми — id. */
function accByName(name) {
  return accounts.find((a) => a.name === name) || null;
}

/** Усі відомі точки ЧВОПА рахунку: заведені руками разом із виведеними з
 *  внесків (сума ÷ одиниці), по одній на дату.
 *
 *  Дзеркалить domain.NPFNavPoints, і ручна точка так само важить більше за
 *  виведену: вона точна, а виведена несе округлення суми внеску. Друга
 *  реалізація тут неминуча — сервер віддає обидва списки сирими, бо крива
 *  малюється лише в UI, — тож правило записане в обох місцях однаково. */
function navSeries(npfID) {
  const byDate = new Map();
  ops.filter((o) => o.npf_id === npfID && o.nav > 0)
    .forEach((o) => byDate.set(o.date, o.nav));
  navPoints.filter((p) => p.npf_id === npfID && p.nav > 0)
    .forEach((p) => byDate.set(p.date, p.nav));
  return [...byDate.entries()].sort((a, b) => a[0] < b[0] ? -1 : 1);
}

/** Крива ЧВОПА. Менше двох точок — не крива, і малювати з однієї означало
 *  б показати горизонтальну лінію там, де даних немає.
 *
 *  Полотно, проріджування підписів і рядок діапазону робить dateCurve —
 *  спільне з кривою ціни фонду, де все це стояло другою однаковою копією.
 *  Разом із нею пішов і рядок-обхід «діапазон текстом, бо svgLine центрує
 *  підписи»: крайні мітки більше не обрізаються (xAnchor), а сам рядок
 *  лишився вже як підпис — він називає рік, якого на осі свідомо немає.
 *
 *  Зміни у відсотках у підписі тут НЕМАЄ, на відміну від кривої фонду, і
 *  це не забутість. Число мусить прийти з бекенда (CLAUDE.md §5), а
 *  дзеркального поля до fund price_change_pct для ЧВОПА ще не заведено;
 *  рахувати його в браузері лише тому, що тут його видно, означало б
 *  завести ту саму другу копію арифметики, від якої відмовились поруч. */
function navChartHTML(npfID) {
  return dateCurve(navSeries(npfID), {
    color: "var(--oi-series-npf)",
    fmt: (v) => v.toFixed(6),
    label: "ЧВОПА",
    empty: `Крива ЧВОПА зʼявиться з двох точок. Кожен внесок приносить
      одну з собою; історію фонду до першого внеску можна вклеїти нижче.`,
  });
}

/** Пара «обіцяли / фактично» — головне, що ця картка показує.
 *
 *  Обидва числа поруч навіть тоді, коли виміряне вже витіснило обіцянку в
 *  real_pct: інакше звірка припущення була б неможлива саме тоді, коли вона
 *  нарешті стала можливою. */
function promiseVsFactHTML(row) {
  const promise = row.expected_pct
    ? `${pct(row.expected_pct)}`
    : `<span class="muted">не задано</span>`;
  if (!row.nav_return_pct) {
    return `<div class="sub">Обіцяно фондом: <b>${promise}</b>.
      Виміряти ще нема по чому — потрібні дві точки ЧВОПА щонайменше за пів року.
      Доти в дохідності стоїть саме обіцянка, і <code>yield_basis</code> це називає.</div>`;
  }
  const diff = row.expected_pct ? row.nav_return_pct - row.expected_pct : 0;
  const tone = diff >= 0 ? "t-ok" : "t-danger";
  const gap = row.expected_pct
    ? ` <span class="${tone}">(${diff >= 0 ? "+" : ""}${pct(diff)})</span>` : "";
  return `<div class="sub">Обіцяно фондом: <b>${promise}</b> ·
    зростання ЧВОПА фактично: <b>${pct(row.nav_return_pct)}</b>${gap}</div>
    <div class="sub-xs muted">Зростання ЧВОПА — це «як спрацював фонд», незалежно від дат моїх
      внесків. «Скільки заробили мої гроші» — інше число, воно в XIRR портфеля, і при
      нерівномірних внесках вони розходяться.</div>`;
}

/** Оцінка податкової знижки з видимою арифметикою.
 *
 *  Нуль означає «не порахувати», і причин рівно дві: не введений річний
 *  ПДФО (він і є перемикачем) або не задана ставка знижки на рахунку.
 *  Сказати це вголос обовʼязково — інакше нуль читається як «знижки немає». */
function creditHTML(ctx, row) {
  const set = (ctx.summary || {}).settings || {};
  const pdfo = set.npf_credit_pdfo_year_uah;
  if (!pdfo) {
    return `<div class="sub muted">Податкова знижка не рахується: не введено утриманий за рік
      ПДФО. Він і є перемикачем — і водночас стелею, бо держава повертає сплачене, а не дарує.
      Поле в «Налаштуваннях». Якщо офіційної зарплати немає (наприклад, дохід ФОПа), знижка
      не працює взагалі — і тоді нуль тут правильна відповідь, а не незаповнена форма.</div>`;
  }
  const cap = set.npf_credit_cap_month_uah || 0;
  const est = row.credit_est_uah || 0;
  // Кнопка з'являється лише коли є що додавати: план із нульовим рядком
  // «знижка» був би обіцянкою держави, якої вона не давала.
  const toPlan = est > 0
    ? `<div class="form-actions"><button class="sm" data-npfcredit="${esc(row.name)}"
         data-amount="${est.toFixed(2)}">Додати знижку в план</button></div>
       <div class="sub-xs muted">Створить річний рядок доходу в «Плані» — з нього знижка почне
         впливати на проєкцію. Дата за замовчуванням — травень наступного року (подання весною,
         гроші за ~60 днів); її можна поправити в самому плані.</div>`
    : "";
  return `<div class="sub">Оцінка податкової знижки за рік: <b>${fmtUAH(est)}</b></div>
    <div class="sub-xs muted">Внески за рік у межах ліміту ${cap ? fmtUAH(cap) + "/міс" : "(ліміт не задано)"}
      × ставка знижки, обмежено утриманим ПДФО ${fmtUAH(pdfo)}. Ліміт щороку інший — він виводиться
      з прожиткового мінімуму працездатних на 1 січня × 1,4.</div>
    <div class="sub-xs t-warn">Сама по собі не входить ні в капітал, ні в календар, ні в проєкцію,
      ні в загальні суми податкового звіту. Щоб знижку отримати, треба подати декларацію до
      31 грудня наступного року.</div>
    ${toPlan}`;
}

/** Журнал внесків рахунку. ЧВОПА в таблиці — ВИВЕДЕНА (сума ÷ одиниці), а
 *  не окреме поле: воно дало б їм розійтись. */
function opsTableHTML(npfID, currency) {
  const rows = ops.filter((o) => o.npf_id === npfID)
    .sort((a, b) => (a.date < b.date ? 1 : a.date > b.date ? -1 : b.id - a.id));
  return opsGrid({
    cols: [
      { key: "date", label: "Дата", cell: (o) => esc(o.date) },
      { key: "amount", label: "Сума", num: true, cell: (o) => fmtMoney(o.amount) },
      { key: "units", label: "Одиниць", num: true, cell: (o) => (o.units || 0).toFixed(6) },
      { key: "nav", label: "ЧВОПА", num: true, prio: 3,
        cell: (o) => (o.nav || 0).toFixed(6) + " " + curSym(currency) },
      { key: "broker", label: "З рахунку", cell: (o) => esc(o.broker || "") },
      { key: "note", label: "Нотатка", cls: "muted", prio: 3, cell: (o) => esc(o.note || "") },
      actionsCol("npf:" + npfID, { label: (o) => "внесок від " + o.date }),
    ],
    rows,
    caption: "Внески: дата, сума, одиниці, ЧВОПА, рахунок",
    empty: "Внесків ще немає.",
  });
}

/** Поля внеску — один список і для запису, і для правки.
 *
 *  «З рахунку» тут ГОЛОВНЕ виправлення. Доти це поле було текстовим
 *  <input> з атрибутом list, який указував на <datalist> із брокерами, — а
 *  того datalist у застосунку не існувало взагалі. Тобто підказка мовчала,
 *  і поле поводилось як звичайний текстовий рядок. У формі купівлі ОВДП той
 *  самий брокер вибирався з довідника; тут його вписували руками, і
 *  «mono » з пробілом заводив другий рахунок, після чого зведення по
 *  брокерах розходилось без жодного натяку, чому.
 *
 *  Правки внеску теж не було, хоча PUT /api/npf/{id} існував. */
export const npfOpFields = (ctx, row = null) => [
  dateField("date", "Дата", row ? { value: row.date } : {}),
  moneyField("amount", "Сума", { required: true, value: row ? row.amount.amount : "" }),
  moneyField("units", "Зараховано одиниць", {
    required: true, value: row ? (row.units || 0).toFixed(6) : "",
    title: "З виписки фонду. ЧВОПА виводиться сама: сума ÷ одиниці",
  }),
  refSelect(ctx, { name: "broker", ref: "broker", label: "З рахунку", value: row ? row.broker || "" : "" }),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

export const npfOpBody = (npfID) => (f) => ({
  npf_id: npfID,
  date: f.date.value,
  amount: f.amount.value.trim(),
  units: f.units.value.trim(),
  broker: refValue(f, "broker"),
  note: f.note.value.trim(),
});

/** Поля однієї точки ЧВОПА. Рахунок не поле: точка належить рахунку, у
 *  чиєму рядку вона й стоїть, а перенести її на інший рахунок — це не
 *  правка, а нова точка. */
export const navFields = (ctx, row = null, placeholder = "") => [
  dateField("date", "Дата", row ? { value: row.date } : {}),
  moneyField("nav", "ЧВОПА", {
    required: true, ph: placeholder,
    value: row ? (row.nav || 0).toFixed(6) : "",
  }),
];

export const navBody = (f) => ({ date: f.date.value, nav: f.nav.value.trim() });

/** Деталі рядка НПФ. row — рядок документа (state.NPFPositionRow). */
export function npfDetailHTML(ctx, row) {
  const acc = accByName(row.name);
  if (!acc) {
    return `<div class="sub t-warn">Рахунку немає в довіднику — деталі показати нема з чого.</div>`;
  }
  const due = row.contrib_due
    ? `<div class="sub t-warn">⚠ Цього місяця внеску ще немає (планово ${acc.contrib_day} числа).
       Нагадування гасне саме, щойно внесок зʼявиться в журналі.</div>` : "";
  return `${due}
    <div class="sub">ЧВОПА: <b>${(row.nav || 0).toFixed(6)} ${curSym(row.currency)}</b>
      ${row.nav_date ? `<span class="muted">на ${esc(row.nav_date)}</span>` : ""}</div>
    ${promiseVsFactHTML(row)}
    <div class="mt">${navChartHTML(acc.id)}</div>
    <div class="sub-xs muted">Між оновленнями ЧВОПА вартість заморожена, тож крива —
      схо́динками. Кожен внесок приносить свіжу ЧВОПА з собою; окремого джерела котирувань у НПФ
      немає.</div>

    <h4 class="mt">Оновити ЧВОПА з кабінету</h4>
    ${formHTML({
    fields: navFields(ctx, null, (row.nav || 0).toFixed(6)), submit: "Оновити",
    attrs: { "data-npfnav-form": acc.id },
  })}

    <h4 class="mt">Внести</h4>
    ${formHTML({
    fields: npfOpFields(ctx), submit: "Записати внесок",
    attrs: { "data-npfop-form": acc.id },
  })}

    <h4 class="mt">Внески</h4>
    ${opsTableHTML(acc.id, row.currency)}

    <h4 class="mt">${creditHTML(ctx, row)}</h4>

    <h4 class="mt">Вклеїти історію ЧВОПА</h4>
    <div class="sub-xs muted">Опублікована фондом таблиця — по рядку на день:
      <code>2020-01-01 1.500000</code>. Дата й число, розділені пробілом, комою або табуляцією.
      Саме вона й робить видимим track record, на якому стоїть обіцянка.</div>
    ${formHTML({
    fields: [textarea("points", "Рядки", { ph: `2020-01-01 1.5
2021-01-01 1.82` })],
    submit: "Вклеїти",
    attrs: { "data-npfnavbulk-form": acc.id },
  })}
    ${navBulkListHTML(acc.id, row.currency)}`;
}

/** Заведені руками точки — окремим списком, бо лише їх і можна видалити:
 *  виведені з внесків живуть у журналі внесків і зникають разом із ним. */
function navBulkListHTML(npfID, currency) {
  const pts = navPoints.filter((p) => p.npf_id === npfID)
    .sort((a, b) => a.date < b.date ? 1 : -1);
  if (!pts.length) return "";
  return opsGrid({
    cols: [
      { key: "date", label: "Дата", cls: "muted", cell: (p) => esc(p.date) },
      { key: "nav", label: "ЧВОПА", num: true,
        cell: (p) => (p.nav || 0).toFixed(6) + " " + curSym(currency) },
      actionsCol("npf-nav", { label: (p) => "точку ЧВОПА за " + p.date }),
    ],
    rows: pts,
    caption: "Вклеєні руками точки ЧВОПА: дата й значення",
    cls: "mt",
  });
}

// parseNavLines ЗВІДСИ ПОЇХАЛА, і цей абзац стоїть замість неї, щоб її не
// написали заново.
//
// Розбір «дата число» знадобився вдруге — для історії цін сертифікатів
// (fund-prices.js), — тож він переїхав у paste.js як parseDatedNumbers.
// Змінилось лише імʼя ключа: точка звідти приходить як {date, value}, і
// кожен викликач сам називає своє число (тут — nav, там — price). Саме
// через це виклик нижче мапить пари, а не передає їх далі як є.

// ШЛЯХИ ТУТ БЕЗ ПРЕФІКСА, як і скрізь у застосунку.
//
// Це не стиль, а виправлення 404: httpTransport будує адресу як
// `base + path` при base = "/api/", тож "/api/npf" давало "/api//api/npf" —
// маршруту з таким шляхом немає, і форма падала ще на кроці /check, тобто до
// будь-якого запису. Читання того самого модуля працювали, бо вони йдуть
// через ctx.soft("npf-accounts") і префікса не мали ніколи; розбіжність жила
// рівно в записах.
//
// Цей модуль був ЄДИНИМ у web/js із таким префіксом, і жодна з чотирьох
// перевірок джоби `ui` його не бачить: синтаксис цілий, імпорти резолвляться,
// імена визначені. Ловить це лише спроба записати.
export function wireNPF(ctx, main) {
  // Внески — вкладені в рахунок лише на екрані, а не в API: шлях у них
  // плоский (/api/npf/{id}). Але форм і таблиць на сторінці стільки ж,
  // скільки рахунків, тож ім'я в розмітці несе номер рахунку — інакше
  // проводка першого підв'язала б кнопки всіх.
  accounts.forEach((acc) => {
    wireCrud(ctx, main, {
      resource: "npf:" + acc.id, path: (id) => "npf/" + id, createPath: "npf",
      form: `[data-npfop-form="${acc.id}"]`,
      title: "Внесок", rows: ops.filter((o) => o.npf_id === acc.id),
      fields: npfOpFields, body: npfOpBody(acc.id),
      // Внесок може не вміститись у баланс рахунку, тож форма та сама, що
      // в покупки паперу: спершу /check, і якщо бракує — пропозиція
      // поповнити рівно на нестачу.
      funded: (f) => ({ check: "npf/check", date: f.date.value, what: "внесок у НПФ" }),
      msg: { add: "Внесок записано", edit: "Внесок виправлено", del: "Внесок видалено" },
    });
  });

  // Точки ЧВОПА, вклеєні руками. Ресурс один на всі рахунки: точка знає
  // свій рахунок сама, а список під кожним рахунком уже відфільтрований.
  wireCrud(ctx, main, {
    resource: "npf-nav", title: "Точку ЧВОПА", rows: navPoints,
    fields: (c, row) => navFields(c, row),
    body: navBody,
    msg: { edit: "ЧВОПА виправлено", del: "Точку видалено" },
  });

  // «Оновити ЧВОПА з кабінету» — не створення точки, а перезапис ОСТАННЬОЇ
  // відомої вартості рахунку (PUT .../nav), тож і лишається окремою
  // формою: це властивість рахунку, а не рядок історії.
  main.querySelectorAll("[data-npfnav-form]").forEach((form) => {
    const id = Number(form.dataset.npfnavForm);
    onSubmit(ctx, form, (f) => ({
      method: "PUT", path: `npf-accounts/${id}/nav`,
      body: navBody(f), msg: "ЧВОПА оновлено",
    }));
  });

  main.querySelectorAll("[data-npfnavbulk-form]").forEach((form) => {
    const id = Number(form.dataset.npfnavbulkForm);
    onSubmit(ctx, form, (f) => {
      const { points, bad } = parseDatedNumbers(new FormData(f).get("points"));
      // Жодного рядка не приймаємо, якщо є зіпсовані: половина вклеєної
      // історії гірша за жодну — на ній порахувалась би дохідність за
      // відрізок, якого ніхто не вибирав.
      if (bad.length) {
        ctx.toast(`Не розібрав рядки: ${bad.join(", ")}. Очікую «РРРР-ММ-ДД число».`, false);
        return null;
      }
      if (!points.length) {
        ctx.toast("Нема чого вклеювати.", false);
        return null;
      }
      return {
        path: "npf-nav",
        body: { npf_id: id, points: points.map((p) => ({ date: p.date, nav: p.value })) },
        msg: `Вклеєно точок: ${points.length}`,
      };
    });
  });

  wireRefs(main);

  // Знижка в план — окремою ДІЄЮ людини, а не автоматично.
  //
  // Трьох речей застосунок знати не може: чи подано декларацію, чи вистачило
  // утриманого ПДФО і який ліміт цього року. Тому оцінка сама нікуди не
  // входить, а рядок плану створюється лише тоді, коли власник вирішив, що
  // знижку він таки отримає, — і далі його видно, можна правити й видалити,
  // як усе інше в плані.
  main.querySelectorAll("[data-npfcredit]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const name = btn.dataset.npfcredit;
      const amount = btn.dataset.amount;
      // Травень наступного року: подання весною, а гроші приходять
      // приблизно за 60 днів після подання.
      const from = `${new Date().getFullYear() + 1}-05-01`;
      await apply(ctx, {
        path: "plan/flows",
        body: {
          name: `Податкова знижка (${name})`, kind: "income", amount,
          currency: "UAH", cadence: "year", from_date: from,
          growth_pct: "0", invest_pct: "100",
          note: "оцінка; потрібна декларація до 31 грудня",
        },
      }, "Знижку додано в план");
    });
  });

}
