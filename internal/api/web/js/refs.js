// Посилання на сутність, яка вже існує.
//
// ПРАВИЛО, ЗАРАДИ ЯКОГО ЦЕЙ МОДУЛЬ Є: якщо поле означає щось, що вже
// записане в базі, — брокера, фонд, пенсійний рахунок, лот, вклад, потік
// плану, — воно НЕ текстове. Воно підставляється зі списку.
//
// Доти це правило трималось на пам'яті й трималось нерівно. Брокер у формі
// купівлі ОВДП — випадайка з довідника з опцією «інший…». Брокер у формі
// внеску в НПФ — вільний текстовий рядок з атрибутом list, який указував на
// <datalist> із брокерами; того datalist у репозиторії не існувало взагалі
// (і він не спрацював би всередині shadow root). Тобто одне й те саме поле в одному
// місці не давало помилитись, а в сусідньому мовчки заводило другий рахунок
// із друкарською помилкою в назві — і зведення по брокерах розповзалось
// без жодного натяку, чому.
//
// ЧОМУ НЕ <datalist>. Він виглядає рівно тим, що тут потрібно, і не працює:
// всередині shadow root браузер його не знаходить, а падає це мовчки — поле
// просто лишається звичайним текстовим. Саме так і сталось у НПФ.
//
// ЧОМУ ВСЕ Ж З ВІЛЬНИМ ВВЕДЕННЯМ. Строгий список був би чистішим, але
// брехав би: назва брокера чи фонда — це рядок, який заводить сам
// користувач у момент першої операції, і бекенд саме так і працює
// (brokerRef/fundRef заводять запис за назвою). Змусити спершу піти в
// «Налаштування» означало б перервати запис операції заради церемонії.
// Тому «інший…» відкриває поле — той самий жест, що вже був у формі
// купівлі, просто тепер він один на всі поля.
//
// Сутності з id (пенсійний рахунок, лот, вклад, потік) вільного введення НЕ
// мають: у них немає назви, за якою бекенд міг би завести запис, — у
// пенсійного рахунку дванадцять полів, і вигадати їх за людину не можна.

import { esc } from "./format.js";
import { CURRENCIES } from "./constants.js";

/** Значення опції «завести нове». Не порожній рядок і не "other": порожній
 *  уже означає «не вказано», а «інший» читалося б як «ще один із тих
 *  самих», тоді як насправді вибір цієї опції СТВОРЮЄ запис. */
export const NEW = "__new__";

// Довідник видів посилань. list(ctx, items) повертає трійки
// [значення, підпис, атрибути?] — третій елемент потрібен лоту, чия опція
// несе валюту в data-cur, щоб форма продажу знала, у чому ціна.
//
// Види діляться навпіл, і межа проходить по тому, звідки беруться дані.
// Брокери, фонди, рахунки НПФ і валюти застосунок тримає при собі весь час
// (ctx), тож поле малюється без жодного запиту. Лоти, вклади й потоки
// вантажить та сторінка, якій вони потрібні, і передає сюди через items —
// класти їх у ctx означало б, що кожна сторінка платить за чужі дані.
const REFS = {
  broker: {
    label: "Брокер", blank: "—", allowNew: true, newPh: "назва рахунку",
    list: (ctx) => ctx.brokerList().map((n) => [n, n]),
  },
  fund: {
    label: "Фонд", blank: "— фонд —", allowNew: true, newPh: "назва фонду",
    list: (ctx) => (ctx.fundCatalog || []).map((f) => [f.name, f.name]),
  },
  npf: {
    label: "Пенсійний рахунок", blank: "— рахунок —", allowNew: false,
    list: (ctx) => (ctx.npfAccounts || []).map((a) => [String(a.id), a.name]),
  },
  currency: {
    label: "Валюта", blank: "", allowNew: false,
    list: () => CURRENCIES.map((c) => [c, c]),
  },
  lot: {
    label: "Лот", blank: "— лот —", allowNew: false,
    list: (ctx, items) => (items || []).map((l) => [
      String(l.id),
      "#" + l.id + " · " + l.isin + " · зал. " + l.remaining,
      { "data-cur": (l.price_per_bond || {}).currency || "" },
    ]),
  },
  deposit: {
    label: "Вклад", blank: "— вклад —", allowNew: false,
    list: (ctx, items) => (items || []).map((d) => [
      String(d.id),
      "#" + d.id + " · " + (d.bank || "—"),
      { "data-cur": (d.principal || {}).currency || "" },
    ]),
  },
  // Два види посилань на борг, а не один із фільтром: питання різні.
  // Рух пишуть під БУДЬ-ЯКИЙ борг, а звірка й прив'язка розстрочки
  // існують лише для КАРТКИ — у розстрочки немає ні балансу, ні виписки,
  // і показати її в тому списку означало б дозволити запис, який нічого
  // не означає.
  debt: {
    label: "Борг", blank: "— борг —", allowNew: false,
    list: (ctx, items) => (items || []).map((d) => [
      String(d.id), d.name, { "data-cur": d.currency || "" },
    ]),
  },
  "debt-card": {
    label: "Картка", blank: "— картка —", allowNew: false,
    list: (ctx, items) => (items || [])
      .filter((d) => d.kind === "card")
      .map((d) => [String(d.id), d.name, { "data-cur": d.currency || "" }]),
  },
  flow: {
    label: "Джерело", blank: "— джерело —", allowNew: false,
    list: (ctx, items) => (items || []).map((f) => [
      String(f.id), f.name, { "data-cur": f.currency || "" },
    ]),
  },
};

function attrStr(map) {
  return Object.entries(map || {}).map(([k, v]) =>
    (v == null || v === "" ? "" : " " + k + '="' + esc(String(v)) + '"')).join("");
}

/** Поле-посилання на існуючу сутність.
 *
 *  value, якого немає в списку, ДОДАЄТЬСЯ окремою опцією замість того, щоб
 *  тихо провалитись у перший рядок. Це не рідкість: довідник брокерів могли
 *  перейменувати вже після того, як лот записано, — і форма правки, яка
 *  показала б чужого брокера замість власного, зіпсувала б запис при
 *  першому ж збереженні. Той самий довід, що й у app.js:_brokerList, який
 *  бере об'єднання довідника з тим, що зустрічалось у лотах.
 *
 *  items — дані для видів, які застосунок не тримає при собі (лот, вклад,
 *  потік). Для решти не передається. */
export function refSelect(ctx, {
  name, ref, label = "", value = "", items = null,
  allowNew = null, required = false, title = "", blank = null,
}) {
  const spec = REFS[ref];
  if (!spec) throw new Error("невідомий вид посилання: " + ref);
  const rows = spec.list(ctx, items);
  const withNew = allowNew == null ? spec.allowNew : allowNew;
  const has = rows.some(([v]) => String(v) === String(value));
  const all = (!has && value ? [[value, String(value)]] : []).concat(rows);

  // blank перекриває типовий підпис порожньої опції. Потрібне рівно там,
  // де порожньо означає не «не вказано», а щось змістовне: у формі купівлі
  // порожня валюта — це «візьми з довідника НБУ за ISIN», і сказати це
  // словом «—» означало б приховати єдину розумну поведінку за прочерком.
  const blankLabel = blank == null ? spec.blank : blank;
  const opts = (blankLabel ? '<option value="">' + esc(blankLabel) + "</option>" : "")
    + all.map(([v, t, a]) => '<option value="' + esc(v) + '"'
      + (String(v) === String(value) ? " selected" : "") + attrStr(a) + ">"
      + esc(t) + "</option>").join("")
    + (withNew ? '<option value="' + NEW + '">інший…</option>' : "");

  // Поле для нової назви лежить У ТОМУ САМОМУ <label>, одразу під
  // випадайкою, і ховається атрибутом hidden, а не стилем: видимість — це
  // стан елемента, і в атрибуті його видно і в інспекторі, і читачеві
  // екрана, а заразом воно випадає з порядку табуляції.
  const newIn = withNew
    ? '<input name="' + esc(name) + '__new" class="mt-sm" placeholder="'
      + esc(spec.newPh || "") + '" hidden>'
    : "";
  return "<label" + attrStr({ title }) + ">" + esc(label || spec.label)
    + '<select name="' + esc(name) + '"' + (required ? " required" : "") + ">"
    + opts + "</select>" + newIn + "</label>";
}

/** Що людина насправді обрала: значення зі списку або щойно вписане.
 *
 *  Кожна форма з полем-посиланням мусить брати значення ЗВІДСИ, а не з
 *  f.name.value — інакше в тіло запиту поїде рядок "__new__". Доти це
 *  правило існувало в одному екземплярі, прямо в збирачі тіла форми
 *  купівлі, і другого місця, яке про нього знало б, не було. */
export function refValue(form, name) {
  const sel = form.elements[name];
  if (!sel) return "";
  if (sel.value !== NEW) return String(sel.value).trim();
  const extra = form.elements[name + "__new"];
  return extra ? extra.value.trim() : "";
}

/** Атрибут обраної опції — валюта лота, вкладу, потоку.
 *
 *  Потрібен там, де сума вводиться В ВАЛЮТІ обраної сутності: форма
 *  продажу не питає валюту окремо, бо питати її означало б дозволити
 *  відповісти неправильно. */
export function refAttr(form, name, attr) {
  const sel = form.elements[name];
  const opt = sel && sel.selectedOptions && sel.selectedOptions[0];
  return opt ? opt.getAttribute(attr) || "" : "";
}

/** Показати поле нової назви, коли обрано «інший…».
 *
 *  Викликається після КОЖНОГО рендера разом із рештою проводки: ctx.reload()
 *  переписує main цілком, і слухачі, повішані минулого разу, зникають разом
 *  із вузлами. */
export function wireRefs(root) {
  if (!root) return;
  root.querySelectorAll("select").forEach((sel) => {
    const extra = sel.parentElement
      && sel.parentElement.querySelector('[name="' + sel.name + '__new"]');
    if (!extra) return;
    sel.addEventListener("change", () => {
      const isNew = sel.value === NEW;
      extra.hidden = !isNew;
      if (isNew) { extra.value = ""; extra.focus(); }
    });
  });
}

// --- підказка замість списку ---
//
// Випуск ОВДП — теж посилання на існуючу сутність, але випадайкою його не
// вибрати: чинних випусків сотні, і список на сотню рядків це не
// підстановка, а стіна. Тут той самий намір іншим жестом — набираєш, і
// довідник НБУ підказує.
//
// Механіка не нова: вона працювала у формі купівлі з першого дня. Нового
// тут два. По-перше, розмітка й проводка розділились, тож те саме поле
// можна поставити і в форму додавання, і в модалку правки — доти
// автокомпліт існував рівно в одному екземплярі, прибитий до #lotForm і
// #bondSuggest за id. По-друге, підказка більше не шукає елементи по id
// документа: сусідній .suggest знаходиться від самого поля, тож двоє таких
// полів на сторінці не поділили б один випадаючий список.
//
// Не <datalist> — з тієї ж причини, що й вище: у shadow root він мовчить.

/** Поле-підказка: набираєш — довідник підказує.
 *
 *  ref поки що один (bond), і другого «про запас» тут немає навмисно:
 *  порожній вид посилання читався б як зразок. Шлях пошуку прив'язаний до
 *  виду, а не переданий аргументом, з тієї ж причини, що й списки вище — щоб
 *  «звідки беруться дані» лишалось в одному місці. */
const SUGGEST = {
  bond: {
    label: "ISIN", ph: "UA4000...",
    path: (q) => "bonds/search?q=" + encodeURIComponent(q),
    // Рядок підказки: сам ISIN плюс те, за чим випуск упізнають, — назва,
    // ставка й погашення. Без них список однакових UA4000… не допомагає.
    row: (b) => ({
      value: b.isin,
      text: esc(b.isin) + " · " + esc(b.descr || "") + " · " + b.rate_pct
        + "% · до " + esc(b.maturity),
    }),
  },
};

export function refSuggest({ name, ref, label = "", value = "", required = false }) {
  const spec = SUGGEST[ref];
  if (!spec) throw new Error("невідомий вид підказки: " + ref);
  return '<label class="has-suggest">' + esc(label || spec.label)
    + '<input name="' + esc(name) + '" data-suggest="' + esc(ref) + '"'
    + ' value="' + esc(value) + '" placeholder="' + esc(spec.ph) + '"'
    + (required ? " required" : "") + ' autocomplete="off">'
    + '<div class="suggest"></div></label>';
}

/** Проводка полів-підказок у межах root.
 *
 *  mousedown, а не click: blur приходить раніше за click і сховав би список
 *  до того, як вибір спрацює. Затримка перед приховуванням на blur — з тієї
 *  ж родини.
 *
 *  Після вибору поле само шле change: далі його слухає той, кому потрібно
 *  доповнити решту форми з довідника (валюту й ціну лота). Підказка про це
 *  нічого не знає — вона лише вибирає сутність. */
export function wireSuggest(ctx, root) {
  if (!root) return;
  root.querySelectorAll("[data-suggest]").forEach((input) => {
    const spec = SUGGEST[input.dataset.suggest];
    const box = input.parentElement && input.parentElement.querySelector(".suggest");
    if (!spec || !box) return;
    const hide = () => box.classList.remove("show");
    let timer;
    input.addEventListener("input", () => {
      clearTimeout(timer);
      const q = input.value.trim();
      if (q.length < 2) { hide(); return; }
      timer = setTimeout(async () => {
        try {
          const found = await ctx.api("GET", spec.path(q));
          if (!found || !found.length) { hide(); return; }
          box.innerHTML = found.map((it) => {
            const r = spec.row(it);
            return '<div class="suggest-item" data-pick="' + esc(r.value) + '">' + r.text + "</div>";
          }).join("");
          box.classList.add("show");
        } catch (_) { hide(); }
      }, 300);
    });
    box.addEventListener("mousedown", (e) => {
      const it = e.target.closest("[data-pick]");
      if (!it) return;
      e.preventDefault();
      input.value = it.dataset.pick;
      hide();
      input.dispatchEvent(new Event("change"));
    });
    input.addEventListener("blur", () => setTimeout(hide, 150));
  });
}
