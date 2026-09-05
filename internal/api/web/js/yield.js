/** Ставка на екрані: номінальна головним числом, реальна поруч, розклад
 *  по кліку.
 *
 *  ЩО ЗМІНИЛОСЬ І ЧОМУ. Доти головним числом скрізь стояла РЕАЛЬНА
 *  дохідність. Вона похідна: валова ставка мінус податок мінус
 *  знецінення, — і жоден із цих кроків ніде не показувався. Людина
 *  бачила «4.2%» там, де в договорі написано 16%, і мусила вірити.
 *  Тепер головне число — те, що справді написано в договорі чи довіднику,
 *  а реальна стоїть поруч дрібним і пояснюється на клік.
 *
 *  ПОРЯДОК У СПИСКАХ ВІД ЦЬОГО НЕ ЗМІНИВСЯ: сортує далі бекенд і далі за
 *  реальною. Показ і порядок — різні питання, і плутати їх не можна:
 *  гривневий вклад під 16% номінальних стоїть нижче за валютний папір
 *  під 4.5% саме тому, що після знецінення все навпаки.
 *
 *  ОДИН КОМПОНЕНТ НА ВІСІМ ЕКРАНІВ. Спільного компонента дохідності доти
 *  не існувало взагалі: yieldTile був локальним замиканням в
 *  instrument-view.js, yieldTilesHTML — окремою функцією в risk.js, а
 *  решта екранів малювала число руками. Через це «реальна» на одному
 *  екрані означала не зовсім те, що на сусідньому, і сказати, чим саме
 *  вони різняться, було ніде.
 *
 *  Розклад малюється у ВЖЕ НАЯВНИЙ <dialog id="infoPop">, той самий, що
 *  й кнопки «i». Другий діалог довелося б навчити пастці фокуса, Escape
 *  і поверненню фокуса заново — усе це вже зроблено в bindInfo, разом із
 *  обходом того, що всередині shadow root подія `cancel` не настає. */

import { esc, pct, pp } from "./format.js";

/** Дані розкладу їдуть в атрибуті самі, а не через модульну мапу з
 *  ключами: рядки перемальовуються від кожного оновлення, і мапа
 *  пережила б свої кнопки. Обсяг тут — десяток чисел. */
const packRate = (r) => encodeURIComponent(JSON.stringify(r));

/** Клітинка ставки. `parts` — обʼєкт rate_parts із бекенда; без нього
 *  малюється саме те, що прийшло, без вигаданих складників. */
export function yieldCell(parts, { real, nominal, bare = false } = {}) {
  const n = parts ? parts.net_pct : nominal;
  const r = parts ? parts.real_fx_pct : real;
  const num = `<b class="yld-n">${pct(n)}</b>`;
  if (!parts) return `<span class="yld">${num}</span>`;
  // bare — лише число, без другого рядка. Для тих місць, де реальна вже
  // стоїть СУСІДНЬОЮ КОЛОНКОЮ (черга погашення): повторити її під числом
  // означало б написати те саме двічі в одному рядку таблиці.
  const sub = bare ? "" : `<span class="yld-r">${pct(r)} реальних</span>`;
  return `<button type="button" class="yld" data-rate="${packRate(parts)}"
    title="З чого складається ця ставка">${num}${sub}</button>`;
}

const row = (k, v) => `<div class="kv"><span>${k}</span><span>${v}</span></div>`;

/** Розклад однієї ставки. Порядок рядків — це порядок відрахувань, і
 *  саме він пояснює число: від того, що в договорі, до того, що лишається. */
export function rateHTML(p) {
  const out = [];
  out.push(row("Ставка з договору", pct(p.gross_pct, 2)));
  if (p.tax_pct) out.push(row("Податок", `−${pp(p.tax_pct, 2)}`));
  out.push(row("<b>Номінальна</b>", `<b>${pct(p.net_pct, 2)}</b>`));
  out.push(`<hr class="sep">`);
  out.push(row("Знецінення гривні", `−${pp(p.devaluation_pct, 1)}`));
  out.push(row("<b>Реальна проти долара</b>", `<b>${pct(p.real_fx_pct, 2)}</b>`));
  if (p.inflation_pct != null) {
    out.push(`<hr class="sep">`);
    out.push(row("Інфляція (ІСЦ НБУ)", `−${pp(p.inflation_pct, 1)}`));
    out.push(row("<b>Реальна проти цін</b>", `<b>${pct(p.real_cpi_pct, 2)}</b>`));
  }
  const notes = [];
  if (p.basis) notes.push(`<p class="sub-xs">Основа: ${esc(p.basis)}.</p>`);
  notes.push(`<p class="sub-xs">Дві реальні <b>не складаються</b>: це дві лінійки, а не два
    відрахування. Перша каже, скільки доларів купить результат, друга — скільки товарів.
    Порядок у списках рахується за першою.</p>`);
  if (p.inflation_pct != null && p.currency && p.currency !== "UAH") {
    notes.push(`<p class="sub-xs">Валютна ставка спершу переведена в гривневі терміни
      знеціненням — гроші прийдуть у ${esc(p.currency)}, а витрачати їх тут, — і вже тоді
      продефльована цінами.</p>`);
  }
  return out.join("") + notes.join("");
}

/** Делегований обробник, як bindInfo. Кличеться раз на оболонку. */
export function bindYield(root) {
  root.addEventListener("click", (e) => {
    const btn = e.target.closest && e.target.closest("[data-rate]");
    if (!btn) return;
    const pop = root.getElementById("infoPop");
    if (!pop) return;
    let parts;
    try {
      parts = JSON.parse(decodeURIComponent(btn.dataset.rate));
    } catch {
      return;
    }
    pop.querySelector(".box").innerHTML =
      `<button class="x" data-closeinfo aria-label="Закрити">×</button>` +
      `<h4 id="infoPopTitle">Звідки ця ставка</h4>${rateHTML(parts)}`;
    pop.showModal();
  });
}
