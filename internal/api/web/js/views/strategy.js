// Конфігуратор політики портфеля.
//
// МЕЖА, ЯКУ ТУТ НЕ ПЕРЕХОДИМО. У коді записано «Це інструмент, не порада»
// (handlers_reinvest.go), те саме — в довідці. Тож жоден рядок звідси не
// каже «вам варто тримати 60% ОВДП». Пресет — це ІМЕНОВАНИЙ НАБІР
// налаштувань із чесним підписом, що він оптимізує і чим за це платить;
// питання нижче не питають «чого ви хочете», вони питають про ОБМЕЖЕННЯ,
// які користувач знає про себе сам (коли можуть знадобитись гроші, в чому
// він витрачає). Підбір відповідає лише на «який набір НЕ суперечить
// названим вами обмеженням», і логіка збігу показана поруч із кожним
// рядком, а не захована в число.
//
// Через це тут немає слова «рекомендований» і немає сортування «найкращий
// зверху»: пресети стоять у сталому порядку, а збіг показано підписом.
// Застосувати можна будь-який, навіть той, що суперечить усім відповідям —
// обмеження міг змінитись, і застосунок про це не знає.
//
// Пресети — константи ФРОНТЕНДУ, які заповнюють ті самі поля, що й форми
// нижче. Окремої сутності «стратегія» в бекенді немає навмисно: інакше
// зʼявився б другий, паралельний спосіб задати ті самі числа, і питання
// «яке з них справжнє» не мало б відповіді.

import { esc } from "../format.js";
import { infoBtn } from "../info.js";
import { apply } from "../forms.js";

// Питання про ОБМЕЖЕННЯ, а не про смаки. Кожне має відповідь, яку людина
// знає про себе без жодних знань про ринок, — у цьому й сенс: інакше
// конфігуратор питав би те, заради чого його й відкрили.
const QUESTIONS = [
  {
    id: "when",
    q: "Коли ці гроші можуть знадобитись?",
    opts: [
      ["any", "Будь-коли — обставини різні"],
      ["mid", "Навряд чи раніше ніж за рік-два"],
      ["long", "Не раніше ніж за 3 роки"],
    ],
  },
  {
    id: "spend",
    q: "У чому ви плануєте їх витрачати?",
    opts: [
      ["uah", "У гривні"],
      ["mixed", "Переважно в гривні, але є валютні плани"],
      ["fx", "Значну частину — у валюті"],
    ],
  },
  {
    id: "cushion",
    q: "Чи є запас на чорний день ПОЗА цим портфелем?",
    opts: [
      ["yes", "Так, окремо"],
      ["no", "Ні, він має бути тут"],
    ],
  },
  {
    id: "effort",
    q: "Скільки різних паперів готові вести?",
    opts: [
      ["few", "Якнайменше — стежити ніколи"],
      ["many", "Кілька десятків не проблема"],
    ],
  },
];

// Набори. `gives` — що набір оптимізує, `costs` — чим за це платить.
// Друге поле обовʼязкове й таке саме помітне, як перше: набір без ціни
// читався б як порада, а безкоштовних наборів не буває.
//
// `fits` — за яких відповідей набір НЕ суперечить обмеженню. Відсутнє
// питання = набору байдуже.
const PRESETS = [
  {
    key: "flow",
    name: "Гривневий потік",
    gives: "поточний дохід: купон ОВДП не оподатковується, а гривневі ставки найвищі",
    costs: "усе тримається на курсі — різка девальвація зʼїдає реальну дохідність цілком",
    fits: { when: ["mid", "long"], spend: ["uah", "mixed"] },
    values: {
      usd_target_share_pct: "10", eur_target_share_pct: "",
      target_bonds_pct: "75", target_funds_pct: "10", target_deposits_pct: "15",
      limit_isin_pct: "25", limit_broker_pct: "60", limit_year_pct: "40",
      reserve_target_months: "3",
    },
  },
  {
    key: "fx",
    name: "Валютний захист",
    gives: "збереження купівельної спроможності: валютна частина не залежить від гривні",
    costs: "нижчі номінальні ставки, а вхід дорожчий — доларовий папір коштує $1000, тож набирається довше",
    fits: { spend: ["mixed", "fx"], when: ["mid", "long"] },
    values: {
      usd_target_share_pct: "40", eur_target_share_pct: "10",
      target_bonds_pct: "70", target_funds_pct: "15", target_deposits_pct: "15",
      limit_isin_pct: "25", limit_broker_pct: "50", limit_year_pct: "40",
      reserve_target_months: "6",
    },
  },
  {
    key: "ladder",
    name: "Драбина без турбот",
    gives: "мало операцій: довгі папери, рівномірні роки погашень, рідкі рішення",
    costs: "гроші замкнені надовго, і реагувати на зміну ставок майже нема чим",
    fits: { when: ["long"], effort: ["few"] },
    values: {
      usd_target_share_pct: "25", eur_target_share_pct: "",
      target_bonds_pct: "85", target_funds_pct: "5", target_deposits_pct: "10",
      limit_isin_pct: "35", limit_broker_pct: "60", limit_year_pct: "30",
      reserve_target_months: "3",
    },
  },
  {
    key: "liquid",
    name: "Ліквідний",
    gives: "доступ до грошей: великий резерв і короткі строки, нічого ламати не доведеться",
    costs: "найнижча дохідність із чотирьох — помітна частина капіталу не працює взагалі",
    fits: { when: ["any"], cushion: ["no"] },
    values: {
      usd_target_share_pct: "25", eur_target_share_pct: "",
      target_bonds_pct: "45", target_funds_pct: "20", target_deposits_pct: "15",
      limit_isin_pct: "20", limit_broker_pct: "50", limit_year_pct: "50",
      reserve_target_months: "12",
    },
  },
];

// Людські назви полів — для показу різниці перед записом.
const FIELD_LABEL = {
  usd_target_share_pct: "Цільова частка USD, %",
  eur_target_share_pct: "Цільова частка EUR, %",
  target_bonds_pct: "Ціль ОВДП, %",
  target_funds_pct: "Ціль фондів, %",
  target_deposits_pct: "Ціль вкладів, %",
  limit_isin_pct: "Макс. в одному папері, %",
  limit_broker_pct: "Макс. в одній установі, %",
  limit_year_pct: "Макс. погашень в один рік, %",
  reserve_target_months: "Ціль резерву, місяців",
};

// Відповіді живуть у localStorage, а не в налаштуваннях сервера: це не
// політика портфеля, а чернетка підбору. Записати в бекенд означало б
// завести десяте налаштування, яке ні на що не впливає.
const ANSWERS_KEY = "oddinvest.strategyAnswers";
function readAnswers() {
  try { return JSON.parse(localStorage.getItem(ANSWERS_KEY) || "{}") || {}; }
  catch (_) { return {}; }
}
function writeAnswers(a) {
  try { localStorage.setItem(ANSWERS_KEY, JSON.stringify(a)); } catch (_) { /* приватний режим */ }
}

// Збіг набору з названими обмеженнями. Повертає РОЗКЛАД, а не оцінку:
// саме він і показується поруч, бо число без пояснення — це та сама
// порада, лише в іншій обгортці.
function matchOf(preset, answers) {
  const ok = [], clash = [];
  for (const q of QUESTIONS) {
    const a = answers[q.id];
    const allowed = preset.fits[q.id];
    if (!a || !allowed) continue; // не відповіли або набору байдуже
    const label = (q.opts.find(([v]) => v === a) || [, a])[1];
    (allowed.includes(a) ? ok : clash).push({ q: q.q, a: label });
  }
  return { ok, clash };
}

export function strategyCardHTML(current) {
  const answers = readAnswers();
  const answered = QUESTIONS.filter((q) => answers[q.id]).length;

  const quiz = QUESTIONS.map((q) => `<div style="margin-bottom:10px">
    <div style="margin-bottom:4px">${esc(q.q)}</div>
    <div style="display:flex;gap:6px;flex-wrap:wrap">
      ${q.opts.map(([v, t]) => `<button type="button" class="sm${
        answers[q.id] === v ? "" : " quiet"}" data-answer="${q.id}:${v}">${esc(t)}</button>`).join("")}
    </div></div>`).join("");

  const cards = PRESETS.map((p) => {
    const { ok, clash } = matchOf(p, answers);
    // Різниця з тим, що стоїть зараз. Порожнє поточне значення — це
    // «не задано», і показуємо це словом, а не порожнім місцем.
    const diff = Object.entries(p.values).filter(([k, v]) => (current[k] || "") !== v);
    const verdict = !answered
      ? `<span class="muted">обмеження ще не названі</span>`
      : clash.length
        ? `<span style="color:var(--oi-warn)">суперечить ${clash.length} з названих обмежень</span>`
        : ok.length
          ? `<span style="color:var(--oi-ok)">не суперечить жодному з ${ok.length} названих</span>`
          : `<span class="muted">до названих обмежень байдужий</span>`;
    const why = [...clash.map((c) => `<div class="sub-xs" style="color:var(--oi-warn)">✕ ${esc(c.q)} → «${esc(c.a)}»</div>`),
      ...ok.map((c) => `<div class="sub-xs">✓ ${esc(c.q)} → «${esc(c.a)}»</div>`)].join("");
    return `<div style="border-top:1px solid var(--oi-border);padding-top:10px;margin-top:10px">
      <div style="display:flex;justify-content:space-between;gap:8px;flex-wrap:wrap">
        <b>${esc(p.name)}</b>${verdict}
      </div>
      <div class="sub">Дає: ${esc(p.gives)}</div>
      <div class="sub">Платить: ${esc(p.costs)}</div>
      ${why}
      <div style="margin-top:6px">
        ${diff.length
          ? `<button class="sm" data-preset="${p.key}">Показати, що зміниться (${diff.length})</button>`
          : `<span class="sub">усе вже стоїть саме так</span>`}
      </div>
      <div data-preview="${p.key}"></div>
    </div>`;
  }).join("");

  return `<div class="card">
    <h2 class="h-row">Готові набори налаштувань ${infoBtn("strategy")}</h2>
    <div class="muted" style="margin-bottom:12px">Це не поради, а іменовані комбінації тих самих
      цілей і лімітів, що у формах нижче — з підписом, що кожна дає і чим за це платить. Питання
      нижче ні на що не впливають самі по собі: вони лише позначають, який набір не суперечить
      обмеженням, які ви назвали. Застосувати можна будь-який, і будь-яке число потім змінити
      руками.</div>
    ${quiz}
    ${cards}
  </div>`;
}

export function wireStrategy(ctx, main, current) {
  main.querySelectorAll("[data-answer]").forEach((b) =>
    b.addEventListener("click", () => {
      const [id, v] = b.dataset.answer.split(":");
      const a = readAnswers();
      // Повторний клік по обраному знімає відповідь: обмеження може
      // виявитись неактуальним, і мовчазної «жодної відповіді» бути не
      // повинно — лише явна.
      if (a[id] === v) delete a[id]; else a[id] = v;
      writeAnswers(a);
      ctx.reload();
    }));

  main.querySelectorAll("[data-preset]").forEach((b) =>
    b.addEventListener("click", () => {
      const p = PRESETS.find((x) => x.key === b.dataset.preset);
      const box = main.querySelector(`[data-preview="${p.key}"]`);
      if (!p || !box) return;
      // Два кроки навмисно, як в імпорті виписки: спершу показати, що
      // саме буде переписано, і лише потім писати. Набір чіпає девʼять
      // налаштувань одразу, і «застосував і не помітив, що затер валютну
      // ціль» — помилка, яка знаходиться не одразу.
      const diff = Object.entries(p.values).filter(([k, v]) => (current[k] || "") !== v);
      box.innerHTML = `<div class="table-scroll" style="margin-top:8px"><table><thead><tr>
          <th>Налаштування</th><th class="num">Зараз</th><th class="num">Стане</th></tr></thead><tbody>
          ${diff.map(([k, v]) => `<tr><td>${esc(FIELD_LABEL[k] || k)}</td>
            <td class="num muted">${current[k] ? esc(current[k]) : "не задано"}</td>
            <td class="num"><b>${v ? esc(v) : "прибрати"}</b></td></tr>`).join("")}
        </tbody></table></div>
        <button class="sm" data-apply="${p.key}" style="margin-top:8px">Застосувати «${esc(p.name)}»</button>`;
      box.querySelector("[data-apply]").addEventListener("click", async (e) => {
        e.target.disabled = true;
        await apply(ctx, { method: "PUT", path: "settings", body: p.values },
          `Застосовано «${p.name}»`);
      });
    }));
}
