// Як росте: крива з добових знімків і таблиця-архів під нею.

import { esc, curSym, plural, uah2 as fmtUAH } from "../format.js";
import { infoBtn } from "../info.js";
import { seriesChart } from "../charts.js";
import { disclosure } from "../disclosure.js";


// Знімки для кривої «Як росте»: тягнуться раз, читаються графіком і
// таблицею під ним.
let snapsCache = [];

// ---------- склад і структура ----------


// seriesFrom — серія, яка починається з першого дня, де є дані. Порожній
// масив, якщо даних немає взагалі: показувати лінію в нулі означало б
// стверджувати, що інструмента немає, хоча насправді його просто не
// записували.
function seriesFrom(snaps, name, color, pick) {
  const vals = snaps.map((s) => pick(s) || 0);
  const from = vals.findIndex((v) => v > 0);
  if (from < 0) return [];
  return [{ name, color, values: vals.map((v, i) => (i < from ? null : v)) }];
}

export function snapNonZero(s) {
  return (s.invested_uah || 0) > 0 || (s.nominal_uah_eq || 0) > 0 || (s.account_uah || 0) > 0;
}

// Блок «Як росте» — угорі «Портфеля», одразу під плитками дохідностей:
// дивишся часто, окрема вкладка була б зайвою.
export async function chartBlockHTML(ctx) {
  const all = await ctx.soft("snapshots", []);
  // Порожні знімки до появи портфеля (зроблені автоматично о 06:10 ще без
  // даних) не малюємо — інакше вони «якорять» графік у нулі й лінія
  // виглядає як фейковий стрибок 0 → капітал за один день.
  let i = 0;
  while (i < (all || []).length && !snapNonZero(all[i])) i++;
  const snaps = (all || []).slice(i);
  snapsCache = snaps;
  if (snaps.length < 2) {
    return `<div class="card"><h2 class="h-row">Як росте ${infoBtn("growth")}</h2>
      <div class="muted">Крива будується з добових знімків (пишуться щодня о 06:10,
      або одразу після «↻ Оновити НБУ»). Потрібно ≥2 знімки з даними — наразі ${snaps.length}.
      Порожні знімки до появи портфеля не рахуються.</div></div>`;
  }
  const dates = snaps.map((s) => s.date);
  // План — накопичувальна сума фактично діючих цілей: кожен день додає
  // target_того_дня / днів_у_місяці. Тож зміна цілі впливає лише вперед,
  // а минула частина лінії лишається такою, якою план був тоді.
  const daysInMonth = (ds) => { const p = ds.split("-"); return new Date(+p[0], +p[1], 0).getDate(); };
  let acc = 0, anyTarget = false;
  const plan = snaps.map((s) => {
    const t = s.month_target_uah || 0;
    if (t > 0) anyTarget = true;
    acc += t / daysInMonth(s.date);
    return acc;
  });
  const series = [
    { name: "Вкладено (грн-екв.)", color: "var(--oi-series-invested)", values: snaps.map((s) => s.invested_uah) },
    { name: "Номінал", color: "var(--oi-series-nominal)", values: snaps.map((s) => s.nominal_uah_eq) },
    { name: "Рахунок", color: "var(--oi-series-account)", values: snaps.map((s) => s.account_uah || 0) },
    // Фонди й вклади потрапили в знімок пізніше за себе самих (міграції
    // 0012 і 0016). Нулі ДО першого ненульового дня — це «колонки ще не
    // було», а не «грошей не було»: намалювати їх означало б показати
    // зліт капіталу в день, коли насправді нічого не сталося. Тому
    // ведучі нулі стають null, і лінія просто починається пізніше.
    ...seriesFrom(snaps, "Фонди", "var(--oi-series-funds)", (s) => s.funds_uah),
    ...seriesFrom(snaps, "Вклади", "var(--oi-series-deposits)", (s) => s.deposits_uah),
  ];
  if (anyTarget) series.push({ name: "План (накопич.)", color: "var(--oi-series-plan)", values: plan, dash: true });
  const x = (ctx.summary || {}).xirr || {};
  const xp = Object.entries(x).filter(([, v]) => v != null).map(([c, v]) => `${curSym(c)} ${v.toFixed(2)}%`);
  const xirrLine = xp.length
    ? `Фактична дохідність (XIRR): <b>${xp.join(" · ")}</b> — деталі у «Портфелі»`
    : `Фактична дохідність (XIRR) з'явиться, коли набереться 30 днів історії`;
  const { svg, legend } = seriesChart(dates, series);
  return `<div class="card"><h2 class="h-row">Як росте ${infoBtn("growth")}</h2>
    <div style="overflow-x:auto">${svg}</div><div style="margin-top:8px">${legend}</div>
    <div class="muted" style="margin-top:8px;font-size:13px">«План (накопич.)» — цільовий темп вкладень наростаючим підсумком (місячна ціль ÷ дні місяця). Факт вище пунктиру = випереджаєш план, нижче = відстаєш.</div>
    <div class="muted" style="margin-top:8px;font-size:13px;border-top:1px solid var(--oi-border);padding-top:8px">${xirrLine}</div></div>`;
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


