// Підключення спільних стилів до shadow root.
//
// Шляхи рахуються від import.meta.url, а не задаються ззовні: панель HA
// вантажиться з версійного префікса (/oddinvest_static/<v>/...), веб — з
// кореня, і будь-яка спроба прописати шлях константою розсипалась би на
// одній із поверхонь. Модуль знає, звідки його самого взяли, — цього
// досить.

const CSS_DIR = new URL("../css/", import.meta.url);

export const cssUrl = (name) => new URL(name, CSS_DIR).href;

/** tokens + base однакові скрізь; тему обирає поверхня. */
const sheetFiles = (theme) => ["tokens.css", `theme-${theme}.css`, "base.css"];

// Один розібраний стайлшит на весь застосунок: конструюємо раз і
// роздаємо посиланням. Панель перемальовує shell при кожному
// перезавантаженні інтеграції, і платити за фетч щоразу нема за що.
const cache = new Map();

function loadSheet(url) {
  if (!cache.has(url)) {
    // cache:"no-cache" — не «не кешувати», а «перепитати, чи не змінилось»:
    // браузер шле умовний запит і на незмінений файл отримує 304 без тіла.
    // Три таких запити на завантаження сторінки в локальній мережі коштують
    // ніщо, а платня за їх відсутність висока: браузер евристично тримає
    // CSS без заголовків кешу годинами, і оновлена панель показує стару
    // палітру без жодного натяку, що саме не так.
    cache.set(url, fetch(url, { cache: "no-cache" })
      .then((r) => {
        if (!r.ok) throw new Error(`${url}: ${r.status}`);
        return r.text();
      })
      .then((text) => {
        const sheet = new CSSStyleSheet();
        sheet.replaceSync(text);
        return sheet;
      }));
  }
  return cache.get(url);
}

/** Резервний варіант для середовищ без конструйованих стайлшитів:
 *  теги <link> усередину самого root. Мигання на першому кадрі є, але
 *  порожня панель була б гіршою. */
export function styleLinks(theme) {
  return sheetFiles(theme)
    .map((f) => `<link rel="stylesheet" href="${cssUrl(f)}">`)
    .join("");
}

/** Підключає стилі до shadow root. Повертає проміс, який резолвиться,
 *  коли стилі вже застосовані — щоб можна було не показувати вміст до
 *  того, як він одягнеться. */
export async function adoptStyles(root, theme) {
  const supported = "adoptedStyleSheets" in Document.prototype
    && typeof CSSStyleSheet !== "undefined"
    && CSSStyleSheet.prototype.replaceSync;
  if (!supported) {
    root.insertAdjacentHTML("afterbegin", styleLinks(theme));
    return;
  }
  const sheets = await Promise.all(sheetFiles(theme).map((f) => loadSheet(cssUrl(f))));
  root.adoptedStyleSheets = sheets;
}
