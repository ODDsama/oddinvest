// Поточний портфель (0054): який із них бачить цей браузер.
//
// Живе в localStorage, а НЕ в адресі. Роутер — хеш із трьох сегментів
// (routes.js), і четвертий означав би правку граматики заради виміру, який
// до маршруту не належить: сторінка «Портфель» та сама в кожному. Той
// самий довід, що й у чипа фільтра (app.js: this._chip) — це погляд на
// дані, а не місце в застосунку.
//
// Транспорт читає slug на КОЖЕН запит (transport.js): перемикання — це
// зміна одного ключа, а не перезавантаження сторінки.
//
// Дип-лінк усе ж потрібен: сповіщення HA колись поведе в конкретний
// портфель. Для цього ?p=<slug> ПЕРЕД хешем: boot() читає його раз при
// відкритті, запамʼятовує й прибирає з адреси — щоб закладка на
// «/?p=wife#/…» не перемикала портфель щоразу, коли нею скористались.

const KEY = "oddinvest.portfolio";

/** Slug поточного портфеля; порожньо = головний. */
export function current() {
  try { return localStorage.getItem(KEY) || ""; } catch (_) { return ""; }
}

/** Запамʼятати вибір. Головний зберігається як ВІДСУТНІСТЬ ключа: так
 *  свіжий браузер і браузер, що повернувся на головний, не відрізняються. */
export function set(slug) {
  try {
    if (!slug || slug === "main") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, slug);
  } catch (_) { /* приватний режим — працюємо в головному */ }
}

/** ?p=<slug> у адресі при відкритті: прочитати, запамʼятати, прибрати. */
export function boot() {
  const q = new URLSearchParams(window.location.search);
  if (!q.has("p")) return;
  set(q.get("p") || "");
  q.delete("p");
  const rest = q.toString();
  window.history.replaceState(null, "",
    window.location.pathname + (rest ? "?" + rest : "") + window.location.hash);
}
