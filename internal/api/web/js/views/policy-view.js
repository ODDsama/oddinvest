// Розділ «Політика» — чого я хочу. Чотири сторінки: Стратегія, Гроші
// місяця, Реінвест, Припущення й важелі.
//
// Доти їх було сім, і межі йшли по механізму, а не по питанню: частки
// стояли окремо від стратегії, яка їх і ставить, а резерв, цілі й борг —
// на трьох сторінках, хоч ріжуть ті самі гроші місяця однією чергою
// (ревізія 2026-10-03). Форми лишились тими самими — склеєні лише
// сторінки, тож жоден ключ не змінив ні назви, ні місця запису.
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

import { esc, pct, uah2 as fmtUAH } from "../format.js";
import { money as moneyField, pct as pctField, date as dateField, selectOf, formHTML } from "../fields.js";
import { tile } from "../components.js";
import { onSubmit } from "../forms.js";
import { infoBtn } from "../info.js";
import { opsGrid } from "../grid.js";
import { strategyCardHTML, wireStrategy, RANKS, RESERVE_FROM } from "./strategy.js";
import { sensitivityHTML } from "./forecast.js";
import { wireDisclosures } from "../disclosure.js";
import { CURRENCIES } from "../constants.js";

// Валюта витрат — звичайний вибір із довідника значень домену, а не
// посилання на сутність, тож selectOf, а не refSelect: у REFS живуть
// брокери, фонди й лоти, тобто те, що в застосунку існує окремим записом.
// Трійка кодів береться з CURRENCIES — літерала тут бути не може, і це
// не стилістика: `make ui-kit-boundary` валить збірку на валютах повз
// довідник.
const CURRENCY_OPTS = CURRENCIES.map((c) => [c, c]);

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


// Форми «Політики» — це такі списки й нічого більше.
const SPEC = {
  deposit: [
    { key: "deposit_min_usd", label: "Мінімум вкладу USD", ph: "порожньо = 100" },
    { key: "deposit_min_eur", label: "Мінімум вкладу EUR", ph: "порожньо = 100" },
    { key: "deposit_min_uah", label: "Мінімум вкладу UAH", ph: "порожньо = вимкнено, дефолту немає" },
    { key: "deposit_rate_usd_pct", label: "Ставка нового вкладу USD, %", type: "pct", ph: "порожньо = без поради" },
    { key: "deposit_rate_eur_pct", label: "Ставка нового вкладу EUR, %", type: "pct", ph: "порожньо = без поради" },
    { key: "deposit_rate_uah_pct", label: "Ставка нового вкладу UAH, %", type: "pct", ph: "порожньо = без поради" },
  ],
  // Критерій і лінійка — ОДНА форма: обидва про порядок у «Що взяти».
  // Лінійка доти жила перемикачем на самій картці й скидалась із кожним
  // перезавантаженням.
  rank: [
    { key: "reinvest_rank", label: "Критерій", type: "select", opts: RANKS },
    { key: "reinvest_order", label: "Лінійка дохідності", type: "select",
      opts: [["real", "реальна — після податку й знецінення"], ["nominal", "номінальна — як у договорі"]] },
  ],
  // Валюта й вид — в ОДНІЙ формі, бо разом і складають «скільки чого»;
  // знаменники в них різні, і це сказано під формою (sharesCard).
  shares: [
    { key: "usd_target_share_pct", label: "Цільова частка USD, %", type: "pct" },
    { key: "eur_target_share_pct", label: "Цільова частка EUR, %", type: "pct" },
    { key: "target_bonds_pct", label: "Цільова частка ОВДП, %", type: "pct", ph: "порожньо = без цілі" },
    { key: "target_funds_pct", label: "Цільова частка фондів, %", type: "pct", ph: "порожньо = без цілі" },
    { key: "target_deposits_pct", label: "Цільова частка вкладів, %", type: "pct", ph: "порожньо = без цілі" },
    { key: "target_npf_pct", label: "Цільова частка НПФ, %", type: "pct", ph: "порожньо = без цілі" },
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
    // Витрати ПАРОЮ. Валюта тут не прикраса: ціль подушки виводиться
    // множенням витрат на місяці, тож долар у цьому полі означає, що
    // гривнева ціль сама їде за курсом, а не занижується тихо, доки її не
    // перепишуть руками.
    { key: "monthly_expenses", label: "Місячні витрати", ph: "порожньо = не рахувати" },
    { key: "monthly_expenses_currency", label: "У валюті", type: "select", opts: CURRENCY_OPTS },
    { key: "reserve_target_months", label: "Резерв, місяців витрат", type: "pct", ph: "напр. 6" },
    { key: "reserve_fill_share_pct", label: "З планового доходу в резерв, %", type: "pct", ph: "порожньо = не пропонувати" },
    // Друга половина тієї самої стелі: скільки — вище, з ЧОГО — тут.
    { key: "reserve_fill_from", label: "Резерв наповнювати", type: "select", opts: RESERVE_FROM },
    // Голова й стеля строку. Обидва про ДОСТУП, а не про розмір: подушку
    // можна тримати на вкладах, але лише в темпі, у якому її витрачають.
    { key: "reserve_liquid_months", label: "Доступно миттєво, місяців витрат", type: "pct",
      ph: "напр. 2 — поза будь-яким вкладом" },
    { key: "reserve_max_term_months", label: "Найдовша сходинка драбини, місяців", type: "pct",
      ph: "0 або порожньо = резерв не замикати" },
    // Ціна користування власною подушкою. Не про розмір і не про доступ —
    // про дисципліну, і саме тому стоїть останньою в блоці, а в наборах
    // стратегій її немає взагалі: від розподілу вона не залежить.
    { key: "reserve_loan_rate_pct", label: "Ставка позики в себе, % річних", type: "pct",
      ph: "напр. 12 — діє на НАСТУПНІ позики" },
  ],
  goals: [
    { key: "goals_fill_share_pct", label: "З планового доходу в цілі, %", type: "pct",
      ph: "порожньо = не пропонувати" },
    { key: "goals_fill_from", label: "Цілі наповнювати", type: "select", opts: RESERVE_FROM },
  ],
  debt: [
    // Підпис називає КАРТКОВІ гроші, і це не косметика: доти тут стояло
    // «з планового доходу», і воно було правдою рівно доти, доки борг
    // різався з портфельних. Тепер він їх не чіпає — стеля стала порадою
    // на сторінці боргу, порахованою від того, що лишається на картці
    // після обовʼязкового платежу.
    { key: "debt_fill_share_pct", label: "З карткових грошей на дострокове погашення, %",
      type: "pct", ph: "порожньо = не пропонувати" },
    { key: "reserve_debt_months", label: "Стеля резерву на час боргу, місяців витрат",
      type: "pct", ph: "порожньо = не обрізати" },
    { key: "goals_while_debt", label: "Цілі накопичення на час боргу", type: "select",
      opts: [["keep", "наповнювати як завжди"], ["pause", "поставити на паузу"]] },
  ],
  forecast: [
    { key: "rate_spread_pp", label: "Розкид ставки, п.п.", type: "pct", ph: "порожньо = 3" },
    { key: "deval_spread_pp", label: "Розкид знецінення, п.п.", type: "pct", ph: "порожньо = 4" },
  ],
  // Мета — разом із тим, що з неї жити: «достатній дохід» і «знімати
  // щомісяця» — це та сама мета, сказана місячними грішми, а не
  // припущення прогнозу, де вони стояли доти.
  goal: [
    { key: "goal_amount_uah", label: "Мета капіталу, ₴", ph: "скільки хочу накопичити" },
    { key: "goal_date", label: "Дедлайн — коли", type: "date" },
    { key: "income_target_uah", label: "Достатній дохід, ₴/міс", ph: "порожньо = місячні витрати" },
    { key: "withdraw_monthly_uah", label: "Знімати щомісяця, ₴", ph: "порожньо = місячні витрати" },
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
    «Новий вклад» у «Що взяти» зʼявляється, коли задані ОБИДВА. Ставка без мінімуму — число, від
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
  <h2 class="h-row">Порядок у «Що взяти» ${infoBtn("setRank")}</h2>
  ${settingsForm("rankForm", s, SPEC.rank)}
  <div class="sub-xs muted">Лінійка міняє лише порядок на екрані. Черга задач, журнал рішень і
    прогноз міряють себе реальною завжди: номінальні ставки гривні й долара незіставні. У валюті
    звітності, відмінній від гривні, номінальної лінійки немає зовсім.</div>
</div>
`;
}

// Підпис про знаменники — обов'язковий: валютна частка міряється від
// УСЬОГО капіталу (разом із резервом і цілями), видова — без них
// (engine/state_rebalance.go). Без цього рядка «USD 40 % + ОВДП 60 %»
// читалось би як одна сотня, поділена двічі.
function sharesCard(s) {
  return `<div class="card">
  <h2 class="h-row">Частки: валюта й вид ${infoBtn("setKinds")}</h2>
  ${settingsForm("sharesForm", s, SPEC.shares)}
  <div class="sub-xs muted">Валюта міряється від УСЬОГО капіталу — разом із резервом і цілями.
    Вид — від портфеля БЕЗ резерву й цілей: у резерву своя ціль у місяцях витрат, у цілі —
    названа сума, і з паперами вони не конкурують. Тому дві групи не складаються в одну сотню.
    Сума валютних не може перевищувати 100 %, і так само сума видових — сервер такої форми не
    прийме.</div>
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
  <div class="sub-xs muted">Витрати задаються тією валютою, у якій вони мисляться: «1 500 $ на
    місяць» — це одиниця рахунку, а не оцінка в чужих грошах. Гривневе число застосунок виводить
    сам за СЬОГОДНІШНІМ курсом, і разом із ним їде ціль резерву — вписана руками гривня стояла б
    на місці й тихо занижувалась, доки курс іде вгору.</div>
  <div class="sub-xs muted">Четверте поле — це СТЕЛЯ, а не черга: доки резерву бракує, у «Що взяти»
    з'явиться рядок «спершу поповнити резерв» на вказану частку планового доходу місяця, а решта
    далі йде в папери. Порожньо = застосунок про резерв не заговорить. Резерв від цього не стає
    купівельною спроможністю: гроші йдуть у нього, а не з нього.</div>
  <div class="sub-xs muted">П'яте — з ЯКИХ грошей та стеля ріже. Сама вона й доти міряла себе
    ПЛАНОВИМ доходом місяця, а забирала своє з будь-чого, що приходило, — тобто купон ішов у
    резерв, хоч рахувалась вона із зарплати. «Лише з планових» узгоджує ці дві половини:
    резерв закривають надходження за планом, а купони, відсотки й дивіденди йдуть у папери.
    Середній варіант лишає йому ще й погашення ОВДП і тіла вкладів — це не заробіток, а власні
    гроші, що вийшли з паперу, і вважати їх новими можна з тим самим правом, що й зарплату.</div>
  <div class="sub-xs muted">Два останні — про ДОСТУП, а не про розмір. Резерв можна тримати
    на строкових вкладах (це ~10% після податку замість нуля готівкою), але лише в темпі,
    у якому його витрачають: 300 000 ₴ на 6 місяців означає 50 000 ₴ щомісяця, і один річний вклад
    на всю суму дає ті самі 300 000 ₴ і НІЧОГО з них тоді, коли вони потрібні. «Доступно
    миттєво» — єдина тверда вимога: аварія не витрачається помісячно.</div>
</div>
`;
}

function goalsSettingsCard(s) {
  return `<div class="card">
  <h2 class="h-row">Цілі накопичення ${infoBtn("setGoals")}</h2>
  ${settingsForm("goalsSettingsForm", s, SPEC.goals)}
  <div class="sub-xs muted">Стеля одна на ВСІ цілі разом, і це рішення, а не спрощення.
    Скільки цілі належить за місяць, вона знає сама: розрив ділиться на місяці до дати.
    Стеля відповідає на інше питання — скільки місяць узагалі витримає. Доки сума
    потрібних темпів у неї влазить, кожна ціль бере рівно своє.</div>
  <div class="sub-xs muted">Коли не влазить — цілі беруть ПО ЧЕРЗІ за порядком, який ти
    задав кожній, а тим, кому не дісталось, застосунок пише «щоб устигнути, бракує стільки-то
    на місяць». Ділити стелю пропорційно він не буде: так кожна ціль дістала б трохи менше,
    ніж треба, тобто всі дедлайни провалились би одразу й мовчки.</div>
  <div class="sub-xs muted">Черга ж проти резерву одна: резерв забирає своє ПЕРШИМ, і цілі
    ріжуть лише з того, що після нього лишилось. Аварія не має дати й може статись завтра, а
    річ, на яку збираєш, дату має — на те вона й ціль.</div>
  <div class="sub-xs muted">Друге поле — з ЯКИХ грошей ця стеля ріже, і воно НЕЗАЛЕЖНЕ від
    такого самого в резерві. Резерв багато хто свідомо наповнює лише зарплатою, а на авто
    збирають і з премії, і з купона; спільний ключ віддав би обом найсуворішу з двох
    політик.</div>
</div>
`;
}

/** Борг: порада дострокового, стеля подушки й пауза цілей. Ключі
 *  зʼявились разом із боргом і доти не мали жодного екрана — задати їх
 *  можна було лише через API, тобто ніяк.
 *
 *  Абзац про те, що перше поле «ріже з портфельних грошей» і стоїть у черзі
 *  між подушкою й цілями, звідси прибрано: розкладка гроші в борг більше не
 *  веде (engine/allocate.go, блок «БОРГУ ТУТ БІЛЬШЕ НЕМАЄ»), і довідка
 *  описувала механізм, якого немає. */
function debtSettingsCard(s) {
  return `<div class="card">
  <h2 class="h-row">Борг ${infoBtn("debts")}</h2>
  ${settingsForm("debtSettingsForm", s, SPEC.debt)}
  <div class="sub-xs muted">Перше поле — ПОРАДА про дострокове погашення на сторінці
    «Борги». Вона міряється від карткових грошей місяця — того, що лишається після
    обовʼязкового платежу й відкладеного в портфель, — і з портфеля нічого не забирає.
    Обовʼязкові платежі стелі не мають: вони не вибір, і застосунок віднімає їх сам.
    Порожньо — порада не показується.</div>
  <div class="sub-xs muted">Друге поле обрізає ціль резерву, доки живий борг, що коштує
    реальних грошей, — або доки на картці стоїть дата виходу з ліміту. Обидва краї погані:
    резерв на шість місяців під нуль відсотків поруч із боргом під пʼятдесят коштує
    різницю ставок, а резерву немає взагалі — це наступна поломка холодильника назад на
    кредитний ліміт. Тому це стеля, а не вимикач, і вона знімається САМА.</div>
  <div class="sub-xs muted">Останнє — пауза цілей. Замовчування «наповнювати»: мовчазна
    зупинка накопичення була б найгіршим виглядом помилки, і обирати паузу треба
    свідомо.</div>
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
// Інфляція — ДРУГА лінійка реальності, і картка навмисно стоїть поруч із
// знеціненням: два числа, які відповідають на різні питання про ті самі
// гроші, мусять бути на одному екрані.
//
// Налаштування в неї немає жодного, на відміну від знецінення. Там
// ручний ярус є, бо очікування людини з даних не виводиться; ІСЦ —
// опублікований факт, і четверте число тут не змогло б ані розійтися з
// ним корисно, ані сказати щось нове.
function inflHTML(d) {
  if (!d) return "";
  const windows = d.windows || [];
  const place = d.place || [];
  const table = opsGrid({
    cols: [
      { key: "label", label: "Вікно", cell: (w) => esc(w.label) },
      { key: "pct", label: "%/рік", num: true, cell: (w) => pct(w.pct) },
      { key: "range", label: "Місяці", cls: "muted sub-xs",
        cell: (w) => esc(w.from) + " → " + esc(w.to) },
    ],
    rows: windows,
    caption: "Інфляція по вікнах: період, відсоток на рік, місяці",
  });
  const placeTable = place.length ? opsGrid({
    cols: [
      { key: "years", label: "Серед", cell: (w) => `${w.years} р. (${w.points} міс.)` },
      { key: "pctile", label: "Перцентиль", num: true, cell: (w) => pct(w.percentile, 0) },
      { key: "median", label: "Медіана", num: true, cell: (w) => pct(w.median_pct) },
      { key: "range", label: "Від → до", cls: "muted sub-xs",
        cell: (w) => `${pct(w.min_pct)} → ${pct(w.max_pct)}` },
    ],
    rows: place,
    caption: "Місце нинішньої річної інфляції серед історії: вікно, перцентиль, медіана, межі",
  }) : "";
  return `<div class="card">
    <h2 class="h-row">Інфляція ${infoBtn("setInflation")}</h2>
    <div class="tiles flush mb">
      ${tile("Чинне значення", pct(d.effective_pct),
    `<div class="sub">${esc(d.source === "measured" ? "виміряно з ІСЦ НБУ" : "ряду ще замало")}</div>`)}
      ${d.now_pct ? tile("Зараз, рік до року", pct(d.now_pct),
    `<div class="sub">за ${esc(d.now_month || "")}</div>`) : ""}
    </div>
    <div class="muted fine mb">Це число НЕ ділить реальну дохідність — нею й далі
      керує знецінення. Інфляція стоїть <b>другою лінійкою</b>: у розкладі кожної ставки
      під «реальною проти долара» є «реальна проти цін». Складати їх не можна.</div>
    ${windows.length ? table : `<div class="muted">${esc(d.note || "ряду цін ще немає")}</div>`}
    ${placeTable ? `<h3 class="mt">Де стоїть нинішня</h3>${placeTable}
      <div class="sub mt-sm">Це <b>вимірювання минулого</b>, а не сигнал: ані порога,
        ані поради тут немає навмисно. Інфляція має ту саму властивість, що й гривня, —
        рекордна вона рівно до наступного разу: 2022-й дав 26.6% після 10.0%, 2023-й —
        5.1% після 26.6%.</div>` : ""}
  </div>`;
}

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

// Мета — половина колишнього #setForm. Друга половина (знецінення,
// довгострокова ставка, сповзання) поїхала в «Припущення»: вона там і
// живе за змістом. Валютні частки — у «Частках», поруч із видовими.
function goalCard(s) {
  return `<div class="card">
    <h2 class="h-row">Мета капіталу ${infoBtn("forecast")}</h2>
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

/** Стратегія: набір, мета капіталу, частки й межі — усе, що каже, яким
 *  має бути портфель. */
export async function strategy(ctx, main) {
  const s = await ctx.api("GET", "settings");
  main.innerHTML = `${strategyCardHTML(ctx, s)}${goalCard(s)}${sharesCard(s)}${limitsCard(s)}`;
  wireStrategy(ctx, main, s);
  onSubmit(ctx, main.querySelector("#goalForm"), settingsPut(SPEC.goal));
  onSubmit(ctx, main.querySelector("#sharesForm"), settingsPut(SPEC.shares));
  onSubmit(ctx, main.querySelector("#limitsForm"), settingsPut(SPEC.limits));
}

/** З чого складається ціль резерву — числами з документа стану.
 *
 *  Доти ціль стояла одним числом на картці резерву, а поля, які її
 *  рухають (витрати, місяці, стеля на час боргу, відсоток позик у себе),
 *  — на трьох сторінках політики. Тепер вони на одній, і над ними —
 *  розклад: що дає яке поле.
 *
 *  Жодної арифметики тут немає (CLAUDE.md §5): кожне число — окреме поле
 *  reserve з бекенда (state/derive.go, ReserveTarget). Без зведення
 *  (сторінка «Політики» його не чекає) картка просто не малюється. */
function reserveTargetHTML(ctx) {
  const r = (ctx.summary || {}).reserve;
  if (!r || !(r.target_uah > 0)) return "";
  const line = (t) => `<div class="sub">${t}</div>`;
  const rows = [
    line(`Витрати ${fmtUAH(r.monthly_expenses_uah)}/міс × ${esc(String(r.target_months))} міс.`),
    r.debt_capped
      ? line(`Стеля на час боргу обрізає ціль — без боргу було б ${fmtUAH(r.full_target_uah)}.`)
      : "",
    r.owed_interest_uah > 0
      ? line(`+ ${fmtUAH(r.owed_interest_uah)} відсотка за позиками в себе (без них ${
        fmtUAH(r.base_target_uah)}).`)
      : "",
    r.debt_cover_uah > 0
      ? line(`Рубіж покриття боргу — ${fmtUAH(r.debt_cover_uah)}${
        r.debt_cover_gap_uah > 0 ? `, бракує ${fmtUAH(r.debt_cover_gap_uah)}` : ", перекрито"}.`)
      : "",
  ].join("");
  return `<div class="card">
    <h2 class="h-row">Ціль резерву — ${fmtUAH(r.target_uah)} ${infoBtn("reserve")}</h2>
    ${rows}
    <div class="sub-xs muted mt-sm">Зараз у резерві ${fmtUAH(r.uah)}${
  r.gap_uah > 0 ? `, до цілі бракує ${fmtUAH(r.gap_uah)}` : " — ціль зібрана"}.
      Кожне число вище рухається полями нижче.</div>
  </div>`;
}

/** Гроші місяця: резерв, цілі й борг однією сторінкою — у тому порядку,
 *  у якому вони ріжуть гроші місяця. Резерв забирає своє першим, цілі —
 *  з того, що лишилось; борг стоїть останнім не за вагою, а тому, що
 *  його ручки — стеля подушки й пауза цілей — змінюють дві картки вище. */
export async function money(ctx, main) {
  const s = await ctx.api("GET", "settings");
  main.innerHTML = `<div class="card"><div class="sub">Черга грошей місяця:
      обов'язкові платежі → резерв → цілі накопичення → папери. Кожна стеля нижче
      ріже лише з того, що лишилось після попередньої.</div></div>
    ${reserveTargetHTML(ctx)}${reserveSettingsCard(s)}${goalsSettingsCard(s)}${debtSettingsCard(s)}`;
  onSubmit(ctx, main.querySelector("#reserveSettingsForm"), settingsPut(SPEC.reserve));
  onSubmit(ctx, main.querySelector("#goalsSettingsForm"), settingsPut(SPEC.goals));
  onSubmit(ctx, main.querySelector("#debtSettingsForm"), settingsPut(SPEC.debt));
}

/** Реінвест: у якому порядку помічник ставить поради, за яких умов радить
 *  вклад і чи рахує податкову знижку НПФ. */
export async function reinvest(ctx, main) {
  const s = await ctx.api("GET", "settings");
  main.innerHTML = `${rankCard(s)}${depositCard(s)}${npfCreditCard(s)}`;
  onSubmit(ctx, main.querySelector("#depositSettingsForm"), settingsPut(SPEC.deposit));
  onSubmit(ctx, main.querySelector("#rankForm"), settingsPut(SPEC.rank));
  onSubmit(ctx, main.querySelector("#npfCreditForm"), settingsPut(SPEC.npfCredit));
}

/** Припущення й важелі: те, що застосунок вважає ймовірним, і що буде з
 *  метою, якщо кожне з цих чисел зрушити. Важелі переїхали сюди з
 *  «Плану»: вони крутять саме ці припущення, і читати їх треба поруч. */
export async function assumptions(ctx, main) {
  const [s, deval, infl] = await Promise.all([
    ctx.api("GET", "settings"),
    ctx.soft("devaluation", null),
    ctx.soft("inflation", null),
  ]);
  main.innerHTML = `${rateAssumptionsCard(s)}${forecastCard(s)}${sensitivityHTML(ctx)}${
    devalHTML(deval)}${inflHTML(infl)}`;
  onSubmit(ctx, main.querySelector("#rateAssumptionsForm"), settingsPut(SPEC.rateAssumptions));
  onSubmit(ctx, main.querySelector("#forecastAssumptionsForm"), settingsPut(SPEC.forecast));
  wireDisclosures(main);
}
