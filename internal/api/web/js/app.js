// <odd-invest-app> — застосунок цілком: оболонка, навігація, контекст.
//
// Один компонент на дві поверхні. Веб-UI бекенда і панель Home Assistant
// монтують саме його й відрізняються рівно двома речами, які й
// передаються властивостями:
//
//   transport — чим ходити в бекенд (свій fetch / hass.fetchWithAuth);
//   theme     — який файл теми одягнути (власна темна / теми HA).
//
// Раніше це були дві незалежні реалізації на ~3500 рядків разом, і
// правило «дзеркалити кожну зміну в обох» тримало податок на кожну
// фічу. Вони встигли розійтись: у панелі були статуси виплат і таблиці
// виписки, у вебі — звірка рахунку й донат брокерів, і жодна сторона не
// мала всього. Тепер розділ пишеться один раз.

import { esc } from "./format.js";
import { TABS } from "./constants.js";
import { bindInfo } from "./info.js";
import { adoptStyles } from "./styles.js";
import { createStore } from "./store.js";

import { renderOverview } from "./views/overview.js";
import { renderPortfolio } from "./views/portfolio.js";
import { renderMoney } from "./views/money.js";
import { renderFuture } from "./views/future.js";
import { renderSettings } from "./views/settings.js";

// Знак: три квадрати, складені сходами. Один квадрат — один папір, і
// саме так портфель і росте: по одному, коли назбиралось на наступний.
// Три, а не чотири — це найменша кількість, яка вже показує напрямок.
//
// currentColor навмисно: у вебі знак бере акцент, у панелі — колір
// тексту шапки HA, яка сама пофарбована темою користувача. Один шлях,
// дві поверхні, жодної другої версії файла.
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

export class OddInvestApp extends HTMLElement {
  constructor() {
    super();
    this.attachShadow({ mode: "open" });
    this._tab = "overview";
    this._theme = "web";
    this._started = false;
  }

  /** Транспорт до бекенда. Ставиться поверхнею; поки його немає —
   *  малювати нема з чого, тож старт відкладається до цього моменту. */
  set transport(t) {
    this._store = createStore(t);
    this._start();
  }

  /** "web" | "ha" — який файл теми одягнути. */
  set theme(name) {
    this._theme = name;
    if (this._started) adoptStyles(this.shadowRoot, name);
  }

  connectedCallback() { this._start(); }

  _start() {
    if (this._started || !this._store || !this.isConnected) return;
    this._started = true;
    this._renderShell();
    this._loadTab();
  }

  // ---------- контекст, який отримують розділи ----------

  get _ctx() {
    const app = this;
    return {
      store: this._store,
      api: (method, path, body) => this._api(method, path, body),
      summary: this._summary,
      brokers: this._brokers,
      fundCatalog: this._fundCatalog,
      root: this.shadowRoot,
      toast: (msg, ok) => this._toast(msg, ok),
      goto: (what) => this._goto(what),
      reload: () => this._loadTab(),
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

  // Швидкий перехід із «Огляду» просто в потрібну форму: банер каже, що
  // зробити, і кнопка має доводити саме туди, а не «десь на вкладку».
  async _goto(what) {
    // topup — секція вкладів у «Портфелі» (не плутати з deposit: то
    // поповнення грошового рахунку в «Грошах»).
    const map = { buy: "portfolio", topup: "portfolio", deposit: "money", convert: "money" };
    this._tab = map[what] || "portfolio";
    await this._loadTab();
    const sel = { buy: "#lotForm", topup: "#termDepForm", deposit: "#depForm", convert: "#convForm" }[what];
    const el = this.shadowRoot.querySelector(sel) || this.shadowRoot.querySelector("#lotForm");
    if (el) {
      el.scrollIntoView({ behavior: "smooth", block: "center" });
      const first = el.querySelector && el.querySelector("input");
      if (first) first.focus();
    }
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
    adoptStyles(this.shadowRoot, this._theme);
    this.shadowRoot.innerHTML = `
      <header>
        ${MARK}
        <h1>ODD Invest</h1>
        <span class="sp"></span>
        <span id="avail" class="muted" style="color:inherit;opacity:.85"></span>
        <button class="ghost" id="refresh">↻ Оновити НБУ</button>
      </header>
      <nav>${TABS.map(([k, t]) => `<a data-tab="${k}">${t}</a>`).join("")}</nav>
      <main id="main"></main>
      <div id="toast" class="toast"></div>
      <div class="infopop" id="infoPop"><div class="box"></div></div>
    `;
    this.shadowRoot.querySelectorAll("nav a").forEach((a) =>
      a.addEventListener("click", () => { this._tab = a.dataset.tab; this._loadTab(); })
    );
    // попапи «як це читати» — делеговано на весь shadow root
    bindInfo(this.shadowRoot);
    this.shadowRoot.getElementById("refresh").addEventListener("click", async (e) => {
      e.target.disabled = true;
      try { await this._api("POST", "refresh"); this._toast("Довідник НБУ оновлено"); this._loadTab(); }
      catch (err) { this._toast(String(err.message || err), false); }
      finally { e.target.disabled = false; }
    });
  }

  async _loadTab() {
    this.shadowRoot.querySelectorAll("nav a").forEach((a) =>
      a.classList.toggle("active", a.dataset.tab === this._tab));
    const main = this.shadowRoot.getElementById("main");
    main.innerHTML = `<div class="muted">Завантаження…</div>`;
    try {
      await this._loadSummaryData();
      const render = VIEWS[this._tab] || VIEWS.overview;
      await render(this._ctx, main);
    } catch (err) {
      main.innerHTML = `<div class="card">Помилка: ${esc(err.message || err)}</div>`;
    }
  }

  // Дані зведення (без рендеру: плитки живуть у розділах). Довідники
  // тягнемо разом зі зведенням: випадайки брокерів малюються в кожному
  // розділі, тож на момент рендеру список має вже бути.
  async _loadSummaryData() {
    const [s, brokers, funds] = await Promise.all([
      this._api("GET", "summary"),
      this._api("GET", "brokers").catch(() => []),
      this._api("GET", "fund-catalog").catch(() => []),
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
