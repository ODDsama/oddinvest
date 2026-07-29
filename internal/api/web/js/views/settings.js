// Розділ «Налаштування» — як воно налаштоване.
//
// Цілі й допущення, довідники брокерів і фондів, бекап. Довідники живуть
// саме тут, бо перейменування підхоплюють усі записи разом: назва — це
// налаштування, а не властивість окремого лота.

import { esc, today, pct } from "../format.js";
import { tile } from "../components.js";
import { onSubmit, onDelete, apply } from "../forms.js";
import { strategyCardHTML, wireStrategy } from "./strategy.js";

// Брокери й фонди — довідники з власними ендпойнтами. Раніше брокери
// жили CSV-рядком у налаштуваннях, тож «перейменувати» означало лише
// підмінити підказку: у самих лотах лишалась стара назва. Тепер записи
// тримаються за id, і перейменування підхоплюють усі разом.
//
// Назва — редаговане поле, збереження по Enter або втраті фокуса:
// окрема кнопка «зберегти» тут лише додала б клік.
// Поля рядка описуються списком, а не окремими класами: у фонда їх уже
// пʼять, і кожне нове інакше вимагало б правки і розмітки, і проводки.
// data-field — це ключ у тілі запиту, тож обидва боки лишаються в одному
// місці.
export function catalogRowHTML(item, fields = []) {
  const inputs = fields.map((f) =>
    `<input class="cat-f" data-field="${f.key}"${f.num ? ' data-num="1"' : ""}
       value="${esc(f.value ?? "")}" placeholder="${esc(f.ph || "")}"
       title="${esc(f.title || "")}" style="width:${f.w || 90}px">`).join("");
  return `<div class="pv-row" data-cat="${item.id}">
    <span style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">
      <input class="cat-name" value="${esc(item.name)}" style="width:190px">${inputs}</span>
    <button class="sm warn" data-catdel="${item.id}">✕</button></div>`;
}

export function catalogsHTML(ctx) {
  const brokers = (ctx.brokers || []).length
    ? ctx.brokers.map((b) => catalogRowHTML(b)).join("")
    : `<div class="sub">Ще немає брокерів. Додай mono, inzhur…</div>`;
  const fundFields = (f) => [
    { key: "currency", value: f.currency, w: 70, title: "Валюта сертифіката" },
    { key: "expected_yield_pct", w: 84, ph: "дохідн., %",
      value: f.expected_yield_bp ? (f.expected_yield_bp / 100).toFixed(2).replace(/\.?0+$/, "") : "",
      title: "Обіцяна фондом дохідність. Використовується, доки не набереться історії для виміряної" },
    { key: "expected_yield_currency", value: f.expected_yield_currency, w: 70, ph: "у валюті",
      title: "Валюта, В ЯКІЙ обіцяна дохідність. USD для фонду, чия ціна йде за курсом НБУ: тоді гривневе знецінення до неї не застосовується" },
    { key: "payout_day", value: f.payout_day || "", w: 76, ph: "день", num: true,
      title: "Число місяця, коли платять дивіденди. Вихідні переносяться на робочий день" },
  ];
  const funds = (ctx.fundCatalog || []).length
    ? ctx.fundCatalog.map((f) => catalogRowHTML(f, fundFields(f))).join("")
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
      <div class="muted" style="margin-bottom:10px">Заводяться самі при першій операції. Тут виправляють назву й валюту
        (помилка в назві більше не розщеплює позицію надвоє) і задають те, чого з операцій не вивести:
        <b>обіцяну фондом дохідність</b> та <b>день виплати</b> дивідендів.</div>
      <div class="sub" style="margin-bottom:10px">Обіцянка потрібна, доки не набереться історії для виміряної повної
        дохідності (30 днів, зважених грошима): без неї фонд порівнюється лише за дивідендами й виглядає гіршим,
        ніж є. Якщо вона у валюті — вкажіть її, і гривневе знецінення до неї не застосується.</div>
      ${funds}
    </div>`;
}

// Спільна прошивка для обох довідників: різняться лише ендпойнтом.
export function bindCatalog(ctx, card, path) {
  if (!card) return;
  card.querySelectorAll("[data-cat]").forEach((row) => {
    const id = row.dataset.cat;
    const name = row.querySelector(".cat-name");
    const fields = [...row.querySelectorAll(".cat-f")];
    const key = () => [name.value.trim(), ...fields.map((f) => f.value.trim())].join("|");
    row.dataset.was = key();
    const commit = async () => {
      if (!name.value.trim() || key() === row.dataset.was) return;
      row.dataset.was = key();
      const body = { name: name.value.trim() };
      // Числові поля йдуть числом: порожнє = 0, тобто «не задано».
      // Відсотки лишаються рядком — так порожнє поле відрізняється від нуля.
      fields.forEach((f) => {
        body[f.dataset.field] = f.dataset.num ? Number(f.value.trim()) || 0 : f.value.trim();
      });
      await apply(ctx, { method: "PUT", path: `${path}/${id}`, body }, "Збережено");
    };
    for (const el of [name, ...fields]) {
      if (!el) continue;
      el.addEventListener("blur", commit);
      el.addEventListener("keydown", (e) => { if (e.key === "Enter") el.blur(); });
    }
    onDelete(ctx, row, "[data-catdel]", () => ({
      path: `${path}/${id}`, confirm: `Видалити «${name.value}»?`,
    }));
  });
}

export function bindBrokers(ctx, main) {
  bindCatalog(ctx, main.querySelector("#brokerCard"), "brokers");
  bindCatalog(ctx, main.querySelector("#fundCatalogCard"), "fund-catalog");
  onSubmit(ctx, main.querySelector("#brokerAddForm"), (f) => {
    const name = f.broker.value.trim();
    if (!name) return null;
    // Тезка ловиться тут, а не бекендом: повідомлення зрозуміліше, і зайвий
    // запит не летить.
    if ((ctx.brokers || []).some((b) => b.name.toLowerCase() === name.toLowerCase())) {
      ctx.toast("Такий брокер уже є", false);
      return null;
    }
    return { path: "brokers", body: { name }, msg: "Брокера додано" };
  });
}

export function bindBackup(ctx, main) {
  // Експорт: тягнемо через проксі (з HA-авторизацією) і зберігаємо як файл.
  main.querySelector("#btnExport")?.addEventListener("click", async () => {
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
  main.querySelector("#importFile")?.addEventListener("change", async (e) => {
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
    ctx.soft("devaluation", null),
  ]);
  main.innerHTML = `
    ${strategyCardHTML(s)}

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

    <div class="card">
      <h2>Вклади як інструмент реінвесту</h2>
      <div class="muted" style="margin-bottom:10px">Мінімум робить простій валюти «готовим до реінвесту» —
        входить у прогноз і задає крок поради «відкрити новий вклад». Ставка потрібна лише для поради:
        без неї запис не показується, але поріг усе одно діє. Порожній мінімум USD/EUR = <b>100</b>;
        «0» вимикає валюту. UAH за замовчуванням вимкнено.</div>
      <form id="depositSettingsForm">
        <label>Мінімум вкладу USD<input name="deposit_min_usd" inputmode="decimal" placeholder="порожньо = 100" value="${esc(s.deposit_min_usd || "")}"></label>
        <label>Мінімум вкладу EUR<input name="deposit_min_eur" inputmode="decimal" placeholder="порожньо = 100" value="${esc(s.deposit_min_eur || "")}"></label>
        <label>Мінімум вкладу UAH<input name="deposit_min_uah" inputmode="decimal" placeholder="порожньо = вимкнено" value="${esc(s.deposit_min_uah || "")}"></label>
        <label>Ставка нового вкладу USD, %<input name="deposit_rate_usd_pct" inputmode="decimal" placeholder="порожньо = без поради" value="${esc(s.deposit_rate_usd_pct || "")}"></label>
        <label>Ставка нового вкладу EUR, %<input name="deposit_rate_eur_pct" inputmode="decimal" placeholder="порожньо = без поради" value="${esc(s.deposit_rate_eur_pct || "")}"></label>
        <label>Ставка нового вкладу UAH, %<input name="deposit_rate_uah_pct" inputmode="decimal" placeholder="порожньо = без поради" value="${esc(s.deposit_rate_uah_pct || "")}"></label>
        <button type="submit">Зберегти</button>
      </form>
    </div>

    <div class="card">
      <h2>Порядок у «Що купити»</h2>
      <div class="muted" style="margin-bottom:10px">За чим ранжувати поради. <b>Під план</b> ставить
        першим те, що найбільше зрушує портфель до заявленої політики — валютної цілі й цілі за видом
        інструмента разом; вигода тоді лише розсуджує рівних, тож зверху може опинитись інструмент із
        меншою дохідністю. Решта режимів прості: <b>за дохідністю</b> — сама реальна дохідність,
        <b>короткі</b> — найближче погашення, <b>драбина</b> — рік із найменшими поверненнями.
        Незалежно від режиму: доступне зараз завжди вище за недоступне, а те, що вже перевищує ліміт
        концентрації, — нижче.</div>
      <form id="rankForm">
        <label>Критерій<select name="reinvest_rank">${
          [["plan", "під план"], ["rate", "за дохідністю"], ["short", "короткі"], ["ladder", "драбина"]]
            .map(([v, t]) => `<option value="${v}"${(s.reinvest_rank || "plan") === v ? " selected" : ""}>${t}</option>`)
            .join("")}</select></label>
        <button type="submit">Зберегти</button>
      </form>
    </div>

    <div class="card">
      <h2>Структура за видом інструмента</h2>
      <div class="muted" style="margin-bottom:10px">Валютна ціль вище каже, В ЧОМУ тримати гроші;
        ця — ЧИМ ризикувати. Сума <b>не мусить</b> давати 100: нерозподілене буде показане, а не
        розтягнуте до сотні мовчки. Порожньо = цілі за цим видом немає, і в «Портфелі» він не
        з'явиться. Резерв тут відсутній навмисно — його ціль задається нижче в місяцях витрат,
        бо саме на це питання він і відповідає.</div>
      <form id="kindTargetsForm">
        <label>Ціль ОВДП, %<input name="target_bonds_pct" inputmode="decimal" placeholder="порожньо = без цілі" value="${esc(s.target_bonds_pct || "")}"></label>
        <label>Ціль фондів, %<input name="target_funds_pct" inputmode="decimal" placeholder="порожньо = без цілі" value="${esc(s.target_funds_pct || "")}"></label>
        <label>Ціль вкладів, %<input name="target_deposits_pct" inputmode="decimal" placeholder="порожньо = без цілі" value="${esc(s.target_deposits_pct || "")}"></label>
        <button type="submit">Зберегти</button>
      </form>
    </div>

    <div class="card">
      <h2>Ліміти концентрації</h2>
      <div class="muted" style="margin-bottom:10px">Стеля, а не ціль: скільки максимум дозволено
        зібрати в одному місці. Три різні питання — що буде, якщо <b>цей емітент</b> не заплатить,
        якщо <b>ця установа</b> зникне, якщо саме <b>того року</b> ставки впадуть і всі погашення
        доведеться вкладати заново за гіршою ставкою. Порожньо = ліміту немає, і вимір не
        показується. Готових чисел застосунок не підставляє: «не більше 20% в один папір» — це
        порада, а він їх не дає.</div>
      <form id="limitsForm">
        <label>Макс. в одному папері, %<input name="limit_isin_pct" inputmode="decimal" placeholder="порожньо = без ліміту" value="${esc(s.limit_isin_pct || "")}"></label>
        <label>Макс. в одній установі, %<input name="limit_broker_pct" inputmode="decimal" placeholder="брокер або банк" value="${esc(s.limit_broker_pct || "")}"></label>
        <label>Макс. погашень в один рік, %<input name="limit_year_pct" inputmode="decimal" placeholder="від усіх погашень" value="${esc(s.limit_year_pct || "")}"></label>
        <button type="submit">Зберегти</button>
      </form>
    </div>

    <div class="card">
      <h2>Резерв на чорний день</h2>
      <div class="muted" style="margin-bottom:10px">Місячні витрати — єдине, від чого можна порахувати,
        на скільки вистачить матраца. Застосунок їх не вгадує: зняття з рахунку це і покупка холодильника,
        і переказ у резерв, тож видавати їх за витрати означало б міряти достатність від випадкового числа.
        Порожньо = «місяців вистачить» не показується взагалі.</div>
      <form id="reserveSettingsForm">
        <label>Місячні витрати, ₴<input name="monthly_expenses_uah" inputmode="decimal" placeholder="порожньо = не рахувати" value="${esc(s.monthly_expenses_uah || "")}"></label>
        <label>Ціль запасу, місяців<input name="reserve_target_months" inputmode="decimal" placeholder="напр. 6" value="${esc(s.reserve_target_months || "")}"></label>
        <button type="submit">Зберегти</button>
      </form>
    </div>

    <div class="card">
      <h2>Припущення прогнозу</h2>
      <div class="muted" style="margin-bottom:10px">Чим живляться картки у «Майбутньому».
        Перші два — про тебе: який дохід ти вважаєш достатнім і скільки збирався б знімати,
        якби перестав вносити. Порожньо в обох = місячні витрати вище, бо це найчастіша
        відповідь, але не єдина розумна: половина витрат — теж ціль.
        Другі два — про ринок: наскільки далеко один від одного стоять оптимістичний і
        песимістичний сценарії. Доти ці числа були зашиті в коді, тобто «песимістично»
        означало рівно те, що вирішив автор. Розкид вішається на ДОВГОСТРОКОВУ ставку:
        сьогоднішня — факт, за яким можна купити просто зараз, а припущенням є те,
        куди вона прийде.</div>
      <form id="forecastAssumptionsForm">
        <label>Достатній дохід, ₴/міс<input name="income_target_uah" inputmode="decimal" placeholder="порожньо = місячні витрати" value="${esc(s.income_target_uah || "")}"></label>
        <label>Знімати щомісяця, ₴<input name="withdraw_monthly_uah" inputmode="decimal" placeholder="порожньо = місячні витрати" value="${esc(s.withdraw_monthly_uah || "")}"></label>
        <label>Розкид ставки, п.п.<input name="rate_spread_pp" inputmode="decimal" placeholder="порожньо = 3" value="${esc(s.rate_spread_pp || "")}"></label>
        <label>Розкид знецінення, п.п.<input name="deval_spread_pp" inputmode="decimal" placeholder="порожньо = 4" value="${esc(s.deval_spread_pp || "")}"></label>
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
  // Обидві форми пишуть у той самий PUT /settings і відрізняються лише
  // списком полів. Збираємо payload з тих, що РЕАЛЬНО є у формі: PUT
  // частковий, тож відсутнє поле не шлеться порожнім і не затирає значення.
  // «channels» тут свідомо немає: брокерами керує окрема картка.
  const settingsPut = (keys) => (f) => {
    const body = {};
    for (const k of keys) {
      if (f.elements[k]) body[k] = f.elements[k].value.trim();
    }
    return { method: "PUT", path: "settings", body, msg: "Налаштування збережено" };
  };
  onSubmit(ctx, main.querySelector("#setForm"), settingsPut([
    "usd_target_share_pct", "eur_target_share_pct", "goal_amount_uah", "goal_date",
    "uah_devaluation_pct", "terminal_rate_pct", "rate_glide_years",
  ]));
  onSubmit(ctx, main.querySelector("#depositSettingsForm"), settingsPut([
    "deposit_min_usd", "deposit_min_eur", "deposit_min_uah",
    "deposit_rate_usd_pct", "deposit_rate_eur_pct", "deposit_rate_uah_pct",
  ]));
  wireStrategy(ctx, main, s);
  onSubmit(ctx, main.querySelector("#rankForm"), settingsPut(["reinvest_rank"]));
  onSubmit(ctx, main.querySelector("#kindTargetsForm"), settingsPut([
    "target_bonds_pct", "target_funds_pct", "target_deposits_pct",
  ]));
  onSubmit(ctx, main.querySelector("#limitsForm"), settingsPut([
    "limit_isin_pct", "limit_broker_pct", "limit_year_pct",
  ]));
  onSubmit(ctx, main.querySelector("#reserveSettingsForm"), settingsPut([
    "monthly_expenses_uah", "reserve_target_months",
  ]));
  onSubmit(ctx, main.querySelector("#forecastAssumptionsForm"), settingsPut([
    "income_target_uah", "withdraw_monthly_uah", "rate_spread_pp", "deval_spread_pp",
  ]));
}

