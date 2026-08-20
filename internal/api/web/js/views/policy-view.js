// Розділ «Політика» — чого я хочу. П'ять сторінок.
//
// Виділений із «Налаштувань», і причина була записана там-таки давно: «у
// політику заходять регулярно, у довідники — раз на кілька місяців».
// Розв'язували її згортанням секцій, тобто ховали половину екрана, замість
// того щоб визнати, що питань два. Вони й справді різні: тут — чого я хочу
// (цілі, частки, ліміти, припущення), там — як воно налаштоване (довідники
// брокерів і фондів, резервна копія).
//
// #setForm РОЗПАВСЯ, і це виправлення, а не косметика. Одна форма з
// заголовком «Налаштування» тримала три різні речі: ціль із дедлайном,
// цільові валютні частки й припущення прогнозу — знецінення гривні,
// довгострокову ставку ОВДП і час її сповзання. Останні три припущеннями
// і є, а стояли поруч із ціллю, ніби задаються так само впевнено.
//
// Розкол безкоштовний, і саме тому його варто було зробити: settingsPut
// збирає тіло запиту з полів, які РЕАЛЬНО є у формі, а PUT /settings
// частковий — відсутнє поле не шлеться порожнім і не затирає значення.
// Тобто дві форми замість однієї не потребують жодної нової машинерії.

import { esc, pct } from "../format.js";
import { money as moneyField, pct as pctField, date as dateField, selectOf, formHTML } from "../fields.js";
import { tile } from "../components.js";
import { onSubmit } from "../forms.js";
import { infoBtn } from "../info.js";
import { opsGrid } from "../grid.js";
import { strategyCardHTML, wireStrategy, RANKS } from "./strategy.js";

// Обидві форми пишуть у той самий PUT /settings і відрізняються лише
// списком полів. Збираємо payload із тих, що РЕАЛЬНО є у формі: PUT
// частковий, тож відсутнє поле не шлеться порожнім і не затирає значення.
// «channels» тут свідомо немає: брокерами керує окремий довідник.
const settingsPut = (spec) => (f) => {
  const body = {};
  for (const { key } of spec) {
    if (f.elements[key]) body[key] = f.elements[key].value.trim();
  }
  return { method: "PUT", path: "settings", body, msg: "Налаштування збережено" };
};

// ОДНА специфікація на форму: з неї малюються поля І з неї ж збирається
// список ключів для часткового PUT.
//
// Доти ключі писались ДВІЧІ — у розмітці поля й у settingsPut([...]) на
// сорок рядків нижче, — і розходились би найтихішим із можливих способів:
// забутий у другому списку ключ просто не зберігався б. Форма подавалась,
// тост казав «Налаштування збережено», поле мовчки лишалось як було.
//
// type: "money" — сума, "pct" — відсоток або кількість років, "date" —
// дата, "select" — вибір із opts. Різниця між money й pct не косметична:
// вона каже, що саме читати в полі, і дозволяє змінити поведінку всіх
// відсоткових полів застосунку однією правкою.
function settingsField({ key, label, ph = "", type = "money", opts = null }, s) {
  const value = s[key] || "";
  if (type === "date") return dateField(key, label, { value });
  if (type === "select") return selectOf(key, label, opts, s[key] || opts[0][0]);
  const make = type === "pct" ? pctField : moneyField;
  return make(key, label, { ph, value });
}

function settingsForm(id, s, spec, submit = "Зберегти") {
  return formHTML({ id, submit, fields: spec.map((f) => settingsField(f, s)) });
}


// Дев'ять форм «Політики» — це дев'ять таких списків і нічого більше.
const SPEC = {
  deposit: [
    { key: "deposit_min_usd", label: "Мінімум вкладу USD", ph: "порожньо = 100" },
    { key: "deposit_min_eur", label: "Мінімум вкладу EUR", ph: "порожньо = 100" },
    { key: "deposit_min_uah", label: "Мінімум вкладу UAH", ph: "порожньо = вимкнено, дефолту немає" },
    { key: "deposit_rate_usd_pct", label: "Ставка нового вкладу USD, %", type: "pct", ph: "порожньо = без поради" },
    { key: "deposit_rate_eur_pct", label: "Ставка нового вкладу EUR, %", type: "pct", ph: "порожньо = без поради" },
    { key: "deposit_rate_uah_pct", label: "Ставка нового вкладу UAH, %", type: "pct", ph: "порожньо = без поради" },
  ],
  rank: [
    { key: "reinvest_rank", label: "Критерій", type: "select", opts: RANKS },
  ],
  kindTargets: [
    { key: "target_bonds_pct", label: "Ціль ОВДП, %", type: "pct", ph: "порожньо = без цілі" },
    { key: "target_funds_pct", label: "Ціль фондів, %", type: "pct", ph: "порожньо = без цілі" },
    { key: "target_deposits_pct", label: "Ціль вкладів, %", type: "pct", ph: "порожньо = без цілі" },
    { key: "target_npf_pct", label: "Ціль НПФ, %", type: "pct", ph: "порожньо = без цілі" },
  ],
  npfCredit: [
    { key: "npf_credit_pdfo_year_uah", label: "Утриманий за рік ПДФО, ₴", ph: "порожньо = знижку не рахувати" },
    { key: "npf_credit_cap_month_uah", label: "Ліміт внеску за місяць, ₴", ph: "4660 у 2026" },
  ],
  limits: [
    { key: "limit_isin_pct", label: "Макс. в одному папері, %", type: "pct", ph: "порожньо = без ліміту" },
    { key: "limit_broker_pct", label: "Макс. в одній установі, %", type: "pct", ph: "брокер або банк" },
    { key: "limit_year_pct", label: "Макс. погашень в один рік, %", type: "pct", ph: "від усіх погашень" },
  ],
  reserve: [
    { key: "monthly_expenses_uah", label: "Місячні витрати, ₴", ph: "порожньо = не рахувати" },
    { key: "reserve_target_months", label: "Ціль запасу, місяців", type: "pct", ph: "напр. 6" },
    { key: "reserve_fill_share_pct", label: "З вільних грошей у резерв, %", type: "pct", ph: "порожньо = не пропонувати" },
  ],
  forecast: [
    { key: "income_target_uah", label: "Достатній дохід, ₴/міс", ph: "порожньо = місячні витрати" },
    { key: "withdraw_monthly_uah", label: "Знімати щомісяця, ₴", ph: "порожньо = місячні витрати" },
    { key: "rate_spread_pp", label: "Розкид ставки, п.п.", type: "pct", ph: "порожньо = 3" },
    { key: "deval_spread_pp", label: "Розкид знецінення, п.п.", type: "pct", ph: "порожньо = 4" },
  ],
  goal: [
    { key: "goal_amount_uah", label: "Ціль, ₴", ph: "скільки хочу накопичити" },
    { key: "goal_date", label: "Дедлайн — коли", type: "date" },
    { key: "usd_target_share_pct", label: "Цільова частка USD, %", type: "pct" },
    { key: "eur_target_share_pct", label: "Цільова частка EUR, %", type: "pct" },
  ],
  rateAssumptions: [
    { key: "uah_devaluation_pct", label: "Гривня слабшає, %/рік", type: "pct", ph: "порожньо = виміряне" },
    { key: "terminal_rate_pct", label: "Довгострокова ставка ОВДП, %", type: "pct", ph: "порожньо = 11" },
    { key: "rate_glide_years", label: "Ставка сповзає туди за, років", type: "pct", ph: "порожньо = 5" },
  ],
};

function depositCard(s) {
  return `<div class="card">
  <h2 class="h-row">Вклади як інструмент реінвесту ${infoBtn("setDeposits")}</h2>
  ${settingsForm("depositSettingsForm", s, SPEC.deposit)}
  <div class="sub-xs muted">Ставка й мінімум працюють лише В ПАРІ, і в одній валюті одразу: рядок
    «Новий вклад» у «Що купити» зʼявляється, коли задані ОБИДВА. Ставка без мінімуму — число, від
    якого нема чого відрахувати крок; мінімум без ставки — крок, який нема з чим порівняти.
    Порожньої половини достатньо лише в USD/EUR: там дефолт мінімуму 100, тож вистачає самої
    ставки. У гривні дефолту немає й не буде — мінімальна сума вкладу це умова конкретного банку,
    а не властивість валюти, і підставити її за тебе означало б вигадати чужий тариф. Тому в UAH
    порада мовчить, доки не задані обидва поля. Уже відкритий вклад це не зачіпає: поповнюваний
    помічник запропонує поповнити й без цих полів — немає лише поради ВІДКРИТИ новий.</div>
</div>
`;
}

function rankCard(s) {
  return `<div class="card">
  <h2 class="h-row">Порядок у «Що купити» ${infoBtn("setRank")}</h2>
  ${settingsForm("rankForm", s, SPEC.rank)}
</div>
`;
}

function kindTargetsCard(s) {
  return `<div class="card">
  <h2 class="h-row">Структура за видом інструмента ${infoBtn("setKinds")}</h2>
  ${settingsForm("kindTargetsForm", s, SPEC.kindTargets)}
</div>
`;
}

function npfCreditCard(s) {
  return `<div class="card">
  <h2 class="h-row">Податкова знижка на внески в НПФ ${infoBtn("setNPFCredit")}</h2>
  ${settingsForm("npfCreditForm", s, SPEC.npfCredit)}
  <div class="sub-xs muted">Перше поле — і перемикач, і стеля: держава повертає сплачене, а не
    дарує. Порожньо = знижка не рахується. Працює лише проти ЗАРПЛАТИ: дохід ФОПа права на неї
    не дає. Ліміт щороку інший — прожитковий мінімум працездатних на 1 січня × 1,4.
    Оцінка нікуди не входить: ні в капітал, ні в календар, ні в проєкцію.</div>
</div>
`;
}

function limitsCard(s) {
  return `<div class="card">
  <h2 class="h-row">Ліміти концентрації ${infoBtn("setLimits")}</h2>
  ${settingsForm("limitsForm", s, SPEC.limits)}
</div>
`;
}

function reserveSettingsCard(s) {
  return `<div class="card">
  <h2 class="h-row">Резерв на чорний день ${infoBtn("setReserve")}</h2>
  ${settingsForm("reserveSettingsForm", s, SPEC.reserve)}
  <div class="sub-xs muted">Третє поле — це СТЕЛЯ, а не черга: доки запасу бракує, у «Що купити»
    з'явиться рядок «спершу поповнити резерв» на вказану частку вільних грошей, а решта далі йде
    в папери. Порожньо = застосунок про резерв не заговорить. Резерв від цього не стає
    купівельною спроможністю: гроші йдуть у нього, а не з нього.</div>
</div>
`;
}

function forecastCard(s) {
  return `<div class="card">
  <h2 class="h-row">Припущення прогнозу ${infoBtn("setForecast")}</h2>
  ${settingsForm("forecastAssumptionsForm", s, SPEC.forecast)}
</div>
`;
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
  const windows = d.windows || [];
  const table = opsGrid({
    cols: [
      { key: "label", label: "Вікно", cell: (w) => esc(w.label) },
      { key: "pct", label: "%/рік", num: true, cell: (w) => pct(w.pct) },
      { key: "range", label: "Курс від → до", cls: "muted sub-xs",
        cell: (w) => esc(w.from) + " → " + esc(w.to) },
    ],
    rows: windows,
    caption: "Знецінення гривні по вікнах: період, відсоток на рік, курси",
  });
  return `<div class="card">
    <h2>Знецінення гривні</h2>
    <div class="tiles flush mb">
      ${tile("Чинне значення", pct(d.effective_pct), `<div class="sub">${src}</div>`)}
    </div>
    <div class="muted fine mb">Це число ділить <b>кожну реальну
      дохідність</b> у застосунку й керує прогнозом. Порожнє поле «Гривня слабшає» вище означає
      «бери виміряне» — саме так його й повертають назад на автоматику.</div>
    ${windows.length ? `${table}
      <div class="sub mt-sm">Застосунок бере <b>десятирічне</b> вікно, і різниця між
        рядками пояснює чому: гривня падає стрибками, тож коротке вікно ловить або стрибок, або
        затишшя між ними. Довге усереднює і те, і те.</div>`
      : `<div class="muted">${esc(d.note || "історії курсу ще немає")}</div>`}
  </div>`;
}

// Ціль і валютні частки — половина колишнього #setForm. Друга половина
// (знецінення, довгострокова ставка, сповзання) поїхала в «Припущення»:
// вона там і живе за змістом.
function goalCard(s) {
  return `<div class="card">
    <h2 class="h-row">Ціль і валюта ${infoBtn("planFlows")}</h2>
    ${settingsForm("goalForm", s, SPEC.goal)}
  </div>`;
}

// Три числа, які застосунок вважає ймовірними, а не заданими. Знецінення
// тут головне: воно ділить КОЖНУ реальну дохідність, і саме тому стоїть на
// одній сторінці з таблицею виміряних вікон унизу — щоб число, яке ти
// вписуєш, було видно поруч із тим, що виміряно з курсів НБУ.
function rateAssumptionsCard(s) {
  return `<div class="card">
    <h2 class="h-row">Ставка й знецінення ${infoBtn("setForecast")}</h2>
    ${settingsForm("rateAssumptionsForm", s, SPEC.rateAssumptions)}
  </div>`;
}

/** Стратегія і ціль: пресети, ціль із дедлайном, цільові валютні частки. */
export async function strategy(ctx, main) {
  const s = await ctx.api("GET", "settings");
  main.innerHTML = `${strategyCardHTML(ctx, s)}${goalCard(s)}`;
  wireStrategy(ctx, main, s);
  onSubmit(ctx, main.querySelector("#goalForm"), settingsPut(SPEC.goal));
}

/** Частки й межі: скільки чого має бути і скільки чого забагато. */
export async function mix(ctx, main) {
  const s = await ctx.api("GET", "settings");
  main.innerHTML = `${kindTargetsCard(s)}${limitsCard(s)}`;
  onSubmit(ctx, main.querySelector("#kindTargetsForm"), settingsPut(SPEC.kindTargets));
  onSubmit(ctx, main.querySelector("#limitsForm"), settingsPut(SPEC.limits));
}

/** Інструменти реінвесту: за яких умов помічник узагалі радить вклад,
 *  у якому порядку сортувати поради й чи рахувати податкову знижку НПФ. */
export async function instruments(ctx, main) {
  const s = await ctx.api("GET", "settings");
  main.innerHTML = `${depositCard(s)}${rankCard(s)}${npfCreditCard(s)}`;
  onSubmit(ctx, main.querySelector("#depositSettingsForm"), settingsPut(SPEC.deposit));
  onSubmit(ctx, main.querySelector("#rankForm"), settingsPut(SPEC.rank));
  onSubmit(ctx, main.querySelector("#npfCreditForm"), settingsPut(SPEC.npfCredit));
}

/** Резерв: скільки я витрачаю за місяць, на скільки місяців хочу запас і
 *  яку частку вільних грошей туди спрямовувати, доки його бракує. Три
 *  числа, але власна сторінка: від них залежить і смужка резерву в
 *  «Активах», і «на скільки вистачить» у прогнозі, і рядок «спершу
 *  поповнити резерв» у «Що купити». */
export async function reserve(ctx, main) {
  const s = await ctx.api("GET", "settings");
  main.innerHTML = reserveSettingsCard(s);
  onSubmit(ctx, main.querySelector("#reserveSettingsForm"), settingsPut(SPEC.reserve));
}

/** Припущення: те, що застосунок вважає ймовірним, а не заданим. */
export async function assumptions(ctx, main) {
  const [s, deval] = await Promise.all([
    ctx.api("GET", "settings"),
    ctx.soft("devaluation", null),
  ]);
  main.innerHTML = `${rateAssumptionsCard(s)}${forecastCard(s)}${devalHTML(deval)}`;
  onSubmit(ctx, main.querySelector("#rateAssumptionsForm"), settingsPut(SPEC.rateAssumptions));
  onSubmit(ctx, main.querySelector("#forecastAssumptionsForm"), settingsPut(SPEC.forecast));
}
