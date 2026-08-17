// Дії: точкові рішення на дату — перевести майбутні внески в іншу валюту
// або замкнути суму під ставку на строк.
//
// Разом із ними тут capitalAt: підказка «за планом на цю дату капітал ≈ …»
// потрібна саме формі замка й більше нікому. Своє означення росту капіталу
// вона НЕ вигадує — читає криву, що приїхала зі стрічкою; четверте
// означення в браузері розійшлося б із трьома наявними в двигуні.

import { esc, today, money as fmtMoney, uah0 as fmtUAH, pct, dayMonth, humanMonths } from "../format.js";
import { empty } from "../components.js";
import { onSubmit, onDelete, openEdit } from "../forms.js";

// ---------- дії: список ----------

// Деталь рядка — те, що різнить дію: частки для set_shares, сума й строк
// для lock. Спільної форми в них немає, тож і колонка не вдає, що є.
function planActionDetail(a) {
  if (a.type === "set_shares") {
    const bits = [
      a.usd_share_pct != null ? `USD ${pct(a.usd_share_pct, 0)}` : null,
      a.eur_share_pct != null ? `EUR ${pct(a.eur_share_pct, 0)}` : null,
    ].filter(Boolean);
    return bits.join(", ") || "—";
  }
  const term = a.months > 0 ? humanMonths(a.months) : "безстроково";
  return `${fmtMoney(a.amount)} під ${pct(a.rate_pct)} · ${esc(term)}`;
}

export function planActionsListHTML(actions) {
  if (!actions.length) {
    return empty("", "Дій ще немає — дві форми нижче: зміна валютних часток і замок під ставку.");
  }
  const rows = actions.slice()
    .sort((a, b) => a.date < b.date ? -1 : a.date > b.date ? 1 : 0)
    .map((a) => `<tr>
      <td>${esc(dayMonth(a.date))}</td>
      <td><span class="pill ${a.type === "lock" ? "coupon" : "early"}">${
  a.type === "lock" ? "замок" : "частки"}</span></td>
      <td>${esc(a.name || "—")}</td>
      <td>${planActionDetail(a)}</td>
      <td class="row-actions">
        <button class="sm" data-editaction="${a.id}"
          aria-label="Змінити дію від ${esc(a.date)}">✎</button>
        <button class="sm warn" data-delaction="${a.id}"
          aria-label="Видалити дію від ${esc(a.date)}">✕</button>
      </td>
    </tr>`).join("");
  return `<div class="table-scroll"><table><thead><tr>
    <th scope="col">Дата</th><th scope="col">Тип</th><th scope="col">Назва</th>
    <th scope="col">Деталі</th><th scope="col"><span class="sr-only">Дії</span></th>
    </tr></thead><tbody>${rows}</tbody></table></div>`;
}

// ---------- дії: форми ----------
//
// Дві форми, а не одна з перемикачем: set_shares і lock не мають
// спільного поля, крім дати, і об'єднана форма лише ховала б, які поля
// стосуються якого типу.

// Підказка під полем частки: скільки її В КАПІТАЛІ зараз і яка ціль.
// Доти частка заводилась наосліп — сторінка знала обидва числа й мовчала.
function shareHint(ctx, cur) {
  const s = ctx.summary || {};
  const now = cur === "USD" ? s.usd_share_pct : s.eur_share_pct;
  const set = s.settings || {};
  const target = cur === "USD" ? set.usd_target_share_pct : set.eur_target_share_pct;
  const bits = [];
  if (now != null) bits.push(`зараз ${pct(now)}`);
  if (target != null) bits.push(`ціль ${pct(target)}`);
  return bits.length ? `<div class="sub-xs">${bits.join(" · ")}</div>` : "";
}

function sharesFields(ctx, values = null) {
  const v = values || {};
  const val = (k, d = "") => `value="${esc(v[k] != null ? v[k] : d)}"`;
  return `
    <label>З дати<input name="date" type="date" ${val("date", today())} required></label>
    <label>Частка USD, %<input name="usd_share_pct" type="number" step="0.01" min="0" max="100"
      placeholder="залишити як є" ${val("usd_share_pct")}>${shareHint(ctx, "USD")}</label>
    <label>Частка EUR, %<input name="eur_share_pct" type="number" step="0.01" min="0" max="100"
      placeholder="залишити як є" ${val("eur_share_pct")}>${shareHint(ctx, "EUR")}</label>
    <label>Нотатка<input name="note" ${val("note")}></label>`;
}

export function planSetSharesFormHTML(ctx) {
  return `<form id="planSetSharesForm">${sharesFields(ctx)}
    <div class="form-actions"><button type="submit">Змінити частки</button></div>
  </form>`;
}

function lockFields(values = null) {
  const v = values || {};
  const val = (k, d = "") => `value="${esc(v[k] != null ? v[k] : d)}"`;
  const cur = v.currency || "UAH";
  return `
    <label>Назва<input name="name" placeholder="MilTech" ${val("name")} required></label>
    <label>Дата<input name="date" type="date" ${val("date", today())} required></label>
    <label>Сума<input name="amount" inputmode="decimal" placeholder="50000.00" ${val("amount")} required></label>
    <label>Валюта<select name="currency">${["UAH", "USD", "EUR"].map((c) =>
    `<option${c === cur ? " selected" : ""}>${c}</option>`).join("")}</select></label>
    <label>Ставка, %<input name="rate_pct" type="number" step="0.01" min="0" max="100"
      placeholder="20" ${val("rate_pct")} required></label>
    <label>Строк, міс. (0 = безстроково)<input name="months" type="number" min="0"
      placeholder="24" ${val("months")}></label>
    <label>Нотатка<input name="note" ${val("note")}></label>`;
}

// Підказку про капітал на дату несе лише форма ДОДАВАННЯ: у модалці
// правки їй нема з чого рахуватись (стрічка туди не передається), а
// порожній рядок під полями читався б як «даних немає».
export function planLockFormHTML() {
  return `<form id="planLockForm">${lockFields()}
    <div class="sub-xs" data-lockhint></div>
    <div class="form-actions"><button type="submit">Замкнути</button></div>
  </form>`;
}

// ---------- капітал на дату ----------

/** Капітал за планом на дату iso, ₴ у сьогоднішніх грошах. Лінійна
 *  інтерполяція між сусідніми точками кривої; null — кривої немає або
 *  дата поза її межами.
 *
 *  Інтерполяція, а не найближча точка: крок кривої — дедлайн/12, тобто на
 *  десятирічному горизонті точка раз на десять місяців, і «найближча»
 *  помилялась би на пів року внесків. */
export function capitalAt(curve, iso) {
  const pts = (curve || []).filter((p) => p && p.date && p.plan != null);
  if (pts.length < 2 || !iso) return null;
  if (iso < pts[0].date || iso > pts[pts.length - 1].date) return null;
  for (let i = 1; i < pts.length; i++) {
    if (iso > pts[i].date) continue;
    const a = pts[i - 1], b = pts[i];
    const span = Date.parse(b.date) - Date.parse(a.date);
    if (!span) return b.plan;
    const t = (Date.parse(iso) - Date.parse(a.date)) / span;
    return a.plan + (b.plan - a.plan) * t;
  }
  return null;
}


export function wirePlanActions(ctx, main, actions, timeline) {
  const byId = new Map(actions.map((a) => [String(a.id), a]));

  const sharesBody = (f) => ({
    date: f.date.value, type: "set_shares",
    usd_share_pct: f.usd_share_pct.value.trim(), eur_share_pct: f.eur_share_pct.value.trim(),
    note: f.note.value.trim(),
  });
  const lockBody = (f) => ({
    date: f.date.value, type: "lock", name: f.name.value.trim(),
    amount: f.amount.value.trim(), currency: f.currency.value,
    rate_pct: f.rate_pct.value.trim(), months: f.months.value ? parseInt(f.months.value, 10) : 0,
    note: f.note.value.trim(),
  });

  onSubmit(ctx, main.querySelector("#planSetSharesForm"),
    (f) => ({ path: "plan/actions", body: sharesBody(f), msg: "Дію додано" }));
  onSubmit(ctx, main.querySelector("#planLockForm"),
    (f) => ({ path: "plan/actions", body: lockBody(f), msg: "Замок додано" }));

  onDelete(ctx, main, "[data-delaction]", (b) => ({
    path: "plan/actions/" + b.dataset.delaction,
    confirm: "Видалити цю дію?",
    msg: "Дію видалено",
  }));

  main.querySelectorAll("[data-editaction]").forEach((b) => b.addEventListener("click", () => {
    const a = byId.get(b.dataset.editaction);
    if (!a) return;
    const shares = a.type === "set_shares";
    // usd_share_pct/eur_share_pct — покажчики на бекенді: відсутнє поле
    // означає «не задано», а 0 — задану частку «долара не лишається зовсім».
    // `|| ""` перетворив би нуль на «не чіпати», тобто мовчки змінив би
    // сенс дії, тож перевіряємо саме на null.
    const values = shares
      ? {
        date: a.date, note: a.note || "",
        usd_share_pct: a.usd_share_pct != null ? a.usd_share_pct : "",
        eur_share_pct: a.eur_share_pct != null ? a.eur_share_pct : "",
      }
      : {
        date: a.date, name: a.name || "", note: a.note || "",
        amount: (a.amount || {}).amount || "",
        currency: (a.amount || {}).currency || "UAH",
        rate_pct: a.rate_pct != null ? a.rate_pct : "",
        months: a.months != null ? a.months : 0,
      };
    openEdit(ctx, {
      title: shares ? "Правка зміни часток" : `Правка замка «${esc(a.name || "")}»`,
      fields: shares ? sharesFields(ctx, values) : lockFields(values),
    }, (form2) => ({
      method: "PUT", path: "plan/actions/" + a.id,
      body: shares ? sharesBody(form2) : lockBody(form2), msg: "Дію змінено",
    }));
  }));

  wireLockHint(main.querySelector("#planLockForm"), timeline);
}

// Скільки буде на рахунку до цієї дати — щоб «замкнути 50 000» не
// заводилось наосліп. Число вже приїхало разом зі стрічкою, тож
// додаткового запиту не треба.
function wireLockHint(form, timeline) {
  if (!form) return;
  const hint = form.querySelector("[data-lockhint]");
  if (!hint) return;
  const curve = (timeline || {}).curve || [];
  const update = () => {
    const v = capitalAt(curve, form.date.value);
    if (v == null) {
      // Крива існує лише коли задано ціль і дедлайн, і обривається на
      // дедлайні. Свого числа замість неї не вигадуємо: «капітал сьогодні
      // + план × місяці» було б четвертим означенням росту капіталу,
      // порахованим у браузері.
      hint.textContent = curve.length
        ? "Поза межами прогнозу — він рахується до дедлайну цілі."
        : "Скільки буде на цю дату, видно, коли задано ціль і дедлайн.";
      hint.classList.remove("t-warn");
      return;
    }
    const amount = parseFloat(String(form.amount.value).replace(",", ".")) || 0;
    const over = form.currency.value === "UAH" && amount > v;
    hint.textContent = `За планом на цю дату капітал ≈ ${fmtUAH(v)}`
      + (over ? " — замок більший за нього." : "");
    hint.classList.toggle("t-warn", over);
  };
  for (const el of [form.date, form.amount, form.currency]) {
    el.addEventListener("change", update);
    el.addEventListener("input", update);
  }
  update();
}
