// <odd-invest-app> — застосунок цілком: оболонка, навігація, контекст.
//
// Поверхня одна: index.html монтує компонент і ставить йому transport —
// чим ходити в бекенд. Більше поверхня нічого не налаштовує.
//
// Історія тут варта одного абзацу, бо вона пояснює форму. Спершу було дві
// НЕЗАЛЕЖНІ реалізації того самого UI на ~3500 рядків разом — веб-UI й
// бічна панель Home Assistant, — і правило «дзеркалити кожну зміну в
// обох» брало податок з кожної фічі. Тоді їх звели в один компонент із
// двома властивостями (transport + theme). Тепер панелі немає зовсім, і
// властивість лишилась одна — але шов під transport цінний і без другої
// поверхні: він тримає компонент незалежним від того, звідки беруться
// дані.
//
// РОЗКЛАДКА — МАЙСТЕР-ДЕТАЛЬ. Три яруси: вкладка (шапка) → рядок
// (лівий список) → панель (рейка над вмістом). Адреса несе всі три, тож
// закладку можна поставити не на сторінку, а на конкретну позицію в
// конкретному розрізі. Дерево живе в nav.js, розбір адреси — в routes.js,
// рядки списку — в master.js; тут лишається саме РОЗКЛАДАННЯ.

import { esc, uah0, signedUAH, capitalUAH } from "./format.js";
import { TABS, PATHS, HOME, panesFor, kindOf } from "./nav.js";
import { bindInfo } from "./info.js";
import { openPalette } from "./palette.js";
import { bindDialogBackdrop } from "./forms.js";
import { field, formHTML } from "./fields.js";
import { adoptStyles } from "./styles.js";
import { createStore } from "./store.js";
import { skeleton } from "./skeleton.js";
import { fitCharts } from "./charts.js";
import { parseRoute, ANCHORS, markerKind, seg } from "./routes.js";
import {
  portfolioRows, moneyRows, staticRows, chipsOf, rowHTML, footValue,
  kindOfItem, KIND_ONE, KIND_COLOR, overLimit,
} from "./master.js";
import { loadPositionsData } from "./views/positions.js";

import { overview } from "./views/overview.js";
import * as path from "./views/path.js";
import * as now from "./views/now-view.js";
import * as instr from "./views/instrument-view.js";
import * as portfolio from "./views/portfolio-view.js";
import * as policy from "./views/policy-view.js";
import * as settings from "./views/settings-view.js";
import * as money from "./views/money-view.js";
import * as plan from "./views/plan-view.js";
import * as goals from "./views/goals.js";

// Знак: три квадрати, складені сходами. Один квадрат — один папір, і
// саме так портфель і росте: по одному, коли назбиралось на наступний.
// Три, а не чотири — це найменша кількість, яка вже показує напрямок.
//
// currentColor навмисно: знак бере колір від посилання, у якому стоїть,
// і не має власної константи, яку довелось би правити разом із палітрою.
const MARK = `<svg class="mark" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
  <rect x="2.5" y="14" width="7.5" height="7.5" rx="2"/>
  <rect x="8.25" y="8.25" width="7.5" height="7.5" rx="2"/>
  <rect x="14" y="2.5" width="7.5" height="7.5" rx="2"/>
</svg>`;

const TAB_BY_KEY = new Map(TABS.map((t) => [t.key, t]));

// Вкладки, які малюються НАВІТЬ коли /api/summary не віддається.
//
// Це не поблажка, а факт про них: жодна панель у цих двох не ЗАЛЕЖИТЬ
// від зведення. «Політика» живе на GET /api/settings, «Налаштування» — на
// довідниках (м'які читання) і на власних кнопках. А /api/summary —
// найскладніший шлях у бекенді, і його поломка нічого не каже про решту
// API, тож гасити разом із ним те, що працює, було б втратою даремно.
//
// Одне уточнення, відколи «Стратегія і ціль» ставить виміряні числа поруч
// із питаннями: вона зведення ЧИТАЄ, але через `(ctx.summary || {})`. Без
// нього зі сторінки зникають рівно рядки фактів, а питання, набори,
// таблиця різниці й запис лишаються. Межа проходить не «не читає», а
// «не порожніє без нього».
//
// Практичний бік: саме тут живе відновлення з резервної копії, тобто
// сторінка, потрібна рівно тоді, коли все інше зламалось.
const SUMMARY_FREE = new Set(["policy", "settings"]);

// Панель на СТАТИЧНИЙ рядок: ключ — повна трійка «вкладка/рядок/панель».
//
// Таблиця пласка, а не дерево, навмисно: маршрут приходить рядком, і
// шукати по ньому одним звертанням дешевше й читабельніше, ніж спускатись
// трьома рівнями. Порядок вкладок — той самий, що в nav.js; розійтись
// вони не можуть, бо шлях, якого тут немає, впаде на очах при першому ж
// переході.
const VIEWS = {
  "overview/main/main": overview,

  "work/todo/main": now.todo,
  "work/buy/main": now.buy,
  "work/buys/main": now.buys,

  "portfolio/all/positions": portfolio.positions,
  "portfolio/all/growth": portfolio.growth,
  "portfolio/all/period": portfolio.period,
  "portfolio/all/year": portfolio.year,
  "portfolio/all/structure": portfolio.structure,
  "portfolio/all/limits": portfolio.limits,
  "portfolio/all/compare": portfolio.compare,
  "portfolio/all/record": portfolio.record,

  "money/all/balances": money.balances,
  "money/all/flows": money.flows,
  "money/all/tax": money.tax,
  // importStatement, а не import: останнє — зарезервоване слово.
  "money/all/import": money.importStatement,
  "money/all/reconcile": money.reconcile,

  "plan/debts/main": plan.debts,
  "plan/inflow/main": plan.inflow,
  "plan/route/main": plan.route,
  "plan/goal/main": plan.goal,
  "plan/levers/main": plan.levers,
  "plan/payouts/main": plan.payouts,

  "path/next/main": path.next,
  "path/milestones/main": path.milestones,
  "path/habit/main": path.habit,
  "path/collection/main": path.collection,

  "policy/strategy/main": policy.strategy,
  "policy/mix/main": policy.mix,
  "policy/instruments/main": policy.instruments,
  "policy/reserve/main": policy.reserve,
  "policy/debt/main": policy.debt,
  "policy/goals/main": policy.goals,
  "policy/assumptions/main": policy.assumptions,

  "settings/refs/main": settings.refs,
  "settings/backup/main": settings.backup,
};

// Панель на рядок ІЗ ДАНИХ: ключ — «вкладка/вид/панель». Рядків тут
// скільки завгодно, а видів п'ять, тож таблиця лишається кінцевою.
const KIND_VIEWS = {
  "portfolio/position": instr.positionPane,
  "portfolio/reserve": instr.reservePane,
  "portfolio/goal": goals.goalPane,
  "money/account": money.accountPane,
};

export class OddInvestApp extends HTMLElement {
  constructor() {
    super();
    this.attachShadow({ mode: "open" });
    const [tab, item, pane] = HOME.split("/");
    this._tab = tab;
    this._item = item;
    this._pane = pane;
    this._started = false;
    // Порожній рядок означає «зріз не вибрано». Живе в пам'яті, а не в
    // адресі, навмисно: чип — це погляд на список, а не місце в
    // застосунку, і закладка на «Портфель, показані лише вклади» обіцяла
    // б стан, якого після покупки паперу вже не буде.
    this._chip = "";
    this._filter = "";
  }

  /** Транспорт до бекенда. Ставиться ззовні; поки його немає —
   *  малювати нема з чого, тож старт відкладається до цього моменту. */
  set transport(t) {
    this._store = createStore(t, (path, err) => this._reportSoft(path, err));
    this._start();
  }

  // Поломка, проковтнута soft(). У консоль іде кожна — саме там її
  // шукатимуть; тостом показуємо ЛИШЕ ПЕРШУ за завантаження панелі:
  // коли бекенд ліг цілком, кожен м'який маршрут дасть свою помилку, і
  // чотири тости підряд кажуть менше, ніж один.
  _reportSoft(path, err) {
    const msg = (err && err.message) || String(err);
    console.warn(`[oddinvest] ${path}: ${msg}`);
    // 401 — не поломка маршруту, а відсутність сесії; про неї вже каже
    // діалог входу, і тост поруч із ним лише дублював би питання.
    if (err && err.status === 401) return;
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
      // страховка під конкретну ціну помилки: поки список відкритий,
      // шапка й <main> лежать під inert, і якщо перетин 900px пройде повз
      // застосунок, він лишиться нерухомим ЦІЛКОМ — не «трохи криво», а
      // без жодної клікабельної точки. Перерахунок ідемпотентний:
      // _setMaster сам зводить стан до нуля на широкому екрані.
      this._setMaster(this.hasAttribute("data-master-open"));
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
    // подія прийде й застане стан уже тим самим, тобто зробить нічого.
    // Якби ми тут виходили, перше завантаження з порожнім хешем залежало
    // б від того, чи браузер вважає replace() зміною фрагмента, — а це
    // рівно та залежність, через яку екран лишається білим.
    //
    // Маркер «@first:<вид>» замінити зараз не можна: який саме папір
    // перший, стане відомо аж після завантаження позицій. Адресу для
    // нього перепише _loadPage.
    if (r.redirect && !markerKind(r.item)) window.location.replace(`#/${r.redirect}`);
    const changed = r.tab !== this._tab || r.item !== this._item || r.pane !== this._pane;
    this._tab = r.tab;
    this._item = r.item;
    this._pane = r.pane;
    // Якір — теж частина адреси, і панель має право його знати ще ДО
    // першого малювання: посилання на форму мусить одразу довести саме до
    // неї.
    this._anchor = r.anchor;
    // Шухляда закривається ПЕРШОЮ, до рендеру. Порядок тут не смаковий:
    // поки вона відкрита, <main> лежить під inert, а фокус на inert-елемент
    // не сідає взагалі — тобто закриття після рендеру мовчки з'їдало б
    // переведення фокуса нижче, і читач екрана лишався б на посиланні.
    this._setMaster(false);
    if (changed || !this._painted) {
      await this._loadPage();
      this._painted = true;
      // Фокус їде в панель: без цього читач екрана лишається на
      // посиланні списку й не дізнається, що вміст замінився цілком.
      if (changed) this.shadowRoot.getElementById("main").focus({ preventScroll: true });
    }
    if (r.anchor) this._reveal(r.anchor);
  }

  // Розкрити ланцюг згорнутих секцій, довести до форми й поставити курсор
  // у перше поле. Наслідок маршруту, а не окремий шлях виконання.
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
    const form = el.matches("form") ? el : el.closest("form");
    if (form) form.querySelector("input, select")?.focus();
    else el.focus({ preventScroll: true });
  }

  // ---------- контекст, який отримують панелі ----------

  get _ctx() {
    return {
      store: this._store,
      api: (method, path, body) => this._api(method, path, body),
      // Читання, яке не валить панель: маршрут може бути новішим за
      // бекенд, а картка без бенчмарку краща за порожню вкладку.
      soft: (path, fallback = []) => this._store.soft(path, fallback),
      // Де ми зараз. `section`/`sub` тут більше немає: їх не читала
      // ЖОДНА в'юшка вже до цього переїзду — порожній шов, який
      // читається як натяк, що ним варто користуватись.
      tab: this._tab,
      item: this._item,
      pane: this._pane,
      // Вид сутності й та її частина id, яка є ключем у даних: для
      // "bond:UA4000227656" це "bond" і "UA4000227656". Розбирати рядок
      // у кожній панелі означало б п'ять копій одного розбору.
      kind: kindOfItem(this._item),
      key: String(this._item).slice(String(this._item).indexOf(":") + 1),
      anchor: this._anchor || "",
      summary: this._summary,
      brokers: this._brokers,
      fundCatalog: this._fundCatalog,
      npfAccounts: this._npfAccounts,
      // Дані позицій, уже завантажені оболонкою для майстер-списку.
      // Панель бере їх звідси, а не тягне вдруге: store дедуплікує GET-и,
      // але зайвий обхід восьми маршрутів усе одно коштує кадр.
      positions: this._posData,
      // Прогрес — так само завантажений оболонкою, і з тієї ж причини:
      // підвал майстер-списку «Шляху» показує рівень, тобто число з
      // цієї ж відповіді. Дати в'юшкам тягнути її самим означало б два
      // читання одного маршруту на кожному переході всередині вкладки.
      progress: this._progress,
      root: this.shadowRoot,
      toast: (msg, ok) => this._toast(msg, ok),
      // Теплий перерендер: панель уже на екрані, змінилось одне число.
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
           рядку ще й посварився б із маршрутом. Читач екрана оголосить
           це кнопкою — жест той самий. -->
      <button class="skip" id="skip">До вмісту</button>
      <header>
        <!-- Гамбургер лише на вузькому екрані: від 900px майстер-список
             стоїть прикріпленою колонкою, і кнопка «показати список» до
             видимого списку — це кнопка, яка нічого не робить. -->
        <button class="hdr-btn master-toggle" id="masterToggle" aria-expanded="false"
          aria-controls="master" aria-label="Список">☰</button>
        <!-- Знак веде на головну. Доти він був картинкою без href, тобто
             найочевидніший жест застосунку — «поверни мене на початок» —
             не працював зовсім. -->
        <a class="mark-l" id="markLink" href="#/${HOME}" aria-label="ODD Invest — на початок">${MARK}</a>
        <nav class="tabs" id="tabs" aria-label="Розділи застосунку"></nav>
        <span class="sp"></span>
        <span id="avail" class="hdr-stamp"></span>
        <!-- Капітал і дельта в шапці: єдине число, яке має бути видно з
             будь-якої панелі. Дельту віддає зведення (capital_delta_30) —
             рух за 30 днів проти добового знімка; тут вона лише
             малюється, і порожня, доки знімка місячної давнини немає. -->
        <span id="cap" class="hdr-cap"></span>
        <span id="delta" class="hdr-delta"></span>
        <!-- ↻ стоїть тут ТИМЧАСОВО. Оновлення довідника НБУ належить
             «Налаштуванням → Довідники», де живе сам довідник, і туди
             воно переїде разом із тією вкладкою. Прибрати кнопку зараз
             означало б лишити застосунок без єдиного способу оновити
             курси; лишити її без цього абзацу — забути перенести. -->
        <!-- Палітра (palette.js): Ctrl+K або ця кнопка — на телефоні
             клавіш немає. -->
        <button class="hdr-btn" id="palette" title="Пошук по застосунку (Ctrl+K)"
          aria-label="Пошук по застосунку">⌕</button>
        <button class="hdr-btn" id="refresh" title="Оновити довідник НБУ"
          aria-label="Оновити довідник НБУ">↻</button>
        <a class="hdr-btn" id="gear" href="#/settings/refs/main"
          title="Налаштування" aria-label="Налаштування">⚙</a>
        <!-- Вихід зʼявляється лише коли бекенд має замок (GET /api/auth):
             без замка кнопка обіцяла б дію, якої немає. -->
        <button class="hdr-btn" id="logout" title="Вийти" aria-label="Вийти" hidden>⎋</button>
      </header>
      <div class="layout">
        <aside id="master" aria-label="Список"></aside>
        <div class="col">
          <!-- role="alert", бо сюди пише рівно одна річ: «бекенд не віддає
               зведення». Це той єдиний випадок у застосунку, який має право
               перебити читача екрана посеред речення. Живе ПОЗА <main>:
               панелі перемальовують main цілком і стерли б смугу першим
               же рендером. -->
          <div id="alert" role="alert" hidden></div>
          <main id="main" tabindex="-1"></main>
        </div>
      </div>
      <!-- Підкладка під шухлядою. Окремим елементом, а не ::backdrop, бо
           шухляда навмисно не <dialog> — причина при _setMaster. -->
      <div class="scrim" id="scrim" hidden></div>
      <div id="live" class="sr-only" role="status" aria-live="polite"></div>
      <div id="toast" class="toast" role="status" aria-live="polite" aria-atomic="true"></div>
      <dialog class="infopop" id="infoPop" aria-labelledby="infoPopTitle"><div class="box"></div></dialog>
      <!-- Палітра. У shell, як і решта діалогів, і з тієї ж причини:
           панелі переписують main цілком. Escape й клік по тлу дістає
           задарма від bindInfo/bindDialogBackdrop. -->
      <dialog class="infopop palpop" id="palettePop" aria-label="Пошук по застосунку"><div class="box"></div></dialog>
      <!-- Підтвердження видалення. Нативний window.confirm() тут НЕ
           годиться: у застосунках, вбудованих у чужу сторінку чи
           автоматизований браузер (той самий клас середовищ, де вже
           довелось патчити Escape для #infoPop — shadow root), він
           мовчки повертає false, і кнопка «видалити» лише виглядає
           зламаною — запит просто ніколи не йде. -->
      <dialog class="infopop" id="confirmPop" aria-labelledby="confirmPopText">
        <div class="box">
          <p id="confirmPopText"></p>
          <div class="form-actions">
            <button type="button" class="warn" data-confirm-yes></button>
            <button type="button" class="quiet" data-confirm-no>Скасувати</button>
          </div>
        </div>
      </dialog>
      <!-- Правка рядка (forms.js:openEdit). Тіло малює той, хто відкриває.
           Стоїть ТУТ, а не в панелі, з конкретної причини: після
           успішного запису apply() кличе ctx.reload(), а той переписує
           main.innerHTML цілком — діалог усередині main знищився б у мить,
           коли він ще відкритий і тримає top layer. -->
      <dialog class="infopop editpop" id="editPop" aria-labelledby="editPopTitle">
        <div class="box"></div>
      </dialog>
      <!-- Вхід. Бекенд відповів 401 (transport.js кидає подію oi:unauth) —
           отже, на /api/* стоїть пароль, а сесії немає. Статика при цьому
           відкрита навмисно (internal/api/auth.go), тож застосунок
           вантажиться, а замок стоїть на даних. Після входу сторінка
           перезавантажується: усе, що впало з 401, піде заново. -->
      <dialog class="infopop editpop" id="loginPop" aria-labelledby="loginPopTitle">
        <div class="box">
          <h4 id="loginPopTitle">Вхід</h4>
          <p class="note">Цей сервіс закритий паролем. Сесія живе 30 днів на цьому пристрої.</p>
          ${formHTML({
            id: "loginForm", submit: "Увійти",
            fields: [field("password", "Пароль", { type: "password", required: true, autocomplete: "current-password" })],
          })}
        </div>
      </dialog>
    `;
    // Обробників кліку на ПОСИЛАННЯХ вкладок і рядків тут немає:
    // посилання веде в хеш, hashchange будить _route(), і той малює
    // панель. Один шлях виконання замість двох, і «назад» працює задарма.
    //
    // А от чипи й фільтр — не адреса (довід у конструкторі), і їм обробник
    // потрібен. Делеговані на сам список, бо він перемальовується на
    // кожному переході.
    const master = this.shadowRoot.getElementById("master");
    master.addEventListener("click", (e) => {
      const chip = e.target.closest(".chip");
      if (!chip) return;
      this._chip = chip.dataset.chip === this._chip ? "" : chip.dataset.chip;
      this._paintMaster();
    });
    master.addEventListener("input", (e) => {
      if (!e.target.classList.contains("master-f")) return;
      this._filter = e.target.value;
      this._paintMaster({ keepFocus: true });
    });

    // Ширина — не подія розкладки, а подія стану: перетнувши 900px із
    // відкритою шухлядою, ми лишили б inert на шапці й <main>, тобто
    // застосунок на десктопі став би нерухомим цілком.
    this._wide = window.matchMedia("(min-width: 900px)");
    this._onWide = () => this._setMaster(false);
    this._wide.addEventListener("change", this._onWide);
    this.shadowRoot.getElementById("masterToggle")?.addEventListener("click", () => {
      this._setMaster(!this.hasAttribute("data-master-open"));
    });
    // Закриття БЕЗ переходу — підкладкою або Escape — вертає фокус на
    // гамбургер: інакше він лишився б на посиланні, якого вже не видно.
    // У випадку переходу цього робити не можна, і саме тому повернення
    // живе тут, а не всередині _setMaster: там воно перебивало б фокус,
    // який _route щойно поставив на панель.
    const closeMaster = () => {
      this._setMaster(false);
      this.shadowRoot.getElementById("masterToggle")?.focus();
    };
    this.shadowRoot.getElementById("scrim")?.addEventListener("click", closeMaster);
    // Escape закриває шухляду — але лише коли зверху немає діалогу.
    // info.js:bindInfo слухає ту саму клавішу на тому самому корені й
    // закриває dialog[open]; без цієї сторожі одне натискання закривало б
    // і попап «як це читати», і шухляду під ним.
    this.shadowRoot.addEventListener("keydown", (e) => {
      if (e.key !== "Escape" || this.shadowRoot.querySelector("dialog[open]")) return;
      if (this.hasAttribute("data-master-open")) closeMaster();
    });
    this.shadowRoot.getElementById("skip")?.addEventListener("click", () => {
      const main = this.shadowRoot.getElementById("main");
      main.focus();
      main.scrollIntoView({ block: "start" });
    });
    // попапи «як це читати» — делеговано на весь shadow root
    bindInfo(this.shadowRoot);
    bindDialogBackdrop(this.shadowRoot);
    // Палітра: кнопка в шапці й Ctrl/Cmd+K на вікні. На вікні, а не на
    // shadowRoot: фокус може стояти поза застосунком (адресний рядок,
    // порожнє тіло сторінки), а комбінація мусить працювати звідусіль.
    const openPal = () => {
      const pop = this.shadowRoot.getElementById("palettePop");
      if (pop.open) return;
      openPalette(this._ctx, pop, () => this._posData);
    };
    this.shadowRoot.getElementById("palette")?.addEventListener("click", openPal);
    window.addEventListener("keydown", (e) => {
      if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        openPal();
      }
    });
    this._wireLogin();
    this.shadowRoot.getElementById("refresh")?.addEventListener("click", async (e) => {
      e.target.disabled = true;
      try {
        await this._api("POST", "refresh");
        this._toast("Довідник НБУ оновлено");
        // Теплим, як і ctx.reload(): вміст на екрані вже є, і міняються
        // самі числа, а не форма панелі — скелет тут стер би те, на що
        // людина дивиться, заради даних, які приїдуть за мить.
        await this._loadPage({ warm: true });
      }
      catch (err) { this._toast(String(err.message || err), false); }
      finally { e.target.disabled = false; }
    });
  }

  // ---------- вхід ----------

  // Діалог входу й кнопка виходу. Замок живе на бекенді (auth.go); тут
  // лише дві речі: показати поле, коли прийшов 401, і показати «Вийти»,
  // коли замок узагалі є.
  _wireLogin() {
    const pop = this.shadowRoot.getElementById("loginPop");
    const form = this.shadowRoot.getElementById("loginForm");
    const logout = this.shadowRoot.getElementById("logout");

    window.addEventListener("oi:unauth", () => {
      // Один 401 тягне за собою десяток інших (кожен маршрут сторінки), а
      // діалог один: showModal() на відкритому <dialog> кидає.
      if (pop.open) return;
      pop.showModal();
      form.querySelector("input")?.focus();
    });

    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const btn = form.querySelector("button[type=submit]");
      btn.disabled = true;
      try {
        await this._store.raw("login", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ password: form.password.value }),
        }).then(async (resp) => {
          if (resp.ok) return;
          const txt = await resp.text().catch(() => "");
          let msg = txt;
          try { msg = JSON.parse(txt).error || txt; } catch (_) { /* не JSON */ }
          throw new Error(msg || `${resp.status}`);
        });
        // Перезавантаження, а не теплий рендер: усе, що впало з 401 до
        // входу, — довідники, зведення, позиції — має піти заново, і
        // одна сторінка робить це надійніше за перелік маршрутів.
        window.location.reload();
      } catch (err) {
        this._toast(String(err.message || err), false);
        btn.disabled = false;
        form.password.select();
      }
    });

    logout?.addEventListener("click", async () => {
      try { await this._store.raw("logout", { method: "POST" }); }
      finally { window.location.reload(); }
    });

    // «Вийти» — лише коли є звідки. Питання відкрите (auth.go), тож іде
    // й до входу; відповідь не кешується store — raw повз кеш.
    this._store.raw("auth").then((r) => (r.ok ? r.json() : null)).then((a) => {
      if (a && a.enabled) logout.hidden = false;
    }).catch(() => { /* без відповіді кнопка лишається схованою */ });
  }

  // ---------- шапка ----------

  _paintHeader() {
    const s = this._summary || {};
    // ⚙ окремо від вкладок: службовий кут застосунку не стоїть в одному
    // ряду з питаннями про гроші.
    this.shadowRoot.getElementById("tabs").innerHTML = TABS
      .filter((t) => !t.gear && !t.mark)
      .map((t) => {
        const first = t.dynamic ? "all" : t.items[0].id;
        const href = `#/${t.key}/${first}/${panesFor(t.key, first)[0].key}`;
        const n = this._tabBadge(t.key);
        return `<a class="tab" href="${href}"${
          t.key === this._tab ? ` aria-current="page"` : ""}>${esc(t.label)}${
          n ? `<span class="tab-n">${esc(n)}</span>` : ""}</a>`;
      }).join("");

    const gear = this.shadowRoot.getElementById("gear");
    if (this._tab === "settings") gear.setAttribute("aria-current", "page");
    else gear.removeAttribute("aria-current");

    const mark = this.shadowRoot.getElementById("markLink");
    const [homeTab, homeItem, homePane] = HOME.split("/");
    const atHome = this._tab === homeTab && this._item === homeItem
      && this._pane === homePane;
    if (atHome) mark.setAttribute("aria-current", "page");
    else mark.removeAttribute("aria-current");

    const cap = this.shadowRoot.getElementById("cap");
    cap.textContent = this._summary ? uah0(capitalUAH(s)) : "";
    // Дельта — зі знаком і з внесеним у підказці: «+12 000» без другого
    // числа читається як заробіток, коли 11 000 із них — власний внесок.
    const delta = this.shadowRoot.getElementById("delta");
    const d = this._summary && s.capital_delta_30;
    delta.textContent = d ? `${signedUAH(d.delta_uah)} за 30 дн.` : "";
    delta.title = d
      ? `з ${d.from_date}: капітал ${uah0(d.from_uah)}, внесено ${signedUAH(d.contributed_uah)}`
      : "";
    delta.classList.toggle("up", !!d && d.delta_uah > 0);
    delta.classList.toggle("down", !!d && d.delta_uah < 0);
  }

  // Лічильник на вкладці. Лише те, що ВИМІРЯНЕ: скільки задач чекає
  // рішення й скільки вимірів вийшло за ліміт. На «Портфелі» лічильника
  // немає навмисно — кількість позицій ні про що не попереджає, а
  // цифра поруч із підписом читається як «тут щось не так».
  _tabBadge(key) {
    const s = this._summary || {};
    if (key === "work") {
      const n = (s.tasks || []).filter((t) => t.sev === "now").length;
      return n ? String(n) : "";
    }
    if (key === "policy") {
      const n = overLimit(s).length;
      return n ? String(n) : "";
    }
    return "";
  }

  // ---------- майстер-список ----------

  /** Рядки цієї вкладки. Одне місце, звідки їх беруть і список, і
   *  розкриття маркера «@first», і перевірка «а чи існує такий рядок». */
  _rows() {
    const tab = TAB_BY_KEY.get(this._tab);
    if (!tab) return [];
    if (tab.dynamic === "positions") return portfolioRows(this._ctx, this._posData || {});
    if (tab.dynamic === "accounts") return moneyRows(this._ctx);
    return staticRows(tab, this._ctx);
  }

  _paintMaster({ keepFocus = false } = {}) {
    const tab = TAB_BY_KEY.get(this._tab);
    const host = this.shadowRoot.getElementById("master");
    // «Огляд» списку не має ЗОВСІМ — не порожній список, а жодного.
    // Порожня колонка на 376 пікселів поруч зі сторінкою, яка про
    // портфель цілком, читалась би як список, що не завантажився.
    this.toggleAttribute("data-solo", tab ? tab.master === false : false);
    if (!tab || tab.master === false) { host.innerHTML = ""; return; }
    const all = this._allRows || [];
    const q = this._filter.trim().toLowerCase();
    const shown = all.filter((r) =>
      (!this._chip || r.kind === this._chip || r.kind === "all")
      && (!q || `${r.name} ${r.sub}`.toLowerCase().includes(q)));

    const chips = tab.chips
      ? `<div class="chips">${chipsOf(all).map((c) =>
        `<button type="button" class="chip" data-chip="${esc(c.key)}"
          aria-pressed="${c.key === this._chip}">${esc(c.label)}</button>`).join("")}</div>`
      : "";
    const a = tab.action;
    const action = a
      ? `<a class="master-a" href="#/${tab.key}/${a.item}/${a.pane}${
        a.anchor ? `/${a.anchor}` : ""}">${esc(a.label)}</a>` : "";

    // Два різні порожні стани, бо сказати треба різне: у списку немає
    // нічого — це факт про портфель; фільтр нічого не знайшов — це факт
    // про набраний рядок, і виправляється він інакше.
    const body = shown.length
      ? `<ul class="m-list">${shown.map((r) =>
        rowHTML(tab.key, r, this._item)).join("")}</ul>`
      : `<div class="m-none">${q || this._chip
        ? "Нічого не знайшлось. Спробуй інакший запит або зніми зріз."
        : "Тут поки порожньо."}</div>`;

    host.innerHTML = `
      <div class="master-h">
        <input class="master-f" type="search" placeholder="${esc(tab.search)}"
          aria-label="${esc(tab.search)}" value="${esc(this._filter)}">
        ${action}
      </div>
      ${chips}
      ${body}
      <div class="master-foot">
        <span>${esc(tab.footLabel)}</span>
        <b>${esc(footValue(tab.key, this._ctx, all))}</b>
      </div>`;

    // Курсор назад у поле фільтра: перемальовування списку інакше
    // викидало б із нього після кожного набраного символа.
    if (keepFocus) {
      const f = host.querySelector(".master-f");
      f?.focus();
      f?.setSelectionRange(f.value.length, f.value.length);
    }
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
  // всередині shadow root. Четвертий <dialog> успадкував би ту саму латку.
  //
  // Третя: посилання списку міняють хеш, тобто закривати його довелось би
  // явно все одно.
  //
  // inert замість пастки фокуса: браузер сам не пускає Tab у приховане,
  // і циклити список посилань руками не треба. Вартовий this._wide тут
  // обов'язковий — на широкому екрані шухляди немає, і глушити під нею
  // половину застосунку не можна.
  _setMaster(open) {
    const on = !!open && !this._wide.matches;
    this.toggleAttribute("data-master-open", on);
    this.shadowRoot.getElementById("masterToggle")?.setAttribute("aria-expanded", String(on));
    const scrim = this.shadowRoot.getElementById("scrim");
    if (scrim) scrim.hidden = !on;
    for (const el of [this.shadowRoot.querySelector("header"), this.shadowRoot.getElementById("main")]) {
      if (el) el.inert = on;
    }
    if (!on) return;
    this.shadowRoot.querySelector("#master .master-f")?.focus();
  }

  // Смуга стану під шапкою. Це факт про застосунок цілком, а не про
  // якусь картку, і живе він ПОЗА <main> навмисно: панелі перемальовують
  // main цілком і стерли б її першим же рендером.
  _alert(html) {
    const el = this.shadowRoot.getElementById("alert");
    el.innerHTML = html || "";
    el.hidden = !html;
  }

  // Смуга оголошень. Панель змінюється БЕЗ переходу сторінки, тож читач
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

  /** Розкрити «@first:<вид>» і виправити рядок, якого більше немає.
   *
   *  Обидві поправки живуть тут, а не в parseRoute, з однієї причини:
   *  щоб їх зробити, треба знати РЯДКИ, а рядки приходять із даних.
   *  Маршрутизатор про дані нічого не знає й знати не повинен. */
  _resolveItem(rows) {
    const kind = markerKind(this._item);
    if (kind) {
      const hit = rows.find((r) => r.kind === kind);
      // Виду без жодної позиції не існує як рядка — тоді ведемо в
      // зведення. Стара сторінка виду в цьому місці показувала порожній
      // стан; «Портфель цілком» каже те саме чесніше.
      this._item = hit ? hit.id : "all";
      return true;
    }
    if (rows.length && !rows.some((r) => r.id === this._item)) {
      // Закладка на папір, який продали, або на рахунок, який
      // перейменували. Мовчки підмінити його зведенням не можна: людина
      // прийшла за конкретною позицією і мусить дізнатись, що її немає.
      this._toast(`«${this._item}» більше немає в списку`, false);
      this._item = "all";
      return true;
    }
    return false;
  }

  /** @param {{warm?: boolean}} opts warm — перемальовування після запису:
   *  старий вміст лишається на екрані, скрол і фокус зберігаються. */
  async _loadPage({ warm = false } = {}) {
    const main = this.shadowRoot.getElementById("main");
    this._alert("");
    this._softShown = false;

    // Два різні очікування, і плутати їх не можна.
    //
    // ХОЛОДНЕ (зміна панелі) — показуємо скелет: вмісту ще немає, і
    // чекати доведеться помітно.
    //
    // ТЕПЛЕ (ctx.reload() після запису) — не витираємо НІЧОГО. Доти
    // кожне збереження стирало main і писало «Завантаження…», тобто
    // після додавання лота екран блимав порожнечею, скрол злітав угору,
    // а курсор із форми зникав.
    const scrollY = window.scrollY;
    const focusKey = warm ? this._focusKey() : null;
    if (warm) main.dataset.busy = "1";
    else main.innerHTML = `<div id="pbody">${skeleton(this._tab, this._pane)}</div>`;
    main.setAttribute("aria-busy", "true");

    // Зведення вантажиться ОКРЕМО від панелі й ОКРЕМО від довідників.
    // Доти все стояло в одному try, і будь-яка його помилка ставала
    // помилкою всіх сторінок одразу — включно з бекапом, тобто відновлення
    // було недосяжне рівно тоді, коли воно й потрібне.
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

    // Дані рядків — лише для вкладки, якій вони справді потрібні. Обхід
    // восьми маршрутів позицій коштує помітно, і платити за нього на
    // «Політиці» нема за що.
    const tab = TAB_BY_KEY.get(this._tab);
    if (tab && tab.dynamic === "positions" && !broken) {
      this._posData = await loadPositionsData(this._ctx).catch(() => ({}));
    }
    // Те саме для «Шляху»: прогрес коштує обходу всієї історії внесків
    // (довід — у шапці internal/api/state_progress.go), і платити за
    // нього на «Грошах» нема за що. М'яке читання: сторінка віх без віх
    // мусить сказати це словами, а не впасти.
    if (this._tab === "path") {
      // Порожнє, а не останнє відоме, коли зведення не приїхало: та сама
      // причина, що й у this._summary вище — вчорашні числа, показані як
      // сьогоднішні, гірші за їх відсутність.
      this._progress = broken ? null : await this._store.soft("progress", null);
    }

    this._allRows = this._rows();
    // Розкриття маркера й підміна зниклого рядка міняють АДРЕСУ, тож
    // адресний рядок треба привести у відповідність — інакше «назад»
    // повернуло б на адресу, якої не існує.
    if (this._resolveItem(this._allRows)) {
      const panes = panesFor(this._tab, this._item) || [];
      if (!panes.some((p) => p.key === this._pane)) this._pane = panes[0].key;
      window.location.replace(`#/${this._tab}/${seg(this._item)}/${this._pane}`);
    }

    this._paintHeader();
    this._paintMaster();
    const page = PATHS.get(`${this._tab}/${this._item}/${this._pane}`);
    const rowOf = this._allRows.find((r) => r.id === this._item);
    this._announce(`${tab ? tab.label : ""} — ${rowOf ? rowOf.name : ""}`);

    // 401 — не «бекенд не віддає зведення», а «сесії немає»: про це вже
    // питає діалог входу, і червона смуга під ним лякала б даремно.
    if (broken && broken.status !== 401) {
      this._alert(`<div class="banner danger"><div class="b-ic" aria-hidden="true">⚠</div><div class="b-tx">
        <div class="b-t">Бекенд не віддає зведення</div>
        <div class="b-s">${esc(broken.message || broken)}${SUMMARY_FREE.has(this._tab) ? ""
          : " · «Політика» й «Налаштування» зведення не читають — відновлення з копії доступне там"}</div>
      </div></div>`);
      // Панелі, що читають зведення, без нього показали б не «даних
      // немає», а нулі — і нуль тут невідрізнимий від справжнього нуля.
      if (!SUMMARY_FREE.has(this._tab)) {
        main.innerHTML = this._inspectorHTML(rowOf, "");
        this._settle(main);
        return;
      }
    }

    if (!warm) main.innerHTML = this._inspectorHTML(rowOf, `<div id="pbody"></div>`);
    const body = main.querySelector("#pbody");

    try {
      // Відкоту на «якийсь інший рендерер» тут немає навмисно: адреса
      // приходить уже звіреною (parseRoute не пропускає панель, якої
      // немає в дереві), тож відсутність панелі в цих таблицях — помилка
      // програміста, і хай вона буде видна одразу.
      const view = VIEWS[`${this._tab}/${this._item}/${this._pane}`]
        || KIND_VIEWS[kindOf(this._tab, this._item)];
      await view(this._ctx, body);
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
    // page лишається для читача: він каже, що трійка статична. Панелі з
    // даних його не мають, і саме тому заголовок береться з рядка.
    void page;
  }

  /** Шапка інспектора плюс рейка панелей. Заголовок береться з РЯДКА, а
   *  не з дерева: рядок «Портфеля» приходить із даних, і дерево його
   *  назви не знає. */
  _inspectorHTML(row, body) {
    // «Огляд» малює себе сам: у нього немає ні сутності, ні панелей, а
    // заголовок «Огляд» над сторінкою, яка й так одна, був би підписом
    // до самого себе.
    if ((TAB_BY_KEY.get(this._tab) || {}).master === false) return body;
    const panes = panesFor(this._tab, this._item) || [];
    const kind = kindOfItem(this._item);
    // Ім'я токена береться з мапи, а не збирається підстановкою: довід
    // при KIND_COLOR у master.js — зібране ім'я не звіряється перевіркою
    // токенів, тобто описка у виді дала б пігулку без кольору мовчки.
    const pill = KIND_ONE[kind]
      ? `<span class="insp-k" style="--oi-c:${KIND_COLOR[kind]}">${esc(KIND_ONE[kind])}</span>`
      : "";
    // Рейка з одного елемента показувала б вибір там, де вибору немає, —
    // лишається сама лінійка.
    const solo = panes.length < 2;
    const rail = `<nav class="panes${solo ? " solo" : ""}" aria-label="Панелі">${
      solo ? "" : panes.map((p) =>
        `<a class="pane-l" href="#/${this._tab}/${seg(this._item)}/${p.key}"${
          p.key === this._pane ? ` aria-current="true"` : ""}>${esc(p.label)}</a>`).join("")
    }</nav>`;
    return `<div class="insp-h">
      ${pill}
      <h1 class="insp-t">${esc(row ? row.name : "")}</h1>
      ${row && row.sub ? `<span class="insp-s">${esc(row.sub)}</span>` : ""}
    </div>${rail}${body}`;
  }

  _settle(main) {
    delete main.dataset.busy;
    main.setAttribute("aria-busy", "false");
  }

  // Довідники: брокери, фонди, пенсійні рахунки. Випадайки з них
  // малюються майже на кожній панелі, тож на момент рендеру списки мають
  // уже бути.
  //
  // ОКРЕМО від зведення, і це не косметика. Доти всі чотири читання стояли
  // в одному Promise.all — а він відхиляється ПЕРШИМ відхиленням, тобто
  // падіння /api/summary забирало з собою й довідники, які самі по собі
  // віддавались чудово.
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

  // Саме зведення (без рендеру: плитки живуть у панелях). Кидає — і
  // має кидати: /api/summary найскладніший шлях у бекенді, і панель, що
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
