// «Налаштування → Портфелі»: чий портфель і з якою стратегією (0054).
//
// Перемикають портфель із шапки (і з палітри); тут його заводять,
// перейменовують і стирають. Сторінка навмисно не показує чисел: капітал
// кожного портфеля живе на його ж «Огляді», а зведена плитка «разом» — це
// окреме питання, якого ця фаза не ставить.
//
// Стерти можна лише НЕ відкритий портфель. Не заради безпеки (питання з
// назвою й так стоїть), а щоб не малювати сторінку в портфелі, якого вже
// немає: після DELETE перемальовування пішло б із його slug-ом у
// заголовку й дістало 404 на все. «Перейди в інший, тоді стирай» дешевше
// за цей кадр.

import { esc } from "../format.js";
import { onSubmit, openEdit } from "../forms.js";
import { text, field, formHTML } from "../fields.js";
import { inlineEdit } from "../crud.js";
import { infoBtn } from "../info.js";

// Латиниця з української для slug-а: щоб друге поле форми заповнювалось
// саме, а людина лише поглянула. Паспортна транслітерація (постанова
// 55/2010), без апострофів і мʼякого знака.
const TRANSLIT = {
  а: "a", б: "b", в: "v", г: "h", ґ: "g", д: "d", е: "e", є: "ie", ж: "zh", з: "z",
  и: "y", і: "i", ї: "i", й: "i", к: "k", л: "l", м: "m", н: "n", о: "o", п: "p",
  р: "r", с: "s", т: "t", у: "u", ф: "f", х: "kh", ц: "ts", ч: "ch", ш: "sh",
  щ: "shch", ь: "", ю: "iu", я: "ia", ы: "y", э: "e", ё: "e", ъ: "",
};

/** Ідентифікатор із назви: «Дружина» → «druzhyna». Ті самі межі, що й у
 *  сховища (латиниця, цифри, дефіс, до 32). */
export function slugOf(name) {
  return String(name || "").toLowerCase().split("")
    .map((c) => (c in TRANSLIT ? TRANSLIT[c] : c)).join("")
    .replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 32);
}

function rowHTML(p, current) {
  const isCur = p.slug === current;
  const deletable = p.slug !== "main" && !isCur;
  return `<div class="pv-row" data-pf="${esc(p.slug)}">
    <span class="row-h">
      <input class="cat-f cat-name w-lg" data-field="name" value="${esc(p.name)}"
        aria-label="Назва портфеля">
      <span class="muted fine">${esc(p.slug)}</span>
      ${isCur
    ? `<span class="t-ok fine">відкритий</span>`
    : `<button type="button" class="sm" data-pfopen="${esc(p.slug)}">Відкрити</button>`}
    </span>
    ${deletable
    ? `<button class="sm warn self-start" data-pfdel="${esc(p.slug)}"
        title="Стерти портфель з усім умістом">✕</button>`
    : ""}
  </div>`;
}

export async function portfolios(ctx, main) {
  const list = ctx.portfolios || [];
  const cur = ctx.portfolio;
  main.innerHTML = `<div class="card" id="pfCard">
    <h2 class="h-row">Портфелі ${infoBtn("setPortfolios")}</h2>
    <div class="note">Кожен портфель — окремий світ: своя стратегія, свої брокери, подушка,
      борги, цілі й план. Спільні лише довідник НБУ, курси й каталог фондів.
      Перемикач — у шапці; Home Assistant бачить лише головний.</div>
    ${list.map((p) => rowHTML(p, cur)).join("")}
    ${formHTML({
    id: "pfAddForm", cls: "row-h mt", submit: "Створити",
    fields: [
      text("name", "", { ph: "назва, напр. Дружина", cls: "w-lg", required: true }),
      text("slug", "", {
        ph: "ідентифікатор латиницею", cls: "w-lg",
        title: "Латиниця, цифри й дефіс, до 32 знаків. Іде в адресу й заголовок; людині показується назва",
      }),
    ],
  })}
    <div class="sub-xs muted">Новий портфель порожній: стратегію обирають у «Політика →
      Стратегія», брокери зʼявляються з першою операцією, а перший імпорт виписки бере всю її
      історію — водяного знака в нього ще немає. Щоб стерти відкритий портфель, спершу
      перейди в інший.</div>
  </div>`;
  const card = main.querySelector("#pfCard");

  inlineEdit(ctx, card, {
    rows: "[data-pf]", fields: ".cat-f",
    path: (row) => `portfolios/${row.dataset.pf}`,
    guard: (values) => !!String(values.name || "").trim(),
    msg: "Назву збережено",
  });
  card.querySelectorAll("[data-pfopen]").forEach((b) => {
    b.addEventListener("click", () => ctx.setPortfolio(b.dataset.pfopen));
  });
  card.querySelectorAll("[data-pfdel]").forEach((b) => {
    const slug = b.dataset.pfdel;
    const p = list.find((x) => x.slug === slug) || { name: slug };
    b.addEventListener("click", () => openEdit(ctx, {
      title: `Стерти портфель «${esc(p.name)}»?`,
      submit: "Стерти",
      fields: `<p class="note">Разом із ним зникнуть усі його лоти, рухи, вклади, борги, цілі,
          план і знімки. Дію не скасувати; добові дампи лишаються в
          <code>portfolios/${esc(slug)}/</code>.</p>`
        + field("confirm", "Введи назву портфеля, щоб підтвердити", { required: true, ph: p.name }),
      // Кнопка спить, доки назва не збіглась: підтвердження назвою — це
      // саме той випадок, коли форма має право не подаватись.
      wire: (form) => {
        const btn = form.querySelector('button[type="submit"]');
        btn.disabled = true;
        form.confirm.addEventListener("input", () => {
          btn.disabled = form.confirm.value.trim() !== p.name;
        });
      },
    }, () => ({ method: "DELETE", path: `portfolios/${slug}`, msg: "Портфель стерто" })));
  });

  const form = main.querySelector("#pfAddForm");
  // Slug пишеться сам із назви, доки людина не торкнулась його поля.
  form.name.addEventListener("input", () => {
    if (form.slug.dataset.touched) return;
    form.slug.value = slugOf(form.name.value);
  });
  form.slug.addEventListener("input", () => { form.slug.dataset.touched = "1"; });
  onSubmit(ctx, form, (f) => {
    const name = f.name.value.trim();
    const slug = f.slug.value.trim() || slugOf(name);
    if (!name) return null;
    if (list.some((x) => x.slug === slug)) {
      ctx.toast(`Ідентифікатор «${slug}» уже зайнятий`, false);
      return null;
    }
    return { path: "portfolios", body: { name, slug }, msg: `Портфель «${name}» створено` };
  });
}
