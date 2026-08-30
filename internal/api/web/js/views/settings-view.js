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
import { onSubmit, onDelete } from "../forms.js";
import { text, formHTML } from "../fields.js";
import { inlineEdit } from "../crud.js";
import { infoBtn } from "../info.js";
import { wireDisclosures } from "../disclosure.js";
import { fundPricePanelHTML, wireFundPrices } from "../fund-prices.js";

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
  // Назва — таке саме поле, як інші, і несе те саме data-field. Доти вона
  // була окремим випадком: проводка тягнула її querySelector'ом по класу й
  // приклеювала до тіла запиту руками. Через це спільної проводки
  // правки-на-місці не виходило — сусідній розділ (продажі ОВДП) мав свою
  // копію того самого коду.
  return `<div class="pv-row" data-cat="${item.id}">
    <span class="row-h">
      <input class="cat-f cat-name w-lg" data-field="name" value="${esc(item.name)}">${inputs}</span>
    <button class="sm warn self-start" data-catdel="${item.id}">✕</button></div>`;
}

// marks / fundOps / fundRows — усе, що потрібно панелі позначок ціни під
// рядком фонда. Параметрами, а не з кешу модуля: сторінка довідників —
// єдиний споживач, і схована залежність від того, хто відрендерився першим,
// тут нікому не потрібна (у npf.js вона є, але там у неї є причина).
export function catalogsHTML(ctx, marks = [], fundOps = [], fundRows = []) {
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
    { key: "kind", value: f.kind || "", w: 190,
      title: "Накопичувальний не платить нічого: увесь дохід сидить у ціні сертифіката. "
        + "Порожній день виплати цього не означає — він означає лише, що день невідомий. "
        + "«Докуповує сертифікати» — фонд утримує виплату й одразу купує на неї свої ж "
        + "папери цілими штуками, а на рахунок падає лише решта: саме її веде «Маршрут "
        + "грошей». Дохід при цьому нікуди не дівається — календар виплат показує всю ренту",
      opts: [
        { v: "", t: "розподільний" },
        { v: "drip", t: "докуповує сертифікати" },
        { v: "accum", t: "накопичувальний" },
      ] },
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
  // Панель позначок ціни йде СУСІДОМ рядка, а не всередину нього: рядок
  // довідника плоский і спільний для брокерів, фондів і НПФ, а inlineEdit
  // збирає в PUT усі .cat-f усередині [data-cat] — форма позначки там
  // додала б довіднику поля, яких він не знає.
  const funds = (ctx.fundCatalog || []).length
    ? ctx.fundCatalog.map((f) => catalogRowHTML(f, fundFields(f))
      + fundPricePanelHTML(ctx, f, marks, fundOps,
        fundRows.find((r) => r.fund === f.name))).join("")
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
      ${formHTML({
    id: "brokerAddForm", cls: "row-h mt", submit: "Додати",
    fields: [text("broker", "", { ph: "назва брокера", cls: "w-xl" })],
  })}
    </div>
    <div class="card" id="fundCatalogCard">
      <h2 class="h-row">Фонди ${infoBtn("setFunds")}</h2>
      ${funds}
    </div>
    <div class="card" id="npfCatalogCard">
      <h2 class="h-row">Пенсійні фонди (НПФ) ${infoBtn("setNPF")}</h2>
      ${npf}
      ${formHTML({
    id: "npfAddForm", cls: "row-h mt", submit: "Додати",
    fields: [text("name", "", { ph: "назва фонду", cls: "w-xl" })],
  })}
      <div class="sub-xs muted">Внески записуються в «Портфелі», у рядку рахунку. Тут — те, чого
        з внесків не вивести: дата доступу, обидва податки й день нагадування.</div>
    </div>`;
}

// Спільна прошивка для довідників: різняться лише ендпойнтом.
//
// Правку-на-місці робить тепер спільний inlineEdit (crud.js). Доти той
// самий прийом був написаний тут і ще раз у продажах ОВДП, майже слово в
// слово: зібрати поля за data-field, звірити з попереднім станом, дописати
// числам Number(), відправити PUT. Розходились копії в дрібниці — тут
// слухали ще й Enter, там ні.
export function bindCatalog(ctx, card, path) {
  if (!card) return;
  inlineEdit(ctx, card, {
    rows: "[data-cat]", fields: ".cat-f",
    path: (row) => `${path}/${row.dataset.cat}`,
    // Порожня назва не зберігається: у довіднику вона єдиний спосіб
    // упізнати запис, і безіменний рядок неможливо ані знайти, ані
    // виправити назад.
    guard: (values) => !!String(values.name || "").trim(),
  });
  card.querySelectorAll("[data-cat]").forEach((row) => {
    const name = row.querySelector(".cat-name");
    onDelete(ctx, row, "[data-catdel]", () => ({
      path: `${path}/${row.dataset.cat}`,
      confirm: `Видалити «${name.value}»?`,
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

/** Довідники: брокери, фонди й пенсійні рахунки.
 *
 *  Три м'які читання перед рендером: позначки ціни, журнал операцій фондів
 *  (з нього беруться ціни купівель для кривої) і саме зведення (з нього —
 *  обіцянка й виміряне зростання ціни для пари «обіцяли / фактично»).
 *  Усі три можуть бути порожні на свіжій базі, і жодне не є приводом не
 *  показати довідники: цю сторінку відкривають саме тоді, коли решта ще не
 *  заведена. */
export async function refs(ctx, main) {
  const [marks, fundOps, sum] = await Promise.all([
    ctx.soft("fund-prices", []),
    ctx.soft("funds", []),
    ctx.soft("summary", {}),
  ]);
  main.innerHTML = catalogsHTML(ctx, marks, fundOps, (sum || {}).funds || []);
  bindBrokers(ctx, main);
  wireFundPrices(ctx, main, marks);
  wireDisclosures(main);
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
