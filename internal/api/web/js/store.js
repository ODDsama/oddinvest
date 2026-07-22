// Кеш відповідей бекенда з дедуплікацією одночасних запитів.
//
// Навіщо. Розділи малюються з тих самих даних: «Огляд», «Портфель»,
// «План» і «Майбутнє» всі починаються з /api/summary. Доки кожен тягнув
// його сам, перемальовка після однієї купівлі давала чотири однакові
// запити (а веб-UI — десять запитів на будь-яку зміну). Це не питання
// економії трафіку: чотири відповіді на один і той самий момент часу
// можуть розійтись між собою, і тоді дві картки поруч показують різні
// числа, а причину не видно.
//
// Правило просте: читання йде через store, будь-який запис одразу по
// собі кличе invalidate(). Кеш тут не «на N секунд», а «доки нічого не
// змінилось» — час протухання ми вгадати не можемо, а момент запису
// знаємо точно.

export function createStore(transport) {
  const cache = new Map();    // path -> значення
  const inflight = new Map(); // path -> проміс, що ще летить
  const listeners = new Set();

  /** Читання з кешу. Паралельні виклики того самого шляху чекають на
   *  один запит, а не породжують кожен свій. */
  function get(path) {
    if (cache.has(path)) return Promise.resolve(cache.get(path));
    if (inflight.has(path)) return inflight.get(path);
    const p = transport.get(path)
      .then((v) => {
        cache.set(path, v);
        return v;
      })
      .finally(() => inflight.delete(path));
    inflight.set(path, p);
    return p;
  }

  /** Те саме, але помилка не валить розділ: маршрут може бути новішим
   *  за бекенд (довідники брокерів і фондів з'явились пізніше). */
  const soft = (path, fallback = []) => get(path).catch(() => fallback);

  /** Скидання кешу. Без аргументу — увесь: після купівлі змінюються і
   *  позиції, і рахунок, і зведення, і календар, тож вибіркове скидання
   *  тільки давало б привід забути якийсь маршрут. */
  function invalidate(prefix) {
    if (prefix === undefined) cache.clear();
    else for (const k of [...cache.keys()]) if (k.startsWith(prefix)) cache.delete(k);
    listeners.forEach((fn) => fn());
  }

  /** Запис + скидання кешу однією дією, щоб не можна було зробити
   *  перше й забути друге. */
  const mutate = (method) => async (path, body) => {
    const res = await transport[method](path, body);
    invalidate();
    return res;
  };

  return {
    get,
    soft,
    invalidate,
    post: mutate("post"),
    put: mutate("put"),
    del: mutate("del"),
    raw: (path, opts) => transport.raw(path, opts),
    /** Підписка на скидання кешу — щоб поверхня перемалювалась. */
    onInvalidate(fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
}
