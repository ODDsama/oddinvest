// Скелет розділу на час завантаження.
//
// Доти на цьому місці стояло слово «Завантаження…» посеред порожнього
// екрана. Воно каже, що треба чекати, і не каже ні скільки, ні чого — а
// коли вміст приїжджає, сторінка стрибає, бо розкладка виявляється
// зовсім іншою. Скелет має ФОРМУ свого розділу: висота лишається тією
// самою до і після, тож стрибка немає зовсім.
//
// Форми навмисно грубі. Скелет не мусить збігатися з вмістом піксель у
// піксель — він мусить збігатися ЗА ЗРОСТОМ і за кількістю блоків;
// точніший лише дорожче коштує в підтримці, бо його доводиться правити
// разом із кожним розділом.

const line = (w) => `<div class="skel-line" style="width:${w}"></div>`;

const tiles = (n) =>
  `<div class="skel-tiles">${Array.from({ length: n }, () =>
    `<div class="skel-tile">${line("55%")}${line("80%")}</div>`).join("")}</div>`;

const card = (rows) =>
  `<div class="skel-card">${line("38%")}${Array.from({ length: rows }, (_, i) =>
    line(`${92 - (i % 3) * 14}%`)).join("")}</div>`;

const chart = () => `<div class="skel-card">${line("38%")}<div class="skel-chart"></div></div>`;

const table = (rows) =>
  `<div class="skel-card">${line("30%")}<div class="skel-rows">${
    Array.from({ length: rows }, () => `<div class="skel-row"></div>`).join("")}</div></div>`;

const banner = () => `<div class="skel-banner">${line("45%")}${line("70%")}</div>`;

// Що з чого складається. Ключі — ті самі, що в TABS.
const SHAPES = {
  overview: () => banner() + tiles(3) + card(6) + card(4),
  portfolio: () => tiles(4) + table(8) + card(3),
  money: () => tiles(5) + card(5) + card(4),
  future: () => card(4) + chart() + chart() + table(6),
  settings: () => card(3) + card(6) + card(6),
};

/** Розмітка скелета для розділу. aria-hidden, бо читачеві екрана про
 *  сірі прямокутники сказати нічого — про очікування він дізнається з
 *  aria-busy на самому <main>. */
export function skeleton(tab) {
  const shape = SHAPES[tab] || SHAPES.overview;
  return `<div class="skel" aria-hidden="true">${shape()}</div>`;
}
