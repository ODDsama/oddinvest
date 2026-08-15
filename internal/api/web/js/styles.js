// Підключення стилів до shadow root.
//
// Шляхи рахуються від import.meta.url, а не задаються ззовні. Спершу це
// було потрібне, бо панель HA вантажилась із версійного префікса
// (/oddinvest_static/<v>/...), а веб — з кореня; панелі більше немає, але
// правило лишається дешевшим за константу: модуль знає, звідки його
// самого взяли, і не має думати, під яким шляхом його змонтували.

const CSS_DIR = new URL("../css/", import.meta.url);

export const cssUrl = (name) => new URL(name, CSS_DIR).href;

// Тема одна. Розділ tokens/theme/base лишається не заради вибору між
// темами, а заради порядку: tokens.css оголошує змінні, theme-web.css
// дає їм значення, base.css їх лише споживає — і правило, яке малює
// щось повз змінну, видно одразу.
//
// css/fonts.css сюди НЕ дописувати, хоч він і четвертий файл у тому ж
// каталозі. @font-face належить документу: правило, що лежить у
// стайлшиті shadow root, не реєструє гарнітуру й мовчки не робить
// нічого. Тому fonts.css підключений лінком у index.html — там він і
// має лишитись.
const sheetFiles = () => ["tokens.css", "theme-web.css", "base.css"];

// Один розібраний стайлшит на весь застосунок: конструюємо раз і
// роздаємо посиланням. Shell перемальовується не лише на старті, і
// платити за фетч щоразу нема за що.
const cache = new Map();

function loadSheet(url) {
  if (!cache.has(url)) {
    // cache:"no-cache" — не «не кешувати», а «перепитати, чи не змінилось»:
    // браузер шле умовний запит і на незмінений файл отримує 304 без тіла.
    // Три таких запити на завантаження сторінки в локальній мережі коштують
    // ніщо, а платня за їх відсутність висока: браузер евристично тримає
    // CSS без заголовків кешу годинами, і оновлений застосунок показує
    // стару палітру без жодного натяку, що саме не так.
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
 *  застосунок без стилів був би гіршим. */
export function styleLinks() {
  return sheetFiles()
    .map((f) => `<link rel="stylesheet" href="${cssUrl(f)}">`)
    .join("");
}

/** Підключає стилі до shadow root. Повертає проміс, який резолвиться,
 *  коли стилі вже застосовані — щоб можна було не показувати вміст до
 *  того, як він одягнеться. */
export async function adoptStyles(root) {
  const supported = "adoptedStyleSheets" in Document.prototype
    && typeof CSSStyleSheet !== "undefined"
    && CSSStyleSheet.prototype.replaceSync;
  if (!supported) {
    root.insertAdjacentHTML("afterbegin", styleLinks());
    return;
  }
  const sheets = await Promise.all(sheetFiles().map((f) => loadSheet(cssUrl(f))));
  root.adoptedStyleSheets = sheets;
}
