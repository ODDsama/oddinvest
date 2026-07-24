// Розділ «Налаштування» — як воно налаштоване.
//
// Цілі й допущення, довідники брокерів і фондів, бекап. Довідники живуть
// саме тут, бо перейменування підхоплюють усі записи разом: назва — це
// налаштування, а не властивість окремого лота.

import { esc, today, pct } from "../format.js";
import { tile } from "../components.js";

// Брокери й фонди — довідники з власними ендпойнтами. Раніше брокери
// жили CSV-рядком у налаштуваннях, тож «перейменувати» означало лише
// підмінити підказку: у самих лотах лишалась стара назва. Тепер записи
// тримаються за id, і перейменування підхоплюють усі разом.
//
// Назва — редаговане поле, збереження по Enter або втраті фокуса:
// окрема кнопка «зберегти» тут лише додала б клік.
export function catalogRowHTML(item, currency) {
  const cur = currency !== undefined
    ? `<input class="cat-cur" value="${esc(currency)}" style="width:80px">` : "";
  return `<div class="pv-row" data-cat="${item.id}">
    <span style="display:flex;gap:8px"><input class="cat-name" value="${esc(item.name)}" style="width:200px">${cur}</span>
    <button class="sm warn" data-catdel="${item.id}">✕</button></div>`;
}

export function catalogsHTML(ctx) {
  const brokers = (ctx.brokers || []).length
    ? ctx.brokers.map((b) => catalogRowHTML(b)).join("")
    : `<div class="sub">Ще немає брокерів. Додай mono, inzhur…</div>`;
  const funds = (ctx.fundCatalog || []).length
    ? ctx.fundCatalog.map((f) => catalogRowHTML(f, f.currency)).join("")
    : `<div class="sub">Фондів ще немає — вони зʼявляться після першої купівлі сертифікатів.</div>`;
  return `<div class="card" id="brokerCard">
      <h2>Брокери</h2>
      <div class="muted" style="margin-bottom:10px">Рахунки, на яких лежать гроші й папери. Перейменування підхоплюють
        усі записи разом. Видалити можна лише того, за ким не лишилось записів.</div>
      ${brokers}
      <form id="brokerAddForm" style="margin-top:10px;display:flex;gap:8px">
        <input name="broker" placeholder="назва брокера" style="flex:0 0 200px" autocomplete="off">
        <button type="submit">Додати</button>
      </form>
    </div>
    <div class="card" id="fundCatalogCard">
      <h2>Фонди</h2>
      <div class="muted" style="margin-bottom:10px">Заводяться самі при першій операції. Тут виправляють назву й валюту —
        помилка в назві більше не розщеплює позицію надвоє.</div>
      ${funds}
    </div>`;
}

// Спільна прошивка для обох довідників: різняться лише ендпойнтом.
export function bindCatalog(ctx, card, path, withCurrency) {
  if (!card) return;
  card.querySelectorAll("[data-cat]").forEach((row) => {
    const id = row.dataset.cat;
    const name = row.querySelector(".cat-name");
    const cur = row.querySelector(".cat-cur");
    const key = () => name.value.trim() + "|" + (cur ? cur.value.trim() : "");
    row.dataset.was = key();
    const commit = async () => {
      if (!name.value.trim() || key() === row.dataset.was) return;
      row.dataset.was = key();
      const body = { name: name.value.trim() };
      if (withCurrency) body.currency = cur ? cur.value.trim() : "";
      try { await ctx.api("PUT", `${path}/${id}`, body); ctx.toast("Збережено"); ctx.reload(); }
      catch (err) { ctx.toast(String(err.message || err), false); }
    };
    for (const el of [name, cur]) {
      if (!el) continue;
      el.addEventListener("blur", commit);
      el.addEventListener("keydown", (e) => { if (e.key === "Enter") el.blur(); });
    }
    row.querySelector("[data-catdel]").addEventListener("click", async () => {
      if (!confirm(`Видалити «${name.value}»?`)) return;
      try { await ctx.api("DELETE", `${path}/${id}`); ctx.toast("Видалено"); ctx.reload(); }
      catch (err) { ctx.toast(String(err.message || err), false); }
    });
  });
}

export function bindBrokers(ctx, main) {
  bindCatalog(ctx, main.querySelector("#brokerCard"), "brokers", false);
  bindCatalog(ctx, main.querySelector("#fundCatalogCard"), "fund-catalog", true);
  const form = main.querySelector("#brokerAddForm");
  if (!form) return;
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = e.target.broker.value.trim();
    if (!name) return;
    if ((ctx.brokers || []).some((b) => b.name.toLowerCase() === name.toLowerCase())) {
      ctx.toast("Такий брокер уже є", false);
      return;
    }
    try { await ctx.api("POST", "brokers", { name }); ctx.toast("Брокера додано"); ctx.reload(); }
    catch (err) { ctx.toast(String(err.message || err), false); }
  });
}

export function bindBackup(ctx, main) {
  // Експорт: тягнемо через проксі (з HA-авторизацією) і зберігаємо як файл.
  main.querySelector("#btnExport").addEventListener("click", async () => {
    try {
      const resp = await ctx.store.raw("backup");
      if (!resp.ok) throw new Error(await resp.text());
      const blob = await resp.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "oddinvest-backup-" + today() + ".json";
      a.click();
      URL.revokeObjectURL(url);
      ctx.toast("Бекап завантажено");
    } catch (err) { ctx.toast(String(err.message || err), false); }
  });

  // Імпорт: читаємо файл, підтверджуємо (замінює ВСЕ), відновлюємо.
  main.querySelector("#importFile").addEventListener("change", async (e) => {
    const file = e.target.files && e.target.files[0];
    if (!file) return;
    const msg = main.querySelector("#restoreMsg");
    try {
      const text = await file.text();
      const data = JSON.parse(text);
      const n = (data.lots || []).length;
      if (!confirm(`Відновити з бекапу? Це ЗАМІНИТЬ усі поточні дані (${n} лот(ів) у файлі). Дію не скасувати.`)) {
        e.target.value = "";
        return;
      }
      const res = await ctx.api("POST", "restore", data);
      const r = res.restored || {};
      msg.textContent = `Відновлено: ${r.lots || 0} лот(ів), ${r.deposits || 0} поповн., ${r.conversions || 0} конверт., ${r.snapshots || 0} знімк.`;
      ctx.toast("Відновлено з бекапу");
      ctx.reload();
    } catch (err) {
      msg.textContent = "Помилка: " + String(err.message || err);
      ctx.toast("Не вдалось відновити", false);
    }
    e.target.value = "";
  });
}

// ---------- НАЛАШТУВАННЯ ----------
// Знецінення — не просто ще одне поле: це дільник під КОЖНИМ реальним
// числом застосунку. Доти екран про це мовчав, а сама шістка бралась
// нізвідки, і перевірити її не було де.
function devalHTML(d) {
  if (!d) return "";
  const src = {
    manual: "задано руками",
    measured: "виміряно з курсів НБУ",
    default: "припущення — даних ще замало",
  }[d.source] || d.source;
  const rows = (d.windows || []).map((w) => `<tr>
    <td>${esc(w.label)}</td><td class="num">${pct(w.pct)}</td>
    <td class="muted sub-xs">${esc(w.from)} → ${esc(w.to)}</td></tr>`).join("");
  return `<div class="card">
    <h2>Знецінення гривні</h2>
    <div class="tiles flush" style="margin-bottom:12px">
      ${tile("Чинне значення", pct(d.effective_pct), `<div class="sub">${src}</div>`)}
    </div>
    <div class="muted" style="font-size:13px;margin-bottom:10px">Це число ділить <b>кожну реальну
      дохідність</b> у застосунку й керує прогнозом. Порожнє поле «Гривня слабшає» вище означає
      «бери виміряне» — саме так його й повертають назад на автоматику.</div>
    ${rows ? `<div class="table-scroll"><table><thead><tr>
        <th>Вікно</th><th class="num">%/рік</th><th>Курс від → до</th></tr></thead>
      <tbody>${rows}</tbody></table></div>
      <div class="sub" style="margin-top:8px">Застосунок бере <b>десятирічне</b> вікно, і різниця між
        рядками пояснює чому: гривня падає стрибками, тож коротке вікно ловить або стрибок, або
        затишшя між ними. Довге усереднює і те, і те.</div>`
      : `<div class="muted">${esc(d.note || "історії курсу ще немає")}</div>`}
  </div>`;
}

export async function renderSettings(ctx, main) {
  const [s, deval] = await Promise.all([
    ctx.api("GET", "settings"),
    ctx.api("GET", "devaluation").catch(() => null),
  ]);
  main.innerHTML = `
    <div class="card">
      <h2>Налаштування</h2>
      <form id="setForm">
        <label>Цільова частка USD, %<input name="usd_target_share_pct" inputmode="decimal" value="${esc(s.usd_target_share_pct || "")}"></label>
        <label>Цільова частка EUR, %<input name="eur_target_share_pct" inputmode="decimal" value="${esc(s.eur_target_share_pct || "")}"></label>
        <label>Ціль, ₴<input name="goal_amount_uah" inputmode="decimal" placeholder="скільки хочу накопичити" value="${esc(s.goal_amount_uah || "")}"></label>
        <label>Дедлайн — коли<input name="goal_date" type="date" value="${esc(s.goal_date || "")}"></label>
        <label>Гривня слабшає, %/рік<input name="uah_devaluation_pct" inputmode="decimal" placeholder="порожньо = виміряне" value="${esc(s.uah_devaluation_pct || "")}"></label>
        <label>Довгострокова ставка ОВДП, %<input name="terminal_rate_pct" inputmode="decimal" placeholder="порожньо = 11" value="${esc(s.terminal_rate_pct || "")}"></label>
        <label>Ставка сповзає туди за, років<input name="rate_glide_years" inputmode="decimal" placeholder="порожньо = 5" value="${esc(s.rate_glide_years || "")}"></label>
        <button type="submit">Зберегти</button>
      </form>
    </div>

    ${devalHTML(deval)}

    ${catalogsHTML(ctx)}

    <div class="card">
      <h2>Бекап</h2>
      <div class="muted" style="margin-bottom:10px">Твої лоти, поповнення, конвертації, налаштування й статуси виплат.
        Довідник НБУ не входить — він відновлюється сам. Плюс сервер щодня пише копію поряд із БД (потрапляє в бекап Proxmox).</div>
      <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">
        <button type="button" id="btnExport">Завантажити бекап</button>
        <label style="display:inline-block"><span class="muted" style="font-size:13px">Відновити з файлу:</span>
          <input type="file" id="importFile" accept="application/json,.json" style="margin-top:6px"></label>
      </div>
      <div class="muted" id="restoreMsg" style="margin-top:8px;font-size:13px"></div>
    </div>`;
  bindBackup(ctx, main);
  bindBrokers(ctx, main);
  main.querySelector("#setForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target;
    // Збираємо payload лише з полів, які РЕАЛЬНО є у формі. Так
    // видалення поля не роняє сабміт і — головне — не шле порожнє
    // значення, яке затерло б налаштування (PUT часткове).
    // «channels» тут свідомо немає: брокерами керує окрема картка.
    const payload = {};
    for (const k of ["usd_target_share_pct", "eur_target_share_pct",
      "goal_amount_uah", "goal_date",
      "uah_devaluation_pct", "terminal_rate_pct", "rate_glide_years"]) {
      if (f.elements[k]) payload[k] = f.elements[k].value.trim();
    }
    try {
      await ctx.api("PUT", "settings", payload);
      ctx.toast("Налаштування збережено"); ctx.reload();
    } catch (err) { ctx.toast(String(err.message || err), false); }
  });
}

