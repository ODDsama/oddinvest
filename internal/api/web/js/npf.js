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

import { esc, curSym, today, pct, uah2 as fmtUAH, money as fmtMoney } from "./format.js";
import { onSubmit, onSubmitFunded, onDelete } from "./forms.js";
import { svgLine } from "./charts.js";

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
 *  б показати горизонтальну лінію там, де даних немає. */
function navChartHTML(npfID) {
  const pts = navSeries(npfID);
  if (pts.length < 2) {
    return `<div class="sub muted">Крива ЧВОПА зʼявиться з двох точок. Кожен внесок приносить
      одну з собою; історію фонду до першого внеску можна вклеїти нижче.</div>`;
  }
  const series = [{
    name: "ЧВОПА", color: "var(--oi-series-npf)",
    values: pts.map((p) => p[1]),
  }];
  // Підписи проріджуємо: історія фонду — це десятки точок, і svgLine малює
  // мітку під кожною, тобто вони злились би в суцільну смугу. Порожній
  // рядок мітки нічого не малює, тож перша, остання й кілька проміжних
  // лишаються читабельними.
  const step = Math.max(1, Math.ceil(pts.length / 6));
  const labels = pts.map((p, i) =>
    (i === 0 || i === pts.length - 1 || i % step === 0) ? p[0] : "");
  const first = pts[0], last = pts[pts.length - 1];
  // Діапазон — текстом, а не лише на осі: svgLine центрує підписи, тож
  // крайні наполовину виходять за полотно й читаються обрізаними. Правити
  // це в самій svgLine означало б зачепити всі наявні криві заради одної.
  const range = `<div class="sub-xs muted">${esc(first[0])} → ${esc(last[0])} ·
    ${first[1].toFixed(6)} → ${last[1].toFixed(6)} · точок: ${pts.length}</div>`;
  return `${svgLine(labels, series, { H: 160 })}${range}`;
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
  return `<div class="sub">Оцінка податкової знижки за рік: <b>${fmtUAH(row.credit_est_uah || 0)}</b></div>
    <div class="sub-xs muted">Внески за рік у межах ліміту ${cap ? fmtUAH(cap) + "/міс" : "(ліміт не задано)"}
      × ставка знижки, обмежено утриманим ПДФО ${fmtUAH(pdfo)}. Ліміт щороку інший — він виводиться
      з прожиткового мінімуму працездатних на 1 січня × 1,4.</div>
    <div class="sub-xs t-warn">Не входить у жоден прогноз: ні в капітал, ні в календар, ні в
      проєкцію, ні в податковий звіт. Щоб знижку отримати, треба подати декларацію до 31 грудня
      наступного року.</div>`;
}

/** Журнал внесків рахунку. ЧВОПА в таблиці — ВИВЕДЕНА (сума ÷ одиниці), а
 *  не окреме поле: воно дало б їм розійтись. */
function opsTableHTML(npfID, currency) {
  const rows = ops.filter((o) => o.npf_id === npfID)
    .sort((a, b) => a.date < b.date ? 1 : a.date > b.date ? -1 : b.id - a.id);
  if (!rows.length) return `<div class="muted">Внесків ще немає.</div>`;
  return `<table><thead><tr><th>Дата</th><th class="num">Сума</th>
      <th class="num">Одиниць</th><th class="num">ЧВОПА</th><th>З рахунку</th><th>Нотатка</th><th></th></tr></thead>
    <tbody>${rows.map((o) => `<tr><td>${esc(o.date)}</td>
      <td class="num">${fmtMoney(o.amount)}</td>
      <td class="num">${(o.units || 0).toFixed(6)}</td>
      <td class="num">${(o.nav || 0).toFixed(6)} ${curSym(currency)}</td>
      <td>${esc(o.broker || "")}</td><td class="muted">${esc(o.note || "")}</td>
      <td class="row-actions"><button class="sm warn" data-delnpfop="${o.id}">✕</button></td></tr>`).join("")}
    </tbody></table>`;
}

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
    <form data-npfnav-form="${acc.id}">
      <label>Дата<input name="date" type="date" value="${today()}" required></label>
      <label>ЧВОПА<input name="nav" inputmode="decimal" placeholder="${(row.nav || 0).toFixed(6)}" required></label>
      <div class="form-actions"><button type="submit">Оновити</button></div>
    </form>

    <h4 class="mt">Внести</h4>
    <form data-npfop-form="${acc.id}">
      <label>Дата<input name="date" type="date" value="${today()}" required></label>
      <label>Сума<input name="amount" inputmode="decimal" required></label>
      <label>Зараховано одиниць<input name="units" inputmode="decimal" required
        title="З виписки фонду. ЧВОПА виводиться сама: сума ÷ одиниці"></label>
      <label>З рахунку<input name="broker" list="brokers-list" placeholder="звідки пішли гроші"></label>
      <label>Нотатка<input name="note"></label>
      <div class="form-actions"><button type="submit">Записати внесок</button></div>
    </form>

    <h4 class="mt">Внески</h4>
    ${opsTableHTML(acc.id, row.currency)}

    <h4 class="mt">${creditHTML(ctx, row)}</h4>

    <h4 class="mt">Вклеїти історію ЧВОПА</h4>
    <div class="sub-xs muted">Опублікована фондом таблиця — по рядку на день:
      <code>2020-01-01 1.500000</code>. Дата й число, розділені пробілом, комою або табуляцією.
      Саме вона й робить видимим track record, на якому стоїть обіцянка.</div>
    <form data-npfnavbulk-form="${acc.id}">
      <label>Рядки<textarea name="points" rows="4" placeholder="2020-01-01 1.5
2021-01-01 1.82"></textarea></label>
      <div class="form-actions"><button type="submit">Вклеїти</button></div>
    </form>
    ${navBulkListHTML(acc.id, row.currency)}`;
}

/** Заведені руками точки — окремим списком, бо лише їх і можна видалити:
 *  виведені з внесків живуть у журналі внесків і зникають разом із ним. */
function navBulkListHTML(npfID, currency) {
  const pts = navPoints.filter((p) => p.npf_id === npfID)
    .sort((a, b) => a.date < b.date ? 1 : -1);
  if (!pts.length) return "";
  return `<table class="mt"><tbody>${pts.map((p) => `<tr>
      <td class="muted">${esc(p.date)}</td>
      <td class="num">${(p.nav || 0).toFixed(6)} ${curSym(currency)}</td>
      <td class="row-actions"><button class="sm warn" data-delnpfnav="${p.id}">✕</button></td>
    </tr>`).join("")}</tbody></table>`;
}

/** parseNavLines — рядки «дата число» у точки.
 *
 *  Свідомо ліберальний до розділювача (пробіл, кома, табуляція) і до коми
 *  як десяткової: історію копіюють із сайту чи з таблиці, і вимагати
 *  єдиного формату означало б змусити людину чистити текст руками. А от
 *  дату не вгадуємо: РРРР-ММ-ДД або помилка, бо 01.02 — це або січень, або
 *  лютий, і вгадування тут тихо зіпсувало б криву. */
export function parseNavLines(text) {
  const out = [];
  const bad = [];
  (text || "").split(/\r?\n/).forEach((line, i) => {
    const s = line.trim();
    if (!s) return;
    const m = s.split(/[\s,;\t]+/).filter(Boolean);
    if (m.length < 2) { bad.push(i + 1); return; }
    const date = m[0];
    const nav = m[m.length - 1].replace(",", ".");
    if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !/^\d+(\.\d+)?$/.test(nav)) {
      bad.push(i + 1);
      return;
    }
    out.push({ date, nav });
  });
  return { points: out, bad };
}

export function wireNPF(ctx, main) {
  main.querySelectorAll("[data-npfop-form]").forEach((form) => {
    const id = Number(form.dataset.npfopForm);
    // Внесок може не вміститись у баланс рахунку, тож форма та сама, що в
    // покупки паперу: спершу /check, і якщо бракує — пропозиція поповнити.
    onSubmitFunded(ctx, form, (f) => {
      const fd = new FormData(f);
      const body = {
        npf_id: id, date: fd.get("date"), amount: fd.get("amount"),
        units: fd.get("units"), broker: fd.get("broker") || "", note: fd.get("note") || "",
      };
      return {
        path: "/api/npf", body, check: "/api/npf/check",
        msg: "Внесок записано", date: body.date, what: "внесок у НПФ",
      };
    });
  });

  main.querySelectorAll("[data-npfnav-form]").forEach((form) => {
    const id = Number(form.dataset.npfnavForm);
    onSubmit(ctx, form, (f) => {
      const fd = new FormData(f);
      return {
        method: "PUT", path: `/api/npf-accounts/${id}/nav`,
        body: { nav: fd.get("nav"), date: fd.get("date") },
        msg: "ЧВОПА оновлено",
      };
    });
  });

  main.querySelectorAll("[data-npfnavbulk-form]").forEach((form) => {
    const id = Number(form.dataset.npfnavbulkForm);
    onSubmit(ctx, form, (f) => {
      const { points, bad } = parseNavLines(new FormData(f).get("points"));
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
        path: "/api/npf-nav", body: { npf_id: id, points },
        msg: `Вклеєно точок: ${points.length}`,
      };
    });
  });

  onDelete(ctx, main, "[data-delnpfop]", (el) => ({
    path: `/api/npf/${el.dataset.delnpfop}`, msg: "Внесок видалено",
  }));
  onDelete(ctx, main, "[data-delnpfnav]", (el) => ({
    path: `/api/npf-nav/${el.dataset.delnpfnav}`, msg: "Точку ЧВОПА видалено",
  }));
}
