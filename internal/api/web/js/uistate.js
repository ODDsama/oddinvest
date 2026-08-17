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

function read() {
  try { return JSON.parse(localStorage.getItem(KEY) || "{}") || {}; }
  catch (_) { return {}; }
}

function write(all) {
  try { localStorage.setItem(KEY, JSON.stringify(all)); } catch (_) { /* приватне вікно */ }
}

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
