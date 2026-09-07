// Ринкові ціни ОВДП: у кого й почім.
//
// Модуль спільний, як fund-ops.js і npf.js, бо читачів двоє: розкриття
// паперу в позиціях (таблиця продавців і форма ручної ціни) і сторінка
// налаштувань брокерів (зіставлення). Дані завантажує сторінка, кладе їх
// сюди через setQuotes, а рендер бере вже готове — щоб розкриття рядка не
// било в мережу.
//
// # ЧОМУ ЦІНА ВЗАГАЛІ ВИДНА ЛЮДИНІ
//
// Її оновлює лише натискання кнопки: фонової джоби немає навмисно
// (jobs/quotes.go). Тому вік ціни показується ЗАВЖДИ — і поруч із кожним
// продавцем, і одним штампом угорі. Ціна без дати виглядала б однаково
// свіжою і сьогодні, і через три тижні.

import { esc, curSym, cur2 as fmtCur, dayMonth } from "./format.js";
import { opsGrid } from "./grid.js";
import { formHTML, text as textField, money as moneyField, date as dateField } from "./fields.js";
import { onSubmit, onDelete } from "./forms.js";

let DOC = { rows: [], sources: [], fetched_at: "" };

export function setQuotes(doc) {
  DOC = doc && Array.isArray(doc.rows) ? doc : { rows: [], sources: [], fetched_at: "" };
}

export function quotesDoc() { return DOC; }

function rowsFor(isin) {
  return (DOC.rows || []).filter((q) => q.isin === String(isin || "").toUpperCase());
}

/** Штамп свіжості плюс кнопка обходу.
 *
 *  Кнопка руками, а не через кит: кит покриває поля, форми й таблиці, а
 *  <button> у views/ дозволений (Makefile: ui-kit-boundary). Заводити
 *  функцію кита заради однієї кнопки означало б абстракцію з одним
 *  користувачем. */
export function quotesBarHTML() {
  const when = DOC.fetched_at
    ? `оновлено ${esc(dayMonth(DOC.fetched_at.slice(0, 10)))}`
    : "ще жодного разу не оновлювались";
  return `<div class="row-h mb-sm">
    <span class="fine-xs muted">Ціни брокерів: ${when}</span>
    <button type="button" id="quotesRefresh" class="sm">Оновити ціни</button>
  </div>`;
}

/** Ціни одного паперу — усі відомі продавці.
 *
 *  Показуємо й тих, у кого рахунку немає: як орієнтир «де взагалі дешевше»
 *  це чесно, доки поруч написано, що рахунку там немає. У квиток така ціна
 *  не потрапляє — відбір іде лише по зіставлених (quotes_pick.go). */
export function quotesTableHTML(isin) {
  const rows = rowsFor(isin);
  if (!rows.length) {
    return `<h4 class="mt">Ціни брокерів</h4>
      <p class="fine-xs muted">Цін для цього паперу ще немає.
      Натисни «Оновити ціни» вгорі списку або впиши ціну руками нижче.</p>`
      + manualFormHTML(isin);
  }
  return `<h4 class="mt">Ціни брокерів</h4>` + opsGrid({
    cols: [
      {
        key: "source", label: "Продавець",
        cell: (q) => esc(q.source)
          + (q.mine
            ? ` <span class="fine-xs muted">твій — ${esc(q.mine)}</span>`
            : ` <span class="fine-xs muted">рахунку немає</span>`),
      },
      { key: "price", label: "Ціна за 1 шт", num: true, cell: (q) => fmtCur(Number(q.price.amount), curSym(q.price.currency)) },
      { key: "date", label: "На дату", cell: (q) => esc(dayMonth(q.date)) },
      {
        // Ручну ціну видно окремо: вона така сама з вигляду, а оновити її
        // може лише людина — і саме тому старіє непомітно.
        key: "origin", label: "Звідки", cls: "muted", prio: 3,
        cell: (q) => (q.origin === "manual" ? "вписана руками" : "джерело"),
      },
      {
        key: "act", label: "",
        cell: (q) => (q.origin === "manual"
          ? `<button type="button" class="sm warn" data-qdel="1"
               data-isin="${esc(q.isin)}" data-source="${esc(q.source)}" data-date="${esc(q.date)}"
               title="Прибрати" aria-label="Прибрати ручну ціну ${esc(q.source)}">✕</button>`
          : ""),
      },
    ],
    rows: rows.map((q, i) => ({ ...q, id: i })),
    caption: `Ціни ${esc(isin)}: продавець, ціна за штуку, дата, походження`,
  }) + manualFormHTML(isin);
}

/** Форма ручної ціни.
 *
 *  Потрібна там, де джерело не покриває брокера: mono ОВДП продає, а цін
 *  не публікує ніде. Підпис поля каже, ЯКУ ціну вписувати — брудну, разом
 *  із НКД, тобто те, що спишуть за штуку. Дві різні за складом ціни в
 *  одній таблиці зробили б дохідність несумісною між рядками. */
function manualFormHTML(isin) {
  return `<details class="mt"><summary class="fine-xs">Вписати ціну руками</summary>`
    + formHTML({
      id: "",
      cls: "mt-sm",
      attrs: { "data-quote-form": isin },
      fields: [
        textField("source", "Продавець", { ph: "mono", required: true }),
        moneyField("price", "Ціна за 1 шт, брудна (з НКД)", { required: true }),
        dateField("date", "На дату"),
      ],
      submit: "Зберегти ціну",
    })
    + `<p class="fine-xs muted">Число з додатка брокера — те, що спишуть за одну штуку
       разом із накопиченим купоном. Старіє так само, як ціни джерела: через кілька
       днів застосунок перестане на неї спиратись.</p></details>`;
}

/** Проводка: кнопка обходу, форми ручної ціни, видалення.
 *
 *  Усі три частини терплять відсутність своїх цілей, як решта wire-функцій:
 *  сторінка без кнопки чи без розкриттів просто нічого не підв'яже. */
export function wireQuotes(ctx, main) {
  const btn = main.querySelector("#quotesRefresh");
  if (btn) {
    btn.addEventListener("click", async () => {
      // Прохід синхронний і йде до хвилини (jobs/quotes.go): кнопка мусить
      // сказати, що вона зайнята, інакше її натиснуть удруге й підуть по
      // тих самих шістдесяти сторінках ще раз.
      btn.disabled = true;
      btn.setAttribute("aria-busy", "true");
      const was = btn.textContent;
      btn.textContent = "Питаю ціни…";
      try {
        const res = await ctx.api("POST", "quotes/refresh", {});
        const bits = [`оновлено ${res.stored}`];
        if (res.noprice) bits.push(`${res.noprice} без пропозицій`);
        if (res.missing) bits.push(`${res.missing} немає в джерела`);
        if (res.failed) bits.push(`${res.failed} не вийшло`);
        if (res.skipped) bits.push(`${res.skipped} не влізли — натисни ще раз`);
        ctx.toast(`Ціни: ${bits.join(", ")}`);
        await ctx.reload();
      } catch (e) {
        ctx.toast(String(e.message || e), false);
        btn.disabled = false;
        btn.removeAttribute("aria-busy");
        btn.textContent = was;
      }
    });
  }
  main.querySelectorAll("[data-quote-form]").forEach((form) => {
    onSubmit(ctx, form, (f) => ({
      path: "quotes/manual",
      body: {
        isin: f.dataset.quoteForm,
        source: f.elements.source.value.trim(),
        price: f.elements.price.value.trim(),
        date: f.elements.date.value,
      },
      msg: "Ціну збережено",
    }));
  });
  onDelete(ctx, main, "[data-qdel]", (el) => ({
    path: `quotes/manual/${encodeURIComponent(el.dataset.isin)}`
      + `?source=${encodeURIComponent(el.dataset.source)}`
      + `&date=${encodeURIComponent(el.dataset.date)}`,
    confirm: `Прибрати ручну ціну ${el.dataset.source} за ${el.dataset.date}?`,
    msg: "Ціну прибрано",
  }));
}
