// Розділ «План» — джерела доходу й витрат, з датами.
//
// Двигун (state_projection.go) бачить ці потоки самостійно: вони живлять
// криву капіталу, віяло сценаріїв, чутливість і незалежність без жодної
// власної арифметики тут — той самий прийом, що й у кошика покупки.
// Розділ лише збирає їх і показує вердикт: скільки план дає і чи цього
// досить.
//
// Форм для дій (set_shares/lock) тут ще немає навмисно — вони приїдуть
// окремим кроком (бекенд уже готовий, /api/plan/actions).

import { esc, today, money as fmtMoney, uah0 as fmtUAH, pct } from "../format.js";
import { infoBtn } from "../info.js";
import { empty } from "../components.js";
import { onSubmit, onDelete } from "../forms.js";
import { fluid, svgTimeline } from "../charts.js";

const CADENCE_LABEL = { month: "щомісяця", quarter: "щокварталу", year: "щороку", once: "разово" };

// ---------- вердикт ----------
//
// Три стани, і плутати їх не можна. Порожній план — запрошення додати
// перший потік. Є потік, немає цілі — саме число, без порівняння: цілі
// немає, тож «бракує/вистачає» відповіді не має. Є й те, й те — порівняння,
// заради якого розділ і існує.
export function planVerdictHTML(ctx) {
  const s = ctx.summary || {};
  const provides = s.plan_provides_uah || 0;
  const f = s.forecast;
  const hasGoal = !!(f && f.goal_amount > 0);

  if (!provides && !hasGoal) {
    return `<div class="card"><h2 class="card-head"><span>План ${infoBtn("planFlows")}</span></h2>
      ${empty("", "Заведи перше джерело доходу нижче — і побачиш, скільки план реально дає щомісяця.")}</div>`;
  }

  const provideTile = `<div class="tile hero"><div class="lbl">План дає</div>
    <div class="val">${fmtUAH(provides)}<span class="muted fine">/міс</span></div></div>`;

  if (!hasGoal) {
    return `<div class="card"><h2 class="card-head"><span>План ${infoBtn("planFlows")}</span></h2>
      <div class="tiles flush">${provideTile}</div>
      <div class="sub-xs mt-sm">Задай ціль і дедлайн у «Налаштуваннях», щоб побачити, чи цього досить.</div></div>`;
  }

  // contrib_plan — те, чого плану бракує ПОНАД себе самого, щоб устигнути
  // до дедлайну (state.Forecast.ContribPlan; коментар у Go — чому саме
  // так). Нуль означає, що план сам виводить на ціль.
  const gap = f.contrib_plan || 0;
  const gapTile = gap > 0
    ? `<div class="tile"><div class="lbl">До цілі бракує ще</div>
        <div class="val t-warn">${fmtUAH(gap)}<span class="muted fine">/міс</span></div></div>`
    : `<div class="tile"><div class="lbl">До цілі</div><div class="val t-ok">із запасом</div></div>`;

  return `<div class="card"><h2 class="card-head"><span>План ${infoBtn("planFlows")}</span></h2>
    <div class="tiles flush">${provideTile}${gapTile}</div>
    <div class="sub-xs mt-sm">Ціль ${fmtUAH(f.goal_amount)} до ${esc(f.date)}.</div></div>`;
}

// ---------- потоки: форма ----------

export function planFlowFormHTML() {
  return `<form id="planFlowForm">
    <label>Назва<input name="name" placeholder="Зарплата" required></label>
    <label>Тип<select name="kind">
      <option value="income">дохід</option>
      <option value="expense">витрата</option>
    </select></label>
    <label>Сума<input name="amount" inputmode="decimal" placeholder="40000.00" required></label>
    <label>Валюта<select name="currency"><option>UAH</option><option>USD</option><option>EUR</option></select></label>
    <label>Періодичність<select name="cadence">
      <option value="month">щомісяця</option>
      <option value="quarter">щокварталу</option>
      <option value="year">щороку</option>
      <option value="once">разово</option>
    </select></label>
    <label>З дати<input name="from_date" type="date" value="${today()}" required></label>
    <label>До дати<input name="until_date" type="date"></label>
    <label>Індексація, %/рік<input name="growth_pct" inputmode="decimal" placeholder="0 (за замовч.)"></label>
    <label>Частка в портфель, %<input name="invest_pct" inputmode="decimal" placeholder="100 (за замовч.)"></label>
    <label>Нотатка<input name="note"></label>
    <div class="form-actions"><button type="submit">Додати</button></div>
  </form>`;
}

// ---------- потоки: список ----------

export function planFlowsListHTML(flows) {
  if (!flows.length) {
    return empty("", "Джерел доходу й витрат ще немає — перше додасть форма нижче.");
  }
  const rows = flows.slice()
    .sort((a, b) => a.from_date < b.from_date ? -1 : a.from_date > b.from_date ? 1 : 0)
    .map((f) => `<tr>
      <td>${esc(f.name)}${f.note ? ` <span class="muted fine-xs">${esc(f.note)}</span>` : ""}</td>
      <td><span class="pill ${f.kind === "income" ? "coupon" : "redemption"}">${
        f.kind === "income" ? "дохід" : "витрата"}</span></td>
      <td class="num">${fmtMoney(f.amount)}</td>
      <td>${CADENCE_LABEL[f.cadence] || esc(f.cadence)}</td>
      <td>${esc(f.from_date)}</td>
      <td>${f.until_date ? esc(f.until_date) : "—"}</td>
      <td class="num">${pct(f.invest_pct)}</td>
      <td class="row-actions"><button class="sm warn" data-delflow="${f.id}"
        aria-label="Видалити потік ${esc(f.name)}">✕</button></td>
    </tr>`).join("");
  return `<div class="table-scroll"><table><thead><tr>
    <th scope="col">Назва</th><th scope="col">Тип</th><th scope="col" class="num">Сума</th>
    <th scope="col">Період</th><th scope="col">З</th><th scope="col">До</th>
    <th scope="col" class="num">У портфель</th><th scope="col"><span class="sr-only">Дії</span></th>
    </tr></thead><tbody>${rows}</tbody></table></div>`;
}

// ---------- стрічка часу ----------
//
// Один запит, одна картинка: смуги потоків і термінів під спільною віссю
// X із кривою капіталу знизу, тож злам кривої стоїть рівно під смугою,
// яка його спричинила (charts.js:svgTimeline).
export function timelineHTML(doc) {
  const empty3 = !(doc.flows || []).length && !(doc.actions || []).length && !(doc.instruments || []).length;
  if (empty3) {
    return `<div class="card"><h2 class="card-head"><span>Стрічка часу ${infoBtn("planTimeline")}</span></h2>
      ${empty("", "Заведи потік чи дію — і на стрічці з'явиться перша смуга.")}</div>`;
  }
  return `<div class="card"><h2 class="card-head"><span>Стрічка часу ${infoBtn("planTimeline")}</span></h2>
    ${fluid((w, h) => svgTimeline(doc, { W: w, H: h }), { cls: "tall" })}
    <div class="sub-xs">Суцільна лінія — за планом, пунктир — за фактичним темпом; затінений
      коридор — між песимістичним і оптимістичним сценаріями.</div></div>`;
}

// ---------- дії: список ----------

// Деталь рядка — те, що різнить дію: частки для set_shares, сума й строк
// для lock. Спільної форми в них немає, тож і колонка не вдає, що є.
function planActionDetail(a) {
  if (a.type === "set_shares") {
    const bits = [
      a.usd_share_pct != null ? `USD ${pct(a.usd_share_pct, 0)}` : null,
      a.eur_share_pct != null ? `EUR ${pct(a.eur_share_pct, 0)}` : null,
    ].filter(Boolean);
    return bits.join(", ") || "—";
  }
  const term = a.months > 0 ? `${a.months} міс.` : "безстроково";
  return `${fmtMoney(a.amount)} під ${pct(a.rate_pct)} · ${term}`;
}

export function planActionsListHTML(actions) {
  if (!actions.length) {
    return empty("", "Дій ще немає — дві форми нижче: зміна валютних часток і замок під ставку.");
  }
  const rows = actions.slice()
    .sort((a, b) => a.date < b.date ? -1 : a.date > b.date ? 1 : 0)
    .map((a) => `<tr>
      <td>${esc(a.date)}</td>
      <td><span class="pill ${a.type === "lock" ? "coupon" : "early"}">${
        a.type === "lock" ? "замок" : "частки"}</span></td>
      <td>${esc(a.name || "—")}</td>
      <td>${planActionDetail(a)}</td>
      <td class="row-actions"><button class="sm warn" data-delaction="${a.id}"
        aria-label="Видалити дію від ${esc(a.date)}">✕</button></td>
    </tr>`).join("");
  return `<div class="table-scroll"><table><thead><tr>
    <th scope="col">Дата</th><th scope="col">Тип</th><th scope="col">Назва</th>
    <th scope="col">Деталі</th><th scope="col"><span class="sr-only">Дії</span></th>
    </tr></thead><tbody>${rows}</tbody></table></div>`;
}

// ---------- дії: форми ----------
//
// Дві форми, а не одна з перемикачем: set_shares і lock не мають
// спільного поля, крім дати, і об'єднана форма лише ховала б, які поля
// стосуються якого типу.
export function planSetSharesFormHTML() {
  return `<form id="planSetSharesForm">
    <label>З дати<input name="date" type="date" value="${today()}" required></label>
    <label>Частка USD, %<input name="usd_share_pct" inputmode="decimal" placeholder="залишити як є"></label>
    <label>Частка EUR, %<input name="eur_share_pct" inputmode="decimal" placeholder="залишити як є"></label>
    <label>Нотатка<input name="note"></label>
    <div class="form-actions"><button type="submit">Змінити частки</button></div>
  </form>`;
}

export function planLockFormHTML() {
  return `<form id="planLockForm">
    <label>Назва<input name="name" placeholder="MilTech" required></label>
    <label>Дата<input name="date" type="date" value="${today()}" required></label>
    <label>Сума<input name="amount" inputmode="decimal" placeholder="50000.00" required></label>
    <label>Валюта<select name="currency"><option>UAH</option><option>USD</option><option>EUR</option></select></label>
    <label>Ставка, %<input name="rate_pct" inputmode="decimal" placeholder="20" required></label>
    <label>Строк, міс. (0 = безстроково)<input name="months" type="number" min="0" placeholder="24"></label>
    <label>Нотатка<input name="note"></label>
    <div class="form-actions"><button type="submit">Замкнути</button></div>
  </form>`;
}

// ---------- розділ цілком ----------

export async function renderPlan(ctx, main) {
  const [flows, actions, timeline] = await Promise.all([
    ctx.soft("plan/flows", []),
    ctx.soft("plan/actions", []),
    ctx.soft("plan", null),
  ]);

  main.innerHTML = `
    ${planVerdictHTML(ctx)}
    ${timeline ? timelineHTML(timeline) : ""}
    <div class="card">
      <h2 class="card-head"><span>Джерела доходу й витрат</span></h2>
      <div class="note">Кожен потік — сума з датою, періодичністю й тим, яка його частка
        доходить до портфеля. «У портфель» рахується від СУМИ потоку: якщо частину зарплати
        зʼїдають витрати, задай тут лише те, що реально йде в інвестиції — задавати 100% і
        ЩЕ заводити витратний потік на ту саму суму означає відняти її двічі.</div>
      ${planFlowsListHTML(flows)}
      ${planFlowFormHTML()}
    </div>
    <div class="card">
      <h2 class="card-head"><span>Дії ${infoBtn("planActions")}</span></h2>
      <div class="note">Точкові рішення на дату: перевести майбутні внески в іншу валюту
        або замкнути суму під ставку на строк — вклад і накопичувальний фонд для проєкції
        не відрізняються, обидва просто лежать і платять за графіком.</div>
      ${planActionsListHTML(actions)}
      ${planSetSharesFormHTML()}
      ${planLockFormHTML()}
    </div>`;

  onSubmit(ctx, main.querySelector("#planFlowForm"), (f) => ({
    path: "plan/flows",
    body: {
      name: f.name.value.trim(), kind: f.kind.value,
      amount: f.amount.value.trim(), currency: f.currency.value,
      cadence: f.cadence.value, from_date: f.from_date.value,
      until_date: f.until_date.value, growth_pct: f.growth_pct.value.trim(),
      invest_pct: f.invest_pct.value.trim(), note: f.note.value.trim(),
    },
    msg: "Потік додано",
  }));

  onDelete(ctx, main, "[data-delflow]", (b) => ({
    path: "plan/flows/" + b.dataset.delflow,
    confirm: "Видалити цей потік?",
    msg: "Потік видалено",
  }));

  onSubmit(ctx, main.querySelector("#planSetSharesForm"), (f) => ({
    path: "plan/actions",
    body: {
      date: f.date.value, type: "set_shares",
      usd_share_pct: f.usd_share_pct.value.trim(), eur_share_pct: f.eur_share_pct.value.trim(),
      note: f.note.value.trim(),
    },
    msg: "Дію додано",
  }));

  onSubmit(ctx, main.querySelector("#planLockForm"), (f) => ({
    path: "plan/actions",
    body: {
      date: f.date.value, type: "lock", name: f.name.value.trim(),
      amount: f.amount.value.trim(), currency: f.currency.value,
      rate_pct: f.rate_pct.value.trim(), months: f.months.value ? parseInt(f.months.value, 10) : 0,
      note: f.note.value.trim(),
    },
    msg: "Замок додано",
  }));

  onDelete(ctx, main, "[data-delaction]", (b) => ({
    path: "plan/actions/" + b.dataset.delaction,
    confirm: "Видалити цю дію?",
    msg: "Дію видалено",
  }));
}
