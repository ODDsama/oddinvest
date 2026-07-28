// Форми ОВДП: купівля з автокомплітом по ISIN і продаж на вторинному
// ринку.

import { esc, today } from "../format.js";
import { onSubmit, onDelete } from "../forms.js";


export function bondBuyFormHTML(ctx, lots) {
  return `
      <form id="lotForm">
        <label style="position:relative">ISIN<input name="isin" required placeholder="UA4000..." autocomplete="off">
          <div id="bondSuggest" class="suggest"></div></label>
        <label>Кількість<input name="qty" type="number" min="1" step="1" required></label>
        <label>Ціна за папір (брудна)<input name="price_per_bond" inputmode="decimal" placeholder="995.00" required></label>
        <label>Комісія (сумарно)<input name="fee" inputmode="decimal" placeholder="0.00"></label>
        <label>Валюта<select name="currency">
          <option value="">авто (з довідника)</option>
          <option value="UAH">UAH</option>
          <option value="USD">USD</option>
          <option value="EUR">EUR</option>
        </select></label>
        <label>Дата купівлі<input name="buy_date" type="date" value="${today()}" required></label>
        <label>Брокер<select name="channel_sel">${ctx.channelOptions(lots)}</select>
          <input name="channel" placeholder="назва каналу" style="margin-top:6px;display:none"></label>
        <label>Нотатка<input name="note"></label>
        <button type="submit">Додати</button>
      </form>
      <div class="muted" id="bondInfo" style="margin-top:8px"></div>`;
}

export function bondSaleFormHTML(ctx, lots) {
  return `
      <form id="saleForm">
        <label>Лот<select name="lot_id" required>
          <option value="">— лот —</option>
          ${lots.filter((l) => l.remaining > 0).map((l) =>
            `<option value="${l.id}" data-cur="${l.price_per_bond.currency}">#${l.id} · ${esc(l.isin)} · зал. ${l.remaining}</option>`).join("")}
        </select></label>
        <label>Дата продажу<input name="sale_date" type="date" value="${today()}" required></label>
        <label>Кількість<input name="qty" type="number" min="1" step="1" required></label>
        <label>Чиста ціна/папір<input name="clean_per_bond" inputmode="decimal" placeholder="1001.50" required></label>
        <label>НКД (сумарно)<input name="accrued" inputmode="decimal" placeholder="0.00"></label>
        <label>Нотатка<input name="note"></label>
        <button type="submit">Записати</button>
      </form>
      <div class="sub" style="margin-top:10px">Записані продажі видно в рядку того паперу, якого вони
        стосуються — розкрий позицію стрілкою.</div>`;
}


export function wireBonds(ctx, main) {
  onDelete(ctx, main, "[data-del]", (b) => ({
    path: "lots/" + b.dataset.del,
    confirm: "Видалити лот #" + b.dataset.del + "?",
    msg: "Лот видалено",
  }));

  // Селектори — від САМОЇ форми, а не від main. Поля звуться загально
  // («isin», «channel»), а розділ тепер зводить в одну сторінку три
  // інструменти: перший-ліпший майбутній інпут із такою назвою тихо
  // перехопив би автопідказку, і зламалось би не там, де змінили.
  const buyForm = main.querySelector("#lotForm");
  const isinInput = buyForm.querySelector('input[name="isin"]');
  const sug = buyForm.querySelector("#bondSuggest");
  const hideSug = () => sug.classList.remove("show");
  let dbt;
  isinInput.addEventListener("input", () => {
    clearTimeout(dbt);
    const q = isinInput.value.trim();
    if (q.length < 2) { hideSug(); return; }
    dbt = setTimeout(async () => {
      try {
        const bonds = await ctx.api("GET", "bonds/search?q=" + encodeURIComponent(q));
        if (!bonds || !bonds.length) { hideSug(); return; }
        sug.innerHTML = bonds.map((b) =>
          `<div class="suggest-item" data-isin="${esc(b.isin)}">${esc(b.isin)} · ${esc(b.descr || "")} · ${b.rate_pct}% · до ${esc(b.maturity)}</div>`).join("");
        sug.classList.add("show");
      } catch (_) { hideSug(); }
    }, 300);
  });
  sug.addEventListener("mousedown", (e) => {
    const it = e.target.closest("[data-isin]");
    if (!it) return;
    e.preventDefault();
    isinInput.value = it.dataset.isin;
    hideSug();
    isinInput.dispatchEvent(new Event("change"));
  });
  isinInput.addEventListener("blur", () => setTimeout(hideSug, 150));

  // авто-заповнення з довідника при виборі ISIN — далі лише коригуєш
  isinInput.addEventListener("change", async () => {
    const isin = isinInput.value.trim();
    const info = main.querySelector("#bondInfo");
    if (!isin) { if (info) info.textContent = ""; return; }
    try {
      const b = await ctx.api("GET", "bonds/" + encodeURIComponent(isin));
      if (!b || !b.nominal) return;
      const f = buyForm;
      if (["UAH", "USD", "EUR"].includes(b.nominal.currency)) f.currency.value = b.nominal.currency;
      if (!f.price_per_bond.value.trim()) f.price_per_bond.value = b.nominal.amount;
      if (info) info.textContent = `${esc(b.descr || "")} · ${b.rate_pct}% · погашення ${esc(b.maturity)} · номінал ${fmtMoney(b.nominal)}`;
    } catch (_) { if (info) info.textContent = ""; }
  });

  // «інший…» відкриває поле для нової назви каналу
  const chSel = buyForm.querySelector('[name="channel_sel"]');
  const chIn = buyForm.querySelector('[name="channel"]');
  if (chSel && chIn) {
    chSel.addEventListener("change", () => {
      const other = chSel.value === "__other__";
      chIn.style.display = other ? "" : "none";
      if (other) { chIn.value = ""; chIn.focus(); }
    });
  }

  onSubmit(ctx, buyForm, (f) => ({
    path: "lots",
    body: {
      isin: f.isin.value.trim(), qty: parseInt(f.qty.value, 10),
      price_per_bond: f.price_per_bond.value.trim(), fee: f.fee.value.trim(),
      currency: f.currency.value.trim(), buy_date: f.buy_date.value,
      // «Інший» відкриває текстове поле поруч із випадайкою.
      channel: f.channel_sel.value === "__other__"
        ? f.channel.value.trim()
        : f.channel_sel.value.trim(),
      note: f.note.value.trim(),
    },
    msg: "Лот додано",
  }));

  onSubmit(ctx, main.querySelector("#saleForm"), (f) => {
    // Валюта продажу — з лота, а не з форми: продати можна лише те, що є.
    const opt = f.lot_id.selectedOptions[0];
    return {
      path: "sales",
      body: {
        lot_id: parseInt(f.lot_id.value, 10), sale_date: f.sale_date.value,
        qty: parseInt(f.qty.value, 10), clean_per_bond: f.clean_per_bond.value.trim(),
        accrued: f.accrued.value.trim(), currency: opt ? opt.dataset.cur : "UAH",
        note: f.note.value.trim(),
      },
      msg: "Продаж записано",
    };
  });
}


