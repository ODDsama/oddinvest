// Розділ «Гроші» — де вони лежать і звідки взялись.
//
// Баланси по брокерах (роздільні: гривня в одного не купує папір в
// іншого), рухи, конвертації, звірка з тим, що показує брокер, і імпорт
// виписки.
//
// Імпорт мав власну вкладку — цілу вкладку на одну дію, до якої
// звертаються раз на місяць. Він тут тому, що це не окрема тема, а
// СПОСІБ поповнити цей самий журнал: після імпорту дивишся на ті самі
// рухи й ту саму звірку, які лежать вище.

import {
  esc, curSym, today, dayMonth,
  uah2 as fmtUAH, cur2 as fmtCur, money as fmtMoney,
} from "../format.js";
import { infoBtn } from "../info.js";
import { tile } from "../components.js";
import { fundStatementHTML, wireFundOps, setFundOps } from "./portfolio.js";

// ---------- РАХУНОК ----------
// Баланси по брокерах: гроші в одного не купують папір в іншого, тож
// «вистачає / не вистачає» має сенс лише в розрізі рахунку.
export function brokerBalancesHTML(ctx) {
  const s = ctx.summary || {};
  const brokers = s.brokers || {};
  const names = Object.keys(brokers).sort((a, b) => a.localeCompare(b, "uk"));
  if (!names.length) return "";
  const rmin = s.reinvest_min || {};
  const sym = { UAH: "₴", USD: "$", EUR: "€" };
  const rows = names.map((b) => {
    const cur = brokers[b] || {};
    const parts = Object.keys(cur).sort().map((c) => {
      const v = cur[c], min = rmin[c] || 0;
      const enough = min > 0 && v >= min;
      const hint = min > 0
        ? (enough ? `вистачає на ${Math.floor(v / min)}` : `до паперу ще ${fmtCur(min - v, sym[c] || c)}`)
        : "";
      return `<div class="pv-row"><span>${esc(c)} · <b>${fmtCur(v, sym[c] || c)}</b></span>
        <span class="${enough ? "" : "muted"}" style="${enough ? "color:var(--oi-ok)" : ""}">${hint}</span></div>`;
    }).join("");
    return `<div style="margin-bottom:14px"><div style="margin-bottom:4px"><b>${esc(b)}</b></div>${parts}</div>`;
  }).join("");
  return `<div class="card"><h2>Рахунки по брокерах</h2>
    <div class="muted" style="margin-bottom:10px">Гроші в одного брокера не купують папір в іншого — тому баланси роздільні.</div>
    ${rows}</div>`;
}

// Звірка: рахунок за записами проти того, що показує брокер.
// Коригування — звичайне поповнення з поміткою, а не окрема сутність:
// так розбіжність лишається видимою в історії, а не ховається.
export function reconcileHTML(ctx) {
  const brokers = (ctx.summary || {}).brokers || {};
  const rows = Object.entries(brokers).flatMap(([b, byCur]) =>
    Object.entries(byCur).map(([c, v]) => ({ b, c, v })));
  if (!rows.length) return "";
  return `<div class="card"><h2 class="h-row" style="justify-content:space-between">
    <span>Звірка рахунку ${infoBtn("reconcile")}</span></h2>
    <table><thead><tr><th>Брокер</th><th class="num">За записами</th>
      <th class="num">Фактично</th><th class="num">Розбіжність</th><th></th></tr></thead>
    <tbody>${rows.map((r) => `<tr data-rec="${esc(r.b)}|${esc(r.c)}">
      <td>${esc(r.b)} ${curSym(r.c)}</td>
      <td class="num">${fmtCur(r.v, curSym(r.c))}</td>
      <td class="num"><input class="recAct" inputmode="decimal" style="width:110px;text-align:right"
        data-expected="${r.v}" placeholder="—"></td>
      <td class="num recDiff muted">—</td>
      <td class="num"><button class="recFix" disabled>виправити</button></td>
    </tr>`).join("")}</tbody></table></div>`;
}

export function wireReconcile(ctx, main) {
  main.querySelectorAll("tr[data-rec]").forEach((tr) => {
    const inp = tr.querySelector(".recAct");
    const out = tr.querySelector(".recDiff");
    const btn = tr.querySelector(".recFix");
    const [broker, currency] = tr.dataset.rec.split("|");
    const recalc = () => {
      const raw = inp.value.trim().replace(/\s/g, "").replace(",", ".");
      const actual = Number(raw);
      if (!raw || Number.isNaN(actual)) {
        out.textContent = "—"; out.className = "num recDiff muted"; btn.disabled = true;
        return null;
      }
      const diff = Math.round((actual - Number(inp.dataset.expected)) * 100) / 100;
      out.textContent = diff === 0 ? "сходиться" : (diff > 0 ? "+" : "") + fmtCur(diff, curSym(currency));
      out.className = "num recDiff" + (diff === 0 ? " ok" : "");
      btn.disabled = diff === 0;
      return diff;
    };
    inp.addEventListener("input", recalc);
    btn.addEventListener("click", async () => {
      const diff = recalc();
      if (!diff) return;
      btn.disabled = true;
      try {
        await ctx.api("POST", "deposits", {
          amount: String(diff), currency, broker,
          note: diff > 0 ? "звірка: незаписане надходження" : "звірка: незаписана витрата",
        });
        ctx.toast("Коригування додано");
        await ctx.reload();
      } catch (err) {
        ctx.toast(String(err.message || err), false);
        btn.disabled = false;
      }
    });
  });
}

// Імпорт виписки. Два кроки навмисно: спершу показати, що буде
// зроблено, і лише потім писати. Ціна помилки тут — подвоєний баланс,
// а він знаходиться не одразу.
export function importHTML(ctx) {
  return `<div class="card"><h2 class="h-row" style="justify-content:space-between">
    <span>Імпорт виписки ${infoBtn("import")}</span></h2>
    <div class="muted" style="font-size:12px;margin-bottom:8px">Файл Inzhur (.xlsx). Спершу перегляд — нічого не записується.</div>
    <div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap">
      <input type="file" id="impFile" accept=".xlsx">
      <button id="impPreview">Переглянути</button>
    </div>
    <div class="muted" style="font-size:12px;margin-top:8px;display:flex;gap:8px;align-items:center;flex-wrap:wrap">
      Враховувати зміни від <input type="date" id="impSince" style="width:150px">
      <span>рухається сама після кожного імпорту</span>
    </div>
    <div id="impOut" style="margin-top:10px"></div></div>`;
}

export function wireImport(ctx, main) {
  const file = main.querySelector("#impFile");
  const out = main.querySelector("#impOut");
  if (!file || !out) return;

  // Водяний знак: показуємо поточний і даємо посунути руками — інакше
  // «перезавантажити позаминулий місяць» стало б неможливим узагалі.
  const since = main.querySelector("#impSince");
  if (since) {
    ctx.api("GET", "settings")
      .then((s) => { if (s && s.import_since) since.value = s.import_since; })
      .catch(() => {});
    since.addEventListener("change", async () => {
      try { await ctx.api("PUT", "settings", { import_since: since.value }); ctx.toast("Дату змінено"); }
      catch (err) { ctx.toast(String(err.message || err), false); }
    });
  }

  const send = async (dry) => {
    if (!file.files || !file.files[0]) { ctx.toast("Обери файл", false); return null; }
    const fd = new FormData();
    fd.append("file", file.files[0]);
    const resp = await ctx.store.raw(
      "import/inzhur" + (dry ? "?dry=1" : ""), { method: "POST", body: fd });
    if (!resp.ok) throw new Error(`${resp.status}: ${(await resp.text()).slice(0, 300)}`);
    // Справжній імпорт міняє геть усе — лоти, рухи, фонди, зведення.
    // Перегляд (dry) не міняє нічого, тож і кеш чіпати нема за що.
    if (!dry) ctx.store.invalidate();
    return resp.json();
  };

  const KIND = { fund_buy: "купівля", fund_sell: "продаж", dividend: "дивіденд",
    deposit: "поповнення", withdrawal: "виведення" };
  const render = (res, dry) => {
    const rows = (res.rows || []).map((r) => {
      const tag = r.conflict
        ? `<div style="color:var(--oi-danger);font-size:11px">⚠ ${esc(r.conflict)}</div>`
        : r.exists ? `<span class="muted" style="font-size:11px">вже є</span>` : "";
      return `<div style="margin-bottom:6px">
        <div style="display:flex;justify-content:space-between;gap:8px">
          <span>${dayMonth(r.date)} · ${KIND[r.kind] || r.kind}${
            r.fund ? ` <span class="muted">${esc(r.fund)}</span>` : ""}${
            r.qty ? ` <span class="muted">${r.qty} серт.</span>` : ""}</span>
          <span><b>${esc(r.amount)}</b>${r.tax && r.tax !== "0.00" ? ` <span class="muted" style="font-size:11px">податок ${esc(r.tax)}</span>` : ""} ${r.exists && !r.conflict ? `<span class="muted" style="font-size:11px">вже є</span>` : ""}</span>
        </div>${r.conflict ? tag : ""}</div>`;
    }).join("");
    const skipped = (res.skipped || []).map((s) =>
      `<div class="sub-xs">${dayMonth(s.Date || s.date)} · ${esc(s.Op || s.op)} — ${esc(s.Reason || s.reason)}</div>`).join("");
    const conflicts = (res.rows || []).filter((r) => r.conflict).length;
    if (!dry && res.since && since) since.value = res.since;
    out.innerHTML = `
      <div style="margin-bottom:8px">Знайдено ${(res.rows || []).length} операцій · <b>${res.new}</b> нових${
        conflicts ? ` · <span style="color:var(--oi-danger)">${conflicts} з конфліктом</span>` : ""}</div>
      ${res.before ? `<div class="muted" style="font-size:12px;margin-bottom:8px">${res.before} рядків старші за ${
        dayMonth(res.since)} — не розглядались</div>` : ""}
      ${rows}
      ${skipped ? `<div style="border-top:1px solid var(--oi-border);margin-top:8px;padding-top:6px">
        <div class="muted" style="font-size:12px;margin-bottom:4px">пропущено:</div>${skipped}</div>` : ""}
      ${dry && res.new > 0 ? `<button id="impGo" style="margin-top:10px">Імпортувати ${res.new}</button>` : ""}
      ${!dry ? `<div style="margin-top:8px;color:var(--oi-ok)">Записано ${res.imported}</div>` : ""}`;
    const go = out.querySelector("#impGo");
    if (go) {
      go.addEventListener("click", async () => {
        go.disabled = true;
        try { render(await send(false), false); ctx.toast("Імпортовано"); await ctx.reload(); }
        catch (err) { ctx.toast(String(err.message || err), false); go.disabled = false; }
      });
    }
  };

  main.querySelector("#impPreview").addEventListener("click", async (e) => {
    e.target.disabled = true;
    try { const res = await send(true); if (res) render(res, true); }
    catch (err) { ctx.toast(String(err.message || err), false); }
    finally { e.target.disabled = false; }
  });
}

export async function renderMoney(ctx, main) {
  const [deposits, conversions, ops] = await Promise.all([
    ctx.api("GET", "deposits").catch(() => []),
    ctx.api("GET", "conversions").catch(() => []),
    ctx.api("GET", "funds").catch(() => []),
  ]);
  setFundOps(ops);
  const s = ctx.summary || {};
  const a = s.accounts || {};
  const curOpts = (sel) => ["UAH", "USD", "EUR"].map((c) => `<option${c === sel ? " selected" : ""}>${c}</option>`).join("");
  const moves = [
    ...deposits.map((d) => ({ date: d.date, id: d.id, kind: "dep", amount: d.amount, note: d.note })),
    ...conversions.map((c) => ({ date: c.date, id: c.id, kind: "conv", from: c.from, to: c.to, note: c.note })),
  ].sort((x, y) => (x.date < y.date ? 1 : x.date > y.date ? -1 : y.id - x.id));
  main.innerHTML = `
    <div class="card">
      <h2>Рахунок (гаманець)</h2>
      <div class="tiles" style="margin:0 0 4px">
        <div class="tile"><div class="lbl">UAH</div><div class="val">${fmtUAH(a.UAH || 0)}</div></div>
        <div class="tile"><div class="lbl">USD</div><div class="val">${fmtCur(a.USD || 0, "$")}</div></div>
        <div class="tile"><div class="lbl">EUR</div><div class="val">${fmtCur(a.EUR || 0, "€")}</div></div>
        <div class="tile"><div class="lbl">Разом (грн-екв.)</div><div class="val">${fmtUAH(s.account_uah || 0)}</div></div>
        <div class="tile"><div class="lbl">Не перевкладено</div><div class="val">${fmtUAH(s.uninvested_uah || 0)}</div>
          <div class="sub">надійшло й ще не вкладено</div></div>
      </div>
    </div>

    ${brokerBalancesHTML(ctx)}

    ${reconcileHTML(ctx)}


    <div class="card">
      <h2>Додати рух</h2>
      <div class="muted" style="margin-bottom:10px">Поповнення (+) / зняття (−) у своїй валюті. Купівля лота й купони рухають рахунок автоматично.</div>
      <form id="depForm">
        <label>Сума (+ / −)<input name="amount" inputmode="decimal" placeholder="5000.00" required></label>
        <label>Валюта<select name="currency">${curOpts("UAH")}</select></label>
        <label>Брокер<select name="broker">${ctx.brokerOptions()}</select></label>
        <label>Дата<input name="date" type="date" value="${today()}"></label>
        <label>Нотатка<input name="note"></label>
        <button type="submit">Записати</button>
      </form>
    </div>

    <div class="card">
      <h2>Конвертація валют</h2>
      <div class="muted" style="margin-bottom:10px">Віддав → отримав (курс рахується сам із сум — те, що реально сталося на Monobank).</div>
      <form id="convForm">
        <label>Віддав<input name="from_amount" inputmode="decimal" placeholder="40000.00" required></label>
        <label>Валюта<select name="from_currency">${curOpts("UAH")}</select></label>
        <label>Отримав<input name="to_amount" inputmode="decimal" placeholder="1000.00" required></label>
        <label>Валюта<select name="to_currency">${curOpts("USD")}</select></label>
        <label>Брокер<select name="broker">${ctx.brokerOptions()}</select></label>
        <label>Дата<input name="date" type="date" value="${today()}"></label>
        <label>Нотатка<input name="note"></label>
        <button type="submit">Записати</button>
      </form>
    </div>

    <div class="card">
      <h2>Історія рухів</h2>
      ${moves.length ? `<table><thead><tr>
        <th>Дата</th><th>Тип</th><th>Сума</th><th>Нотатка</th><th></th></tr></thead><tbody>
        ${moves.map((m) => {
          if (m.kind === "dep") {
            const label = Number(m.amount.amount) >= 0 ? "Поповнення" : "Зняття";
            return `<tr><td>${esc(m.date)}</td><td>${label}</td><td class="num">${fmtMoney(m.amount)}</td>
              <td>${esc(m.note || "")}</td>
              <td class="row-actions"><button class="sm warn" data-deldep="${m.id}">✕</button></td></tr>`;
          }
          const rate = Number(m.from.amount) / Number(m.to.amount);
          return `<tr><td>${esc(m.date)}</td><td>Конвертація</td>
            <td class="num">${fmtMoney(m.from)} → ${fmtMoney(m.to)}</td>
            <td>${esc(m.note || "")}${isFinite(rate) ? ` (${rate.toFixed(4)})` : ""}</td>
            <td class="row-actions"><button class="sm warn" data-delconv="${m.id}">✕</button></td></tr>`;
        }).join("")}</tbody></table>` : `<div class="muted">Рухів ще немає.</div>`}
    </div>

    ${importHTML(ctx)}
    ${fundStatementHTML(ctx)}`;

  main.querySelector("#depForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target;
    try {
      await ctx.api("POST", "deposits", {
        amount: f.amount.value.trim(), currency: f.currency.value, broker: f.broker.value,
        date: f.date.value, note: f.note.value.trim(),
      });
      ctx.toast("Рух записано"); ctx.reload();
    } catch (err) { ctx.toast(String(err.message || err), false); }
  });
  main.querySelector("#convForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target;
    try {
      await ctx.api("POST", "conversions", {
        from_amount: f.from_amount.value.trim(), from_currency: f.from_currency.value,
        to_amount: f.to_amount.value.trim(), to_currency: f.to_currency.value,
        broker: f.broker.value, date: f.date.value, note: f.note.value.trim(),
      });
      ctx.toast("Конвертацію записано"); ctx.reload();
    } catch (err) { ctx.toast(String(err.message || err), false); }
  });
  main.querySelectorAll("[data-deldep]").forEach((b) =>
    b.addEventListener("click", async () => {
      try { await ctx.api("DELETE", "deposits/" + b.dataset.deldep); ctx.toast("Рух видалено"); ctx.reload(); }
      catch (err) { ctx.toast(String(err.message || err), false); }
    }));
  main.querySelectorAll("[data-delconv]").forEach((b) =>
    b.addEventListener("click", async () => {
      try { await ctx.api("DELETE", "conversions/" + b.dataset.delconv); ctx.toast("Конвертацію видалено"); ctx.reload(); }
      catch (err) { ctx.toast(String(err.message || err), false); }
    }));
  wireReconcile(ctx, main);
  wireImport(ctx, main);
  wireFundOps(ctx, main);
}

