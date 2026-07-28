// Банківські строкові вклади: форма, поповнення, закриття, архів.

import { esc, today, plural, money as fmtMoney } from "../format.js";
import { onSubmit, onDelete } from "../forms.js";
import { disclosure } from "../disclosure.js";


export function depositFormHTML(ctx) {
  return `
    <form id="termDepForm">
      <label>Банк<select name="bank">${ctx.brokerOptions()}</select></label>
      <label>Валюта<select name="currency"><option>UAH</option><option>USD</option><option>EUR</option></select></label>
      <label>Тіло<input name="principal" inputmode="decimal" placeholder="100000.00" required></label>
      <label>Ставка, %<input name="rate_pct" inputmode="decimal" placeholder="16.5" required></label>
      <label>Відкрито<input name="open_date" type="date" value="${today()}" required></label>
      <label>Погашення<input name="maturity_date" type="date" required></label>
      <label>Виплата відсотків<select name="payout">
        <option value="end">у кінці строку</option>
        <option value="monthly">щомісяця</option>
        <option value="quarterly">щокварталу</option>
      </select></label>
      <label style="flex-direction:row;align-items:center;gap:8px">
        <input name="capitalized" type="checkbox" style="width:auto">Капіталізація</label>
      <label style="flex-direction:row;align-items:center;gap:8px">
        <input name="replenishable" type="checkbox" style="width:auto">Поповнюваний</label>
      <label>Податок, %<input name="tax_pct" inputmode="decimal" placeholder="19.5 (за замовч.)"></label>
      <label>Нотатка<input name="note"></label>
      <button type="submit">Додати</button>
    </form>`;
}

// Розірвані вклади — вже не позиція, а історія: згорнуто.
export function closedDepositsHTML(ctx, deposits) {
  const closed = deposits.filter((d) => d.closed_date);
  if (!closed.length) return "";
  return `<div class="card">${disclosure("closeddep", "Закриті достроково", `
    <div class="table-scroll"><table><thead><tr><th>Банк</th><th class="num">Тіло</th><th>Розірвано</th>
      <th class="num">Отримано</th><th></th></tr></thead><tbody>
      ${closed.map((d) => `<tr>
        <td>${esc(d.bank || "—")}</td><td class="num">${fmtMoney(d.principal)}</td>
        <td>${esc(d.closed_date)}</td><td class="num">${fmtMoney(d.closed_amount)}</td>
        <td class="row-actions"><button class="sm warn" data-deldep="${d.id}">✕</button></td></tr>`).join("")}
      </tbody></table></div>`,
    `${closed.length} ${plural(closed.length, "вклад", "вклади", "вкладів")}`)}</div>`;
}

// PUT шле вклад ЦІЛКОМ, тож кожна часткова зміна мусить перекласти всі інші
// поля з уже завантаженого запису. Доти це робилось двічі — окремо при
// перемиканні «поповнюваний» і окремо при закритті, — і забутий у другій
// копії replenishable свого часу мовчки скидав прапорець. Тепер тіло
// збирається в одному місці, а викликач вказує лише те, що змінює.
function depositBody(d, patch) {
  return {
    bank: d.bank, currency: d.principal.currency,
    principal: d.principal.amount, rate_pct: String(d.rate_pct),
    open_date: d.open_date, maturity_date: d.maturity_date,
    payout: d.payout, capitalized: !!d.capitalized,
    replenishable: !!d.replenishable,
    tax_pct: String(d.tax_pct), note: d.note || "",
    closed_date: d.closed_date || "", closed_amount: (d.closed_amount || {}).amount || "",
    ...patch,
  };
}

export function wireDeposits(ctx, main) {
  const byId = new Map((ctx._deposits || []).map((d) => [String(d.id), d]));

  onSubmit(ctx, main.querySelector("#termDepForm"), (f) => ({
    path: "term-deposits",
    body: {
      bank: f.bank.value, currency: f.currency.value,
      principal: f.principal.value.trim(), rate_pct: f.rate_pct.value.trim(),
      open_date: f.open_date.value, maturity_date: f.maturity_date.value,
      payout: f.payout.value, capitalized: f.capitalized.checked,
      replenishable: f.replenishable.checked,
      tax_pct: f.tax_pct.value.trim(), note: f.note.value.trim(),
    },
    msg: "Вклад додано",
  }));

  // Перемикач «поповнюваний» просто на рядку: це властивість вкладу, яку
  // дізнаєшся вже після відкриття, і заводити заради неї окрему форму
  // редагування було б надміру.
  main.querySelectorAll("[data-repl]").forEach((cb) =>
    cb.addEventListener("change", async () => {
      const d = byId.get(cb.dataset.repl);
      if (!d) return;
      try {
        await ctx.api("PUT", "term-deposits/" + d.id,
          depositBody(d, { replenishable: cb.checked }));
        ctx.toast(cb.checked ? "Вклад поповнюваний" : "Вклад не поповнюваний");
        ctx.reload();
      } catch (err) {
        // Галочку повертаємо назад: інакше на екрані лишився б стан, якого
        // на сервері немає.
        ctx.toast(String(err.message || err), false);
        cb.checked = !cb.checked;
      }
    }));

  onDelete(ctx, main, "[data-deldep]", (b) => ({
    path: "term-deposits/" + b.dataset.deldep,
    confirm: "Видалити вклад #" + b.dataset.deldep + "?",
    msg: "Вклад видалено",
  }));

  // Окремих кнопок «Поповнити» й «Закрити» більше немає: обидві форми
  // живуть у рядку-деталях, який відкриває та сама стрілка, що показує
  // лоти в ОВДП. Один жест на всі інструменти замість трьох кнопок.

  // Поповнення: сума в валюті вкладу, за замовчуванням = тіло відкриття.
  main.querySelectorAll("[data-topup-form]").forEach((f) =>
    onSubmit(ctx, f, () => ({
      path: "term-deposits/" + f.dataset.topupForm + "/topups",
      body: { date: f.date.value, amount: f.amount.value.trim() },
      msg: "Поповнення додано",
    })));

  onDelete(ctx, main, "[data-deltopup]", (b) => {
    const [depID, topupID] = b.dataset.deltopup.split(":");
    return {
      path: "term-deposits/" + depID + "/topups/" + topupID,
      msg: "Поповнення видалено",
    };
  });

  // Розірвання = PUT усього вкладу з проставленими closed_*: банк перерахує
  // відсотки сам за штрафною ставкою, ми лише вводимо отриману суму.
  main.querySelectorAll("[data-close-form]").forEach((f) =>
    onSubmit(ctx, f, () => {
      const d = byId.get(f.dataset.closeForm);
      if (!d) return null;
      return {
        method: "PUT", path: "term-deposits/" + d.id,
        body: depositBody(d, {
          closed_date: f.closed_date.value,
          closed_amount: f.closed_amount.value.trim(),
        }),
        msg: "Вклад закрито",
      };
    }));
}


