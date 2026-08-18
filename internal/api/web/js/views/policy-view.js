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
import { tile } from "../components.js";
import { onSubmit } from "../forms.js";
import { infoBtn } from "../info.js";
import { strategyCardHTML, wireStrategy, RANKS } from "./strategy.js";

// Обидві форми пишуть у той самий PUT /settings і відрізняються лише
// списком полів. Збираємо payload із тих, що РЕАЛЬНО є у формі: PUT
// частковий, тож відсутнє поле не шлеться порожнім і не затирає значення.
// «channels» тут свідомо немає: брокерами керує окремий довідник.
const settingsPut = (keys) => (f) => {
  const body = {};
  for (const k of keys) {
    if (f.elements[k]) body[k] = f.elements[k].value.trim();
  }
  return { method: "PUT", path: "settings", body, msg: "Налаштування збережено" };
};


function depositCard(s) {
  return `<div class="card">
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
`;
}

function rankCard(s) {
  return `<div class="card">
  <h2 class="h-row">Порядок у «Що купити» ${infoBtn("setRank")}</h2>
  <form id="rankForm">
    <label>Критерій<select name="reinvest_rank">${
      RANKS
        .map(([v, t]) => `<option value="${v}"${(s.reinvest_rank || "plan") === v ? " selected" : ""}>${t}</option>`)
        .join("")}</select></label>
    <div class="form-actions"><button type="submit">Зберегти</button></div>
  </form>
</div>
`;
}

function kindTargetsCard(s) {
  return `<div class="card">
  <h2 class="h-row">Структура за видом інструмента ${infoBtn("setKinds")}</h2>
  <form id="kindTargetsForm">
    <label>Ціль ОВДП, %<input name="target_bonds_pct" inputmode="decimal" placeholder="порожньо = без цілі" value="${esc(s.target_bonds_pct || "")}"></label>
    <label>Ціль фондів, %<input name="target_funds_pct" inputmode="decimal" placeholder="порожньо = без цілі" value="${esc(s.target_funds_pct || "")}"></label>
    <label>Ціль вкладів, %<input name="target_deposits_pct" inputmode="decimal" placeholder="порожньо = без цілі" value="${esc(s.target_deposits_pct || "")}"></label>
    <label>Ціль НПФ, %<input name="target_npf_pct" inputmode="decimal" placeholder="порожньо = без цілі" value="${esc(s.target_npf_pct || "")}"></label>
    <div class="form-actions"><button type="submit">Зберегти</button></div>
  </form>
</div>
`;
}

function npfCreditCard(s) {
  return `<div class="card">
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
`;
}

function limitsCard(s) {
  return `<div class="card">
  <h2 class="h-row">Ліміти концентрації ${infoBtn("setLimits")}</h2>
  <form id="limitsForm">
    <label>Макс. в одному папері, %<input name="limit_isin_pct" inputmode="decimal" placeholder="порожньо = без ліміту" value="${esc(s.limit_isin_pct || "")}"></label>
    <label>Макс. в одній установі, %<input name="limit_broker_pct" inputmode="decimal" placeholder="брокер або банк" value="${esc(s.limit_broker_pct || "")}"></label>
    <label>Макс. погашень в один рік, %<input name="limit_year_pct" inputmode="decimal" placeholder="від усіх погашень" value="${esc(s.limit_year_pct || "")}"></label>
    <div class="form-actions"><button type="submit">Зберегти</button></div>
  </form>
</div>
`;
}

function reserveSettingsCard(s) {
  return `<div class="card">
  <h2 class="h-row">Резерв на чорний день ${infoBtn("setReserve")}</h2>
  <form id="reserveSettingsForm">
    <label>Місячні витрати, ₴<input name="monthly_expenses_uah" inputmode="decimal" placeholder="порожньо = не рахувати" value="${esc(s.monthly_expenses_uah || "")}"></label>
    <label>Ціль запасу, місяців<input name="reserve_target_months" inputmode="decimal" placeholder="напр. 6" value="${esc(s.reserve_target_months || "")}"></label>
    <label>З вільних грошей у резерв, %<input name="reserve_fill_share_pct" inputmode="decimal"
      placeholder="порожньо = не пропонувати" value="${esc(s.reserve_fill_share_pct || "")}"></label>
    <div class="form-actions"><button type="submit">Зберегти</button></div>
  </form>
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
  <form id="forecastAssumptionsForm">
    <label>Достатній дохід, ₴/міс<input name="income_target_uah" inputmode="decimal" placeholder="порожньо = місячні витрати" value="${esc(s.income_target_uah || "")}"></label>
    <label>Знімати щомісяця, ₴<input name="withdraw_monthly_uah" inputmode="decimal" placeholder="порожньо = місячні витрати" value="${esc(s.withdraw_monthly_uah || "")}"></label>
    <label>Розкид ставки, п.п.<input name="rate_spread_pp" inputmode="decimal" placeholder="порожньо = 3" value="${esc(s.rate_spread_pp || "")}"></label>
    <label>Розкид знецінення, п.п.<input name="deval_spread_pp" inputmode="decimal" placeholder="порожньо = 4" value="${esc(s.deval_spread_pp || "")}"></label>
    <div class="form-actions"><button type="submit">Зберегти</button></div>
  </form>
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

// Ціль і валютні частки — половина колишнього #setForm. Друга половина
// (знецінення, довгострокова ставка, сповзання) поїхала в «Припущення»:
// вона там і живе за змістом.
function goalCard(s) {
  return `<div class="card">
    <h2 class="h-row">Ціль і валюта ${infoBtn("planFlows")}</h2>
    <form id="goalForm">
      <label>Ціль, ₴<input name="goal_amount_uah" inputmode="decimal" placeholder="скільки хочу накопичити" value="${esc(s.goal_amount_uah || "")}"></label>
      <label>Дедлайн — коли<input name="goal_date" type="date" value="${esc(s.goal_date || "")}"></label>
      <label>Цільова частка USD, %<input name="usd_target_share_pct" inputmode="decimal" value="${esc(s.usd_target_share_pct || "")}"></label>
      <label>Цільова частка EUR, %<input name="eur_target_share_pct" inputmode="decimal" value="${esc(s.eur_target_share_pct || "")}"></label>
      <div class="form-actions"><button type="submit">Зберегти</button></div>
    </form>
  </div>`;
}

// Три числа, які застосунок вважає ймовірними, а не заданими. Знецінення
// тут головне: воно ділить КОЖНУ реальну дохідність, і саме тому стоїть на
// одній сторінці з таблицею виміряних вікон унизу — щоб число, яке ти
// вписуєш, було видно поруч із тим, що виміряно з курсів НБУ.
function rateAssumptionsCard(s) {
  return `<div class="card">
    <h2 class="h-row">Ставка й знецінення ${infoBtn("setForecast")}</h2>
    <form id="rateAssumptionsForm">
      <label>Гривня слабшає, %/рік<input name="uah_devaluation_pct" inputmode="decimal" placeholder="порожньо = виміряне" value="${esc(s.uah_devaluation_pct || "")}"></label>
      <label>Довгострокова ставка ОВДП, %<input name="terminal_rate_pct" inputmode="decimal" placeholder="порожньо = 11" value="${esc(s.terminal_rate_pct || "")}"></label>
      <label>Ставка сповзає туди за, років<input name="rate_glide_years" inputmode="decimal" placeholder="порожньо = 5" value="${esc(s.rate_glide_years || "")}"></label>
      <div class="form-actions"><button type="submit">Зберегти</button></div>
    </form>
  </div>`;
}

/** Стратегія і ціль: пресети, ціль із дедлайном, цільові валютні частки. */
export async function strategy(ctx, main) {
  const s = await ctx.api("GET", "settings");
  main.innerHTML = `${strategyCardHTML(s)}${goalCard(s)}`;
  wireStrategy(ctx, main, s);
  onSubmit(ctx, main.querySelector("#goalForm"), settingsPut([
    "goal_amount_uah", "goal_date", "usd_target_share_pct", "eur_target_share_pct",
  ]));
}

/** Частки й межі: скільки чого має бути і скільки чого забагато. */
export async function mix(ctx, main) {
  const s = await ctx.api("GET", "settings");
  main.innerHTML = `${kindTargetsCard(s)}${limitsCard(s)}`;
  onSubmit(ctx, main.querySelector("#kindTargetsForm"), settingsPut([
    "target_bonds_pct", "target_funds_pct", "target_deposits_pct", "target_npf_pct",
  ]));
  onSubmit(ctx, main.querySelector("#limitsForm"), settingsPut([
    "limit_isin_pct", "limit_broker_pct", "limit_year_pct",
  ]));
}

/** Інструменти реінвесту: за яких умов помічник узагалі радить вклад,
 *  у якому порядку сортувати поради й чи рахувати податкову знижку НПФ. */
export async function instruments(ctx, main) {
  const s = await ctx.api("GET", "settings");
  main.innerHTML = `${depositCard(s)}${rankCard(s)}${npfCreditCard(s)}`;
  onSubmit(ctx, main.querySelector("#depositSettingsForm"), settingsPut([
    "deposit_min_usd", "deposit_min_eur", "deposit_min_uah",
    "deposit_rate_usd_pct", "deposit_rate_eur_pct", "deposit_rate_uah_pct",
  ]));
  onSubmit(ctx, main.querySelector("#rankForm"), settingsPut(["reinvest_rank"]));
  onSubmit(ctx, main.querySelector("#npfCreditForm"), settingsPut([
    "npf_credit_pdfo_year_uah", "npf_credit_cap_month_uah",
  ]));
}

/** Резерв: скільки я витрачаю за місяць, на скільки місяців хочу запас і
 *  яку частку вільних грошей туди спрямовувати, доки його бракує. Три
 *  числа, але власна сторінка: від них залежить і смужка резерву в
 *  «Активах», і «на скільки вистачить» у прогнозі, і рядок «спершу
 *  поповнити резерв» у «Що купити». */
export async function reserve(ctx, main) {
  const s = await ctx.api("GET", "settings");
  main.innerHTML = reserveSettingsCard(s);
  onSubmit(ctx, main.querySelector("#reserveSettingsForm"), settingsPut([
    "monthly_expenses_uah", "reserve_target_months", "reserve_fill_share_pct",
  ]));
}

/** Припущення: те, що застосунок вважає ймовірним, а не заданим. */
export async function assumptions(ctx, main) {
  const [s, deval] = await Promise.all([
    ctx.api("GET", "settings"),
    ctx.soft("devaluation", null),
  ]);
  main.innerHTML = `${rateAssumptionsCard(s)}${forecastCard(s)}${devalHTML(deval)}`;
  onSubmit(ctx, main.querySelector("#rateAssumptionsForm"), settingsPut([
    "uah_devaluation_pct", "terminal_rate_pct", "rate_glide_years",
  ]));
  onSubmit(ctx, main.querySelector("#forecastAssumptionsForm"), settingsPut([
    "income_target_uah", "withdraw_monthly_uah", "rate_spread_pp", "deval_spread_pp",
  ]));
}
