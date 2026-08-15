// Згортання секцій: <details> плюс памʼять про те, що було розгорнуто.
//
// Стан живе в localStorage, а не в DOM, бо ctx.reload() перемальовує розділ
// цілком — без цього кожен запис згортав би те, що ти щойно відкрив.




// Стан згорнутих секцій переживає перезавантаження — за прикладом
// перемикача ₴/$ у прогнозі. Розкривати ті самі три секції щоразу
// заново дратує більше, ніж саме згортання допомагає.
const FOLDS_KEY = "oddinvest.folds";

function readFolds() {
  try { return JSON.parse(localStorage.getItem(FOLDS_KEY) || "{}") || {}; }
  catch (_) { return {}; }
}

export function wireDisclosures(main) {
  const folds = readFolds();
  main.querySelectorAll("[data-fold]").forEach((d) => {
    // Записане значення перекриває розмітку — В ОБИДВА боки. Доти пам'ять
    // уміла тільки відкривати, і поки всі секції за замовчуванням були
    // згорнуті, різниці не було. З появою секцій, розгорнутих одразу
    // (section({open:true})), вона з'явилась: згорнуту людиною секцію
    // наступний рендер відкривав би назад, бо `open` стоїть у розмітці.
    const saved = folds[d.dataset.fold];
    if (saved !== undefined) d.open = saved;
    // Слухач на кожному, а не делегований: подія toggle не спливає.
    d.addEventListener("toggle", () => {
      const cur = readFolds();
      cur[d.dataset.fold] = d.open;
      try { localStorage.setItem(FOLDS_KEY, JSON.stringify(cur)); } catch (_) {}
    });
  });
}


// disclosure — згорнута секція з підписом. hint — сірий текст праворуч
// від заголовка, коли треба сказати, коли цим користуються.
//
// open розкриває її одразу — для випадку, коли всередині вже щось є й
// сховати це означало б збрехати (форма правки з заповненими полями).
// Пам'ять розкриття (wireDisclosures) поверх цього все одно чинна там, де
// вона взагалі підключена.
export function disclosure(key, title, body, hint = "", open = false) {
  return `<details class="disclosure" data-fold="${key}"${open ? " open" : ""}>
    <summary>${title}${hint ? `<span class="hint">${hint}</span>` : ""}</summary>
    <div class="disclosure-body">${body}</div>
  </details>`;
}


// section — той самий механізм, лише крупнішим зерном: не картка
// всередині розділу, а ГРУПА карток під спільним питанням.
//
// Завелось це через «Портфель»: шістнадцять карток підряд, і таблиця
// позицій — те, заради чого туди й заходять, — дев'ята. Розбити вкладку
// на дві заборонено (шоста вкладка це відповідь на розділ, який не
// тримає своє питання), та й не треба: питання в розділу одне, просто
// відповідей на нього шістнадцять. Іменована група каже, яка відповідь
// на що, і дозволяє згорнути ті, по які приходять раз на місяць.
//
// Жодної нової машинерії стану: це той самий <details data-fold> із
// пам'яттю в localStorage, і wireDisclosures підхоплює його як є.
export function section(key, title, body, { open = false, hint = "" } = {}) {
  return `<section class="sect">
    <details class="disclosure sect-d" data-fold="sect.${key}"${open ? " open" : ""}>
      <summary><h2 class="sect-t">${title}</h2>${
        hint ? `<span class="hint">${hint}</span>` : ""}</summary>
      <div>${body}</div>
    </details>
  </section>`;
}


