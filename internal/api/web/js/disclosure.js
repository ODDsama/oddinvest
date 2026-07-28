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
    if (folds[d.dataset.fold]) d.open = true;
    // Слухач на кожному, а не делегований: подія toggle не спливає.
    d.addEventListener("toggle", () => {
      const cur = readFolds();
      if (d.open) cur[d.dataset.fold] = true; else delete cur[d.dataset.fold];
      try { localStorage.setItem(FOLDS_KEY, JSON.stringify(cur)); } catch (_) {}
    });
  });
}


// disclosure — згорнута секція з підписом. hint — сірий текст праворуч
// від заголовка, коли треба сказати, коли цим користуються.
export function disclosure(key, title, body, hint = "") {
  return `<details class="disclosure" data-fold="${key}">
    <summary>${title}${hint ? `<span class="hint">${hint}</span>` : ""}</summary>
    <div class="disclosure-body">${body}</div>
  </details>`;
}


