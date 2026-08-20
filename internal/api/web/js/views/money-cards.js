// Картки «Грошей»: баланси, звірка, резерв, рухи, потік, податок, імпорт.
//
// Бібліотека, а не розділ. Складають її дві сторони, і саме через це вона
// й виділена: журнал живе в «Грошах» і «Активах», а форми — у «Записати»,
// тобто одна й та сама сутність тепер показується у двох різних місцях із
// різних причин. Доти все це лежало в одному файлі поруч зі своїм
// рендером, і кожна картка мовчки вважала, що поверхня в неї одна.
//
// Резерв через це розколотий на три частини (плитки, журнал, форма): він
// єдиний, чиї числа читають в «Активах», а рухи записують у «Записати».
// Розкол зроблено функціями, а не прапорцем-режимом: аргумент «покажи
// мені половину себе» читається гірше, ніж три імені.

import {
  esc, curSym, dayMonth, plural, pct,
  uah2 as fmtUAH, cur2 as fmtCur, money as fmtMoney,
} from "../format.js";
import { infoBtn } from "../info.js";
import { empty } from "../components.js";
import { opsGrid, rowActions, actionsCol } from "../grid.js";
import {
  money as moneyField, text as textField, date as dateField,
  note as noteField, formHTML,
} from "../fields.js";
import { refSelect, refValue } from "../refs.js";
import { routeFor } from "../routes.js";
import { disclosure } from "../disclosure.js";

// ---------- ГАМАНЕЦЬ ----------

/** Скільки лежить на рахунках, по валютах. «Дохід без діла» тут не
 *  прикраса, а єдине місце, де видно гроші, що надійшли й лежать. */
export function walletHTML(ctx) {
  const s = ctx.summary || {};
  const a = s.accounts || {};
  return `<div class="card">
    <h2>Рахунок (гаманець)</h2>
    <div class="tiles flush">
      <div class="tile"><div class="lbl">UAH</div><div class="val">${fmtUAH(a.UAH || 0)}</div></div>
      <div class="tile"><div class="lbl">USD</div><div class="val">${fmtCur(a.USD || 0, "$")}</div></div>
      <div class="tile"><div class="lbl">EUR</div><div class="val">${fmtCur(a.EUR || 0, "€")}</div></div>
      <div class="tile"><div class="lbl">Разом (грн-екв.)</div><div class="val">${fmtUAH(s.account_uah || 0)}</div></div>
      <div class="tile"><div class="lbl">Дохід без діла ${infoBtn("idle")}</div>
        <div class="val">${fmtUAH(s.uninvested_uah || 0)}</div>
        <div class="sub">надійшло й ще не вкладено</div></div>
    </div>
  </div>`;
}

/** Історія рухів: поповнення, зняття, конвертації одним списком за датою.
 *  Купівлі паперів і купони сюди не пишуться — вони рухають рахунок самі. */
export function movesHTML(deposits, conversions) {
  const moves = [
    ...deposits.map((d) => ({ date: d.date, id: d.id, kind: "dep", amount: d.amount, note: d.note })),
    ...conversions.map((c) => ({ date: c.date, id: c.id, kind: "conv", from: c.from, to: c.to, note: c.note })),
  ].sort((x, y) => (x.date < y.date ? 1 : x.date > y.date ? -1 : y.id - x.id));

  // Два ресурси в одному гріді, і саме тому колонка дій описана тут, а не
  // взята готовою з actionsCol(): кнопка мусить назвати СВІЙ ресурс, бо
  // рядок поповнення й рядок конвертації видаляються різними шляхами.
  // Об'єднані вони не для краси — питання «що відбувалось з грошима» не
  // розрізняє, чим саме рух був записаний.
  const cols = [
    { key: "date", label: "Дата", cell: (m) => esc(m.date) },
    { key: "kind", label: "Тип", cell: (m) => (m.kind === "conv" ? "Конвертація"
      : Number(m.amount.amount) >= 0 ? "Поповнення" : "Зняття") },
    { key: "amount", label: "Сума", num: true, cell: (m) => (m.kind === "conv"
      ? `${fmtMoney(m.from)} → ${fmtMoney(m.to)}` : fmtMoney(m.amount)) },
    { key: "note", label: "Нотатка", cell: (m) => {
      if (m.kind !== "conv") return esc(m.note || "");
      // Курс не зберігається — він рахується із сум, тобто з того, що
      // реально сталося в банку. Показуємо його поруч із нотаткою, бо саме
      // за ним конвертацію впізнають, а не за нотаткою.
      const rate = Number(m.from.amount) / Number(m.to.amount);
      return esc(m.note || "") + (isFinite(rate) ? ` (${rate.toFixed(4)})` : "");
    } },
    { key: "acts", label: "", cls: "row-actions nowrap",
      cell: (m) => rowActions(m.kind === "dep" ? "deposits" : "conversions", m.id, {
        label: (m.kind === "conv" ? "конвертацію" : "рух") + " від " + m.date,
      }) },
  ];

  return `<div class="card">
    <h2>Історія рухів</h2>
    ${opsGrid({
    cols, rows: moves,
    caption: "Історія рухів: дата, тип, сума, нотатка",
    empty: "",
  }) || empty(
    "Рухів ще немає",
    "Сюди лягають поповнення, зняття й конвертації. Купівлі паперів і купони рухають рахунок самі.",
    { href: routeFor("deposit"), label: "Додати рух" })}
  </div>`;
}

// ---------- РЕЗЕРВ («МАТРАЦ») ----------
// Окрема сутність, а не рядок у гаманці: на рахунку брокера лежать гроші,
// що ЧЕКАЮТЬ на вкладення, а тут — ті, які вкладати не збираються.
// Змішати їх означало б запропонувати купити папір за аварійні гроші.
// «1.1 місяць» — двічі неправильно: дробові в українській вимагають
// родового («1,1 місяця»), якого в plural() немає, а крапка суперечить
// решті чисел на екрані. Скорочення «міс.» знімає обидва питання.
const monthsNum = (m) => m.toFixed(1).replace(".", ",");

/** Плитки резерву: скільки відкладено, на скільки місяців вистачить, яку
 *  частку капіталу з'їло. Смужка йде до ЦІЛІ, а не до 100% капіталу:
 *  питання «чи вистачить прожити», а не «яку частку портфеля з'їв
 *  матрац» — на друге відповідає окрема плитка поруч. */
export function reserveTilesHTML(ctx) {
  const r = (ctx.summary || {}).reserve;
  if (!r || !r.uah) return "";
  const months = r.months || 0;
  const target = r.target_months || 0;
  const fill = target > 0 ? Math.min(100, (months / target) * 100) : 0;
  const enough = target > 0 && months >= target;
  const places = r.places ? Object.entries(r.places).sort((a, b) => b[1] - a[1]) : [];
  const byCur = r.by_currency ? Object.entries(r.by_currency).sort() : [];
  return `<div class="card">
    <h2 class="h-row">Резерв ${infoBtn("reserve")}</h2>
    <div class="note">Гроші на чорний день. Не інвестиція — але саме тому вони й доступні миттєво, без продажу паперу й розірвання вкладу. У купівельну спроможність не входять.</div>
    <div class="tiles flush">
      <div class="tile"><div class="lbl">Відкладено</div>
        <div class="val">${fmtUAH(r.uah || 0)}</div>
        ${byCur.length > 1 ? `<div class="sub">${byCur.map(([c, v]) =>
    fmtCur(v, curSym(c))).join(" · ")}</div>` : ""}</div>
      <div class="tile"><div class="lbl">Вистачить на</div>
        <div class="val">${months ? `${monthsNum(months)} міс.` : "—"}</div>
        <div class="sub">${months
    ? (target ? `ціль — ${target} ${plural(target, "місяць", "місяці", "місяців")}` : "ціль не задана")
    : (target
      // Ціль СТОЇТЬ, але порахувати її нема з чого. Доти цей стан
      // склеювався з «цілі теж немає» — обидва писали «постав місячні
      // витрати», і людина, яка щойно застосувала набір із ціллю в 12
      // місяців, не мала звідки дізнатись, що число вже записане й мовчить.
      ? "ціль стоїть, але порахувати нема з чого"
      : "постав місячні витрати в «Політиці»")}</div></div>
      <div class="tile"><div class="lbl">Частка капіталу</div>
        <div class="val">${r.share_pct ? pct(r.share_pct) : "—"}</div>
        <div class="sub">не інвестиція, але капітал</div></div>
    </div>
    ${target > 0 && !months ? `<div class="note">Ціль резерву задана — ${target} ${
    plural(target, "місяць", "місяці", "місяців")} витрат, — але самі «місячні витрати» порожні,
      тож ні цілі в гривнях, ні розриву тут не буде: ділити нема на що. Це не «цілі немає»:
      число стоїть і чекає на друге. Задай витрати в
      <a class="lnk" href="${routeFor("policy/reserve")}">Політиці → Резерв</a> — і смужка
      з розривом стануть на місце самі.</div>` : ""}
    ${target > 0 && months ? `<div class="progress mb-sm">
      <span style="--oi-fill:${fill}%;--oi-c:${enough ? "var(--oi-ok)" : "var(--oi-info)"}"></span></div>
      <div class="note">${enough
    ? `запас зібраний${r.uah > r.target_uah ? ` — з перевищенням на ${fmtUAH(r.uah - r.target_uah)}` : ""}`
    : `до цілі ще ${fmtUAH(r.gap_uah || 0)} · ціль ${fmtUAH(r.target_uah || 0)}`}</div>` : ""}
    ${places.length ? `<div class="note">Де лежить: ${places.map(([p, v]) =>
    `${esc(p)} — ${fmtUAH(v)}`).join(" · ")}</div>` : ""}
  </div>`;
}

/** Журнал рухів резерву. */
/** Поля руху резерву — один список і для запису, і для правки.
 *
 *  Місце — вільний текст навмисно, і це єдине поле-не-посилання серед усіх
 *  мігрованих форм. Резерв лежить не в установі, а «в готівці», «у сейфі»,
 *  «на картці дружини»: заводити для цього довідник означало б вигадати
 *  сутність, якої в житті немає, — так само вирішено й у самій схемі
 *  (міграція 0020 лишає place рядком).
 *
 *  Правки резерву доти не було, хоча PUT /api/reserve/{id} існував. */
export const reserveFields = (ctx, row = null) => [
  moneyField("amount", "Сума (+ відклав / − узяв)", {
    ph: "5000.00", required: true, value: row ? row.amount.amount : "",
  }),
  refSelect(ctx, { name: "currency", ref: "currency", value: row ? row.amount.currency : "UAH" }),
  textField("place", "Місце", {
    ph: "готівка / сейф / картка", value: row ? row.place || "" : "",
  }),
  dateField("date", "Дата", row ? { value: row.date } : {}),
  noteField("note", "Нотатка", row ? { value: row.note || "" } : {}),
];

export const reserveBody = (f) => ({
  amount: f.amount.value.trim(),
  currency: refValue(f, "currency"),
  place: f.place.value.trim(),
  date: f.date.value,
  note: f.note.value.trim(),
});

export function reserveJournalHTML(ops) {
  const list = (ops || []).slice()
    .sort((a, b) => (a.date < b.date ? 1 : a.date > b.date ? -1 : b.id - a.id));
  return `<div class="card"><h2>Рухи резерву</h2>
    ${opsGrid({
    cols: [
      { key: "date", label: "Дата", cell: (o) => esc(o.date) },
      { key: "kind", label: "Рух",
        cell: (o) => (Number(o.amount.amount) >= 0 ? "Відклав" : "Узяв") },
      { key: "amount", label: "Сума", num: true, cell: (o) => fmtMoney(o.amount) },
      { key: "place", label: "Місце", cell: (o) => esc(o.place || "")
        + (o.note ? ` <span class="muted">${esc(o.note)}</span>` : "") },
      actionsCol("reserve", { label: (o) => "рух резерву від " + o.date }),
    ],
    rows: list,
    caption: "Рухи резерву: дата, напрям, сума, місце",
    empty: "Рухів резерву ще немає — перший запис заведе матрац і покаже, на скільки місяців його вистачає.",
  })}
  </div>`;
}

/** Форма руху резерву. */
export function reserveFormHTML(ctx) {
  return `<div class="card"><h2 class="h-row">Рух резерву ${infoBtn("reserve")}</h2>
    ${formHTML({ id: "resForm", fields: reserveFields(ctx), submit: "Записати", cls: "mb" })}
    <div class="note">Переклав із рахунку? Запиши ще й зняття в «Записати → Готівка» —
      інакше відкладене виглядатиме як втрата капіталу.</div>
  </div>`;
}

// ---------- РАХУНОК ----------
// Баланси по брокерах: гроші в одного не купують папір в іншого, тож
// «вистачає / не вистачає» має сенс лише в розрізі рахунку.
export function brokerBalancesHTML(ctx) {
  const s = ctx.summary || {};
  const brokers = s.brokers || {};
  const names = Object.keys(brokers).sort((a, b) => a.localeCompare(b, "uk"));
  if (!names.length) return "";
  const rmin = s.reinvest_min || {};
  const rows = names.map((b) => {
    const cur = brokers[b] || {};
    const parts = Object.keys(cur).sort().map((c) => {
      const v = cur[c], min = rmin[c] || 0;
      const enough = min > 0 && v >= min;
      const hint = min > 0
        ? (enough ? `вистачає на ${Math.floor(v / min)}` : `до паперу ще ${fmtCur(min - v, curSym(c))}`)
        : "";
      return `<div class="pv-row"><span>${esc(c)} · <b>${fmtCur(v, curSym(c))}</b></span>
        <span class="${enough ? "t-ok" : "muted"}">${hint}</span></div>`;
    }).join("");
    return `<div class="mb-lg"><div class="mb-xs"><b>${esc(b)}</b></div>${parts}</div>`;
  }).join("");
  return `<div class="card"><h2>Рахунки по брокерах</h2>
    <div class="note">Гроші в одного брокера не купують папір в іншого — тому баланси роздільні.</div>
    ${rows}</div>`;
}

// Звірка: рахунок за записами проти того, що показує брокер.
// Коригування — звичайне поповнення з поміткою, а не окрема сутність:
// так розбіжність лишається видимою в історії, а не ховається.
export function reconcileHTML(ctx) {
  const brokers = (ctx.summary || {}).brokers || {};
  const rows = Object.entries(brokers).flatMap(([b, byCur]) =>
    Object.entries(byCur).map(([c, v]) => ({ b, c, v })));
  if (!rows.length) return "";
  return `<div class="card"><h2 class="card-head">
    <span>Звірка рахунку ${infoBtn("reconcile")}</span></h2>
    ${opsGrid({
    cols: [
      { key: "broker", label: "Брокер", cell: (r) => `${esc(r.b)} ${curSym(r.c)}` },
      { key: "book", label: "За записами", num: true,
        cell: (r) => fmtCur(r.v, curSym(r.c)) },
      { key: "actual", label: "Фактично", num: true,
        cell: (r) => `<input class="recAct num-in" inputmode="decimal"
          data-expected="${r.v}" placeholder="—">` },
      { key: "diff", label: "Розбіжність", num: true, cls: "recDiff muted", cell: () => "—" },
      { key: "fix", label: "", num: true,
        cell: () => `<button class="recFix" disabled>виправити</button>` },
    ],
    rows,
    caption: "Звірка рахунку: брокер, сума за записами, фактична сума, розбіжність",
    rowAttrs: (r) => ({ "data-rec": `${r.b}|${r.c}` }),
  })}</div>`;
}

export function wireReconcile(ctx, main) {
  main.querySelectorAll("tr[data-rec]").forEach((tr) => {
    const inp = tr.querySelector(".recAct");
    const out = tr.querySelector(".recDiff");
    const btn = tr.querySelector(".recFix");
    const [broker, currency] = tr.dataset.rec.split("|");
    const recalc = () => {
      const raw = inp.value.trim().replace(/\s/g, "").replace(",", ".");
      const actual = Number(raw);
      if (!raw || Number.isNaN(actual)) {
        out.textContent = "—"; out.className = "num recDiff muted"; btn.disabled = true;
        return null;
      }
      const diff = Math.round((actual - Number(inp.dataset.expected)) * 100) / 100;
      out.textContent = diff === 0 ? "сходиться" : (diff > 0 ? "+" : "") + fmtCur(diff, curSym(currency));
      // t-ok, а не ok: класу .ok у CSS не існує й ніколи не існувало,
      // тож «сходиться» два роки виходило звичайним текстом — рівно тим
      // самим, що й розбіжність.
      out.className = "num recDiff" + (diff === 0 ? " t-ok" : "");
      btn.disabled = diff === 0;
      return diff;
    };
    inp.addEventListener("input", recalc);
    btn.addEventListener("click", async () => {
      const diff = recalc();
      if (!diff) return;
      btn.disabled = true;
      try {
        await ctx.api("POST", "deposits", {
          amount: String(diff), currency, broker,
          note: diff > 0 ? "звірка: незаписане надходження" : "звірка: незаписана витрата",
        });
        ctx.toast("Коригування додано");
        await ctx.reload();
      } catch (err) {
        ctx.toast(String(err.message || err), false);
        btn.disabled = false;
      }
    });
  });
}

// Імпорт виписки. Два кроки навмисно: спершу показати, що буде
// зроблено, і лише потім писати. Ціна помилки тут — подвоєний баланс,
// а він знаходиться не одразу.
export function importHTML(ctx) {
  return `<div class="card"><h2 class="card-head">
    <span>Імпорт виписки ${infoBtn("import")}</span></h2>
    <div class="muted fine mb-sm">Файл Inzhur (.xlsx). Спершу перегляд — нічого не записується.</div>
    <div class="row-h">
      <input type="file" id="impFile" accept=".xlsx">
      <button id="impPreview">Переглянути</button>
    </div>
    <div class="muted fine mt-sm row-h">
      Враховувати зміни від <input type="date" id="impSince" class="w-md">
      <span>рухається сама після кожного імпорту</span>
    </div>
    <div id="impOut" class="mt"></div></div>`;
}

export function wireImport(ctx, main) {
  const file = main.querySelector("#impFile");
  const out = main.querySelector("#impOut");
  if (!file || !out) return;

  // Водяний знак: показуємо поточний і даємо посунути руками — інакше
  // «перезавантажити позаминулий місяць» стало б неможливим узагалі.
  const since = main.querySelector("#impSince");
  if (since) {
    ctx.api("GET", "settings")
      .then((s) => { if (s && s.import_since) since.value = s.import_since; })
      .catch(() => {});
    since.addEventListener("change", async () => {
      try { await ctx.api("PUT", "settings", { import_since: since.value }); ctx.toast("Дату змінено"); }
      catch (err) { ctx.toast(String(err.message || err), false); }
    });
  }

  const send = async (dry) => {
    if (!file.files || !file.files[0]) { ctx.toast("Обери файл", false); return null; }
    const fd = new FormData();
    fd.append("file", file.files[0]);
    const resp = await ctx.store.raw(
      "import/inzhur" + (dry ? "?dry=1" : ""), { method: "POST", body: fd });
    if (!resp.ok) throw new Error(`${resp.status}: ${(await resp.text()).slice(0, 300)}`);
    // Справжній імпорт міняє геть усе — лоти, рухи, фонди, зведення.
    // Перегляд (dry) не міняє нічого, тож і кеш чіпати нема за що.
    if (!dry) ctx.store.invalidate();
    return resp.json();
  };

  const KIND = { fund_buy: "купівля", fund_sell: "продаж", dividend: "дивіденд",
    deposit: "поповнення", withdrawal: "виведення" };
  const render = (res, dry) => {
    const rows = (res.rows || []).map((r) => {
      const tag = r.conflict
        ? `<div class="t-danger fine-xs">⚠ ${esc(r.conflict)}</div>`
        : r.exists ? `<span class="muted fine-xs">вже є</span>` : "";
      return `<div class="mb-sm">
        <div class="kv">
          <span>${dayMonth(r.date)} · ${KIND[r.kind] || r.kind}${
            r.fund ? ` <span class="muted">${esc(r.fund)}</span>` : ""}${
            r.qty ? ` <span class="muted">${r.qty} серт.</span>` : ""}</span>
          <span><b>${esc(r.amount)}</b>${r.tax && r.tax !== "0.00" ? ` <span class="muted fine-xs">податок ${esc(r.tax)}</span>` : ""} ${r.exists && !r.conflict ? `<span class="muted fine-xs">вже є</span>` : ""}</span>
        </div>${r.conflict ? tag : ""}</div>`;
    }).join("");
    const skipped = (res.skipped || []).map((s) =>
      `<div class="sub-xs">${dayMonth(s.Date || s.date)} · ${esc(s.Op || s.op)} — ${esc(s.Reason || s.reason)}</div>`).join("");
    const conflicts = (res.rows || []).filter((r) => r.conflict).length;
    if (!dry && res.since && since) since.value = res.since;
    out.innerHTML = `
      <div class="mb-sm">Знайдено ${(res.rows || []).length} операцій · <b>${res.new}</b> нових${
        conflicts ? ` · <span class="t-danger">${conflicts} з конфліктом</span>` : ""}</div>
      ${res.before ? `<div class="muted fine mb-sm">${res.before} рядків старші за ${
        dayMonth(res.since)} — не розглядались</div>` : ""}
      ${rows}
      ${skipped ? `<div class="rule-top tight">
        <div class="muted fine mb-xs">пропущено:</div>${skipped}</div>` : ""}
      ${dry && res.new > 0 ? `<button id="impGo" class="mt">Імпортувати ${res.new}</button>` : ""}
      ${!dry ? `<div class="mt-sm t-ok">Записано ${res.imported}</div>` : ""}`;
    const go = out.querySelector("#impGo");
    if (go) {
      go.addEventListener("click", async () => {
        go.disabled = true;
        try { render(await send(false), false); ctx.toast("Імпортовано"); await ctx.reload(); }
        catch (err) { ctx.toast(String(err.message || err), false); go.disabled = false; }
      });
    }
  };

  main.querySelector("#impPreview")?.addEventListener("click", async (e) => {
    e.target.disabled = true;
    try { const res = await send(true); if (res) render(res, true); }
    catch (err) { ctx.toast(String(err.message || err), false); }
    finally { e.target.disabled = false; }
  });
}

// Рух грошей за період — казан і те, що він купив.
//
// Питання «по операціях не видно, як і куди я перевклав» — це запит на
// звіт про рух, а не на прив'язку купона до покупки. Купон і власні
// внески змішуються на рахунку, звідти йде покупка; показати треба саме
// це, а не вигадану лінію від однієї виплати до одного паперу.
//
// Тотожність унизу — не оздоба: якщо вона не сходиться, розійшлись облік
// і дійсність, і це має бути видно.
export function flowHTML(f) {
  if (!f) return "";
  // Рядки виписки — дані, а не розмітка: підпис, число і знак перед ним.
  // Знак тут не арифметика, а НАПРЯМ: «− куплено» показує додатну суму зі
  // словом «куплено», бо в виписці читають рух, а не сальдо.
  const lines = [
    { label: "Було на рахунках", uah: f.opening_uah, sign: "" },
    { label: "+ надійшло доходу", uah: f.income_uah, sign: "+" },
    { label: "+ внесено своїх", uah: f.contributed_uah, sign: "+" },
    { label: "− куплено", uah: f.purchased_uah, sign: "−" },
    f.conversions_uah
      ? { label: "± конвертації", uah: f.conversions_uah, sign: f.conversions_uah > 0 ? "+" : "−" }
      : null,
  ].filter(Boolean);
  const detail = (f.rows || []).filter((r) => r.kind === "purchase" && r.uah < 0);
  return `<div class="card">
    <h2 class="h-row">Рух грошей ${infoBtn("cashflow")}</h2>
    <div class="note">${esc(f.from)} → ${esc(f.to)}</div>
    ${opsGrid({
    head: false,
    cols: [
      { key: "label", label: "Стаття", cell: (r) => r.label },
      { key: "uah", label: "Сума", num: true,
        cell: (r) => r.sign + fmtUAH(Math.abs(r.uah || 0)) },
    ],
    rows: lines,
    caption: `Рух грошей ${esc(f.from)} — ${esc(f.to)}: стаття й сума`,
    foot: [{ cell: "= лишилось" }, { cell: fmtUAH(f.closing_uah || 0), num: true }],
  })}
    ${detail.length ? `<details class="disclosure" data-fold="flowbuys">
      <summary>Куди пішли<span class="hint">${detail.length} ${
  plural(detail.length, "операція", "операції", "операцій")}</span></summary>
      <div class="disclosure-body">${opsGrid({
    head: false,
    cols: [
      { key: "date", label: "Дата", cls: "muted", cell: (r) => esc(r.date) },
      { key: "label", label: "Що", cell: (r) => esc(r.label) },
      { key: "uah", label: "Сума", num: true, cell: (r) => fmtUAH(Math.abs(r.uah)) },
    ],
    rows: detail,
    caption: "Куди пішли гроші: дата, операція, сума",
  })}</div>
    </details>` : ""}
  </div>`;
}

// Скільки з доходу забрала держава — грошима, а не ставкою.
//
// Асиметрія між інструментами вже зашита в реальну дохідність, але
// відсотком її не відчуваєш. Вклад під 16% і папір під 16% — це різні
// гроші, і рядок «податок з'їв стільки-то» каже це пряміше.
// Рік звітності. Живе в localStorage, як і решта перемикачів періоду:
// декларацію заповнюють не в той самий день, коли дивляться картку, і
// повертатись до того самого року щоразу руками — зайва робота.
const TAX_KEY = "oddinvest.taxYear";
export function taxYear() {
  const now = new Date().getFullYear();
  try {
    const v = parseInt(localStorage.getItem(TAX_KEY), 10);
    // Межа знизу та сама, що й у бекенді: сміття в сховищі не має
    // перетворюватись на запит, який упаде чотирисоткою.
    if (v >= 1990 && v <= now) return v;
  } catch (_) { /* приватний режим */ }
  return now;
}

export function taxHTML(x) {
  if (!x) return "";
  const now = new Date().getFullYear();
  const sel = x.year || taxYear();
  const years = Array.from({ length: 5 }, (_, i) => now - i);
  const picker = `<select data-tax-year>${years.map((y) =>
    `<option value="${y}"${y === sel ? " selected" : ""}>${y}</option>`).join("")}</select>`;
  // Порожній рік — не привід ховати картку: «за 2023-й податків не було»
  // це відповідь, а зникла картка читається як поломка.
  const body = opsGrid({
    cols: [
      { key: "label", label: "Джерело", cell: (l) => esc(l.label) },
      { key: "gross", label: "Нараховано", num: true, cell: (l) => fmtUAH(l.gross_uah) },
      { key: "tax", label: "Податок", num: true,
        cell: (l) => (l.tax_uah ? "−" + fmtUAH(l.tax_uah) : "—") },
      { key: "net", label: "Чистими", num: true, cell: (l) => fmtUAH(l.net_uah) },
      { key: "rate", label: "Ставка", num: true,
        cell: (l) => (l.gross_uah ? pct(l.rate_pct) : "—") },
    ],
    rows: x.by_kind || [],
    caption: `Податок на дохід за ${esc(String(sel))}: джерело, нараховано, податок, чистими, ставка`,
    foot: [
      { cell: "Разом" },
      { cell: fmtUAH(x.gross_uah), num: true },
      { cell: "−" + fmtUAH(x.tax_uah), num: true },
      { cell: fmtUAH(x.net_uah), num: true },
      { cell: pct(x.rate_pct), num: true },
    ],
    // Порожній рік — не привід ховати картку: «за 2023-й податків не було»
    // це відповідь, а зникла картка читається як поломка.
    empty: "За цей рік оподаткованого доходу не було.",
  });
  // Знижка — ОКРЕМОЮ таблицею під основною, а не рядком у ній: там
  // арифметика «нараховано − податок = чистими», і від'ємний рядок ламає
  // всі три числа разом зі ставкою. І в «Разом» вона не входить навмисно:
  // те число відповідає на «скільки з мене взяли».
  const credits = (x.credits || []).length
    ? `<h4 class="mt">Держава повертає</h4>
       ${opsGrid({
    cols: [
      { key: "label", label: "Підстава", cell: (l) => esc(l.label) },
      { key: "back", label: "Повернення", num: true, cls: "t-ok",
        cell: (l) => "+" + fmtUAH(l.net_uah) },
    ],
    rows: x.credits,
    caption: "Що держава повертає: підстава й сума",
  })}
       <div class="sub-xs t-warn">Це ОЦІНКА, а не факт, і в «Разом» вище вона не входить.
         Знижку треба отримати декларацією до 31 грудня наступного року; вона працює лише
         проти зарплати й не переноситься на інші роки.</div>`
    : "";
  return `<div class="card">${disclosure("tax", "Податок на дохід",
    `<div class="sub card-head">
       <span>${esc(x.from)} → ${esc(x.to)}</span><span>рік: ${picker}</span></div>
     ${body}
     ${credits}
     <div class="sub card-head mt-sm">
       <span>Купон ОВДП звільнений від податку, дивіденд фонду й відсотки вкладу — ні.
         Ставки не зашиті: у фонду береться фактично утримане з виписки, у вкладу —
         ставка самого вкладу.</span>
       <button class="sm" data-tax-csv="${sel}">Завантажити CSV</button></div>
     ${x.fx_basis ? `<div class="sub-xs">Валютні суми: ${esc(x.fx_basis)}${
        x.fx_max_lag_days > 1 ? `; найбільше відставання ${x.fx_max_lag_days} ${
          plural(x.fx_max_lag_days, "день", "дні", "днів")}` : ""}.${
        x.note ? ` ${esc(x.note)}.` : ""}</div>` : ""}`,
    pct(x.rate_pct))}</div>`;
}
