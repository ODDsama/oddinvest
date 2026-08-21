// План купівель: що я збираюсь узяти, коли — і що з цього вийде.
//
// Рядки живуть у базі (таблиця plan_buys), а не в браузері. Аргумент за
// переїзд і проти нього записаний у шапці handlers_whatif.go: доки рядок
// не мав дати, кошик справді був чернеткою на дві хвилини; щойн дата
// зʼявилась, рядок став частиною плану поруч із потоками доходу.
//
// Форма ОДНА на всі чотири види, з перемикачем зверху. Це протилежне
// рішенню plan-actions.js («дві форми, а не одна з перемикачем»), і
// різниця не в смаку: там set_shares і lock не мали жодного спільного
// поля, крім дати, а тут усі чотири види ділять посилання, дату, брокера
// й гроші й різняться трьома полями. Ховаються вони атрибутом hidden, а
// не стилем — видимість це стан елемента, і в атрибуті його видно і в
// інспекторі, і читачеві екрана, а заразом поле випадає з табуляції.
//
// Таблиця малюється з basket.lines відповіді /api/whatif, а не з
// GET /api/plan/buys. Ціна кроку, брокер за замовчуванням і підсумки вже
// пораховані там одним unit_cost.go; порахувати їх ще й тут означало б
// показати ту саму облігацію за двома цінами на одному екрані.

import { esc, curSym, today, cur2 as fmtCur } from "../format.js";
import {
  money as moneyField, num as numField, date as dateField,
  pct as pctField, check as checkField, note as noteField,
  selectOf, formHTML,
} from "../fields.js";
import { refSelect, refSuggest, refValue, wireRefs, wireSuggest } from "../refs.js";
import { opsGrid, rowActions } from "../grid.js";
import { wireCrud } from "../crud.js";
import { apply, openEdit } from "../forms.js";
import { kindPill } from "../components.js";
import { routeFor } from "../routes.js";
import { fetchWhatIf, impactHTML } from "./buy-plan.js";
import { lotFields, lotBody } from "./bonds.js";
import { depositFields, depositBody } from "./deposits.js";
import { fundOpFields, fundOpBody } from "../fund-ops.js";
import { npfOpFields, npfOpBody } from "../npf.js";

const KINDS = [
  ["bond", "ОВДП"], ["fund", "Сертифікат фонду"],
  ["deposit", "Вклад"], ["npf", "Внесок у пенсійний"],
];

/** Додати рядок у план. Кличеться з «Що купити» кнопкою «+».
 *
 *  Через apply(), а не через сире збереження: тост, перемальовування й
 *  скидання кеша тут потрібні всі три — на відміну від превʼю, де кожен
 *  із них заважав би (див. wirePlanBuys). */
export function addToPlan(ctx, { kind, ref, qty = 1 }) {
  return apply(ctx, { path: "plan/buys", body: { kind, ref, qty } },
    "Додано в план купівель");
}

// --- форма ---

// Групи полів за видом. data-forkind несе список видів, для яких група
// потрібна; перемикач лише ставить/знімає hidden.
const group = (kinds, inner) =>
  `<div data-forkind="${esc(kinds.join(" "))}">${inner}</div>`;

function planBuyFields(ctx, row = null) {
  const r = row || {};
  return [
    selectOf("kind", "Що купуєш", KINDS, r.kind || "bond"),
    group(["bond"], refSuggest({
      name: "isin", ref: "bond", label: "Папір (ISIN)",
      value: r.kind === "bond" ? r.ref || "" : "",
    })),
    group(["fund"], refSelect(ctx, {
      name: "fund", ref: "fund", value: r.kind === "fund" ? r.ref || "" : "",
    })),
    group(["deposit"], refSelect(ctx, {
      name: "bank", ref: "broker", label: "Банк",
      value: r.kind === "deposit" ? r.ref || "" : "",
    })),
    group(["npf"], refSelect(ctx, {
      name: "npf_id", ref: "npf", value: r.kind === "npf" ? r.ref || "" : "",
    })),
    group(["bond", "fund"], numField("qty", "Кількість", {
      min: 1, value: r.qty || 1,
    })),
    // Ціна вручну — лише сертифікату, і лише тому, що каталогу цін фондів
    // у застосунку немає: про фонд, якого ще немає в портфелі, сказати
    // нічого не можна. Ціна ОВДП — номінал плюс НКД із довідника.
    group(["fund"], moneyField("unit_price", "Ціна за штуку", {
      ph: "порожньо — узяти з позиції", value: r.unit_price || "",
    })),
    group(["deposit", "npf"], moneyField("amount", "Сума", {
      ph: "100000.00", value: r.amount || "",
    })),
    group(["deposit"], pctField("rate_pct", "Ставка, %", {
      ph: "порожньо — узяти з налаштувань", value: r.rate_pct || "",
    })),
    group(["deposit"], numField("months", "Строк, місяців", {
      min: 1, value: r.months || 12,
    })),
    group(["deposit"], checkField("is_reserve", "Це подушка (резерв)", {
      checked: !!r.is_reserve,
    })),
    group(["bond", "fund", "deposit"], refSelect(ctx, {
      name: "currency", ref: "currency", blank: "авто (з довідника)",
      value: r.currency || "",
    })),
    group(["bond", "fund", "npf"], refSelect(ctx, {
      name: "broker", ref: "broker", label: "З рахунку",
      value: r.broker || "",
    })),
    // Дата НЕОБОВʼЯЗКОВА, і саме тому value:"" замість типового «сьогодні»:
    // порожньо означає «купую зараз», а майбутня дата переводить рядок у
    // прогноз. Підставлений сьогоднішній день зробив би «зараз»
    // невимовним — довелося б чистити поле руками.
    dateField("buy_date", "Коли планую", { value: r.buy_date || "" }),
    noteField("note", "Нотатка", { value: r.note || "" }),
    `<div class="note">Порожня дата — «купую зараз»: рядок одразу видно в
      частках, драбині й лімітах. Дата попереду переносить його в прогноз —
      сьогоднішні числа він не рухає, зате рухає ціль і точку незалежності.</div>`,
  ];
}

// Тіло запиту. Поля, яких цей вид не має, не надсилаються зовсім:
// бекенд відхиляє зайве (ціна вручну для паперу — 400), і мовчки
// підкладати нулі означало б обходити його перевірку.
function planBuyBody(f) {
  const kind = f.kind.value;
  const val = (name) => (f.elements[name] ? f.elements[name].value.trim() : "");
  const out = {
    kind,
    buy_date: f.buy_date.value,
    note: f.note.value.trim(),
  };
  switch (kind) {
    case "bond":
      out.ref = val("isin");
      out.qty = parseInt(val("qty"), 10) || 0;
      out.currency = refValue(f, "currency");
      out.broker = refValue(f, "broker");
      break;
    case "fund":
      out.ref = refValue(f, "fund");
      out.qty = parseInt(val("qty"), 10) || 0;
      out.unit_price = val("unit_price");
      out.currency = refValue(f, "currency");
      out.broker = refValue(f, "broker");
      break;
    case "deposit":
      out.ref = refValue(f, "bank");
      out.amount = val("amount");
      out.currency = refValue(f, "currency");
      out.rate_pct = val("rate_pct");
      out.months = parseInt(val("months"), 10) || 0;
      out.is_reserve = f.is_reserve.checked;
      break;
    default: // npf
      out.ref = refValue(f, "npf_id");
      out.amount = val("amount");
      out.broker = refValue(f, "broker");
  }
  return out;
}

/** Показати рівно ті групи полів, які потрібні обраному виду. */
function syncKind(form) {
  if (!form || !form.kind) return;
  const kind = form.kind.value;
  form.querySelectorAll("[data-forkind]").forEach((g) => {
    g.hidden = !g.dataset.forkind.split(" ").includes(kind);
  });
}

export function planBuyFormHTML(ctx) {
  return formHTML({
    id: "planBuyForm", fields: planBuyFields(ctx), submit: "Додати в план",
  });
}

// --- таблиця ---

// «Коли» одним словом. Прострочене підписуємо вголос: рядок із учорашньою
// датою рахується як «зараз» (гроші за ним досі не витрачені), і мовчазне
// «зараз» приховало б, що ти щось відклав і забув.
function whenCell(l) {
  if (!l.buy_date) return `<span class="muted">зараз</span>`;
  if (l.future) return esc(l.buy_date);
  return `${esc(l.buy_date)} <span class="t-warn fine-xs">· прострочено</span>`;
}

function linesHTML(basket) {
  return opsGrid({
    cols: [
      {
        key: "label", label: "Що",
        cell: (l) => kindPill(l.kind) + " " + esc(l.label)
          + (l.is_reserve ? ` <span class="muted fine-xs">· подушка</span>` : ""),
      },
      { key: "when", label: "Коли", cell: (l) => whenCell(l) },
      { key: "qty", label: "К-сть", num: true, cell: (l) => String(l.qty) },
      {
        key: "unit", label: "За штуку", num: true,
        cell: (l) => fmtCur(Number(l.unit.amount), curSym(l.currency)),
      },
      {
        key: "total", label: "Разом", num: true,
        cell: (l) => fmtCur(Number(l.total.amount), curSym(l.currency)),
      },
      {
        key: "broker", label: "Де",
        cell: (l) => esc(l.broker) + (l.broker_assumed
          ? `<span class="muted fine-xs"> · обрано за залишком</span>` : ""),
      },
      {
        key: "acts", label: "", cls: "row-actions nowrap",
        cell: (l) => `<button class="sm" data-done="${esc(l.id)}"
          title="Записати як справжню операцію"
          aria-label="Виконано: ${esc(l.label)}">✓</button>`
          + rowActions("plan/buys", l.id, { label: l.label }),
      },
    ],
    rows: (basket.lines || []).map((l) => ({ ...l, id: l.id })),
    caption: "План купівель: що, коли, кількість, ціна, сума, рахунок",
  });
}

/** Картка плану: таблиця, підсумок і нестача. */
export function planBuysHTML(res) {
  const basket = (res || {}).basket || {};
  const totals = (basket.totals || [])
    .map((t) => fmtCur(Number(t.amount), curSym(t.currency))).join(" · ");
  const shorts = (basket.shorts || []).map((s) =>
    `${esc(s.broker)}: бракує ${fmtCur(Number(s.short.amount), curSym(s.currency))}`).join(" · ");
  const anyNow = (basket.lines || []).some((l) => !l.future);
  return `<div class="card"><h2>Що заплановано</h2>
    ${linesHTML(basket)}
    <div class="pv-row mt"><span><b>Разом</b></span><span><b>${totals}</b></span></div>
    ${shorts ? `<div class="sub-xs t-warn mt-xs">${esc(shorts)}</div>`
    : anyNow ? `<div class="sub-xs t-ok mt-xs">грошей вистачає</div>` : ""}
    <div class="sub-xs mt-sm">Ціна тут — «номінал + НКД» для паперу й остання
      відома ціна для сертифіката. У брокера може бути інша, і тоді інші будуть
      усі числа нижче. Нестача рахується лише по рядках «зараз»: сьогоднішній
      залишок нічого не каже про покупку в наступному році.</div>
  </div>`;
}

// --- проводка ---

// Превʼю під час введення.
//
// raw, а не store.post: той на кожен натиск клавіші скидав би кеш усього
// застосунку. Точкова заміна [data-impact], а не ctx.reload(): reload
// переписує main цілком і зʼїв би недонабрану форму разом із фокусом.
//
// AbortController перериває запит, який устиг застаріти, а лічильник seq
// відкидає відповідь, що приїхала не остання: самого abort мало, бо
// перерваний запит може завершитись уже після того, як його змінник
// відповів.
function wirePreview(ctx, main, excludeID) {
  const form = main.querySelector("#planBuyForm");
  const box = main.querySelector("[data-impact]");
  if (!form || !box) return;
  let timer = null, ctl = null, seq = 0;

  const ready = (body) => {
    if (body.kind === "bond" || body.kind === "fund") return !!body.ref && body.qty > 0;
    return !!body.ref && !!body.amount;
  };

  const run = async () => {
    const draft = planBuyBody(form);
    const mine = ++seq;
    if (ctl) ctl.abort();
    ctl = new AbortController();
    // Недонабрана чернетка — не помилка й не привід мовчати: показуємо
    // вплив уже ЗБЕРЕЖЕНОГО плану, тобто те, що людина бачила до того, як
    // почала друкувати.
    const body = ready(draft)
      ? { draft: [draft], ...(excludeID ? { exclude: [excludeID] } : {}) }
      : {};
    try {
      const res = await fetchWhatIf(ctx, body, ctl.signal);
      if (mine !== seq) return;
      box.innerHTML = impactHTML(ctx, res);
    } catch (err) {
      if (mine !== seq || (err && err.name === "AbortError")) return;
      // Тихий рядок, а не тост: тост на кожну літеру неможливо читати, а
      // помилка тут — звичайний стан недонабраного ISIN.
      box.innerHTML = `<div class="card"><div class="muted">${esc(err.message || err)}</div></div>`;
    }
  };

  const schedule = () => { clearTimeout(timer); timer = setTimeout(run, 300); };
  form.addEventListener("input", schedule);
  form.addEventListener("change", schedule);
}

/** Проводка сторінки плану. rows — сирі рядки GET /api/plan/buys: саме
 *  ними заповнюється модалка правки, і саме тому вони потрібні поруч із
 *  готовими рядками кошика. */
export function wirePlanBuys(ctx, main, rows) {
  // Прибираємо ключ старого кошика. НЕ переносимо: формат [{kind,id,qty}]
  // не має ні дати, ні брокера, ні ціни, тож будь-який перенесений рядок
  // став би «купую зараз» — тобто рівно тим станом, у якому користувач і
  // так перебуває. Одноразова міграція виконалась би один раз, ніколи не
  // перевірилась би на реальних даних і лишилась мертвою гілкою назавжди.
  // Прибрати цей рядок можна після наступного релізу (додано 2026-08-21).
  try { localStorage.removeItem("oddinvest.basket"); } catch (_) { /* приватний режим */ }
  const form = main.querySelector("#planBuyForm");
  if (form) {
    syncKind(form);
    form.addEventListener("change", (e) => {
      if (e.target && e.target.name === "kind") syncKind(form);
    });
  }
  wireCrud(ctx, main, {
    resource: "plan/buys", form: "#planBuyForm", title: "Планована купівля",
    rows, fields: planBuyFields, body: planBuyBody,
    confirm: (row) => "Прибрати з плану " + (row.ref || "рядок #" + row.id) + "?",
    msg: { add: "Додано в план", edit: "План виправлено", del: "Прибрано з плану" },
  });
  // Перемикач виду в МОДАЛЦІ: wireCrud проводить поля через wire(), але
  // про власний перемикач цієї форми він не знає — і не має знати.
  main.querySelectorAll('[data-res="plan/buys"][data-edit]').forEach((btn) => {
    btn.addEventListener("click", () => {
      const f = ctx.root && ctx.root.querySelector("#editForm");
      if (!f) return;
      syncKind(f);
      f.addEventListener("change", (e) => {
        if (e.target && e.target.name === "kind") syncKind(f);
      });
    });
  });
  wireDone(ctx, main, rows);
  wirePreview(ctx, main, 0);
}

// «Виконано»: план стає справжньою операцією.
//
// Одна дія користувача — дві операції над моделлю: записати покупку й
// прибрати рядок плану. Через два окремі apply() це дало б два тости, два
// перемальовування, а на збої другого — операцію, записану двічі при
// наступній спробі. applyAll спиняється на першій помилці, і порядок тут
// саме такий: спершу ОПЕРАЦІЯ, потім видалення рядка. Зворотний порядок
// на збої лишив би людину без плану й без покупки.
//
// Поля — ті самі, що у формі відповідної операції, і беруться з неї ж:
// другий список полів купівлі лота розійшовся б із першим на першій зміні.
function wireDone(ctx, main, rows) {
  const byId = new Map((rows || []).map((r) => [String(r.id), r]));
  main.querySelectorAll("[data-done]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const row = byId.get(btn.dataset.done);
      if (!row) { ctx.toast("Рядок не знайдено — онови сторінку", false); return; }
      const spec = doneSpec(ctx, row);
      if (!spec) {
        ctx.toast("Цей вид поки не можна записати одним рухом", false);
        return;
      }
      openEdit(ctx, {
        title: "Записати: " + (row.ref || "покупка"),
        fields: spec.fields, submit: "Записати й прибрати з плану",
        wire: (f) => { wireRefs(f); wireSuggest(ctx, f); },
      }, (f) => ({
        requests: [
          { method: "POST", path: spec.path, body: spec.body(f) },
          { method: "DELETE", path: "plan/buys/" + row.id },
        ],
        msg: "Записано, рядок прибрано з плану",
      }));
    });
  });
}

// Передзаповнення форми операції з рядка плану. Заповнюємо лише те, що в
// рядку СПРАВДІ є: ціну паперу план не тримає (її знає довідник), і
// підставити туди номінал означало б записати вигадану ціну як факт.
//
// Дата операції — СЬОГОДНІ, а не запланована. «Виконано» означає «я це
// щойно зробив», і в паперу, купленого сьогодні, дата купівлі сьогоднішня,
// хай би на коли ти його планував. Запланована дата лишається наміром і
// зникає разом із рядком.
//
// Дата погашення вкладу приходить ГОТОВОЮ з бекенда (maturity_date):
// «сьогодні плюс строк» уже пораховане там, і другий екземпляр цього
// додавання в браузері розійшовся б із першим на кроці місяця.
function doneSpec(ctx, row) {
  const when = today();
  switch (row.kind) {
    case "bond":
      return {
        path: "lots", body: lotBody,
        fields: lotFields(ctx, {
          isin: row.ref, qty: row.qty, price_per_bond: { amount: "", currency: row.currency || "" },
          fee: { amount: "" }, buy_date: when, channel: row.broker || "", note: row.note || "",
        }).join(""),
      };
    case "fund":
      return {
        path: "funds", body: fundOpBody,
        fields: fundOpFields(ctx, {
          fund: row.ref, kind: "buy", date: when, qty: row.qty,
          amount: { amount: "" }, tax: null, broker: row.broker || "", note: row.note || "",
        }).join(""),
      };
    case "deposit":
      return {
        path: "term-deposits", body: (f) => depositBody(f),
        fields: depositFields(ctx, {
          bank: row.ref, principal: { amount: row.amount || "", currency: row.currency || "UAH" },
          rate_pct: row.rate_pct || "", open_date: when,
          maturity_date: row.maturity_date || "",
          payout: "end", capitalized: false, replenishable: false,
          is_reserve: !!row.is_reserve, revocable: false, tax_pct: "", note: row.note || "",
        }).join(""),
      };
    case "npf":
      return {
        path: "npf", body: npfOpBody(parseInt(row.ref, 10)),
        fields: npfOpFields(ctx, {
          date: when, amount: { amount: row.amount || "" }, units: 0,
          broker: row.broker || "", note: row.note || "",
        }).join(""),
      };
    default:
      return null;
  }
}

/** Порожній стан сторінки. Малює той, хто кличе: сторінка, на яку можна
 *  прийти за посиланням і побачити білий екран, — зламана. */
export function emptyPlanHTML() {
  return `<div class="card"><h2>Що заплановано</h2>
    <div class="note">Тут порожньо. Внеси сюди те, що збираєшся взяти — і
      побачиш, що станеться з капіталом, частками, подушкою й ціллю ДО того,
      як гроші підуть. Рядки додаються формою нижче або кнопкою «+» у
      <a href="${routeFor("now/buy")}">«Що купити»</a>.</div>
  </div>`;
}
