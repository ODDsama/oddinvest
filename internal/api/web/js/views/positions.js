// Єдина таблиця позицій: ОВДП, фонди, НПФ, вклади й резерв в одному
// списку.
//
// Один рядок на інструмент, розкриття показує деталі саме його виду —
// лоти в облігації, операції у фонді, внески й ЧВОПА в пенсійному,
// поповнення у вкладі. Резерв стоїть тут теж, хоч інструментом і не є: він
// частина капіталу, і питання «що я маю» без нього неповне.

import {
  esc, curSym, today, dayMonth, pct, daysUntil,
  uah2 as fmtUAH, money as fmtMoney,
} from "../format.js";
import { PAYOUT_LABEL } from "../constants.js";
import { infoBtn } from "../info.js";
import { yieldPair, empty, kindPill } from "../components.js";
import { routeFor } from "../routes.js";
import { fundTable, setFundOps, wireFundOps } from "../fund-ops.js";
import { npfDetailHTML, setNPF, wireNPF } from "../npf.js";
import { isOpen, remember } from "../uistate.js";
import { wireDisclosures } from "../disclosure.js";
import { wireBonds } from "./bonds.js";
import { wireDeposits } from "./deposits.js";


const POS_COLS = 7;

// Які рядки розкриті. Живе поза рендером навмисно: ctx.reload() стирає
// main.innerHTML цілком, і без цього кожне поповнення згортало б той
// вклад, у якому ти його щойно записав.
//
// Було Set на рівні модуля: він переживав перемальовування, але вмирав
// разом зі вкладкою — перехід у «Гроші» й назад згортав усе. Тепер це
// спільне сховище (js/uistate.js), і розкрите живе далі навіть після
// перезавантаження сторінки.
const OPEN_SCOPE = "positions";

// Реальна дохідність — колонка, заради якої таблиця й спільна. Під нею
// номінальна й основа: з чого число взялося — обіцянка (купон, ставка)
// чи факт (дивіденди зі зміною ціни). Ховати цю різницю нечесно.
function realCell(real, nominal, basis) {
  if (!real) return `<td class="num muted col-yield" data-label="Дохідність">—</td>`;
  return `<td class="num col-yield" data-label="Дохідність">${yieldPair(real, nominal, basis)}</td>`;
}

function bondDetailHTML(p, lots, sales) {
  const myLots = lots.filter((l) => l.isin === p.isin);
  const mySales = sales.filter((s) => s.isin === p.isin);
  const next = p.next_pay_date
    ? `<div class="sub">Наступна виплата: ${esc(p.next_pay_date)} · ${fmtMoney(p.next_pay_amount)}</div>` : "";
  const lotsTbl = myLots.length ? `<h4>Лоти</h4><table><thead><tr>
      <th>ID</th><th class="num">К-сть</th><th class="num">Залишок</th><th class="num">Ціна</th>
      <th class="num">Комісія</th><th>Куплено</th><th>Брокер</th><th></th></tr></thead><tbody>
      ${myLots.map((l) => `<tr><td>${l.id}</td><td class="num">${l.qty}</td><td class="num">${l.remaining}</td>
        <td class="num">${fmtMoney(l.price_per_bond)}</td><td class="num">${fmtMoney(l.fee)}</td>
        <td>${esc(l.buy_date)}</td><td>${esc(l.channel || "")}</td>
        <td class="row-actions"><button class="sm warn" data-del="${l.id}">✕</button></td></tr>`).join("")}
      </tbody></table>` : "";
  // Продажі редагуються на місці. Лот поруч — лише з кнопкою видалення,
  // і різниця навмисна: помилку в лоті видно одразу (він з'явився не з
  // тим ISIN), а в продажі — ні, бо на екрані стоїть уже ЗВЕДЕНИЙ
  // результат, і розходження з випискою брокера в 6 гривень не каже,
  // котре з чотирьох полів набрано не так.
  //
  // lot_id і валюта їдуть у data-атрибутах: PUT замінює продаж цілком,
  // а в самому рядку їх не видно.
  const saleF = (field, attrs) =>
    `<td class="num"><input class="sale-f" data-field="${field}" ${attrs}></td>`;
  const salesTbl = mySales.length ? `<h4 class="mt">Продажі</h4><table><thead><tr>
      <th>Дата</th><th class="num">К-сть</th><th class="num">Чиста</th>
      <th class="num">НКД</th><th class="num">Результат</th><th></th></tr></thead><tbody>
      ${mySales.map((s) => `<tr data-sale="${s.id}" data-lot="${s.lot_id}"
        data-cur="${esc(s.clean_per_bond.currency)}">
        <td><input class="sale-f" data-field="sale_date" type="date" value="${esc(s.sale_date)}"></td>
        ${saleF("qty", `data-num="1" type="number" min="1" step="1" value="${s.qty}"`)}
        ${saleF("clean_per_bond", `inputmode="decimal" value="${esc(s.clean_per_bond.amount)}"`)}
        ${saleF("accrued", `inputmode="decimal" value="${esc(s.accrued.amount)}"`)}
        <td class="num">${fmtMoney(s.realized_result)}</td>
        <td class="row-actions"><button class="sm warn" data-delsale="${s.id}">✕</button></td></tr>`).join("")}
      </tbody></table>` : "";
  return next + lotsTbl + salesTbl;
}

function fundDetailHTML(ctx, f) {
  const bits = [`Дивіденди ${fmtUAH(f.dividends_net)}`];
  if (f.dividends_tax > 0) bits.push(`податок ${fmtUAH(f.dividends_tax)}`);
  if (f.realized) bits.push(`продажі ${fmtUAH(f.realized)}`);
  if (f.last_price_date) bits.push(`ціна від ${dayMonth(f.last_price_date)}`);
  if (f.next_payout) bits.push(`наступна виплата ${dayMonth(f.next_payout)}`);
  // Обіцянку показуємо лише тоді, коли вона й пішла в число: інакше поруч
  // із виміряною дохідністю вона читалась би як друга думка про те саме.
  if (f.expected_pct && f.yield_basis === "обіцяно фондом") {
    // Обіцянку, задану ПРОСТОЮ, показуємо обома числами. Інакше в картці
    // стоїть 20.5% там, де людина вписала 25, і зрозуміти різницю нізвідки:
    // проста 25% за три роки це ×1.75, а не ×1.95.
    bits.push(f.expected_simple_years
      ? `фонд обіцяє ${pct(f.expected_simple_pct)} простих за ${f.expected_simple_years} р.
         = ${pct(f.expected_pct)} складних`
      : `фонд обіцяє ${pct(f.expected_pct)}`);
  }
  return `<div class="sub">${bits.join(" · ")}</div>
    <h4 class="mt">Лоти</h4>
    ${fundTable(ctx, "buy",
      `<th class="num">ID</th><th>Фонд</th><th class="num">К-сть</th><th class="num">Ціна</th><th class="num">Сплачено</th>`,
      (o, c) => `<td class="num">${o.qty}</td><td class="num">${c.price(o)}</td><td class="num">${c.money(o.amount)}</td>`,
      "Купівель ще немає.", (o) => o.fund === f.fund)}`;
}

function depositDetailHTML(d) {
  const topups = d.topups || [];
  // Поповнення дозволяємо лише позначеному вкладу, а розірвання — будь-
  // якому: закрити достроково можна який завгодно.
  const topupForm = d.replenishable ? `<h4>Поповнити</h4>
    <form data-topup-form="${d.id}">
      <label>Дата поповнення<input name="date" type="date" value="${today()}" required></label>
      <label>Сума<input name="amount" inputmode="decimal" value="${d.principal.amount}" required></label>
      <div class="form-actions"><button type="submit">Поповнити</button></div>
    </form>` : "";
  const topupsTbl = topups.length ? `<h4 class="mt">Поповнення</h4><table><tbody>
    ${topups.map((t) => `<tr><td class="muted">${esc(t.date)}</td><td class="num">${fmtMoney(t.amount)}</td>
      <td class="row-actions"><button class="sm warn" data-deltopup="${d.id}:${t.id}">✕</button></td></tr>`).join("")}
    </tbody></table>` : "";
  return `${topupForm}${topupsTbl}
    <h4 class="mt">Розірвати достроково</h4>
    <form data-close-form="${d.id}">
      <label>Дата розірвання<input name="closed_date" type="date" value="${today()}" required></label>
      <label>Отримано (тіло + відсотки)<input name="closed_amount" inputmode="decimal" placeholder="${d.balance.amount}" required></label>
      <div class="form-actions"><button type="submit">Підтвердити розірвання</button></div>
    </form>`;
}

// Усі сутності зводяться до одного вигляду рядка. sortBy — вкладене
// в НАТИВНІЙ валюті: сортуємо лише всередині групи одного інструмента,
// тож валюти між собою тут не зустрічаються.
//
// kinds — необов'язковий фільтр за видом, і саме він дозволив «Активам»
// стати сімома сторінками, не розпавшись назад на три таблиці з трьома
// наборами колонок. Розріз іде ПО РЯДКАХ, а не по колонках: сторінка виду
// показує ту саму таблицю, лише звужену, тож «вкладено», «вартість» і
// «реальна дохідність» усюди означають те саме й порівнюються між
// сторінками поглядом. Заперечення проти спільної таблиці колись було
// справедливе саме до колонок — специфіка виду живе в рядку деталей, і
// там вона й лишилась.
function positionItems(ctx, positions, lots, sales, deposits, kinds = null) {
  const bonds = positions.map((p) => ({
    key: "bond:" + p.isin, kind: "bond",
    // Паперу немає в довіднику НБУ — номінал, строк і виплати приходять
    // нулями. Кажемо це вголос: інакше нуль читається як «нічого не
    // повернеться», а насправді це «ми не знаємо». Так буває, коли папір
    // щойно розміщений або лот прийшов із виписки раніше за оновлення.
    name: `<b>${esc(p.isin)}</b>${p.unknown
      ? `<div class="sub-xs t-warn">⚠ немає в довіднику НБУ —
         номінал і строк невідомі, оновити можна кнопкою «↻ Оновити НБУ»</div>` : ""}
      <div class="sub-xs">${p.qty} шт.</div>`,
    invested: fmtMoney(p.invested),
    value: p.unknown ? `<span class="muted">невідомо</span>` : fmtMoney(p.nominal),
    pct: p.real_pct, nominal: p.ytm_pct, basis: p.yield_basis,
    term: p.unknown
      ? `<span class="muted">невідомо</span>`
      : `${esc(p.maturity)}<div class="sub-xs">${p.days_to_maturity} дн.</div>`,
    actions: "", sortBy: Number((p.invested || {}).amount || 0),
    detail: bondDetailHTML(p, lots, sales),
  }));

  // Закритий фонд — не позиція: показувати «0 серт.» означає питати про
  // те, чого вже немає. Виняток — фонд із дірою в журналі: він лишається
  // на видноті саме тому, що його числа неправильні.
  const funds = ((ctx.summary || {}).funds || [])
    .filter((f) => f.qty > 0 || f.short > 0)
    .map((f) => {
      const pnl = f.market_value - f.cost_basis;
      // Клас, а не колір: знак прибутку — це СТАН, і називати його
      // «зелений» означає домовлятись про колір у чотирнадцяти місцях
      // замість одного.
      const pnlTone = pnl >= 0 ? "t-ok" : "t-danger";
      const short = f.short > 0
        ? `<div class="sub-xs t-warn">⚠ продано на ${f.short} серт. більше,
           ніж куплено — у журналі бракує надходження, числа занижені</div>` : "";
      return {
        key: "fund:" + f.fund, kind: "fund",
        name: `<b>${esc(f.fund)}</b>${short}`,
        invested: fmtUAH(f.cost_basis),
        value: `${fmtUAH(f.market_value)}<div class="sub-xs ${pnlTone}">${pnl >= 0 ? "+" : ""}${fmtUAH(pnl)}</div>`,
        // Номінальна мусить бути двійником реальної, тобто тим самим
        // числом до поправки на знецінення. Тому вона йде за ОСНОВОЮ:
        // коли real_pct порахований з обіцянки, поруч має стояти
        // обіцянка, а не виміряні дивіденди. Доти тут завжди бралась
        // виміряна, і в рядку опинялась пара з різних джерел — той самий
        // ґандж, що колись давав реальну вищу за номінальну в плитці.
        pct: f.real_pct, basis: f.yield_basis,
        nominal: f.yield_basis === "обіцяно фондом"
          ? f.expected_pct
          : f.total_pct || f.yield_net_pct,
        // У БЕЗСТРОКОВОГО сертифіката строку немає — на його місці те, що
        // для фонду важить натомість: скільки їх і почім останній раз.
        // У строкового строк є, і писати «безстроково» означало б
        // сховати найважливіше про інструмент: дату, коли він
        // закривається й повертає гроші.
        term: `<span class="muted">${f.close_date
          ? "до " + dayMonth(f.close_date)
          : "безстроково"}</span><div class="sub-xs">${f.qty} серт. · ${(f.last_price || 0).toFixed(4)} ${curSym(f.currency)}</div>`,
        actions: "", sortBy: f.cost_basis,
        detail: fundDetailHTML(ctx, f),
      };
    });

  const deps = deposits.filter((d) => !d.closed_date).map((d) => {
    const topups = (d.topups || []).length;
    return {
      key: "dep:" + d.id, kind: "deposit",
      name: `<b>${esc(d.bank || "—")}</b><div class="sub-xs">${PAYOUT_LABEL[d.payout] || d.payout}${d.capitalized ? " · кап." : ""}</div>`,
      invested: `${fmtMoney(d.principal)}${topups ? `<div class="sub-xs">+${topups} поповн.</div>` : ""}`,
      value: fmtMoney(d.balance),
      // Номінальна вкладу — ПІСЛЯ податку (net_pct), а не договірна
      // ставка поруч: між нею й реальною було б дві поправки одразу.
      pct: d.real_pct, nominal: d.net_pct, basis: d.yield_basis,
      // «Ставка» тут — договірна, до податку: це умова вкладу, а не
      // дохідність, і слово поруч рятує від читання її як третього
      // числа в тому самому рядку.
      term: `${esc(d.maturity_date)}<div class="sub-xs">${daysUntil(d.maturity_date)} дн. · ставка ${pct(d.rate_pct)}</div>`,
      actions: `<label class="sub-xs row-h inline"
             title="Чи приймає цей вклад поповнення — від цього залежить, чи радить його помічник">
          <input type="checkbox" class="w-auto" data-repl="${d.id}"${d.replenishable ? " checked" : ""}>попов.</label>
        <button class="sm warn" data-deldep="${d.id}">✕</button>`,
      sortBy: Number(d.principal.amount), detail: depositDetailHTML(d),
    };
  });

  // НПФ. Стоїть серед інструментів, а не поруч із резервом: він
  // компаундиться, він просто неліквідний.
  const npf = ((ctx.summary || {}).npf || []).map((n) => {
    const gain = n.gain_uah || 0;
    const gainTone = gain >= 0 ? "t-ok" : "t-danger";
    const due = n.contrib_due
      ? `<div class="sub-xs t-warn">⚠ внеску за цей місяць ще немає</div>` : "";
    return {
      key: "npf:" + n.name, kind: "npf",
      name: `<b>${esc(n.name)}</b>${due}<div class="sub-xs">${esc(n.administrator || "пенсійний фонд")}</div>`,
      invested: fmtUAH(n.cost_uah || 0),
      value: `${fmtUAH(n.value_uah || 0)}<div class="sub-xs ${gainTone}">${gain >= 0 ? "+" : ""}${fmtUAH(gain)}</div>`,
      // Номінальна йде за ОСНОВОЮ, як у фонда: коли real_pct порахований з
      // обіцянки, поруч мусить стояти обіцянка, а не виміряне зростання
      // ЧВОПА, — інакше в рядку стояла б пара з різних джерел.
      pct: n.real_pct, basis: n.yield_basis,
      nominal: n.yield_basis === "обіцяно фондом" ? n.expected_pct : n.nav_return_pct,
      // Ніколи «безстроково»: строк у НПФ є, він просто довший за все інше,
      // і саме він — найважливіше, що варто знати про цей інструмент.
      term: n.access_date
        ? `${esc(n.access_date)}<div class="sub-xs">замкнено · ${daysUntil(n.access_date)} дн.</div>`
        : `<span class="muted">дату доступу не задано</span>`,
      actions: "", sortBy: n.value_uah || 0,
      detail: npfDetailHTML(ctx, n),
    };
  });

  // Резерв — один рядок, не список: журнал рухів живе в «Грошах», а тут
  // питання «що я маю». Останнім навмисно: він нічого не заробляє, тож
  // серед інструментів йому не місце, але й ховати його не можна — він
  // частина капіталу й валютної експозиції.
  const r = (ctx.summary || {}).reserve;
  const res = r && r.uah ? [{
    key: "reserve", kind: "reserve",
    name: `<b>Резерв</b><div class="sub-xs">${r.places
      ? Object.keys(r.places).map(esc).join(" · ") : "на чорний день"}</div>`,
    // Вкладено = вартість: резерв нічого не заробляє й не втрачає, тож
    // його собівартість дорівнює самій сумі.
    invested: fmtUAH(r.uah), value: fmtUAH(r.uah),
    // Дохідності немає — і це не пропуск даних, а властивість сутності.
    // realCell це вже вміє: порожні числа дають прочерк.
    pct: 0, nominal: 0, basis: "",
    term: `<span class="muted">без строку</span><div class="sub-xs">${r.months
      ? `${r.months.toFixed(1).replace(".", ",")} міс. витрат`
      : "доступний миттєво"}</div>`,
    actions: "", sortBy: r.uah,
    detail: `<div class="sub">Не інвестиція, а страховка: єдине, що доступне миттєво й без втрат,
      коли гроші раптом знадобились. Входить у капітал і валютні частки, але не в дохідність і не в
      купівельну спроможність — помічник реінвесту його не бачить. Рухи записуються в «Грошах».</div>`,
  }] : [];

  const bySize = (a, b) => b.sortBy - a.sortBy;
  // Фільтр стоїть НАПРИКІНЦІ, після склеювання, а не всередині кожної
  // гілки: інакше порядок довелось би відтворювати в кожній сторінці
  // окремо, а він тут змістовний — резерв останній навмисно, бо він
  // єдиний нічого не заробляє.
  const all = [...bonds.sort(bySize), ...funds.sort(bySize), ...npf.sort(bySize),
    ...deps.sort(bySize), ...res];
  return kinds ? all.filter((it) => kinds.includes(it.kind)) : all;
}

/** Таблиця позицій. opts.kinds звужує її до одного виду, opts.title і
 *  opts.empty дають сторінці виду назвати себе своїм ім'ям — «Позиція
 *  з'явиться після першої покупки паперу» на сторінці вкладів було б
 *  порадою не в те місце.
 *
 *  opts.rowDetail = false прибирає розкриття цілком — і рядок деталей, і
 *  каретку. Потрібно рівно там, де ті самі форми вже стоять на сторінці
 *  окремим блоком: у НПФ рядок деталей — це npfDetailHTML із формою внеску
 *  й формою ЧВОПА, і поруч із такою ж формою нижче вийшло б два входи до
 *  одного запису на одному екрані (обидва підв'яже wireNPF, бо він ходить
 *  querySelectorAll). Не падіння, але саме та плутанина, від якої розділи
 *  й розводили. За замовчуванням true: жодна наявна сторінка не міняється. */
export function positionsTableHTML(ctx, positions, lots, sales, deposits, opts = {}) {
  const { kinds = null, title = "Позиції", empty: emptyOpts = null,
    rowDetail = true } = opts;
  const items = positionItems(ctx, positions, lots, sales, deposits, kinds);
  if (!items.length) {
    const e = emptyOpts || {
      text: "Позиція з'явиться після першої покупки паперу або відкритого вкладу.",
      action: { href: routeFor("buy"), label: "Записати покупку" },
    };
    return `<div class="card"><h2>${esc(title)}</h2>${empty(
      "Тут ще порожньо", e.text, e.action || undefined)}</div>`;
  }
  // data-label і data-prio — увесь механізм адаптивності цієї таблиці.
  // Ширина вирішує CSS, а розмітка лише каже, ЯК називається кожна
  // комірка (щоб на телефоні підпис можна було дописати перед числом)
  // і НАСКІЛЬКИ вона потрібна (щоб у тісноті прибрати найменш потрібну).
  // Пріоритет 1 — те, без чого рядок не має сенсу; 3 — те, що і так
  // повторюється в рядку деталей.
  const rows = items.map((it) => {
    const open = isOpen(OPEN_SCOPE, it.key);
    const detailId = `pos-d-${it.key}`.replace(/[^\w-]/g, "-");
    // Без розкриття каретки немає ВЗАГАЛІ, а не «є, але нічого не робить»:
    // кнопка, яка нікуди не веде, — гірше за її відсутність.
    const caret = rowDetail
      ? `<button class="caret${open ? " open" : ""}" data-exp="${it.key}"
            aria-expanded="${open}" aria-controls="${detailId}"
            title="Показати, звідки взялася позиція">▸</button>`
      : "";
    return `<tr>
      <td class="col-kind" data-label="Тип">${caret}${kindPill(it.kind)}</td>
      <td data-label="Назва">${it.name}</td>
      <td class="num" data-label="Вкладено" data-prio="3">${it.invested}</td>
      <td class="num" data-label="Вартість" data-prio="2">${it.value}</td>
      ${realCell(it.pct, it.nominal, it.basis)}
      <td data-label="Строк" data-prio="2">${it.term}</td>
      <td class="row-actions nowrap">${it.actions}</td></tr>
    ${rowDetail ? `<tr class="detail-row" id="${detailId}" data-detail="${it.key}"${
  open ? "" : " hidden"}>
      <td colspan="${POS_COLS}">${it.detail}</td></tr>` : ""}`;
  }).join("");

  return `<div class="card"><h2 class="h-row">${esc(title)} ${infoBtn("positions")}</h2>
    <div class="table-scroll"><table class="pos-table">
      <caption class="sr-only">${esc(title)}: вкладено, вартість, реальна дохідність, строк</caption>
      <thead><tr>
      <th class="col-kind" scope="col">Тип</th><th scope="col">Назва</th>
      <th class="num" scope="col" data-prio="3">Вкладено</th>
      <th class="num" scope="col" data-prio="2">Вартість</th>
      <th class="num col-yield" scope="col">Дохідність</th>
      <th scope="col" data-prio="2">Строк</th><th scope="col"><span class="sr-only">Дії</span></th>
      </tr></thead>
      <tbody>${rows}</tbody></table></div>
    <div class="sub mt">Велике число — <b>реальна</b> річна дохідність після
      податку, у сьогоднішній купівельній спроможності: саме вона порівнює ОВДП, фонд і вклад між
      собою. Дрібне під ним — <b>номінальна</b>: скільки гривень додасться, те, що видно у виписці.
      Далі — звідки число взялося: у ОВДП і вкладу це <b>обіцянка</b>, ставка зафіксована до
      погашення; у фонду <b>факт</b> по прожитому, бо ні строку, ні ставки він не має.
      ${rowDetail ? "Стрілка розкриває лоти, продажі й поповнення." : ""}</div>
  </div>`;
}


export function wirePositions(ctx, main) {
  main.querySelectorAll("[data-exp]").forEach((b) =>
    b.addEventListener("click", () => {
      const key = b.dataset.exp;
      const row = main.querySelector(`[data-detail="${key}"]`);
      if (!row) return;
      // hidden, а не style.display. Тут це не лише охайність: рядок
      // деталей у .pos-table має ТРИ яруси розкладки (картка-рядок до
      // 640, шість колонок до 900, сім далі), і кожен задає display
      // явно. Інлайновий стиль їх перебивав, атрибут — ні, тому в
      // base.css поруч із ярусами стоїть правило `tr[hidden]`.
      const open = row.hidden;
      row.hidden = !open;
      b.classList.toggle("open", open);
      b.setAttribute("aria-expanded", String(open));
      remember(OPEN_SCOPE, key, open);
    }));
}



// ---------- спільний вхід у дані таблиці ----------

/** Один запит-набір на сторінку, скільки б сторінок його не потребувало.
 *
 *  Це не ліньки, а страховка під конкретний клас помилок. fund-ops.js і
 *  npf.js тримають журнал у МОДУЛЬНІЙ змінній, яку мусить виставити той,
 *  хто дані вантажив (setFundOps/setNPF); пропущений виклик не падає, а
 *  ТИХО малює порожні деталі. Один вхід замість багатьох знімає цей клас
 *  цілком.
 *
 *  Живе тут, а не в розділі, з якого приїхав: даних потребують уже двоє —
 *  сторінки портфеля й воронки інструментів, — і поки функція була
 *  приватною в одному розділі, другий мусив би або імпортувати чужий
 *  розділ, або завести другу копію. Друга копія — рівно те місце, де
 *  setNPF і забувають.
 *
 *  Платня — нуль: GET-и йдуть через кеш store.js, тож обхід усіх сторінок,
 *  які його кличуть, коштує один набір запитів. */
export async function loadPositionsData(ctx) {
  const [positions, lots, sales, ops, deposits, npfAcc, npfOps, npfNav] = await Promise.all([
    ctx.api("GET", "positions"),
    ctx.api("GET", "lots"),
    ctx.api("GET", "sales"),
    ctx.soft("funds", []),
    ctx.soft("term-deposits", []),
    // М'яко, як фонди й вклади: на старій БД НПФ немає, і валити через це
    // сторінку означало б показати порожній екран замість портфеля.
    ctx.soft("npf-accounts", []),
    ctx.soft("npf", []),
    ctx.soft("npf-nav", []),
  ]);
  setFundOps(ops);
  setNPF({ accounts: npfAcc, ops: npfOps, nav: npfNav });
  return { positions, lots, sales, deposits };
}

/** Проводка рядків деталей — і всього, що з ними приїхало.
 *
 *  У розкриттях живуть форми продажу, поповнення й закриття вкладу, тож
 *  підв'язувати треба на КОЖНІЙ сторінці, де таблиця взагалі є. Усі шість
 *  функцій терплять відсутність своїх цілей — forms.js вартує кожен
 *  querySelector, а querySelectorAll просто дає порожній список, — тож
 *  зайвий виклик тут нічого не коштує.
 *
 *  Саме через цю терплячість один виклик покриває і таблицю, і форми
 *  ЗАПИСУ, коли вони стоять на тій самій сторінці: wireBonds має ранній
 *  вихід без #lotForm, а з формою підв'яже й автокомпліт ISIN, і перевірку
 *  грошей; wireDeposits однаково бачить і #termDepForm, і поповнення в
 *  рядках. */
export function wirePositionRows(ctx, main, deposits) {
  wirePositions(ctx, main);
  wireBonds(ctx, main);
  wireFundOps(ctx, main);
  wireNPF(ctx, main);
  wireDeposits(ctx, main, deposits);
  wireDisclosures(main);
}
