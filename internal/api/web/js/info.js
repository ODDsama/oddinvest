// Пояснення «як це читати» — по одному на кожен нетривіальний блок.
//
// Тут лише кнопка й попап. Самі тексти — в info-text.js і довантажуються
// на перший клік (довід — у шапці того файлу).

export const infoBtn = (k) =>
  `<button class="info" data-info="${k}" aria-label="Як це читати" title="Як це читати">i</button>`;

/** Делегований обробник кнопок «i». root — shadowRoot компонента, але
 *  функція не залежить від того: document і shadowRoot однаково вміють
 *  getElementById і addEventListener, тож перевіряти тут нема чого.
 *
 *  Очікує в root елемент `<dialog class="infopop" id="infoPop"><div class="box"></div></dialog>`.
 *
 *  Нативний <dialog> замість <div> із класом .show дає задарма чотири
 *  речі, яких не було жодної: пастку фокуса, закриття на Escape,
 *  повернення фокуса на ту саму кнопку «i» після закриття і власний
 *  верхній шар — тобто попап більше не залежить від z-index сусідів.
 *  Усе це працює й усередині shadow root. Коду при цьому стало менше. */
export function bindInfo(root) {
  // Escape — власним обробником, хоч <dialog> і зобов'язаний закриватись
  // сам. Перевірено вживу: keydown до сторінки доходить, а подія `cancel`
  // не настає й попап лишається відкритим. Причина, найпевніше, у тому,
  // що діалог живе всередині shadow root; але з'ясовувати її дорожче,
  // ніж написати три рядки, після яких поведінка перестає залежати від
  // особливостей рушія взагалі.
  //
  // dialog[open], а не жорстко #infoPop: той самий обробник закриває й
  // #confirmPop (forms.js) — діалог у застосунку рівно один відкритий
  // одночасно, тож який саме відкритий, шукати нема потреби.
  root.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    const pop = root.querySelector("dialog[open]");
    // data-sticky — діалог, який Escape не закриває: форма першого пароля
    // (довід — при ній самій, app.js). preventDefault лишається: інакше
    // Escape дійшов би до рідного закриття, яке ми й перехоплюємо.
    if (!pop) return;
    e.preventDefault();
    if (pop.hasAttribute("data-sticky")) return;
    pop.close();
  });

  root.addEventListener("click", (e) => {
    const pop = root.getElementById("infoPop");
    if (!pop) return;
    const btn = e.target.closest && e.target.closest("[data-info]");
    if (btn) {
      const show = (title, body) => {
        pop.querySelector(".box").innerHTML =
          `<button class="x" data-closeinfo aria-label="Закрити">×</button>` +
          `<h4 id="infoPopTitle">${title}</h4>${body}`;
        if (!pop.open) pop.showModal();
      };
      // import() браузер кешує сам: другий клік дістає вже розібраний
      // модуль. Невдача (мережа зникла між відкриттям і кліком) — вголос,
      // у тому самому попапі: кнопка, що мовчки нічого не робить, гірша.
      import("./info-text.js").then(({ INFO }) => {
        const entry = INFO[btn.dataset.info];
        if (!entry) return;
        // Порожній рядок у тексті = новий абзац. Довгі пояснення суцільною
        // стіною не читаються, а розбивати їх розміткою в самому словнику
        // означало б тримати HTML там, де має бути текст.
        show(entry[0], entry[1].split("\n\n").map((t) => `<p>${t}</p>`).join(""));
      }, () => show("Пояснення не завантажилось", "<p>Перевір зʼєднання й натисни «i» ще раз.</p>"));
      return;
    }
    if (e.target.closest && e.target.closest("[data-closeinfo]")) {
      pop.close();
      return;
    }
    // Клік по підкладці. У <dialog> ::backdrop — частина самого елемента,
    // тож подія приходить на нього; лишається перевірити, що влучили
    // повз коробку з текстом, а не в неї.
    if (e.target === pop) {
      const r = pop.getBoundingClientRect();
      const out = e.clientX < r.left || e.clientX > r.right
        || e.clientY < r.top || e.clientY > r.bottom;
      if (out) pop.close();
    }
  });
}
