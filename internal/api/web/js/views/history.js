// Як росте: крива з добових знімків і таблиця-архів під нею.

import { esc, curSym, plural, uah2 as fmtUAH } from "../format.js";
import { infoBtn } from "../info.js";
import { seriesChart, wireChartTips } from "../charts.js";
import { disclosure } from "../disclosure.js";


// Знімки для кривої «Як росте»: тягнуться раз, читаються графіком і
// таблицею під ним.
let snapsCache = [];

// ---------- склад і структура ----------



export function snapNonZero(s) {
  return (s.invested_uah || 0) > 0 || (s.nominal_uah_eq || 0) > 0 || (s.account_uah || 0) > 0;
}

// Собівартість дня: облігації + фонди + вклади.
//
// invested_uah — це ЛИШЕ облігації (рахується циклом по позиціях ОВДП),
// тож саме по собі воно не «скільки я вклав». deposits_uah — внесені
// гроші, тобто водночас і вартість, і собівартість. Бракувало фондів, і
// саме заради цього зʼявилась колонка funds_cost_uah (міграція 0018).
//
// null, якщо фонди є, а їхньої собівартості в знімку ще не писали: без
// неї сума занижена рівно на вартість фондів, і різниця з капіталом
// прочиталась би як прибуток, якого не було.
function costOf(s) {
  if ((s.funds_uah || 0) > 0 && !(s.funds_cost_uah > 0)) return null;
  return (s.invested_uah || 0) + (s.funds_cost_uah || 0) + (s.deposits_uah || 0);
}

const RANGE_KEY = "oddinvest.planRange";
function planRange() {
  try { return localStorage.getItem(RANGE_KEY) === "all" ? "all" : "month"; }
  catch (_) { return "month"; }
}

const daysInMonth = (ds) => { const p = ds.split("-"); return new Date(+p[0], +p[1], 0).getDate(); };

// Дані для підказок: будуються разом із графіками, читаються проводкою.
let capTip = null, planTip = null;

// Порожні знімки до появи портфеля (зроблені автоматично о 06:10 ще без
// даних) не малюємо — інакше вони «якорять» графік у нулі й лінія
// виглядає як фейковий стрибок 0 → капітал за один день.
function usableSnaps(all) {
  let i = 0;
  while (i < (all || []).length && !snapNonZero(all[i])) i++;
  return (all || []).slice(i);
}

function tooShortHTML(title, key, n) {
  return `<div class="card"><h2 class="h-row">${title} ${infoBtn(key)}</h2>
    <div class="muted">Крива будується з добових знімків (пишуться щодня о 06:10,
    або одразу після «↻ Оновити НБУ»). Потрібно ≥2 знімки з даними — наразі ${n}.
    Порожні знімки до появи портфеля не рахуються.</div></div>`;
}

// ---------- картка 1: з чого складається капітал ----------

// Смугами, а не лініями: верхня межа стосу — увесь капітал, число, якого
// на лініях від нуля не прочитати. І новий інструмент більше не «зʼявляється
// в повітрі» — його смуга просто починає рости від нуля.
function capitalCardHTML(ctx, snaps) {
  const dates = snaps.map((s) => s.date);
  const areas = [
    { name: "ОВДП (номінал)", color: "var(--oi-series-nominal)", area: true, values: snaps.map((s) => s.nominal_uah_eq || 0) },
    { name: "Фонди", color: "var(--oi-series-funds)", area: true, values: snaps.map((s) => s.funds_uah || 0) },
    { name: "Вклади", color: "var(--oi-series-deposits)", area: true, values: snaps.map((s) => s.deposits_uah || 0) },
    { name: "Рахунок", color: "var(--oi-series-account)", area: true, values: snaps.map((s) => s.account_uah || 0) },
  ].filter((s) => s.values.some((v) => v > 0));

  const cost = snaps.map(costOf);
  const series = areas.concat(cost.some((v) => v != null)
    ? [{ name: "Собівартість", color: "var(--oi-series-neutral)", values: cost, dash: true }] : []);

  capTip = { dates, series, total: snaps.map((_, i) => areas.reduce((sum, a) => sum + a.values[i], 0)), cost };
  const { svg, legend } = seriesChart(dates, series, { label: "Структура капіталу по днях" });
  const gain = cost[cost.length - 1] != null
    ? capTip.total[capTip.total.length - 1] - cost[cost.length - 1] : null;
  // Розрив може бути й відʼємним — тоді це збиток, і називати його
  // «заробітком зі знаком мінус» означало б змусити читати мінус двічі.
  const gainLine = gain == null
    ? `Лінія собівартості починається там, звідки її записують у знімок: домальовувати минуле заднім числом означало б вигадати історію.`
    : gain >= 0
      ? `Розрив між верхом смуг і пунктиром — заробіток: зараз <b>${fmtUAH(gain)}</b>.`
      : `Зараз капітал НИЖЧЕ за вкладене на <b>${fmtUAH(-gain)}</b> — пунктир іде поверх смуг.`;
  return `<div class="card"><h2 class="h-row">Капітал ${infoBtn("capital")}</h2>
    <div class="chart-wrap">${svg}<div class="chart-tip" data-tip="cap"></div></div>
    <div class="lg">${legend}</div>
    <div class="sub">Смуги складаються одна на одну, тож верхня межа — увесь капітал. ${gainLine}</div></div>`;
}

// ---------- картка 2: чи встигаю за планом ----------

// Обидві лінії рахуються ВІД ПОЧАТКУ ВІКНА, а не від нуля історії. Доти
// план накопичувався від першого знімка, а факт показував увесь портфель —
// величини різної природи в одних осях, тож пунктир неминуче відривався й
// тиснув масштаб. Тепер обидві відповідають на одне питання: скільки
// додано з початку періоду.
function planCardHTML(ctx, allSnaps) {
  const mode = planRange();
  const month = (allSnaps[allSnaps.length - 1].date || "").slice(0, 7);
  const snaps = mode === "month" ? allSnaps.filter((s) => s.date.slice(0, 7) === month) : allSnaps;
  const btn = (v, t) => `<button class="sm ${mode === v ? "" : "quiet"}" data-range="${v}">${t}</button>`;
  const head = `<h2 class="h-row" style="justify-content:space-between">
    <span>Факт vs план ${infoBtn("plan")}</span>
    <span style="display:flex;gap:var(--oi-gap-xs)">${btn("month", "місяць")}${btn("all", "уся історія")}</span></h2>`;

  if (snaps.length < 2) {
    return `<div class="card">${head}<div class="muted">Для цього періоду ще замало знімків —
      наразі ${snaps.length}. Зʼявляться протягом місяця.</div></div>`;
  }
  const dates = snaps.map((s) => s.date);
  // План: кожен день додає ціль ТОГО дня ÷ днів у місяці. Зміна цілі
  // впливає лише вперед — минула частина лінії лишається такою, якою план
  // був тоді, тож стрибок нахилу означає, що ціль змінили.
  let acc = 0, anyTarget = false;
  const plan = snaps.map((s) => {
    const t = s.month_target_uah || 0;
    if (t > 0) anyTarget = true;
    acc += t / daysInMonth(s.date);
    return acc;
  });
  const cost = snaps.map(costOf);
  const base = cost.find((v) => v != null);
  const fact = cost.map((v) => (v == null || base == null ? null : v - base));

  const series = [{ name: "Внесено", color: "var(--oi-series-invested)", values: fact }];
  if (anyTarget) series.push({ name: "План", color: "var(--oi-series-plan)", values: plan, dash: true });
  planTip = { dates, series };
  const { svg, legend } = seriesChart(dates, series, { label: "Внесено проти плану" });

  const last = fact[fact.length - 1], target = plan[plan.length - 1];
  const verdict = !anyTarget ? `Ціль не задана — пунктира немає. Задається в «Налаштуваннях».`
    : last == null ? `Внесене за період порахувати нема з чого.`
    : last >= target
      ? `Випереджаєш план на <b>${fmtUAH(last - target)}</b>.`
      : `Відстаєш від плану на <b>${fmtUAH(target - last)}</b>.`;
  const scope = mode === "month" ? "цього місяця" : "за всю історію знімків";
  return `<div class="card">${head}
    <div class="chart-wrap">${svg}<div class="chart-tip" data-tip="plan"></div></div>
    <div class="lg">${legend}</div>
    <div class="sub">Обидві лінії рахуються від початку періоду, тож порівнюються напряму.
      Внесено ${scope}: <b>${last == null ? "—" : fmtUAH(last)}</b>. ${verdict}</div></div>`;
}

// Блок історії — угорі «Портфеля», одразу під плитками дохідностей:
// дивишся часто, окрема вкладка була б зайвою.
export async function chartBlockHTML(ctx) {
  const snaps = usableSnaps(await ctx.soft("snapshots", []));
  snapsCache = snaps;
  capTip = planTip = null;
  if (snaps.length < 2) return tooShortHTML("Капітал", "capital", snaps.length);

  const x = (ctx.summary || {}).xirr || {};
  const xp = Object.entries(x).filter(([, v]) => v != null).map(([c, v]) => `${curSym(c)} ${v.toFixed(2)}%`);
  const xirrLine = xp.length
    ? `Фактична дохідність (XIRR): <b>${xp.join(" · ")}</b>`
    : `Фактична дохідність (XIRR) зʼявиться, коли набереться 30 днів історії`;
  return `${capitalCardHTML(ctx, snaps)}${planCardHTML(ctx, snaps)}
    <div class="card"><div class="sub">${xirrLine}</div></div>`;
}

// Проводка обох графіків: підказки під курсором і перемикач періоду.
export function wireHistory(ctx, main) {
  const money = (v) => (v == null ? "—" : fmtUAH(v));
  const rows = (tip, i, extra) => {
    if (!tip) return "";
    const list = tip.series.map((s) =>
      `<div class="r"><span><i style="background:${s.color}"></i>${esc(s.name)}</span>
        <b>${money(s.values[i])}</b></div>`).join("");
    return `<div><b>${esc(tip.dates[i])}</b></div>${list}${extra || ""}`;
  };
  wireChartTips(main.querySelector('[data-tip="cap"]')?.closest(".chart-wrap"), (i) =>
    rows(capTip, i, capTip
      ? `<div class="r tot"><span>Разом</span><b>${money(capTip.total[i])}</b></div>` : ""));
  wireChartTips(main.querySelector('[data-tip="plan"]')?.closest(".chart-wrap"), (i) => rows(planTip, i));

  main.querySelectorAll("[data-range]").forEach((b) =>
    b.addEventListener("click", () => {
      try { localStorage.setItem(RANGE_KEY, b.dataset.range); } catch (_) {}
      ctx.reload();
    }));
}

export function snapshotsTableHTML(ctx) {
  const snaps = snapsCache || [];
  if (snaps.length < 2) return "";
  // Тепер справді згорнута — раніше про це казав лише коментар. Це архів:
  // крива вище відповідає на те саме питання, а числа потрібні зрідка.
  const shown = Math.min(snaps.length, 14);
  const rows = snaps.slice(-14).reverse();
  // Колонку показуємо лише тоді, коли в ній є хоч щось: інакше портфель
  // без вкладів отримав би стовпчик нулів, який читається як «нічого не
  // заробив», а не «такого інструмента в мене немає».
  const hasFunds = rows.some((s) => (s.funds_uah || 0) > 0);
  const hasDeps = rows.some((s) => (s.deposits_uah || 0) > 0);
  const hasAcc = rows.some((s) => (s.account_uah || 0) > 0);
  const col = (on, head, cell) => on ? { head, cell } : null;
  const cols = [
    { head: `<th class="num">ОВДП</th>`, cell: (s) => fmtUAH(s.nominal_uah_eq) },
    col(hasFunds, `<th class="num">Фонди</th>`, (s) => fmtUAH(s.funds_uah || 0)),
    col(hasDeps, `<th class="num">Вклади</th>`, (s) => fmtUAH(s.deposits_uah || 0)),
    col(hasAcc, `<th class="num">Рахунок</th>`, (s) => fmtUAH(s.account_uah || 0)),
    { head: `<th class="num">Частка USD</th>`, cell: (s) => `${(s.usd_share_pct || 0).toFixed(1)}%` },
    { head: `<th class="num">Не перевкл.</th>`, cell: (s) => fmtUAH(s.uninvested_uah) },
  ].filter(Boolean);
  // На кривій ведучі нулі просто не малюються, а в таблиці клітинку не
  // сховаєш — тож тут те саме доводиться сказати словами.
  const gap = (hasDeps && rows.some((s) => !(s.deposits_uah > 0)))
    || (hasFunds && rows.some((s) => !(s.funds_uah > 0)))
    ? `<div class="sub" style="margin-top:8px">Нулі на ранніх днях означають «тоді ще не записували
       в історію», а не «тоді цього не було»: колонки фондів і вкладів з'явились у знімку пізніше
       за самі інструменти.</div>` : "";
  return `<div class="card">${disclosure("snaps", "Останні знімки", `
    <div class="table-scroll"><table>
      <thead><tr><th>Дата</th><th class="num">Вкладено</th>${cols.map((c) => c.head).join("")}</tr></thead>
      <tbody>${rows.map((s) => `<tr><td>${esc(s.date)}</td>
        <td class="num">${fmtUAH(s.invested_uah)}</td>
        ${cols.map((c) => `<td class="num">${c.cell(s)}</td>`).join("")}</tr>`).join("")}</tbody></table></div>${gap}`,
    `${shown} ${plural(shown, "день", "дні", "днів")}`)}</div>`;
}

// ---------- банківські вклади ----------
// Третій інструмент: розклад, як в облігації, але оподаткований, як фонд,
// і без вторинного ринку. Діючі вклади живуть у спільній таблиці позицій
// — тут лишились форма відкриття й архів розірваних.


