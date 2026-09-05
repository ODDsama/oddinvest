// Транспорт до REST-бекенда.
//
// Реалізація лишилась одна: UI віддається тим самим сервісом, у який
// ходить. Друга була для бічної панелі HA — вона ходила через проксі
// /api/oddinvest/* заради HA-авторизації; панелі більше немає.
//
// Сам шов лишається: store.js будується поверх переданого транспорту, і
// саме тому мокнути бекенд у тесті чи стенді — це передати інший об'єкт,
// а не перехопити глобальний fetch.
//
// Шляхи скрізь БЕЗ префікса: "summary", "lots/7", "calendar?from=…".

/** 401 — не помилка запиту, а стан застосунку: сесії немає. Про це
 *  кажемо оболонці подією, а не поверненням: raw() віддає відповідь як є
 *  (шість викликачів самі дивляться resp.ok), і без події кожен із них
 *  мусив би знати про вхід. Оболонка (app.js) відкриває діалог входу;
 *  сам запит при цьому все одно падає — повторити його після входу
 *  дешевше перезавантаженням, ніж чергою відкладених промісів. */
function noteUnauthorized(resp) {
  if (resp.status === 401) window.dispatchEvent(new Event("oi:unauth"));
  return resp;
}

/** Спільний розбір відповіді: помилку піднімаємо з текстом тіла, бо
 *  голий 400 не каже, що саме бекенд не прийняв. */
async function unwrap(resp) {
  noteUnauthorized(resp);
  if (!resp.ok) {
    const txt = await resp.text().catch(() => "");
    // Бекенд віддає {"error": "..."} — витягуємо саме його, якщо є.
    let msg = txt.slice(0, 300);
    try {
      const j = JSON.parse(txt);
      if (j && j.error) msg = j.error;
    } catch (_) { /* не JSON — лишаємо як є */ }
    const err = new Error(`${resp.status}: ${msg}`);
    // Статус ще й окремим полем. soft() мусить відрізнити «маршруту ще
    // немає» (404 на старшому бекенді — очікувано) від поломки, а
    // виколупувати число регуляркою з тексту означало б прив'язати цю
    // відмінність до формату повідомлення.
    err.status = resp.status;
    throw err;
  }
  const ct = resp.headers.get("content-type") || "";
  if (resp.status === 204 || !ct.includes("application/json")) return null;
  return resp.json();
}

function make(doFetch) {
  const send = async (method, path, body) => {
    const opts = { method, headers: {} };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    return unwrap(await doFetch(path, opts));
  };
  return {
    get: (path) => send("GET", path),
    post: (path, body) => send("POST", path, body),
    put: (path, body) => send("PUT", path, body),
    del: (path) => send("DELETE", path),
    /** Сирий запит — для того, що не є JSON в обидва боки: вивантаження
     *  бекапу (blob) і завантаження виписки (FormData). */
    raw: (path, opts) => doFetch(path, opts || {}).then(noteUnauthorized),
  };
}

/** Той самий origin, що й сторінка.
 *
 *  portfolio() — slug поточного портфеля (portfolio.js), читається на
 *  КОЖЕН запит і йде заголовком X-Portfolio; порожній означає головний, і
 *  заголовок тоді не ставиться зовсім — бекенд без нього поводиться як
 *  доти. Саме тут, а не в store: raw() (бекап, виписка) іде повз store, а
 *  портфель мусить бути один на всі шляхи. */
export const httpTransport = (base = "/api/", portfolio = () => "") =>
  make((path, opts) => {
    const slug = portfolio();
    if (!slug) return fetch(base + path, opts);
    const headers = new Headers(opts.headers || {});
    headers.set("X-Portfolio", slug);
    return fetch(base + path, { ...opts, headers });
  });
