// Форматування — числа, гроші, дати, українська множина.
//
// Спільний модуль для веб-UI бекенда і панелі Home Assistant. Доти, доки
// ці функції жили двома копіями, вони встигли розійтись у дрібницях:
// у панелі був `esc`, у вебі — ні, і саме тому веб не екранував вивід.
// Тут канонічна версія одна.

/** Екранування для вставки в innerHTML. Обов'язкове для всього, що
 *  прийшло з бекенда: ISIN, назви брокерів і фондів, нотатки. */
export const esc = (s) =>
  String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

export const curSym = (c) => ({ UAH: "₴", USD: "$", EUR: "€" }[c] || c);

export const today = () => new Date().toISOString().slice(0, 10);

// --- дати ---

const MON_NOM = ["січень", "лютий", "березень", "квітень", "травень", "червень",
  "липень", "серпень", "вересень", "жовтень", "листопад", "грудень"];
const MON_GEN = ["січня", "лютого", "березня", "квітня", "травня", "червня",
  "липня", "серпня", "вересня", "жовтня", "листопада", "грудня"];

/** Українська множина: 1 рік / 2-4 роки / 5+ років (з винятком 11-14). */
export function plural(n, one, few, many) {
  const d = n % 10, h = n % 100;
  if (d === 1 && h !== 11) return one;
  if (d >= 2 && d <= 4 && (h < 10 || h >= 20)) return few;
  return many;
}

/** 32 -> «2 роки 8 місяців» (замість «2.6 р.»). */
export function humanMonths(m) {
  m = Math.max(0, Math.round(m));
  const y = Math.floor(m / 12), mo = m % 12, parts = [];
  if (y) parts.push(`${y} ${plural(y, "рік", "роки", "років")}`);
  if (mo) parts.push(`${mo} ${plural(mo, "місяць", "місяці", "місяців")}`);
  return parts.join(" ") || "менше місяця";
}

/** «2029-02-19» -> «лютий 2029». */
export function monthYear(iso) {
  const p = String(iso || "").split("-");
  if (p.length < 2) return String(iso || "");
  return `${MON_NOM[+p[1] - 1] || p[1]} ${p[0]}`;
}

/** «2029-03-17» -> «березня 2029»: родовий відмінок для конструкцій
 *  «до <місяця>», де називний дав би «до березень». */
export function monthYearGen(iso) {
  const p = String(iso || "").split("-");
  if (p.length < 2) return String(iso || "");
  return `${MON_GEN[+p[1] - 1] || p[1]} ${p[0]}`;
}

/** «2026-07-22» -> «22 липня», а якщо не цьогоріч — «20 січня 2027»:
 *  без року дата наступної виплати читається як «ось-ось». */
export function dayMonth(iso) {
  const p = String(iso || "").split("-");
  if (p.length < 3) return monthYear(iso);
  const d = `${+p[2]} ${MON_GEN[+p[1] - 1] || p[1]}`;
  return p[0] === String(new Date().getFullYear()) ? d : `${d} ${p[0]}`;
}

// --- числа й гроші ---

/** Компактно для осей і підписів: 1,2М / 34к / 567. */
export const compact = (v) => {
  const a = Math.abs(v);
  if (a >= 1e6) return (v / 1e6).toFixed(1).replace(".", ",") + "М";
  if (a >= 1e3) return Math.round(v / 1e3) + "к";
  return String(Math.round(v));
};

/** Гривні без копійок: суми рівня «капітал» і «внесок», де копійка
 *  рішення не змінює, зате заважає порівнювати рядки поглядом. */
export const uah0 = (v) => Math.round(Number(v) || 0).toLocaleString("uk") + " ₴";

/** Гривні з копійками: дивіденд 16.33 ₴ і податок 2.66 ₴ — числа, де
 *  округлення до гривні з'їдає п'яту частину значення. */
export const uah2 = (v) =>
  (Number(v) || 0).toLocaleString("uk", { minimumFractionDigits: 2, maximumFractionDigits: 2 }) + " ₴";

/** Довільна валюта з копійками. */
export const cur2 = (v, c) =>
  (Number(v) || 0).toLocaleString("uk", { minimumFractionDigits: 2, maximumFractionDigits: 2 }) + " " + curSym(c);

/** Об'єкт {amount, currency} з бекенда. */
export const money = (m) =>
  m ? `${Number(m.amount).toLocaleString("uk", { minimumFractionDigits: 2 })} ${esc(m.currency)}` : "—";

/** Те саме, але сумою як прийшла — без локалізації.
 *  Спадок веб-UI (таблиці позицій, лотів, продажів); зникне разом із
 *  його розміткою, коли обидві поверхні перейдуть на спільні view. */
export const moneyRaw = (m) => (m ? `${esc(m.amount)} ${esc(m.currency)}` : "—");

// --- похідні від документа стану ---

/** Капітал — це ВСЕ, що працює: номінал паперів, гроші на рахунку й
 *  сертифікати фондів. Останні довго рахувались окремо просто тому, що
 *  з'явились пізніше, і капітал через це занижувався на їхню вартість. */
export const capitalUAH = (s) =>
  ((s || {}).nominal_uah_eq || 0) + ((s || {}).account_uah || 0) + ((s || {}).funds_uah || 0);

/** Собівартість сертифікатів — «вкладено» тієї ж природи, що й вартість
 *  входу лотів, тож у плитці вони стоять разом. */
export const fundsCost = (s) =>
  ((s || {}).funds || []).reduce((a, f) => a + (f.cost_basis || 0), 0);
