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
      if (req.confirm && !confirm(req.confirm)) return;
      await apply(ctx, { method: "DELETE", path: req.path }, req.msg || "Видалено");
    });
  });
}
