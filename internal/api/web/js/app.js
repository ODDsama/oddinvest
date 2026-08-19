// <odd-invest-app> — застосунок цілком: оболонка, навігація, контекст.
//
// Поверхня одна: index.html монтує компонент і ставить йому transport —
// чим ходити в бекенд. Більше поверхня нічого не налаштовує.
//
// Історія тут варта одного абзацу, бо вона пояснює форму. Спершу було дві
// НЕЗАЛЕЖНІ реалізації того самого UI на ~3500 рядків разом — веб-UI й
// бічна панель Home Assistant, — і правило «дзеркалити кожну зміну в
// обох» брало податок з кожної фічі. Вони встигли розійтись: у панелі
// були статуси виплат і таблиці виписки, у вебі — звірка рахунку й
// донат брокерів, і жодна сторона не мала всього. Тоді їх звели в один
// компонент із двома властивостями (transport + theme). Тепер панелі
// немає зовсім, і властивість лишилась одна — але шов під transport
// цінний і без другої поверхні: він тримає компонент незалежним від
// того, звідки беруться дані.

import { esc } from "./format.js";
import { NAV, PATHS } from "./nav.js";
import { bindInfo } from "./info.js";
import { bindDialogBackdrop } from "./forms.js";
import { adoptStyles } from "./styles.js";
import { createStore } from "./store.js";
import { skeleton } from "./skeleton.js";
import { fitCharts } from "./charts.js";
import { parseRoute, ANCHORS } from "./routes.js";

import * as now from "./views/now-view.js";
import * as instr from "./views/instrument-view.js";
import * as portfolio from "./views/portfolio-view.js";
import * as policy from "./views/policy-view.js";
import * as settings from "./views/settings-view.js";
import * as money from "./views/money-view.js";
import * as plan from "./views/plan-view.js";

// Знак: три квадрати, складені сходами. Один квадрат — один папір, і
// саме так портфель і росте: по одному, коли назбиралось на наступний.
// Три, а не чотири — це найменша кількість, яка вже показує напрямок.
//
// currentColor навмисно: знак бере колір від тексту, серед якого стоїть,
// і не має власної константи, яку довелось би правити разом із палітрою.
const MARK = `<svg class="mark" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
  <rect x="2.5" y="14" width="7.5" height="7.5" rx="2"/>
  <rect x="8.25" y="8.25" width="7.5" height="7.5" rx="2"/>
  <rect x="14" y="2.5" width="7.5" height="7.5" rx="2"/>
</svg>`;

// Розділи, які малюються НАВІТЬ коли /api/summary не віддається.
//
// Це не поблажка, а факт про них: жодна сторінка в цих двох не ЗАЛЕЖИТЬ
// від зведення. «Політика» живе на GET /api/settings, «Налаштування» — на
// довідниках (м'які читання) і на власних кнопках. А /api/summary —
// найскладніший шлях у бекенді, і його поломка нічого не каже про решту
// API, тож гасити разом із ним те, що працює, було б втратою даремно.
//
// Одне уточнення, відколи «Стратегія і ціль» ставить виміряні числа поруч
// із питаннями: вона зведення ЧИТАЄ, але через `(ctx.summary || {})`. Без
// нього зі сторінки зникають рівно рядки фактів, а питання, набори,
// таблиця різниці й запис лишаються. Доти тут стояло «жодна сторінка не
// читає зведення» — тепер це було б неправдою, а межа насправді проходить
// не там: не «не читає», а «не порожніє без нього».
//
// Практичний бік: саме тут живе відновлення з резервної копії, тобто
// сторінка, потрібна рівно тоді, коли все інше зламалось.
const SUMMARY_FREE = new Set(["policy", "settings"]);

// Сторінка на підрозділ: ключ — повний шлях «розділ/підрозділ».
//
// Таблиця пласка, а не дерево, навмисно: маршрут приходить рядком, і
// шукати по ньому одним звертанням дешевше й читабельніше, ніж спускатись
// двома рівнями. Порядок груп — той самий, що в nav.js; розійтись вони не
// можуть, бо шлях, якого тут немає, впаде на очах при першому ж переході.
const VIEWS = {
  "now/todo": now.todo,
  "now/buy": now.buy,
  "now/basket": now.basket,

  "instr/bonds": instr.bonds,
  "instr/funds": instr.funds,
  "instr/npf": instr.npf,
  "instr/deposits": instr.deposits,
  "instr/reserve": instr.reserve,

  "portfolio/positions": portfolio.positions,
  "portfolio/growth": portfolio.growth,
  "portfolio/structure": portfolio.structure,
  "portfolio/limits": portfolio.limits,
  "portfolio/compare": portfolio.compare,

  "money/balances": money.balances,
  "money/flows": money.flows,
  "money/tax": money.tax,
  // importStatement, а не import: останнє — зарезервоване слово.
  "money/import": money.importStatement,
  "money/reconcile": money.reconcile,

  "plan/inflow": plan.inflow,
  "plan/goal": plan.goal,
  "plan/levers": plan.levers,
  "plan/payouts": plan.payouts,

  "policy/strategy": policy.strategy,
  "policy/mix": policy.mix,
  "policy/instruments": policy.instruments,
  "policy/reserve": policy.reserve,
  "policy/assumptions": policy.assumptions,

  "settings/refs": settings.refs,
  "settings/backup": settings.backup,
};


// Розділ живе в хеші адреси, а не тільки в пам'яті компонента. Розбір
// маршруту й адреси форм — у js/routes.js: їх будують самі розділи, і
// тримати їх тут означало б замкнути граф імпортів у кільце.

export class OddInvestApp extends HTMLElement {
  constructor() {
    super();
    this.attachShadow({ mode: "open" });
    this._section = "now";
    this._sub = "todo";
    this._started = false;
  }

  /** Транспорт до бекенда. Ставиться ззовні; поки його немає —
   *  малювати нема з чого, тож старт відкладається до цього моменту. */
  set transport(t) {
    this._store = createStore(t, (path, err) => this._reportSoft(path, err));
    this._start();
  }

  // Поломка, проковтнута soft(). У консоль іде кожна — саме там її
  // шукатимуть; тостом показуємо ЛИШЕ ПЕРШУ за завантаження вкладки:
  // коли бекенд ліг цілком, кожен м'який маршрут дасть свою помилку, і
  // чотири тости підряд кажуть менше, ніж один.
  _reportSoft(path, err) {
    const msg = (err && err.message) || String(err);
    console.warn(`[oddinvest] ${path}: ${msg}`);
    if (!this._started || this._softShown) return;
    this._softShown = true;
    this._toast(`${path} не завантажився: ${msg}`, false);
  }

  connectedCallback() { this._start(); }

  _start() {
    if (this._started || !this._store || !this.isConnected) return;
    this._started = true;
    this._renderShell();
    // Слухач ставиться ДО першого маршруту: перехід, що стався поки
    // вантажився транспорт, інакше загубився б.
    this._onHash = () => this._route();
    window.addEventListener("hashchange", this._onHash);
    // Зміна ширини вікна міняє ширину карток, а з нею — правильний
    // розмір полотна графіка. Із затримкою, бо під час перетягування
    // рамки вікна подія летить десятками за секунду.
    this._onResize = () => {
      // Стан шухляди перераховується ТУТ ТЕЖ, а не лише в слухачі
      // matchMedia. Це не дублювання заради надійності взагалі, а
      // страховка під конкретну ціну помилки: поки шухляда відкрита,
      // шапка й <main> лежать під inert, і якщо перетин 900px пройде повз
      // застосунок, він лишиться нерухомим ЦІЛКОМ — не «трохи криво», а
      // без жодної клікабельної точки. Перерахунок ідемпотентний:
      // _setNav сам зводить стан до нуля на широкому екрані.
      this._setNav(this.hasAttribute("data-nav-open"));
      clearTimeout(this._resizeTimer);
      this._resizeTimer = setTimeout(
        () => fitCharts(this.shadowRoot.getElementById("pbody")), 150);
    };
    window.addEventListener("resize", this._onResize);
    this._route();
  }

  disconnectedCallback() {
    if (this._onHash) window.removeEventListener("hashchange", this._onHash);
    if (this._onResize) window.removeEventListener("resize", this._onResize);
    if (this._onWide) this._wide.removeEventListener("change", this._onWide);
  }

  async _route() {
    const r = parseRoute(window.location.hash);
    // Адресу міняємо, але рендер НЕ відкладаємо до наступного hashchange:
    // подія прийде й застане this._section уже тим самим, тобто зробить
    // нічого. Якби ми тут виходили, перше завантаження з порожнім хешем
    // залежало б від того, чи браузер вважає replace() зміною фрагмента, —
    // а це рівно та залежність, через яку екран лишається білим.
    if (r.redirect) window.location.replace(`#/${r.redirect}`);
    const changed = r.section !== this._section || r.sub !== this._sub;
    this._section = r.section;
    this._sub = r.sub;
    // Якір — теж частина адреси, і сторінка має право його знати ще ДО
    // першого малювання: рейка кроків воронки мусить одразу підсвітити
    // той крок, на який прийшли за посиланням. Далі, коли рейкою ходять
    // усередині сторінки, підсвітку пересуває вона сама — перемальовування
    // там не відбувається (changed === false).
    this._anchor = r.anchor;
    // Шухляда закривається ПЕРШОЮ, до рендеру. Порядок тут не смаковий:
    // поки вона відкрита, <main> лежить під inert, а фокус на inert-елемент
    // не сідає взагалі — тобто закриття після рендеру мовчки з'їдало б
    // переведення фокуса нижче, і читач екрана лишався б на посиланні.
    this._setNav(false);
    if (changed || !this._painted) {
      await this._loadPage();
      this._painted = true;
      // Фокус їде в сторінку: без цього читач екрана лишається на
      // посиланні меню й не дізнається, що вміст замінився цілком.
      if (changed) this.shadowRoot.getElementById("main").focus({ preventScroll: true });
    }
    if (r.anchor) this._reveal(r.anchor);
  }

  // Те, що робив хвіст _goto: розкрити ланцюг згорнутих секцій, довести
  // до форми й поставити курсор у перше поле. Тепер це наслідок
  // маршруту, а не окремий шлях виконання.
  _reveal(anchor) {
    const sel = ANCHORS[anchor];
    const el = sel && this.shadowRoot.querySelector(sel);
    if (!el) return;
    // Циклом, а не одним closest: секція може лежати в секції, і
    // відкрити треба весь ланцюг, інакше зовнішня лишить внутрішню
    // невидимою. Слухач toggle від wireDisclosures на цьому спрацює й
    // запам'ятає секцію відкритою — так і треба: її щойно відкрили.
    for (let d = el.closest("details"); d; d = d.parentElement && d.parentElement.closest("details")) {
      d.open = true;
    }
    const smooth = !window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    el.scrollIntoView({ behavior: smooth ? "smooth" : "auto", block: "center" });
    // Курсор у поле — ЛИШЕ коли ціль і є формою: тоді якір вів саме до
    // запису, і людина продовжує з першого поля.
    //
    // Коли ціль — блок сторінки (крок воронки), поле всередині нього все
    // одно знайдеться, але це буде перше-ліпше: на кроці «Що в мене» —
    // чекбокс «поповнюваний» у рядку вкладу десь посеред таблиці. Тому
    // фокус іде на сам блок (звідси tabindex="-1" на .step), і читач
    // екрана приземляється на його заголовок, а не в середину змісту.
    const form = el.matches("form") ? el : el.closest("form");
    if (form) form.querySelector("input, select")?.focus();
    else el.focus({ preventScroll: true });
  }

  // ---------- контекст, який отримують розділи ----------

  get _ctx() {
    return {
      store: this._store,
      api: (method, path, body) => this._api(method, path, body),
      // Читання, яке не валить розділ: маршрут може бути новішим за
      // бекенд, а картка без бенчмарку краща за порожню вкладку.
      soft: (path, fallback = []) => this._store.soft(path, fallback),
      // Де ми зараз. Потрібне тим карткам, які живуть на двох сторінках і
      // показують різне: журнал резерву в «Активах» і форма резерву в
      // «Записати» — це одна функція з двома режимами.
      section: this._section,
      sub: this._sub,
      anchor: this._anchor || "",
      summary: this._summary,
      brokers: this._brokers,
      fundCatalog: this._fundCatalog,
      npfAccounts: this._npfAccounts,
      root: this.shadowRoot,
      toast: (msg, ok) => this._toast(msg, ok),
      // goto тут більше немає. Він лишався швом для розділів, які кличуть
      // перехід кодом, — але жодного такого розділу не існує відколи
      // швидкі дії «Огляду» стали звичайними <a href>. Порожній шов
      // читається як натяк, що ним варто користуватись.
      // Теплий перерендер: сторінка вже на екрані, змінилось одне число.
      reload: () => this._loadPage({ warm: true }),
      brokerList: (lots) => this._brokerList(lots),
      brokerOptions: (sel) => this._brokerOptions(sel),
      channelOptions: (lots) => this._channelOptions(lots),
    };
  }

  // Тонка обгортка над store — GET іде через кеш із дедуплікацією, будь-який
  // запис сам скидає кеш: забути invalidate після мутації тут нема де.
  _api(method, path, body) {
    if (method === "GET") return this._store.get(path);
    const m = { POST: "post", PUT: "put", DELETE: "del" }[method];
    if (!m) throw new Error("невідомий метод " + method);
    return this._store[m](path, body);
  }

  _toast(msg, ok = true) {
    const t = this.shadowRoot.getElementById("toast");
    t.textContent = msg;
    t.className = ok ? "toast ok show" : "toast err show";
    clearTimeout(this._toastTimer);
    this._toastTimer = setTimeout(() => t.classList.remove("show"), 4000);
  }

  // Брокери: з довідника ∪ ті, що вже зустрічались у лотах і балансах.
  // Довідник міг відстати, а випадайка без брокера власного лота гірша за
  // зайвий рядок у списку.
  _brokerList(lots) {
    const s = this._summary || {};
    const set = new Set((this._brokers || []).map((b) => b.name));
    Object.keys(s.brokers || {}).forEach((b) => { if (b && b !== "—") set.add(b); });
    (lots || []).forEach((l) => { if (l.channel) set.add(String(l.channel).trim()); });
    return [...set].sort((a, b) => a.localeCompare(b, "uk"));
  }

  // Для форм грошей: без «інший…», бо рахунок має існувати заздалегідь.
  _brokerOptions(sel = "") {
    return `<option value="">—</option>` + this._brokerList().map((c) =>
      `<option value="${esc(c)}"${c === sel ? " selected" : ""}>${esc(c)}</option>`).join("");
  }

  // Для форми покупки: плюс «інший…» на разовий випадок.
  _channelOptions(lots) {
    return `<option value="">—</option>` +
      this._brokerList(lots).map((c) => `<option value="${esc(c)}">${esc(c)}</option>`).join("") +
      `<option value="__other__">інший…</option>`;
  }

  // ---------- оболонка ----------

  _renderShell() {
    // Позначка «стилі на місці» — вмикач переходів, а не косметика.
    // Пояснення, чому без неї шухляда їде на завантаженні й може лишитись
    // поверх вмісту назавжди, лежить у base.css при :host([data-ready]).
    // Два кадри, а не один: після першого стилі ще в тому ж перерахунку,
    // і браузер устигає побачити зміну transform як анімовану.
    adoptStyles(this.shadowRoot).then(() => requestAnimationFrame(
      () => requestAnimationFrame(() => this.setAttribute("data-ready", ""))));
    this.shadowRoot.innerHTML = `
      <!-- Кнопка, а не посилання з href="#main": усередині shadow root
           фрагмент документа не знаходить нічого, а хеш у адресному
           рядку ще й посварився б із маршрутом розділу. Читач екрана
           оголосить це кнопкою — жест той самий. -->
      <button class="skip" id="skip">До вмісту</button>
      <header>
        <!-- Гамбургер лише на вузькому екрані: від 900px панель стоїть
             прикріпленою колонкою, і кнопка «показати меню» до видимого
             меню — це кнопка, яка нічого не робить. display:none, а не
             visibility чи opacity, — щоб вона вийшла й із порядку
             табуляції теж. -->
        <button class="ghost nav-toggle" id="navToggle" aria-expanded="false"
          aria-controls="nav" aria-label="Розділи">☰</button>
        ${MARK}
        <!-- Не <h1>: єдиний перший заголовок сторінки тепер у <main> і
             називає підрозділ, у якому ти стоїш. Два <h1> на сторінці
             зробили б дерево заголовків пласким рівно там, де воно
             нарешті стало дворівневим. -->
        <p class="brand">ODD Invest</p>
        <span class="sp"></span>
        <span id="avail" class="hdr-stamp"></span>
        <button class="ghost" id="refresh">↻ Оновити НБУ</button>
      </header>
      <div class="layout">
        <!-- href, а не голий <a>: доти вкладки не мали ні адреси, ні
             tabindex, тобто головна навігація застосунку була недосяжна
             з клавіатури ЗОВСІМ. Тепер це звичайні посилання, і браузер
             сам дає і фокус, і Enter, і «відкрити в новій вкладці». -->
        <nav id="nav" aria-label="Розділи застосунку"></nav>
        <div class="col">
          <!-- role="alert", бо сюди пише рівно одна річ: «бекенд не віддає
               зведення». Це той єдиний випадок у застосунку, який має право
               перебити читача екрана посеред речення. Живе ПОЗА <main>:
               сторінки перемальовують main цілком і стерли б смугу першим
               же рендером. -->
          <div id="alert" role="alert" hidden></div>
          <main id="main" tabindex="-1"></main>
        </div>
      </div>
      <!-- Підкладка під шухлядою. Окремим елементом, а не ::backdrop, бо
           шухляда навмисно не <dialog> — причина при _setNav. -->
      <div class="scrim" id="scrim" hidden></div>
      <div id="live" class="sr-only" role="status" aria-live="polite"></div>
      <div id="toast" class="toast" role="status" aria-live="polite" aria-atomic="true"></div>
      <dialog class="infopop" id="infoPop" aria-labelledby="infoPopTitle"><div class="box"></div></dialog>
      <!-- Підтвердження видалення. Нативний window.confirm() тут НЕ
           годиться: у застосунках, вбудованих у чужу сторінку чи
           автоматизований браузер (той самий клас середовищ, де вже
           довелось патчити Escape для #infoPop — shadow root), він
           мовчки повертає false, і кнопка «видалити» лише виглядає
           зламаною — запит просто ніколи не йде. Свій <dialog> від
           цього не залежить: показує й закриває його сам застосунок. -->
      <dialog class="infopop" id="confirmPop" aria-labelledby="confirmPopText">
        <div class="box">
          <p id="confirmPopText"></p>
          <div class="form-actions">
            <!-- Підпис і клас цієї кнопки виставляє confirmDialog під
                 кожне питання: діалог один, а питань уже два («видалити?»
                 і «додати поповнення?»), і зашите слово перетворювало
                 друге на пропозицію протилежного. -->
            <button type="button" class="warn" data-confirm-yes></button>
            <button type="button" class="quiet" data-confirm-no>Скасувати</button>
          </div>
        </div>
      </dialog>
      <!-- Правка рядка (forms.js:openEdit). Тіло малює той, хто відкриває.
           Стоїть ТУТ, а не в розділі, з конкретної причини: після
           успішного запису apply() кличе ctx.reload(), а той переписує
           main.innerHTML цілком — діалог усередині main знищився б у мить,
           коли він ще відкритий і тримає top layer.
           Escape приходить сюди задарма: спільний обробник (info.js:bindInfo)
           ловить dialog[open] загалом, а не якийсь один попап. -->
      <dialog class="infopop editpop" id="editPop" aria-labelledby="editPopTitle">
        <div class="box"></div>
      </dialog>
    `;
    // Обробника кліку на ПОСИЛАННЯХ меню тут немає: посилання веде в
    // хеш, hashchange будить _route(), і той малює сторінку. Один шлях
    // виконання замість двох, і «назад» працює задарма.
    //
    // А от заголовок групи — кнопка, і їй обробник потрібен. Делегований на
    // сам <nav>, бо меню перемальовується на кожному переході: слухач на
    // кожній кнопці довелось би вішати заново після кожного рендеру.
    this.shadowRoot.getElementById("nav").addEventListener("click", (e) => {
      const btn = e.target.closest(".nav-g");
      if (!btn) return;
      this._openGroup(btn.dataset.group, btn.getAttribute("aria-expanded") !== "true");
    });
    //
    // Ширина — не подія розкладки, а подія стану: перетнувши 900px із
    // відкритою шухлядою, ми лишили б inert на шапці й <main>, тобто
    // застосунок на десктопі став би нерухомим цілком. Слухач тримається
    // на компоненті, тож disconnectedCallback його й прибирає.
    this._wide = window.matchMedia("(min-width: 900px)");
    this._onWide = () => this._setNav(false);
    this._wide.addEventListener("change", this._onWide);
    this.shadowRoot.getElementById("navToggle")?.addEventListener("click", () => {
      this._setNav(!this.hasAttribute("data-nav-open"));
    });
    // Закриття БЕЗ переходу — підкладкою або Escape — вертає фокус на
    // гамбургер: інакше він лишився б на посиланні, якого вже не видно.
    // У випадку переходу цього робити не можна, і саме тому повернення
    // живе тут, а не всередині _setNav: там воно перебивало б фокус,
    // який _route щойно поставив на сторінку.
    const closeNav = () => {
      this._setNav(false);
      this.shadowRoot.getElementById("navToggle")?.focus();
    };
    this.shadowRoot.getElementById("scrim")?.addEventListener("click", closeNav);
    // Escape закриває шухляду — але лише коли зверху немає діалогу.
    // info.js:bindInfo слухає ту саму клавішу на тому самому корені й
    // закриває dialog[open]; без цієї сторожі одне натискання закривало б
    // і попап «як це читати», і шухляду під ним.
    this.shadowRoot.addEventListener("keydown", (e) => {
      if (e.key !== "Escape" || this.shadowRoot.querySelector("dialog[open]")) return;
      if (this.hasAttribute("data-nav-open")) closeNav();
    });
    this.shadowRoot.getElementById("skip")?.addEventListener("click", () => {
      const main = this.shadowRoot.getElementById("main");
      main.focus();
      main.scrollIntoView({ block: "start" });
    });
    // попапи «як це читати» — делеговано на весь shadow root
    bindInfo(this.shadowRoot);
    bindDialogBackdrop(this.shadowRoot);
    this.shadowRoot.getElementById("refresh")?.addEventListener("click", async (e) => {
      e.target.disabled = true;
      try {
        await this._api("POST", "refresh");
        this._toast("Довідник НБУ оновлено");
        // _loadPage, а не _loadTab: методу з таким іменем не існує від
        // самого переходу на два яруси, і виклик кидав TypeError. Ловив
        // його ось цей catch — тобто УСПІШНЕ оновлення закінчувалось
        // тостом з помилкою, а сторінка лишалась зі старими числами.
        //
        // Теплим, як і ctx.reload(): вміст на екрані вже є, і міняються
        // самі числа, а не форма сторінки — скелет тут стер би те, на що
        // людина дивиться, заради даних, які приїдуть за мить.
        await this._loadPage({ warm: true });
      }
      catch (err) { this._toast(String(err.message || err), false); }
      finally { e.target.disabled = false; }
    });
  }

  // ---------- навігація ----------

  // Меню двома ярусами. Розкрита одна група — вісім заголовків плюс
  // тридцять п'ять пунктів це сорок три рядки, тобто вище за ноутбучний
  // екран, і половина меню жила б за краєм.
  //
  // Заголовок групи — КНОПКА, яка розкриває, а не посилання, яке веде.
  // Спершу він був посиланням на перший підрозділ: так адреса «#/assets»
  // кудись вела, а стан розкриття лишався похідним від маршруту й не міг
  // розійтися з нею. Розсинхронізації справді немає, але ціна виявилась
  // не та: щоб просто ЗАЗИРНУТИ, що лежить в «Активах», доводилось туди
  // піти — тобто перше натискання завжди міняло сторінку, навіть коли
  // питання було «а що там усередині».
  //
  // Адреса «#/assets» від цього нікуди не поділась: routes.js розкриває
  // голий розділ у перший підрозділ, просто тепер її не пропонує меню.
  //
  // aria-expanded замість aria-current на заголовку — стан розкриття;
  // aria-current="location" лишається окремо й каже, у якій групі лежить
  // сторінка, ДЕ Б НЕ БУЛА розкрита. Це різні речі, і саме тому їх двоє:
  // можна дивитись у «Записати», стоячи в «Грошах», і бачити обидва факти.
  //
  // Списки малюються для ВСІХ груп і ховаються атрибутом hidden, а не
  // викидаються з розмітки: aria-controls мусить указувати на елемент,
  // який існує, а прихований hidden'ом вузол чесно зникає й із дерева
  // доступності.
  _navHTML() {
    const here = `${this._section}/${this._sub}`;
    return `<ul class="nav-l">${NAV.map((g) => {
      const open = g.key === this._open;
      const listId = `nav-g-${g.key}`;
      const head = `<button class="nav-g" type="button" data-group="${g.key}"
        aria-expanded="${open}" aria-controls="${listId}"${
  g.key === this._section ? ` aria-current="location"` : ""}>${g.label}</button>`;
      const items = g.items.map((it) => {
        const path = `${g.key}/${it.key}`;
        // Розділювач висить на <li>, а не на посиланні: у посилання вже є
        // ліва смуга активного стану, і друга лінія на тому самому
        // елементі малювалась би кутом.
        return `<li${it.gap ? ` class="nav-sep"` : ""}><a class="nav-i" href="#/${path}"${
          path === here ? ` aria-current="page"` : ""}>${it.label}</a></li>`;
      }).join("");
      return `<li>${head}<ul class="nav-l" id="${listId}"${open ? "" : " hidden"}>${items}</ul></li>`;
    }).join("")}</ul>`;
  }

  // Розкрити групу (або згорнути ту саму). Гармошка: розкрита завжди одна.
  //
  // Правиться DOM на місці, а не перемальовується меню цілком, і це не
  // оптимізація: innerHTML замінив би кнопку, на якій щойно був фокус, і
  // клавіатура опинилась би на початку документа після кожного розкриття.
  _openGroup(key, open) {
    const nav = this.shadowRoot.getElementById("nav");
    this._open = open ? key : "";
    nav.querySelectorAll(".nav-g").forEach((b) => {
      const on = b.dataset.group === this._open;
      b.setAttribute("aria-expanded", String(on));
      const list = nav.querySelector(`#nav-g-${b.dataset.group}`);
      if (list) list.hidden = !on;
    });
  }

  // Шухляда — атрибут на хості, а не <dialog>. Три причини, і всі три з
  // цього застосунку.
  //
  // Перша: розмітка мусить лишатись однією на всіх ширинах, а showModal()
  // потрібен був би лише під 900px — тобто режими все одно було б два,
  // просто схованих усередині.
  //
  // Друга: цей застосунок ходить у вбудованих і автоматизованих
  // браузерах, де нативна модальність уже одного разу збрехала —
  // window.confirm() мовчки повертав false, і через це в forms.js живе
  // власний #confirmPop, а в info.js — рукописний Escape для dialog[open]
  // всередині shadow root. Четвертий <dialog> успадкував би ту саму
  // латку.
  //
  // Третя: посилання шухляди міняють хеш, тобто закривати її довелось би
  // явно все одно.
  //
  // inert замість пастки фокуса: браузер сам не пускає Tab у приховане,
  // і циклити список посилань руками не треба. Вартовий this._wide тут
  // обов'язковий — на широкому екрані шухляди немає, і глушити під нею
  // половину застосунку не можна.
  _setNav(open) {
    const on = !!open && !this._wide.matches;
    this.toggleAttribute("data-nav-open", on);
    this.shadowRoot.getElementById("navToggle")?.setAttribute("aria-expanded", String(on));
    const scrim = this.shadowRoot.getElementById("scrim");
    if (scrim) scrim.hidden = !on;
    for (const el of [this.shadowRoot.querySelector("header"), this.shadowRoot.getElementById("main")]) {
      if (el) el.inert = on;
    }
    if (!on) return;
    this.shadowRoot.querySelector("nav a")?.focus();
  }

  // Смуга стану під навігацією. Це факт про застосунок цілком, а не про
  // якусь картку, і живе він ПОЗА <main> навмисно: розділи перемальовують
  // main цілком і стерли б її першим же рендером.
  _alert(html) {
    const el = this.shadowRoot.getElementById("alert");
    el.innerHTML = html || "";
    el.hidden = !html;
  }

  // Смуга оголошень. Розділ змінюється БЕЗ переходу сторінки, тож читач
  // екрана про це не дізнається ніяк — крім як звідси.
  _announce(msg) {
    const el = this.shadowRoot.getElementById("live");
    if (el) el.textContent = msg;
  }

  // Що зараз під фокусом — так, щоб це пережило перемальовування. Ім'я
  // поля плюс форма, у якій воно лежить: після ctx.reload() елемента з
  // тим самим посиланням уже не існує, а поле з тим самим іменем у тій
  // самій формі — те саме поле для того, хто в ньому друкував.
  _focusKey() {
    const el = this.shadowRoot.activeElement;
    if (!el || !el.name) return null;
    // Поле всередині <dialog> не відновлюємо. Модалка правки живе в
    // оболонці й закривається одразу після запису, а її поля звуться так
    // само, як у формі додавання (name, amount, note) — тож пошук за
    // іменем після ре-рендеру знайшов би ЧУЖЕ поле й кинув би туди
    // курсор. Стан модалки транзитний: відновлювати там нема чого.
    if (el.closest("dialog")) return null;
    const form = el.closest("form");
    return { form: form && form.id, name: el.name, start: el.selectionStart, end: el.selectionEnd };
  }

  _restoreFocus(k) {
    if (!k) return;
    const scope = (k.form && this.shadowRoot.getElementById(k.form)) || this.shadowRoot;
    // Перебором за властивістю name, а не селектором [name="…"]: селектор
    // довелось би екранувати (CSS.escape), а це ще одна глобаль у списку
    // eslint заради випадку, якого в застосунку немає.
    const el = [...scope.querySelectorAll("input, select, textarea")].find((e) => e.name === k.name);
    if (!el) return;
    el.focus({ preventScroll: true });
    // Позиція курсора теж: без неї фокус повертається, але текст
    // виділяється цілком, і наступний символ стирає введене.
    try { el.setSelectionRange(k.start, k.end); } catch (_) { /* не текстове поле */ }
  }

  /** @param {{warm?: boolean}} opts warm — перемальовування після запису:
   *  старий вміст лишається на екрані, скрол і фокус зберігаються. */
  async _loadPage({ warm = false } = {}) {
    const path = `${this._section}/${this._sub}`;
    const page = PATHS.get(path);
    // Перехід розкриває групу сторінки, у яку прийшли, — навіть якщо перед
    // тим руками розкрили сусідню, щоб подивитись. Орієнтир важливіший за
    // збереження чужого розкриття: інакше після переходу меню показувало б
    // не те місце, де ти опинився.
    this._open = this._section;
    this.shadowRoot.getElementById("nav").innerHTML = this._navHTML();
    // Разом: розділ і підрозділ. Сама назва сторінки не каже, у якій із
    // семи груп вона лежить, а однакові підписи в дереві лишились: «Резерв»
    // стоїть і в «Інструментах», і в «Політиці». Після розрізу по воронках
    // ця пара єдина — ОВДП, фонди й вклади більше не дублюються, бо дані й
    // форми в них тепер на одній сторінці.
    this._announce(page ? `${page.sectionLabel} — ${page.label}` : "");
    const main = this.shadowRoot.getElementById("main");
    // Ширина сторінки приходить із дерева навігації, а не з CSS. Доти це
    // саме правило жило літералом main[data-tab="portfolio"], тобто
    // політика ширини трималась на магічному імені вкладки; тепер вона
    // стоїть поруч зі сторінкою, якої стосується (js/nav.js).
    main.dataset.path = path;
    main.toggleAttribute("data-wide", !!(page && page.wide));
    this._alert("");
    this._softShown = false;

    // Два різні очікування, і плутати їх не можна.
    //
    // ХОЛОДНЕ (зміна розділу) — показуємо скелет форми розділу: вмісту
    // ще немає, і чекати доведеться помітно.
    //
    // ТЕПЛЕ (ctx.reload() після запису) — не витираємо НІЧОГО. Доти
    // кожне збереження стирало main і писало «Завантаження…», тобто
    // після додавання лота екран блимав порожнечею, скрол злітав угору,
    // а курсор із форми зникав. Дані вже на екрані правильні майже
    // цілком; міняється одне число.
    const scrollY = window.scrollY;
    const focusKey = warm ? this._focusKey() : null;
    if (warm) main.dataset.busy = "1";
    else {
      // Заголовок сторінки малює ОБОЛОНКА і бере його з nav.js — тож
      // підпис у меню й заголовок під ним не можуть розійтися.
      //
      // В'юшка отримує #pbody, а не сам <main>. Усередині вона й далі
      // робить innerHTML цілком — переписувати тридцять в'юшок заради
      // цього не треба, — але без обгортки кожен її рендер зносив би
      // заголовок разом із вмістом. Найпомітніше це в календарі виплат:
      // він приїжджає окремим запитом уже після того, як сторінка
      // намальована, і затирає те, у що його поклали.
      main.innerHTML = `<h1 class="page-t">${esc(page ? page.label : "")}</h1>`
        + `<div id="pbody">${skeleton(this._section, this._sub)}</div>`;
    }
    const body = main.querySelector("#pbody");
    main.setAttribute("aria-busy", "true");

    // Зведення вантажиться ОКРЕМО від сторінки й ОКРЕМО від довідників.
    // Доти все стояло в одному try, і будь-яка його помилка ставала
    // помилкою всіх сторінок одразу — включно з бекапом, тобто відновлення
    // було недосяжне рівно тоді, коли воно й потрібне. Довідники ж
    // віддаються й тоді, коли зведення не порахувалось: вони прості
    // читання таблиць.
    await this._loadRefs();
    let broken = null;
    try {
      await this._loadSummaryData();
    } catch (err) {
      broken = err;
      // Порожнє, а не останнє відоме: вчорашні числа, показані як
      // сьогоднішні, гірші за їх відсутність.
      this._summary = {};
    }

    if (broken) {
      this._alert(`<div class="banner danger"><div class="b-ic" aria-hidden="true">⚠</div><div class="b-tx">
        <div class="b-t">Бекенд не віддає зведення</div>
        <div class="b-s">${esc(broken.message || broken)}${SUMMARY_FREE.has(this._section) ? ""
          : " · «Політика» й «Налаштування» зведення не читають — відновлення з копії доступне там"}</div>
      </div></div>`);
      // Сторінки, що читають зведення, без нього показали б не «даних
      // немає», а нулі — і нуль тут невідрізнимий від справжнього нуля.
      //
      // Заголовок сторінки при цьому лишається: він приходить із дерева
      // навігації, а не з даних, і сказати «ти в „Позиціях“, але їх нема
      // звідки взяти» чесніше, ніж показати порожнечу без підпису.
      if (!SUMMARY_FREE.has(this._section)) {
        body.innerHTML = "";
        this._settle(main);
        return;
      }
    }

    try {
      // Відкоту на «якийсь інший рендерер» тут немає навмисно: маршрут
      // приходить із nav.js уже звіреним (parseRoute не пропускає шлях,
      // якого немає в PATHS), тож відсутність сторінки в цій таблиці —
      // помилка програміста, і хай вона буде видна одразу.
      await VIEWS[path](this._ctx, body);
    } catch (err) {
      body.innerHTML = `<div class="card">Помилка: ${esc(err.message || err)}</div>`;
    }

    // Графіки малюються ПІСЛЯ вставки: рамка вже в розкладці, тож її
    // ширина відома, і полотно можна зробити рівно таким — інакше
    // viewBox розтягується разом із текстом усередині.
    fitCharts(body);
    // Рамка всередині ЗГОРНУТОЇ секції має нульову ширину, тобто малювати
    // її нема під що. Домальовуємо в мить розкриття. Слухач на кожному
    // <details>, а не делегований: подія toggle не спливає.
    body.querySelectorAll("details").forEach((d) =>
      d.addEventListener("toggle", () => { if (d.open) fitCharts(body); }));

    this._settle(main);
    if (warm) {
      // Після кадру, а не одразу: доти розкладка ще не порахована, і
      // scrollTo впирається у висоту, якої ще немає.
      requestAnimationFrame(() => {
        window.scrollTo({ top: scrollY, behavior: "instant" });
        this._restoreFocus(focusKey);
      });
    }
  }

  _settle(main) {
    delete main.dataset.busy;
    main.setAttribute("aria-busy", "false");
  }

  // Довідники: брокери, фонди, пенсійні рахунки. Випадайки з них
  // малюються майже на кожній сторінці, тож на момент рендеру списки мають
  // уже бути.
  //
  // ОКРЕМО від зведення, і це не косметика. Доти всі чотири читання стояли
  // в одному Promise.all — а він відхиляється ПЕРШИМ відхиленням, тобто
  // падіння /api/summary забирало з собою й довідники, які самі по собі
  // віддавались чудово. «Налаштування» через це малювались лише тому, що
  // catalogsHTML терпить порожні списки: вони не «виживали», а показували
  // порожньо. Тепер м'які читання осідають самі по собі й від зведення не
  // залежать.
  async _loadRefs() {
    const [brokers, funds, npf] = await Promise.all([
      this._store.soft("brokers", []),
      this._store.soft("fund-catalog", []),
      this._store.soft("npf-accounts", []),
    ]);
    this._brokers = brokers || [];
    this._fundCatalog = funds || [];
    this._npfAccounts = npf || [];
  }

  // Саме зведення (без рендеру: плитки живуть у сторінках). Кидає — і
  // має кидати: /api/summary найскладніший шлях у бекенді, і сторінка, що
  // читає з нього числа, без нього показала б не «даних немає», а нулі.
  async _loadSummaryData() {
    this._summary = await this._api("GET", "summary");
    const avail = this.shadowRoot.getElementById("avail");
    avail.textContent = this._summary.generated_at
      ? "стан на " + new Date(this._summary.generated_at).toLocaleString("uk-UA") : "";
  }
}

if (!customElements.get("odd-invest-app")) {
  customElements.define("odd-invest-app", OddInvestApp);
}
