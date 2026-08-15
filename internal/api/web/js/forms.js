// Запис на бекенд: одна послідовність замість двох десятків копій.
//
// Кожна мутація в застосунку робила те саме вручну: preventDefault, зібрати
// поля, api(), toast про успіх, reload, catch і toast про помилку. Двадцять
// шість копій рядка `ctx.toast(String(err.message || err), false)` і двадцять
// чотири `ctx.reload()` — і кожна нова форма починалася з копіювання сусідньої
// разом із її помилками.
//
// Збирач тіла лишається за викликачем: одна форма кладе parseInt, інша тягне
// валюту з dataset вибраної опції, третя підставляє id у шлях. Уніфікувати це
// означало б завести конфіг на кожне поле — довше за сам код. Спільне тут
// інше: що робиться ПІСЛЯ, і саме воно й дублювалось.

/** Виконати запит і показати результат. Спільне ядро для форм і кнопок. */
export async function apply(ctx, { method = "POST", path, body }, msg) {
  try {
    await ctx.api(method, path, body);
    if (msg) ctx.toast(msg);
    ctx.reload();
  } catch (err) {
    ctx.toast(String(err.message || err), false);
  }
}

/** Сабміт форми. build(form) повертає {method?, path, body, msg}
 *  або null/undefined, щоб тихо скасувати відправку.
 *
 *  form може бути null: проводка шукає елементи через querySelector, і доти,
 *  доки перевірки не було, умовний рендер однієї картки обривав реєстрацію
 *  всіх наступних обробників розділу. */
export function onSubmit(ctx, form, build) {
  if (!form) return;
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const req = build(e.target);
    if (!req) return;
    await apply(ctx, req, req.msg);
  });
}

/** Кнопки видалення за селектором. build(btn) повертає
 *  {path, msg?, confirm?} або null, щоб нічого не робити.
 *
 *  confirm — текст питання; порожній означає «без питання». Питаємо ДО
 *  запиту й лише коли є що видаляти. */
export function onDelete(ctx, root, sel, build) {
  if (!root) return;
  root.querySelectorAll(sel).forEach((btn) => {
    btn.addEventListener("click", async () => {
      const req = build(btn);
      if (!req) return;
      if (req.confirm && !(await confirmDialog(ctx, req.confirm))) return;
      await apply(ctx, { method: "DELETE", path: req.path }, req.msg || "Видалено");
    });
  });
}

/** Своє підтвердження замість window.confirm().
 *
 *  Нативний confirm() тут не годиться: у застосунку, вбудованому в чужу
 *  сторінку чи автоматизований браузер, він мовчки повертає false —
 *  запит на видалення просто ніколи не йде, а кнопка виглядає
 *  зламаною. Той самий клас середовищ, де вже довелось патчити Escape
 *  для попапу «i» (info.js) — shadow root заважає нативному діалогу
 *  так само, і власний <dialog> від цих квирків не залежить, бо
 *  показує й закриває його сам застосунок.
 *
 *  ctx.root — shadowRoot компонента. Немає розмітки (старий шаблон
 *  шапки) — падаємо назад на window.confirm(), щоб видалення хоч якось
 *  працювало, а не мовчало. */
export function confirmDialog(ctx, message) {
  const root = ctx && ctx.root;
  const pop = root && root.getElementById && root.getElementById("confirmPop");
  if (!pop) return Promise.resolve(window.confirm(message));
  return new Promise((resolve) => {
    pop.querySelector("#confirmPopText").textContent = message;
    const yes = pop.querySelector("[data-confirm-yes]");
    const no = pop.querySelector("[data-confirm-no]");
    let decided = false;
    const finish = (val) => {
      if (decided) return;
      decided = true;
      yes.removeEventListener("click", onYes);
      no.removeEventListener("click", onNo);
      pop.removeEventListener("close", onClose);
      if (pop.open) pop.close();
      resolve(val);
    };
    const onYes = () => finish(true);
    const onNo = () => finish(false);
    // Escape (через bindConfirmDialog/info.js) чи клік по підкладці
    // закривають <dialog> нативно — це та сама відповідь, що й
    // «Скасувати», тож саме подія close, а не власний слухач, дає
    // йому означати «ні».
    const onClose = () => finish(false);
    yes.addEventListener("click", onYes);
    no.addEventListener("click", onNo);
    pop.addEventListener("close", onClose);
    pop.showModal();
  });
}

/** Клік по підкладці підтвердження скасовує його — той самий прийом,
 *  що й у попапу «i» (info.js:bindInfo), і з тієї самої причини:
 *  ::backdrop нативного <dialog> сам по собі клік не закриває. */
export function bindConfirmDialog(root) {
  root.addEventListener("click", (e) => {
    const pop = root.getElementById("confirmPop");
    if (!pop || e.target !== pop) return;
    const r = pop.getBoundingClientRect();
    const out = e.clientX < r.left || e.clientX > r.right
      || e.clientY < r.top || e.clientY > r.bottom;
    if (out) pop.close();
  });
}
