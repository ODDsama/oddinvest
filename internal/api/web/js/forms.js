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

import { money as fmtMoney } from "./format.js";

/** Виконати запит і показати результат. Спільне ядро для форм і кнопок.
 *
 *  Повертає true/false — вийшло чи ні. Двом десяткам наявних викликачів
 *  це байдуже (значення просто ігнорується), а модальній правці — ні:
 *  без нього вона не відрізнить «збережено» від «400 від валідатора» і
 *  або закриється, зʼївши введене, або не закриється ніколи. */
export async function apply(ctx, { method = "POST", path, body }, msg) {
  try {
    await ctx.api(method, path, body);
    if (msg) ctx.toast(msg);
    ctx.reload();
    return true;
  } catch (err) {
    ctx.toast(String(err.message || err), false);
    return false;
  }
}

/** Кілька записів поспіль як ОДНА правка: усі запити по черзі, тост і
 *  перемальовування — раз наприкінці.
 *
 *  Потрібне там, де одна дія користувача — це дві операції над моделлю.
 *  Підвищення зарплати, наприклад: старий потік закривається датою, новий
 *  відкривається з наступного дня. Через два окремі apply() це дало б два
 *  тости й два перемальовування, а на помилці другого запиту — половину
 *  зробленої зміни без жодного натяку, що сталось.
 *
 *  Зупиняється на першій помилці: решта не виконується, тост каже, що
 *  саме не вийшло. */
export async function applyAll(ctx, reqs, msg) {
  try {
    for (const { method = "POST", path, body } of reqs) {
      await ctx.api(method, path, body);
    }
    if (msg) ctx.toast(msg);
    ctx.reload();
    return true;
  } catch (err) {
    ctx.toast(String(err.message || err), false);
    return false;
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

/** Сабміт операції, за яку може не вистачити грошей на рахунку.
 *
 *  build(form) повертає {path, body, msg, check, date, what} або null.
 *  Порядок дій:
 *    1) POST на {check} ТИМ САМИМ тілом — бекенд каже, скільки бракує;
 *    2) вистачає — звичайний apply(), нічого не питаємо;
 *    3) бракує — питаємо; «ні» не пише НІЧОГО;
 *    4) «так» — поповнення рівно на нестачу і сама операція, одним applyAll.
 *
 *  Суму рахує бекенд і лише бекенд. qty×ціна+комісія тут були б п'ятою
 *  копією domain.LotCost, а обидва випадки, коли в цьому застосунку
 *  з'являлась друга копія арифметики, записані в handlers_whatif.go:9 —
 *  обидва скінчились різними числами на одному екрані. Рядок суми
 *  приходить готовим і йде в тіло поповнення ДОСЛІВНО.
 *
 *  Поповнення ПЕРШЕ. applyAll спиняється на першій помилці, і з двох
 *  половинчастих результатів ця чесніша: у «Грошах» лишається підписаний
 *  рядок, який видно й можна зняти однією кнопкою. Зворотний порядок на
 *  тому самому збої лишив би записану покупку з мінусом на рахунку —
 *  рівно той стан, якого людина щойно уникала.
 *
 *  Другий сабміт, доки перший не завершився, ІГНОРУЄТЬСЯ. Тут це не
 *  перестраховка: між натисканням і записом лежать запит на сервер і
 *  модальне питання, тобто секунди, а діалог у оболонці ОДИН на весь
 *  застосунок. Два накладені сабміти вішають на його кнопку два
 *  обробники, і одне «так» розв'язує обидва — виходить дві покупки й два
 *  поповнення з одного кліку. Ловилось саме так. */
export function onSubmitFunded(ctx, form, build) {
  if (!form) return;
  let busy = false;
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (busy) return;
    const req = build(e.target);
    if (!req) return;
    busy = true;
    try {
      let chk;
      try {
        chk = await ctx.api("POST", req.check, req.body);
      } catch (err) {
        ctx.toast(String(err.message || err), false);
        return;
      }
      if (chk.enough) {
        await apply(ctx, req, req.msg);
        return;
      }
      // Порожня назва брокера — законний стан («гроші без прив'язки»), тож
      // у питанні підміняємо її словами, а в тіло поповнення кладемо СИРЕ
      // значення: бекенд заводить брокера за назвою, і «—» осів би в
      // довіднику окремим рахунком.
      const ok = await confirmDialog(ctx,
        `На рахунку ${chk.broker || "без брокера"} не вистачає `
        + `${fmtMoney(chk.short)}. Додати поповнення на цю суму і записати?`,
        { yes: "Поповнити й записати", danger: false });
      if (!ok) return;
      await applyAll(ctx, [
        {
          path: "deposits",
          body: {
            // Дата поповнення — дата САМОЇ операції, не «сьогодні»: виписка
            // руху грошей розкладена по датах, і внесок іншим місяцем
            // показав би надходження, якого того місяця не було.
            date: req.date, amount: chk.short.amount,
            currency: chk.short.currency, broker: chk.broker,
            note: "автопоповнення: " + req.what,
          },
        },
        req,
      ], req.msg);
    } finally {
      busy = false;
    }
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
 *  працювало, а не мовчало.
 *
 *  Підпис кнопки «так» задає ВИКЛИКАЧ. Доти він був зашитий у розмітці
 *  словом «Видалити», бо єдиним питанням у застосунку було видалення, —
 *  і перше ж інше питання («додати поповнення?») отримало кнопку
 *  «Видалити», тобто діалог пропонував протилежне тому, що робив.
 *  danger малює її червоним: колір тут не оздоба, а обіцянка
 *  незворотності, і на додаванні грошей вона була б неправдою. */
export function confirmDialog(ctx, message, { yes: label = "Видалити", danger = true } = {}) {
  const root = ctx && ctx.root;
  const pop = root && root.getElementById && root.getElementById("confirmPop");
  if (!pop) return Promise.resolve(window.confirm(message));
  // Діалог у оболонці один. Якщо на екрані вже стоїть питання, друге
  // не ставимо: showModal() на відкритому <dialog> кидає, а гірше — два
  // питання ділили б одну кнопку, і одне «так» відповіло б на обидва.
  // «Ні» тут означає «нічого не роблю», а не «людина відмовилась».
  if (pop.open) return Promise.resolve(false);
  return new Promise((resolve) => {
    pop.querySelector("#confirmPopText").textContent = message;
    const yes = pop.querySelector("[data-confirm-yes]");
    const no = pop.querySelector("[data-confirm-no]");
    // Діалог у оболонці один на всі питання, тож підпис і клас
    // виставляються на КОЖНЕ відкриття: інакше попереднє питання
    // лишало б свій вигляд наступному.
    yes.textContent = label;
    yes.className = danger ? "warn" : "";
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
    // Escape (через bindDialogBackdrop/info.js) чи клік по підкладці
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

/** Клік по підкладці будь-якого відкритого <dialog> скасовує його — той
 *  самий прийом, що й у попапу «i» (info.js:bindInfo), і з тієї самої
 *  причини: ::backdrop нативного <dialog> сам по собі клік не закриває.
 *
 *  Шукає dialog[open], а не жорсткий #confirmPop: діалогів у оболонці
 *  тепер три (довідка, підтвердження, правка), і третя копія того самого
 *  прийому не потрібна. Та сама форма, що вже має обробник Escape. */
export function bindDialogBackdrop(root) {
  root.addEventListener("click", (e) => {
    const pop = root.querySelector("dialog[open]");
    if (!pop || e.target !== pop) return;
    const r = pop.getBoundingClientRect();
    const out = e.clientX < r.left || e.clientX > r.right
      || e.clientY < r.top || e.clientY > r.bottom;
    if (out) pop.close();
  });
}

/** Заповнити форму значеннями за іменами полів.
 *
 *  Відсутні в формі ключі мовчки пропускаються, а відсутні у values поля
 *  лишаються як є — тож одна й та сама мапа годиться і формі додавання,
 *  і тілу модалки, і кнопці «копія». */
export function fillForm(form, values) {
  if (!form || !values) return;
  for (const [name, v] of Object.entries(values)) {
    const el = form.elements[name];
    if (!el || el.type === "submit") continue;
    if (el.type === "checkbox") el.checked = !!v;
    else el.value = v == null ? "" : String(v);
  }
}

/** Модальна правка рядка.
 *
 *  fields — HTML полів, той самий набір, що й у формі додавання (саме тому
 *  розмітку малює одна функція: два списки полів розійшлися б на першій же
 *  зміні). build(form) повертає {method, path, body, msg} як в onSubmit,
 *  {requests: [...], msg} для правки з кількох записів (див. applyAll),
 *  або null, щоб тихо скасувати.
 *
 *  Діалог живе в ОБОЛОНЦІ (#editPop), а не в розділі, і це не смак:
 *  apply() після успішного запису кличе ctx.reload(), а той переписує
 *  main.innerHTML цілком — діалог усередині main знищився б у мить, коли
 *  він ще відкритий і тримає top layer.
 *
 *  Escape сюди приходить задарма: спільний обробник (info.js:bindInfo)
 *  ловить dialog[open] загалом. Покладатись на нативний cancel не можна —
 *  усередині shadow root він не настає.
 *
 *  wire(form) — проводка самих ПОЛІВ, а не форми: «інший…» біля
 *  випадайки сутності, підказка ISIN. Без цього гачка модалка малювала б
 *  ті самі поля, що й форма додавання, і мовчки лишала половину з них
 *  мертвими: вибір «інший…» не відкривав би поля назви, бо слухач на
 *  нього вішається після рендера, а рендер тут відбувається на кілька
 *  екранів пізніше за проводку сторінки. Ловилось би це не помилкою, а
 *  тим, що поле просто не з'являється.
 *
 *  → Promise<boolean>: збережено чи ні. Помилка бекенда лишає діалог
 *  відкритим разом із уже введеним. */
export function openEdit(ctx, { title, fields, submit = "Зберегти", wire = null }, build) {
  const root = ctx && ctx.root;
  const pop = root && root.getElementById && root.getElementById("editPop");
  if (!pop) {
    ctx.toast("Правка недоступна", false);
    return Promise.resolve(false);
  }
  const box = pop.querySelector(".box");
  box.innerHTML = `<h4 id="editPopTitle">${title}</h4>
    <form id="editForm">${fields}
      <div class="form-actions">
        <button type="submit">${submit}</button>
        <button type="button" class="quiet" data-editcancel>Скасувати</button>
      </div>
    </form>`;
  const form = box.querySelector("#editForm");
  if (wire) wire(form);
  return new Promise((resolve) => {
    let done = false;
    const finish = (val) => {
      if (done) return;
      done = true;
      pop.removeEventListener("close", onClose);
      if (pop.open) pop.close();
      box.innerHTML = "";
      resolve(val);
    };
    const onClose = () => finish(false);
    pop.addEventListener("close", onClose);
    box.querySelector("[data-editcancel]").addEventListener("click", () => finish(false));
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const req = build(form);
      if (!req) return finish(false);
      const ok = req.requests
        ? await applyAll(ctx, req.requests, req.msg)
        : await apply(ctx, req, req.msg);
      if (ok) finish(true);
      // Інакше нічого не робимо: тост уже сказав, що не так, а введене
      // лишається в полях — саме заради цього apply і повертає результат.
    });
    pop.showModal();
    const first = form.querySelector("input, select, textarea");
    if (first) first.focus();
  });
}
