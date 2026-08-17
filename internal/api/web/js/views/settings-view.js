// Розділ «Налаштування» — як воно налаштоване. Дві сторінки.
//
// Довідники брокерів, фондів і пенсійних рахунків та резервна копія.
// Довідники живуть саме тут, бо перейменування підхоплюють усі записи
// разом: назва — це налаштування, а не властивість окремого лота.
//
// Розділ навмисно маленький. Усе, що відповідає на «чого я хочу» — цілі,
// частки, ліміти, припущення, — поїхало в окрему «Політику»: туди заходять
// регулярно, а сюди раз на кілька місяців. Те, що лишилось, — службовий кут
// застосунку, і саме він мусить працювати, коли бекенд ліг: «Резервна
// копія» не читає зведення взагалі й через це доступна тоді, коли вона
// найпотрібніша.

import { esc, today } from "../format.js";
import { onSubmit, onDelete, apply } from "../forms.js";
import { infoBtn } from "../info.js";

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

/** Довідники: брокери, фонди й пенсійні рахунки. */
export async function refs(ctx, main) {
  main.innerHTML = catalogsHTML(ctx);
  bindBrokers(ctx, main);
}

/** Резервна копія. Єдина сторінка застосунку, яка не читає нічого, крім
 *  власних кнопок, — і саме тому вона лишається досяжною, коли зведення не
 *  віддається взагалі. Умова в app.js тримається за цей факт. */
export async function backup(ctx, main) {
  main.innerHTML = `<div class="card">
    <h2 class="h-row">Бекап ${infoBtn("setBackup")}</h2>
    <div class="row-h">
      <button type="button" id="btnExport">Завантажити бекап</button>
      <label class="inline-block"><span class="muted fine">Відновити з файлу:</span>
        <input type="file" id="importFile" accept="application/json,.json" class="mt-sm"></label>
    </div>
    <div class="muted mt-sm fine" id="restoreMsg"></div>
  </div>`;
  bindBackup(ctx, main);
}
