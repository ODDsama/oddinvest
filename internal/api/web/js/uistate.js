// Що людина розкрила — одним місцем на весь застосунок.
//
// Доти пам'ять про розкрите існувала в трьох різних видах: Set на рівні
// модуля в positions.js, такий самий Set у «Що купити» і localStorage у
// disclosure.js. Перші два вмирали разом зі вкладкою — тобто варто було
// перейти в «Гроші» й повернутись, як усі розкриті позиції згортались, —
// а третій жив далі. Різниця ніде не була заявлена: вона просто
// випливала з того, який файл писали раніше.
//
// Тут одна відповідь: розкрите переживає і перемальовування, і
// перезавантаження сторінки. Простір імен (scope) розділяє позиції,
// поради й будь-що наступне, щоб ключі не зіштовхувались.
//
// localStorage у try/catch скрізь: у приватному вікні Safari він є, але
// кидає на записі, і застосунок не має від цього падати.

const KEY = "oddinvest.open";

const read = () => loadJSON(KEY, {});
const write = (all) => saveJSON(KEY, all);

/** Чи розкрито `key` в просторі `scope`. */
export function isOpen(scope, key) {
  const all = read();
  return Boolean(all[scope] && all[scope][String(key)]);
}

/** Запам'ятати стан. `on === false` прибирає ключ, а не пише false:
 *  згорнуте — це стан за замовчуванням, і зберігати його нема потреби. */
export function remember(scope, key, on) {
  const all = read();
  const bag = all[scope] || (all[scope] = {});
  if (on) bag[String(key)] = true;
  else delete bag[String(key)];
  if (!Object.keys(bag).length) delete all[scope];
  write(all);
}

// ---------------------------------------------------------------------
// Решта «поглядів» браузера: обране вікно, рік, чернетка
// ---------------------------------------------------------------------
//
// Та сама обгортка в try/catch, що й для розкритого вище. Доти її
// переписували руками в шістнадцяти модулях, і сім перемикачів-сегментів
// («день / тиждень / 30 днів», «портфель / усі гроші»…) несли кожен свою
// копію читання, перевірки й запису.

/** JSON під ключем; def — коли порожньо, зіпсовано чи сховище заблоковане. */
export function loadJSON(key, def) {
  try { return JSON.parse(localStorage.getItem(key)) ?? def; } catch (_) { return def; }
}

export function saveJSON(key, v) {
  try { localStorage.setItem(key, JSON.stringify(v)); } catch (_) { /* приватне вікно */ }
}

/** Збережений вибір. allowed — дозволені значення (будь-якого типу,
 *  звіряються як рядки, повертається саме значення зі списку); null —
 *  будь-який непорожній рядок. Сміття чи порожнеча дають def. */
export function pref(key, allowed, def) {
  let raw = null;
  try { raw = localStorage.getItem(key); } catch (_) { /* приватне вікно */ }
  if (!allowed) return raw || def;
  const hit = allowed.find((v) => String(v) === raw);
  return hit === undefined ? def : hit;
}

/** Порожнє значення прибирає ключ: «за замовчуванням» не зберігається. */
export function setPref(key, v) {
  try {
    if (v === "" || v == null) localStorage.removeItem(key);
    else localStorage.setItem(key, String(v));
  } catch (_) { /* приватне вікно */ }
}

/** Кнопки сегмента `data-pref="<key>" value="…"`: клік запамʼятовує вибір
 *  і перемальовує розділ. Ключ у селекторі — щоб дві картки на одній
 *  панелі не чіплялись до кнопок одна одної. */
export function wirePrefs(root, ctx, key) {
  root.querySelectorAll(`[data-pref="${key}"]`).forEach((b) =>
    b.addEventListener("click", () => { setPref(key, b.value); ctx.reload(); }));
}
