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
import { TABS } from "./constants.js";
import { bindInfo } from "./info.js";
import { adoptStyles } from "./styles.js";
import { createStore } from "./store.js";
import { skeleton } from "./skeleton.js";
import { fitCharts } from "./charts.js";
import { parseRoute, routeFor, ANCHORS } from "./routes.js";

import { renderOverview } from "./views/overview.js";
import { renderPortfolio } from "./views/portfolio.js";
import { renderMoney } from "./views/money.js";
import { renderFuture } from "./views/future.js";
import { renderSettings } from "./views/settings.js";

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

const VIEWS = {
  overview: renderOverview,
  portfolio: renderPortfolio,
  money: renderMoney,
  future: renderFuture,
  settings: renderSettings,
};

// Розділ живе в хеші адреси, а не тільки в пам'яті компонента. Розбір
// маршруту й адреси форм — у js/routes.js: їх будують самі розділи, і
// тримати їх тут означало б замкнути граф імпортів у кільце.

export class OddInvestApp extends HTMLElement {
  constructor() {
    super();
    this.attachShadow({ mode: "open" });
    this._tab = "overview";
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
      clearTimeout(this._resizeTimer);
      this._resizeTimer = setTimeout(
        () => fitCharts(this.shadowRoot.getElementById("main")), 150);
    };
    window.addEventListener("resize", this._onResize);
    this._route();
  }

  disconnectedCallback() {
    if (this._onHash) window.removeEventListener("hashchange", this._onHash);
    if (this._onResize) window.removeEventListener("resize", this._onResize);
  }

  async _route() {
    const { tab, anchor } = parseRoute(window.location.hash);
    const changed = tab !== this._tab;
    this._tab = tab;
    if (changed || !this._painted) {
      await this._loadTab();
      this._painted = true;
      // Фокус їде в розділ: без цього читач екрана лишається на
      // посиланні вкладки й не дізнається, що вміст замінився цілком.
      if (changed) this.shadowRoot.getElementById("main").focus({ preventScroll: true });
    }
    if (anchor) this._reveal(anchor);
  }

  // Те, що робив хвіст _goto: розкрити ланцюг згорнутих секцій, довести
  // до форми й поставити курсор у перше поле. Тепер це наслідок
  // маршруту, а не окремий шлях виконання.
  _reveal(anchor) {
    const a = ANCHORS[anchor];
    const el = a && this.shadowRoot.querySelector(a.sel);
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
      summary: this._summary,
      brokers: this._brokers,
      fundCatalog: this._fundCatalog,
      root: this.shadowRoot,
      toast: (msg, ok) => this._toast(msg, ok),
      goto: (what) => this._goto(what),
      // Теплий перерендер: розділ уже на екрані, змінилось одне число.
      reload: () => this._loadTab({ warm: true }),
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

  // Перехід із «Огляду» просто в потрібну форму. Лишається як шов для
  // розділів, які кличуть ctx.goto(), — але всередині тепер звичайна
  // навігація, тож перехід потрапляє в історію браузера й на нього
  // працює «назад». Форми живуть у згорнутих <details>, і розкриває їх
  // _reveal(): доти кнопка «Купити папір» скролила до ЗАКРИТОЇ секції,
  // тобто в порожнє місце, а .focus() всередині закритого <details> не
  // робить нічого взагалі.
  _goto(what) {
    const next = routeFor(what);
    // Той самий маршрут не породжує hashchange — розкриваємо форму самі.
    if (window.location.hash === next) this._reveal(what);
    else window.location.hash = next;
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
    adoptStyles(this.shadowRoot);
    this.shadowRoot.innerHTML = `
      <!-- Кнопка, а не посилання з href="#main": усередині shadow root
           фрагмент документа не знаходить нічого, а хеш у адресному
           рядку ще й посварився б із маршрутом розділу. Читач екрана
           оголосить це кнопкою — жест той самий. -->
      <button class="skip" id="skip">До вмісту</button>
      <header>
        ${MARK}
        <h1>ODD Invest</h1>
        <span class="sp"></span>
        <span id="avail" class="muted" style="color:inherit;opacity:.85"></span>
        <button class="ghost" id="refresh">↻ Оновити НБУ</button>
      </header>
      <!-- href, а не голий <a>: доти вкладки не мали ні адреси, ні
           tabindex, тобто головна навігація застосунку була недосяжна
           з клавіатури ЗОВСІМ. Тепер це звичайні посилання, і браузер
           сам дає і фокус, і Enter, і «відкрити в новій вкладці». -->
      <nav aria-label="Розділи застосунку">${
        TABS.map(([k, t]) => `<a href="#/${k}" data-tab="${k}">${t}</a>`).join("")}</nav>
      <!-- role="alert", бо сюди пише рівно одна річ: «бекенд не віддає
           зведення». Це той єдиний випадок у застосунку, який має право
           перебити читача екрана посеред речення. -->
      <div id="alert" role="alert" hidden></div>
      <main id="main" tabindex="-1"></main>
      <div id="live" class="sr-only" role="status" aria-live="polite"></div>
      <div id="toast" class="toast" role="status" aria-live="polite" aria-atomic="true"></div>
      <dialog class="infopop" id="infoPop" aria-labelledby="infoPopTitle"><div class="box"></div></dialog>
    `;
    // Обробника кліку на вкладках тут більше немає: посилання веде в
    // хеш, hashchange будить _route(), і той малює розділ. Один шлях
    // виконання замість двох, і «назад» працює задарма.
    this.shadowRoot.getElementById("skip")?.addEventListener("click", () => {
      const main = this.shadowRoot.getElementById("main");
      main.focus();
      main.scrollIntoView({ block: "start" });
    });
    // попапи «як це читати» — делеговано на весь shadow root
    bindInfo(this.shadowRoot);
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
  async _loadTab({ warm = false } = {}) {
    // aria-current — єдине джерело правди про активну вкладку. Класу
    // .active більше немає: два позначення того самого стану рано чи
    // пізно розходяться, а CSS однаково вміє вибирати за атрибутом.
    this.shadowRoot.querySelectorAll("nav a").forEach((a) => {
      if (a.dataset.tab === this._tab) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    });
    this._announce(TABS.find(([k]) => k === this._tab)?.[1] || "");
    const main = this.shadowRoot.getElementById("main");
    // Розділ у атрибуті: «Портфель» має право бути ширшим за решту —
    // у нього сім колонок і рік історії, а в тексту й форм оптимальна
    // ширина не залежить від того, скільки місця на екрані.
    main.dataset.tab = this._tab;
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
    else main.innerHTML = skeleton(this._tab);
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
        <div class="b-s">${esc(broken.message || broken)}${this._tab === "settings" ? ""
          : " · «Налаштування» зведення не читають — бекап і відновлення доступні там"}</div>
      </div></div>`);
      // Розділи, що читають зведення, без нього показали б не «даних
      // немає», а нулі — і нуль тут невідрізнимий від справжнього нуля.
      // «Налаштування» зведення не читають узагалі, тож малюються.
      if (this._tab !== "settings") {
        main.innerHTML = "";
        this._settle(main);
        return;
      }
    }

    try {
      const render = VIEWS[this._tab] || VIEWS.overview;
      await render(this._ctx, main);
    } catch (err) {
      main.innerHTML = `<div class="card">Помилка: ${esc(err.message || err)}</div>`;
    }

    // Графіки малюються ПІСЛЯ вставки: рамка вже в розкладці, тож її
    // ширина відома, і полотно можна зробити рівно таким — інакше
    // viewBox розтягується разом із текстом усередині.
    fitCharts(main);
    // Рамка всередині ЗГОРНУТОЇ секції має нульову ширину, тобто малювати
    // її нема під що. Домальовуємо в мить розкриття. Слухач на кожному
    // <details>, а не делегований: подія toggle не спливає.
    main.querySelectorAll("details").forEach((d) =>
      d.addEventListener("toggle", () => { if (d.open) fitCharts(main); }));

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
    const [s, brokers, funds] = await Promise.all([
      this._api("GET", "summary"),
      this._store.soft("brokers", []),
      this._store.soft("fund-catalog", []),
    ]);
    this._summary = s;
    this._brokers = brokers || [];
    this._fundCatalog = funds || [];
    const avail = this.shadowRoot.getElementById("avail");
    avail.textContent = s.generated_at ? "стан на " + new Date(s.generated_at).toLocaleString("uk-UA") : "";
  }
}

if (!customElements.get("odd-invest-app")) {
  customElements.define("odd-invest-app", OddInvestApp);
}
