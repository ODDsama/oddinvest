// Розділ «Налаштування» — як воно налаштоване.
//
// Цілі й допущення, довідники брокерів і фондів, бекап. Довідники живуть
// саме тут, бо перейменування підхоплюють усі записи разом: назва — це
// налаштування, а не властивість окремого лота.

import { esc, today, pct } from "../format.js";
import { tile } from "../components.js";
import { onSubmit, onDelete, apply } from "../forms.js";
import { infoBtn } from "../info.js";
import { section, wireDisclosures } from "../disclosure.js";
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
  // Поле з опціями стає списком, а не текстовим полем: вид фонду має рівно
  // два значення, і вводити їх руками означало б ловити друкарські
  // помилки там, де вибір скінченний. Клас той самий, тож проводка нижче
  // не розрізняє select від input — обидва мають .value.
  const inputs = fields.map((f) => {
    const attrs = `class="cat-f" data-field="${f.key}" title="${esc(f.title || "")}"
       style="--oi-w:${f.w || 90}px"`;
    if (f.opts) {
      const opts = f.opts.map((o) =>
        `<option value="${esc(o.v)}"${o.v === (f.value ?? "") ? " selected" : ""}>${esc(o.t)}</option>`).join("");
      return `<select ${attrs}>${opts}</select>`;
    }
    return `<input ${attrs}${f.num ? ' data-num="1"' : ""}
       value="${esc(f.value ?? "")}" placeholder="${esc(f.ph || "")}">`;
  }).join("");
  // align-self кнопці потрібен саме тут: .pv-row тягне дітей на всю
  // висоту, і доки поля фонда вміщались в один рядок, це було непомітно.
  // Дев'ять полів переносяться, і ✕ розтягувався червоною смугою на дві
  // лінії. Правити .pv-row не можна — на ній стоять усі списки позицій.
  return `<div class="pv-row" data-cat="${item.id}">
    <span class="row-h">
      <input class="cat-name w-lg" value="${esc(item.name)}">${inputs}</span>
    <button class="sm warn self-start" data-catdel="${item.id}">✕</button></div>`;
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
    { key: "yield_simple_years", value: f.yield_simple_years || "", w: 92, ph: "проста, р.", num: true,
      title: "Заповнюйте, лише якщо фонд називає дохідність ПРОСТОЮ середньорічною: скільки років вона охоплює. "
        + "Проста 25% за 3 роки — це ×1.75, тобто 20.5% складних. Порожньо = ставка складна, як усі інші" },
    { key: "payout_day", value: f.payout_day || "", w: 76, ph: "день", num: true,
      title: "Число місяця, коли платять дивіденди. Вихідні переносяться на робочий день" },
    { key: "kind", value: f.kind || "", w: 130,
      title: "Накопичувальний не платить нічого: увесь дохід сидить у ціні сертифіката. "
        + "Порожній день виплати цього не означає — він означає лише, що день невідомий",
      opts: [{ v: "", t: "розподільний" }, { v: "accum", t: "накопичувальний" }] },
    { key: "close_date", value: f.close_date, w: 110, ph: "закриття",
      title: "Дата, коли фонд закривається й повертає гроші. Порожньо = безстроковий" },
    { key: "buy_until", value: f.buy_until, w: 110, ph: "купувати до",
      title: "Остання дата, коли фонд можна купити. Після неї він не потрапляє в «Що купити»" },
    { key: "income_tax_pct", w: 84, ph: "податок, %",
      value: f.income_tax_bp ? (f.income_tax_bp / 100).toFixed(2).replace(/\.?0+$/, "") : "",
      title: "Податок на дохід фонду, якщо дожити до закриття. Купон ОВДП від податку "
        + "звільнений, дохід фонду ні — без цього числа вони порівнюються в різних мірах" },
    { key: "exit_tax_pct", w: 92, ph: "вихід, %",
      value: f.exit_tax_bp ? (f.exit_tax_bp / 100).toFixed(2).replace(/\.?0+$/, "") : "",
      title: "Податок при ДОСТРОКОВОМУ виході — на різницю між купівлею й продажем "
        + "сертифікатів. Інша подія й інша ставка; його бачить картка «На скільки вистачить»" },
  ];
  const funds = (ctx.fundCatalog || []).length
    ? ctx.fundCatalog.map((f) => catalogRowHTML(f, fundFields(f))).join("")
    : `<div class="sub">Фондів ще немає — вони зʼявляться після першої купівлі сертифікатів.</div>`;
  // НПФ, на відміну від фондів, СТВОРЮЄТЬСЯ тут: у фонда рахунок заводить
  // перша операція з виписки, а пенсійний рахунок мусить існувати до
  // першого внеску — інакше внеску нема куди записати.
  const npfFields = (a) => [
    { key: "administrator", value: a.administrator || "", w: 110, ph: "адміністратор",
      title: "Хто веде рахунок. Контрагент, а не мій рахунок: із нього нічого не списати, "
        + "тому в брокерах його немає. У концентрації за контрагентом він є" },
    { key: "currency", value: a.currency, w: 70, title: "Валюта рахунку" },
    { key: "nav", value: a.nav ? a.nav.toFixed(6) : "", w: 100, ph: "ЧВОПА",
      title: "Остання відома чиста вартість одиниці пенсійних активів. Може бути свіжішою за "
        + "останній внесок: її оновлюють руками з кабінету" },
    { key: "nav_date", value: a.nav_date || "", w: 110, ph: "на дату",
      title: "На яку дату ЧВОПА відома. Обовʼязкова разом зі значенням: модель порівнює дати, "
        + "обираючи найсвіжіше джерело між довідником і внесками" },
    { key: "expected_yield_pct", value: a.expected_yield_pct || "", w: 84, ph: "дохідн., %",
      title: "Обіцяна дохідність. Показується, доки не набереться двох точок ЧВОПА для виміряної" },
    { key: "yield_simple_years", value: a.yield_simple_years || "", w: 92, ph: "проста, р.", num: true,
      title: "Лише якщо дохідність названа ПРОСТОЮ середньорічною: скільки років вона охоплює. "
        + "Проста 15% за 3 роки — це 13.19% складних" },
    { key: "access_date", value: a.access_date || "", w: 110, ph: "доступ з",
      title: "Коли виплати стають законними — 50 років, тобто 10 до державної пенсії. "
        + "Задає строк замка й місяць, коли гроші входять у проєкцію" },
    { key: "income_tax_pct", value: a.income_tax_pct || "", w: 84, ph: "податок, %",
      title: "Податок на виплату на пенсії. За п. 170.8.2 ПКУ 40% виплати на визначений строк "
        + "звільнено, тобто оподатковується 60%: ПДФО 18% + ВЗ 5% дають ефективні 13.8%" },
    { key: "credit_rate_pct", value: a.credit_rate_pct || "", w: 92, ph: "знижка, %",
      title: "Ставка ПДФО-знижки на внески — 18%. Оцінка знижки нікуди не входить: "
        + "її ще треба отримати декларацією до 31 грудня наступного року" },
    { key: "contrib_day", value: a.contrib_day || "", w: 76, ph: "день", num: true,
      title: "Число місяця, коли планую вносити. З нього вмикається нагадування «цього місяця "
        + "внеску ще немає»; гасне саме, щойно внесок зʼявиться. Порожньо = не нагадувати" },
    { key: "payout_years", value: a.payout_years || "", w: 92, ph: "виплата, р.", num: true,
      title: "На скільки років розтягнута виплата після дати доступу. За законом мінімум 10; "
        + "0 = разово, і так можна лише коли сума нижче межі одноразової виплати. "
        + "Разова модель вивалювала б увесь капітал у готівку одного місяця й далі "
        + "реінвестувала його — тобто малювала гроші, яких не буде" },
    { key: "payout_freq", value: a.payout_freq || "", w: 118,
      title: "Як часто платять протягом строку виплати",
      opts: [{ v: "month", t: "щомісяця" }, { v: "quarter", t: "щокварталу" },
        { v: "year", t: "щороку" }] },
  ];
  const npf = (ctx.npfAccounts || []).length
    ? ctx.npfAccounts.map((a) => catalogRowHTML(a, npfFields(a))).join("")
    : `<div class="sub">Пенсійних рахунків ще немає.</div>`;
  return `<div class="card" id="brokerCard">
      <h2 class="h-row">Брокери ${infoBtn("setBrokers")}</h2>
      ${brokers}
      <form id="brokerAddForm" class="row-h mt">
        <input name="broker" class="w-xl" placeholder="назва брокера" autocomplete="off">
        <div class="form-actions"><button type="submit">Додати</button></div>
      </form>
    </div>
    <div class="card" id="fundCatalogCard">
      <h2 class="h-row">Фонди ${infoBtn("setFunds")}</h2>
      ${funds}
    </div>
    <div class="card" id="npfCatalogCard">
      <h2 class="h-row">Пенсійні фонди (НПФ) ${infoBtn("setNPF")}</h2>
      ${npf}
      <form id="npfAddForm" class="row-h mt">
        <input name="name" class="w-xl" placeholder="назва фонду" autocomplete="off">
        <div class="form-actions"><button type="submit">Додати</button></div>
      </form>
      <div class="sub-xs muted">Внески записуються в «Портфелі», у рядку рахунку. Тут — те, чого
        з внесків не вивести: дата доступу, обидва податки й день нагадування.</div>
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
      // change потрібен випадайці: вибір мишею фокуса не знімає, тож на
      // самому blur вид фонду зберігався б аж після кліку кудись повз.
      // Для текстових полів це зайвий виклик, але не зайва робота —
      // commit виходить одразу, якщо з минулого разу нічого не змінилось.
      el.addEventListener("change", commit);
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
  bindCatalog(ctx, main.querySelector("#npfCatalogCard"), "npf-accounts");
  onSubmit(ctx, main.querySelector("#npfAddForm"), (f) => {
    const name = f.name.value.trim();
    if (!name) return null;
    if ((ctx.npfAccounts || []).some((a) => a.name.toLowerCase() === name.toLowerCase())) {
      ctx.toast("Такий пенсійний фонд уже є", false);
      return null;
    }
    return { path: "npf-accounts", body: { name }, msg: "Пенсійний фонд додано" };
  });
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
    <div class="tiles flush mb">
      ${tile("Чинне значення", pct(d.effective_pct), `<div class="sub">${src}</div>`)}
    </div>
    <div class="muted fine mb">Це число ділить <b>кожну реальну
      дохідність</b> у застосунку й керує прогнозом. Порожнє поле «Гривня слабшає» вище означає
      «бери виміряне» — саме так його й повертають назад на автоматику.</div>
    ${rows ? `<div class="table-scroll"><table><thead><tr>
        <th>Вікно</th><th class="num">%/рік</th><th>Курс від → до</th></tr></thead>
      <tbody>${rows}</tbody></table></div>
      <div class="sub mt-sm">Застосунок бере <b>десятирічне</b> вікно, і різниця між
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
  // Одинадцять однакових карток підряд — найдовший скрол у застосунку, і
  // жодна з них не каже, до чого належить: щоб знайти «Ліміти
  // концентрації», доводилось прочитати десять заголовків. Чотири
  // секції за питаннями, і три з них згорнуті: у політику заходять
  // регулярно, у довідники — раз на кілька місяців.
  //
  // Групи навмисно збігаються з наявним порядком карток: переставляти їх
  // місцями всередині одного великого шаблонного рядка — та правка, яку
  // найлегше зробити наполовину.
  main.innerHTML = `
    ${section("policy", "Політика", `
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
        <div class="form-actions"><button type="submit">Зберегти</button></div>
      </form>
    </div>`, { open: true, hint: "цілі, ліміти, порядок порад" })}

    ${section("instruments", "Інструменти й межі", `
    <div class="card">
      <h2 class="h-row">Вклади як інструмент реінвесту ${infoBtn("setDeposits")}</h2>
      <form id="depositSettingsForm">
        <label>Мінімум вкладу USD<input name="deposit_min_usd" inputmode="decimal" placeholder="порожньо = 100" value="${esc(s.deposit_min_usd || "")}"></label>
        <label>Мінімум вкладу EUR<input name="deposit_min_eur" inputmode="decimal" placeholder="порожньо = 100" value="${esc(s.deposit_min_eur || "")}"></label>
        <label>Мінімум вкладу UAH<input name="deposit_min_uah" inputmode="decimal" placeholder="порожньо = вимкнено" value="${esc(s.deposit_min_uah || "")}"></label>
        <label>Ставка нового вкладу USD, %<input name="deposit_rate_usd_pct" inputmode="decimal" placeholder="порожньо = без поради" value="${esc(s.deposit_rate_usd_pct || "")}"></label>
        <label>Ставка нового вкладу EUR, %<input name="deposit_rate_eur_pct" inputmode="decimal" placeholder="порожньо = без поради" value="${esc(s.deposit_rate_eur_pct || "")}"></label>
        <label>Ставка нового вкладу UAH, %<input name="deposit_rate_uah_pct" inputmode="decimal" placeholder="порожньо = без поради" value="${esc(s.deposit_rate_uah_pct || "")}"></label>
        <div class="form-actions"><button type="submit">Зберегти</button></div>
      </form>
    </div>

    <div class="card">
      <h2 class="h-row">Порядок у «Що купити» ${infoBtn("setRank")}</h2>
      <form id="rankForm">
        <label>Критерій<select name="reinvest_rank">${
          [["plan", "під план"], ["rate", "за дохідністю"], ["short", "короткі"], ["ladder", "драбина"]]
            .map(([v, t]) => `<option value="${v}"${(s.reinvest_rank || "plan") === v ? " selected" : ""}>${t}</option>`)
            .join("")}</select></label>
        <div class="form-actions"><button type="submit">Зберегти</button></div>
      </form>
    </div>

    <div class="card">
      <h2 class="h-row">Структура за видом інструмента ${infoBtn("setKinds")}</h2>
      <form id="kindTargetsForm">
        <label>Ціль ОВДП, %<input name="target_bonds_pct" inputmode="decimal" placeholder="порожньо = без цілі" value="${esc(s.target_bonds_pct || "")}"></label>
        <label>Ціль фондів, %<input name="target_funds_pct" inputmode="decimal" placeholder="порожньо = без цілі" value="${esc(s.target_funds_pct || "")}"></label>
        <label>Ціль вкладів, %<input name="target_deposits_pct" inputmode="decimal" placeholder="порожньо = без цілі" value="${esc(s.target_deposits_pct || "")}"></label>
        <label>Ціль НПФ, %<input name="target_npf_pct" inputmode="decimal" placeholder="порожньо = без цілі" value="${esc(s.target_npf_pct || "")}"></label>
        <div class="form-actions"><button type="submit">Зберегти</button></div>
      </form>
    </div>

    <div class="card">
      <h2 class="h-row">Податкова знижка на внески в НПФ ${infoBtn("setNPFCredit")}</h2>
      <form id="npfCreditForm">
        <label>Утриманий за рік ПДФО, ₴<input name="npf_credit_pdfo_year_uah" inputmode="decimal"
          placeholder="порожньо = знижку не рахувати" value="${esc(s.npf_credit_pdfo_year_uah || "")}"></label>
        <label>Ліміт внеску за місяць, ₴<input name="npf_credit_cap_month_uah" inputmode="decimal"
          placeholder="4660 у 2026" value="${esc(s.npf_credit_cap_month_uah || "")}"></label>
        <div class="form-actions"><button type="submit">Зберегти</button></div>
      </form>
      <div class="sub-xs muted">Перше поле — і перемикач, і стеля: держава повертає сплачене, а не
        дарує. Порожньо = знижка не рахується. Працює лише проти ЗАРПЛАТИ: дохід ФОПа права на неї
        не дає. Ліміт щороку інший — прожитковий мінімум працездатних на 1 січня × 1,4.
        Оцінка нікуди не входить: ні в капітал, ні в календар, ні в проєкцію.</div>
    </div>

    <div class="card">
      <h2 class="h-row">Ліміти концентрації ${infoBtn("setLimits")}</h2>
      <form id="limitsForm">
        <label>Макс. в одному папері, %<input name="limit_isin_pct" inputmode="decimal" placeholder="порожньо = без ліміту" value="${esc(s.limit_isin_pct || "")}"></label>
        <label>Макс. в одній установі, %<input name="limit_broker_pct" inputmode="decimal" placeholder="брокер або банк" value="${esc(s.limit_broker_pct || "")}"></label>
        <label>Макс. погашень в один рік, %<input name="limit_year_pct" inputmode="decimal" placeholder="від усіх погашень" value="${esc(s.limit_year_pct || "")}"></label>
        <div class="form-actions"><button type="submit">Зберегти</button></div>
      </form>
    </div>

    <div class="card">
      <h2 class="h-row">Резерв на чорний день ${infoBtn("setReserve")}</h2>
      <form id="reserveSettingsForm">
        <label>Місячні витрати, ₴<input name="monthly_expenses_uah" inputmode="decimal" placeholder="порожньо = не рахувати" value="${esc(s.monthly_expenses_uah || "")}"></label>
        <label>Ціль запасу, місяців<input name="reserve_target_months" inputmode="decimal" placeholder="напр. 6" value="${esc(s.reserve_target_months || "")}"></label>
        <div class="form-actions"><button type="submit">Зберегти</button></div>
      </form>
    </div>`, { hint: "вклади, види, ліміти, резерв" })}

    ${section("assumptions", "Припущення", `
    <div class="card">
      <h2 class="h-row">Припущення прогнозу ${infoBtn("setForecast")}</h2>
      <form id="forecastAssumptionsForm">
        <label>Достатній дохід, ₴/міс<input name="income_target_uah" inputmode="decimal" placeholder="порожньо = місячні витрати" value="${esc(s.income_target_uah || "")}"></label>
        <label>Знімати щомісяця, ₴<input name="withdraw_monthly_uah" inputmode="decimal" placeholder="порожньо = місячні витрати" value="${esc(s.withdraw_monthly_uah || "")}"></label>
        <label>Розкид ставки, п.п.<input name="rate_spread_pp" inputmode="decimal" placeholder="порожньо = 3" value="${esc(s.rate_spread_pp || "")}"></label>
        <label>Розкид знецінення, п.п.<input name="deval_spread_pp" inputmode="decimal" placeholder="порожньо = 4" value="${esc(s.deval_spread_pp || "")}"></label>
        <div class="form-actions"><button type="submit">Зберегти</button></div>
      </form>
    </div>

    ${devalHTML(deval)}`, { hint: "що застосунок вважає ймовірним" })}

    ${section("refs", "Довідники й обслуговування", `
    ${catalogsHTML(ctx)}

    <div class="card">
      <h2 class="h-row">Бекап ${infoBtn("setBackup")}</h2>
      <div class="row-h">
        <button type="button" id="btnExport">Завантажити бекап</button>
        <label class="inline-block"><span class="muted fine">Відновити з файлу:</span>
          <input type="file" id="importFile" accept="application/json,.json" class="mt-sm"></label>
      </div>
      <div class="muted mt-sm fine" id="restoreMsg"></div>
    </div>`, { hint: "брокери, фонди, бекап" })}`;
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
    "target_bonds_pct", "target_funds_pct", "target_deposits_pct", "target_npf_pct",
  ]));
  onSubmit(ctx, main.querySelector("#npfCreditForm"), settingsPut([
    "npf_credit_pdfo_year_uah", "npf_credit_cap_month_uah",
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
  // Останнім: до цього моменту вся розмітка вже на місці, включно з тією,
  // що всередині згорнутих секцій.
  wireDisclosures(main);
}

