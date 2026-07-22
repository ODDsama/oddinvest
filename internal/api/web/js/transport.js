// Транспорт до REST-бекенда — єдина річ, якою поверхні по-справжньому
// відрізняються.
//
// Веб-UI ходить у бекенд напряму: він із нього ж і віддається.
// Панель HA ходить через проксі /api/oddinvest/*, бо так працює
// HA-авторизація, не потрібен CORS і адреса бекенда не світиться в
// браузері. Різниця рівно в тому, ЯКИМ fetch і за ЯКИМ префіксом —
// решта коду про неї знати не повинна.
//
// Шляхи скрізь БЕЗ префікса: "summary", "lots/7", "calendar?from=…".

/** Спільний розбір відповіді: помилку піднімаємо з текстом тіла, бо
 *  голий 400 не каже, що саме бекенд не прийняв. */
async function unwrap(resp) {
  if (!resp.ok) {
    const txt = await resp.text().catch(() => "");
    // Бекенд віддає {"error": "..."} — витягуємо саме його, якщо є.
    let msg = txt.slice(0, 300);
    try {
      const j = JSON.parse(txt);
      if (j && j.error) msg = j.error;
    } catch (_) { /* не JSON — лишаємо як є */ }
    throw new Error(`${resp.status}: ${msg}`);
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
    raw: (path, opts) => doFetch(path, opts || {}),
  };
}

/** Веб-UI: той самий origin, що й сторінка. */
export const httpTransport = (base = "/api/") =>
  make((path, opts) => fetch(base + path, opts));

/** Панель HA: fetchWithAuth підставляє токен користувача. */
export const hassTransport = (hass, base = "/api/oddinvest/") =>
  make((path, opts) => hass.fetchWithAuth(base + path, opts));
