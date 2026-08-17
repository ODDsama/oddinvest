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

import { renderOverview } from "./views/overview.js";
import { renderPortfolio } from "./views/portfolio.js";
import { renderMoney } from "./views/money.js";
import { renderSettings } from "./views/settings.js";
import * as assets from "./views/assets-view.js";
import * as plan from "./views/plan-view.js";
import * as risk from "./views/risk-view.js";

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

// Сторінка на підрозділ: ключ — повний шлях «розділ/підрозділ».
// Заповнюється в міру того, як розділи розбираються на сторінки.
const VIEWS = {
  "assets/positions": assets.positions,
  "assets/bonds": assets.bonds,
  "assets/funds": assets.funds,
  "assets/npf": assets.npf,
  "assets/deposits": assets.deposits,
  "assets/reserve": assets.reserve,
  "assets/growth": assets.growth,

  "plan/inflow": plan.inflow,
  "plan/goal": plan.goal,
  "plan/levers": plan.levers,
  "plan/payouts": plan.payouts,

  "risk/structure": risk.structure,
  "risk/limits": risk.limits,
  "risk/compare": risk.compare,
};

// ТИМЧАСОВО, на час розбиття. Доки розділ не розібраний, усі його
// підрозділи малює той самий старий рендерер — тобто адреси вже працюють,
// а вміст на них поки однаковий. Кожен наступний коміт забирає звідси по
// рядку; коли таблиця спорожніє, її треба видалити разом із цим абзацом.
//
// Проміжний стан навмисно видимий, а не схований за «зробимо все одразу»:
// маршрутизація й розкладка — самі по собі велика зміна, і везти її разом
// із перекладанням десяти тисяч рядків в'юшок означало б один коміт, який
// неможливо ні перевірити, ні відкотити частинами.
const LEGACY_VIEW = {
  now: renderOverview,
  money: renderMoney,
  // «Записати» досі малює старий «Портфель» цілком — форми купівлі,
  // продажу й вкладу живуть саме там. Наступний коміт забирає їх звідти
  // разом із рештою форм, і тоді portfolio.js піде теж.
  entry: renderPortfolio,
  policy: renderSettings,
  settings: renderSettings,
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
    el.querySelector("input, select")?.focus();
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
    // Обробника кліку на посиланнях меню тут немає: посилання веде в
    // хеш, hashchange будить _route(), і той малює сторінку. Один шлях
    // виконання замість двох, і «назад» працює задарма.
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
        this._loadTab({ warm: true });
      }
      catch (err) { this._toast(String(err.message || err), false); }
      finally { e.target.disabled = false; }
    });
  }

  // ---------- навігація ----------

  // Меню двома ярусами. Вкладений список малюється ЛИШЕ для активної
  // групи, і це не оптимізація розмітки, а рішення про поведінку: вісім
  // заголовків плюс тридцять п'ять пунктів — сорок три рядки, тобто вище
  // за ноутбучний екран, і половина меню жила б за краєм. Розкрито те, де
  // ти стоїш; решта — заголовки.
  //
  // Стан розкриття ПОХІДНИЙ від маршруту, а не збережений. Четвертий
  // словник у localStorage поруч із oddinvest.folds, oddinvest.open і
  // діапазоном календаря був би не лише зайвим — він давав би змогу меню
  // розійтися з адресою, а це найгірший різновид розсинхронізації: очі
  // бачать одне, «назад» робить інше.
  //
  // Заголовок групи — посилання на її ПЕРШИЙ підрозділ, а не текст:
  // нефокусований підпис у списку посилань — мертвий рядок для клавіатури,
  // а адреса «#/assets» мусить кудись вести, бо її набирають руками.
  //
  // aria-current двох різних значень: page — сторінка, на якій стоїмо,
  // location — гілка, у якій вона лежить. Два page в одній навігації були
  // б помилкою розмітки, а location саме про «поточне місце в ієрархії».
  _navHTML() {
    const here = `${this._section}/${this._sub}`;
    return `<ul class="nav-l">${NAV.map((g) => {
      const open = g.key === this._section;
      const head = `<a class="nav-g" href="#/${g.key}/${g.items[0].key}"${
        open ? ` aria-current="location"` : ""}>${g.label}</a>`;
      if (!open) return `<li>${head}</li>`;
      return `<li>${head}<ul class="nav-l">${g.items.map((it) => {
        const path = `${g.key}/${it.key}`;
        // Розділювач висить на <li>, а не на посиланні: у посилання вже є
        // ліва смуга активного стану, і друга лінія на тому самому
        // елементі малювалась би кутом.
        return `<li${it.gap ? ` class="nav-sep"` : ""}><a class="nav-i" href="#/${path}"${
          path === here ? ` aria-current="page"` : ""}>${it.label}</a></li>`;
      }).join("")}</ul></li>`;
    }).join("")}</ul>`;
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
    // Меню перемальовується цілком, а не правиться атрибутами: змінилась
    // не лише позначка активного пункту, а й те, який ВКЛАДЕНИЙ список
    // взагалі існує. aria-current лишається єдиним джерелом правди про
    // активне — класу .active тут немає й не було.
    this.shadowRoot.getElementById("nav").innerHTML = this._navHTML();
    // Разом: розділ і підрозділ. Сама «ОВДП» не каже, у якій із восьми
    // груп вона лежить, а сторінок із однаковими підписами тепер дві
    // пари («Резерв» в «Активах» і в «Політиці», «ОВДП» в «Активах» і в
    // «Записати»).
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

    // Зведення вантажиться ОКРЕМО від розділу. Доти воно стояло в тому
    // самому try, і будь-яка його помилка ставала помилкою ВСІХ вкладок
    // одразу — включно з «Налаштуваннями», де живе бекап. Тобто
    // відновлення було недосяжне рівно тоді, коли воно й потрібне.
    // А падає саме зведення: /api/summary — найскладніший шлях у
    // бекенді, і його поломка нічого не каже про решту API.
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
        <div class="b-s">${esc(broken.message || broken)}${this._section === "settings" ? ""
          : " · «Налаштування → Резервна копія» зведення не читають — відновлення доступне там"}</div>
      </div></div>`);
      // Сторінки, що читають зведення, без нього показали б не «даних
      // немає», а нулі — і нуль тут невідрізнимий від справжнього нуля.
      // «Налаштування» зведення не читають узагалі, тож малюються.
      //
      // Заголовок сторінки при цьому лишається: він приходить із дерева
      // навігації, а не з даних, і сказати «ти в „Позиціях“, але їх нема
      // звідки взяти» чесніше, ніж показати порожнечу без підпису.
      if (this._section !== "settings") {
        body.innerHTML = "";
        this._settle(main);
        return;
      }
    }

    try {
      const render = VIEWS[path] || LEGACY_VIEW[this._section] || LEGACY_VIEW.now;
      await render(this._ctx, body);
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

  // Дані зведення (без рендеру: плитки живуть у розділах). Довідники
  // тягнемо разом зі зведенням: випадайки брокерів малюються в кожному
  // розділі, тож на момент рендеру список має вже бути.
  async _loadSummaryData() {
    const [s, brokers, funds, npf] = await Promise.all([
      this._api("GET", "summary"),
      this._store.soft("brokers", []),
      this._store.soft("fund-catalog", []),
      // Довідник НПФ разом зі зведенням, як і решта: його читають і
      // «Налаштування» (картка рахунків), і «Портфель» (деталі рядка), тож
      // на момент рендеру список має вже бути.
      this._store.soft("npf-accounts", []),
    ]);
    this._summary = s;
    this._brokers = brokers || [];
    this._fundCatalog = funds || [];
    this._npfAccounts = npf || [];
    const avail = this.shadowRoot.getElementById("avail");
    avail.textContent = s.generated_at ? "стан на " + new Date(s.generated_at).toLocaleString("uk-UA") : "";
  }
}

if (!customElements.get("odd-invest-app")) {
  customElements.define("odd-invest-app", OddInvestApp);
}
