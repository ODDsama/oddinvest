// Єдина таблиця позицій: ОВДП, фонди, вклади й резерв в одному списку.
//
// Один рядок на інструмент, розкриття показує деталі саме його виду —
// лоти в облігації, операції у фонді, поповнення у вкладі. Резерв стоїть
// тут теж, хоч інструментом і не є: він частина капіталу, і питання «що я
// маю» без нього неповне.

import {
  esc, curSym, today, dayMonth, pct, daysUntil,
  uah2 as fmtUAH, money as fmtMoney,
} from "../format.js";
import { PAYOUT_LABEL, KIND_LABEL } from "../constants.js";
import { infoBtn } from "../info.js";
import { yieldPair } from "../components.js";
import { fundTable } from "../fund-ops.js";


const POS_COLS = 7;

// Які рядки розкриті. Живе поза рендером навмисно: ctx.reload() стирає
// main.innerHTML цілком, і без цього кожне поповнення згортало б той
// вклад, у якому ти його щойно записав.
const openRows = new Set();

// Реальна дохідність — колонка, заради якої таблиця й спільна. Під нею
// номінальна й основа: з чого число взялося — обіцянка (купон, ставка)
// чи факт (дивіденди зі зміною ціни). Ховати цю різницю нечесно.
function realCell(real, nominal, basis) {
  if (!real) return `<td class="num muted col-yield">—</td>`;
  return `<td class="num col-yield">${yieldPair(real, nominal, basis)}</td>`;
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
  const salesTbl = mySales.length ? `<h4 style="margin-top:12px">Продажі</h4><table><thead><tr>
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
    <h4 style="margin-top:12px">Лоти</h4>
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
    <form class="topup-form" data-topup-form="${d.id}">
      <label>Дата поповнення<input name="date" type="date" value="${today()}" required></label>
      <label>Сума<input name="amount" inputmode="decimal" value="${d.principal.amount}" required></label>
      <button type="submit">Поповнити</button>
    </form>` : "";
  const topupsTbl = topups.length ? `<h4 style="margin-top:12px">Поповнення</h4><table><tbody>
    ${topups.map((t) => `<tr><td class="muted">${esc(t.date)}</td><td class="num">${fmtMoney(t.amount)}</td>
      <td class="row-actions"><button class="sm warn" data-deltopup="${d.id}:${t.id}">✕</button></td></tr>`).join("")}
    </tbody></table>` : "";
  return `${topupForm}${topupsTbl}
    <h4 style="margin-top:12px">Розірвати достроково</h4>
    <form class="close-form" data-close-form="${d.id}">
      <label>Дата розірвання<input name="closed_date" type="date" value="${today()}" required></label>
      <label>Отримано (тіло + відсотки)<input name="closed_amount" inputmode="decimal" placeholder="${d.balance.amount}" required></label>
      <button type="submit">Підтвердити розірвання</button>
    </form>`;
}

// Усі сутності зводяться до одного вигляду рядка. sortBy — вкладене
// в НАТИВНІЙ валюті: сортуємо лише всередині групи одного інструмента,
// тож валюти між собою тут не зустрічаються.
function positionItems(ctx, positions, lots, sales, deposits) {
  const bonds = positions.map((p) => ({
    key: "bond:" + p.isin, kind: "bond",
    // Паперу немає в довіднику НБУ — номінал, строк і виплати приходять
    // нулями. Кажемо це вголос: інакше нуль читається як «нічого не
    // повернеться», а насправді це «ми не знаємо». Так буває, коли папір
    // щойно розміщений або лот прийшов із виписки раніше за оновлення.
    name: `<b>${esc(p.isin)}</b>${p.unknown
      ? `<div class="sub-xs" style="color:var(--oi-warn)">⚠ немає в довіднику НБУ —
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
      const col = pnl >= 0 ? "var(--oi-ok)" : "var(--oi-danger)";
      const short = f.short > 0
        ? `<div class="sub-xs" style="color:var(--oi-warn)">⚠ продано на ${f.short} серт. більше,
           ніж куплено — у журналі бракує надходження, числа занижені</div>` : "";
      return {
        key: "fund:" + f.fund, kind: "fund",
        name: `<b>${esc(f.fund)}</b>${short}`,
        invested: fmtUAH(f.cost_basis),
        value: `${fmtUAH(f.market_value)}<div class="sub-xs" style="color:${col}">${pnl >= 0 ? "+" : ""}${fmtUAH(pnl)}</div>`,
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
      actions: `<label class="sub-xs" style="flex-direction:row;align-items:center;gap:4px;display:inline-flex"
             title="Чи приймає цей вклад поповнення — від цього залежить, чи радить його помічник">
          <input type="checkbox" data-repl="${d.id}" style="width:auto"${d.replenishable ? " checked" : ""}>попов.</label>
        <button class="sm warn" data-deldep="${d.id}">✕</button>`,
      sortBy: Number(d.principal.amount), detail: depositDetailHTML(d),
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
  return [...bonds.sort(bySize), ...funds.sort(bySize), ...deps.sort(bySize), ...res];
}

export function positionsTableHTML(ctx, positions, lots, sales, deposits) {
  const items = positionItems(ctx, positions, lots, sales, deposits);
  if (!items.length) {
    return `<div class="card"><h2>Позиції</h2>
      <div class="muted">Позицій ще немає — почни з покупки або відкрий вклад нижче.</div></div>`;
  }
  const rows = items.map((it) => {
    const open = openRows.has(it.key);
    return `<tr>
      <td class="col-kind"><button class="caret${open ? " open" : ""}" data-exp="${it.key}"
            aria-expanded="${open}" title="Показати, звідки взялася позиція">▸</button><span
          class="pill pill-${it.kind}">${KIND_LABEL[it.kind]}</span></td>
      <td>${it.name}</td>
      <td class="num">${it.invested}</td>
      <td class="num">${it.value}</td>
      ${realCell(it.pct, it.nominal, it.basis)}
      <td>${it.term}</td>
      <td class="row-actions" style="white-space:nowrap">${it.actions}</td></tr>
    <tr class="detail-row" data-detail="${it.key}"${open ? "" : ` style="display:none"`}>
      <td colspan="${POS_COLS}">${it.detail}</td></tr>`;
  }).join("");

  return `<div class="card"><h2 class="h-row">Позиції ${infoBtn("positions")}</h2>
    <div class="table-scroll"><table><thead><tr>
      <th class="col-kind">Тип</th><th>Назва</th><th class="num">Вкладено</th><th class="num">Вартість</th>
      <th class="num col-yield">Дохідність</th><th>Строк</th><th></th></tr></thead>
      <tbody>${rows}</tbody></table></div>
    <div class="sub" style="margin-top:10px">Велике число — <b>реальна</b> річна дохідність після
      податку, у сьогоднішній купівельній спроможності: саме вона порівнює ОВДП, фонд і вклад між
      собою. Дрібне під ним — <b>номінальна</b>: скільки гривень додасться, те, що видно у виписці.
      Далі — звідки число взялося: у ОВДП і вкладу це <b>обіцянка</b>, ставка зафіксована до
      погашення; у фонду <b>факт</b> по прожитому, бо ні строку, ні ставки він не має.
      Стрілка розкриває лоти, продажі й поповнення.</div>
  </div>`;
}


export function wirePositions(ctx, main) {
  main.querySelectorAll("[data-exp]").forEach((b) =>
    b.addEventListener("click", () => {
      const key = b.dataset.exp;
      const row = main.querySelector(`[data-detail="${key}"]`);
      if (!row) return;
      const open = row.style.display === "none";
      row.style.display = open ? "" : "none";
      b.classList.toggle("open", open);
      b.setAttribute("aria-expanded", String(open));
      if (open) openRows.add(key); else openRows.delete(key);
    }));
}


