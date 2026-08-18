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
  // Два питання про ЗАМКИ. Обидва тієї самої природи, що й чотири вище:
  // відповідь людина знає про себе сама, без жодних знань про ринок.
  //
  // Чого тут НЕМА і не буде. Питання про утриманий ПДФО напрошується (без
  // нього податкова знижка на внески в НПФ не рахується), але це не
  // обмеження, а число з довідки — йому місце у формі, і воно там уже є.
  // Питання «чи вносите регулярно» напрошується так само й так само не є
  // обмеженням: темп поповнень застосунок ВИМІРЮЄ сам, і питати про
  // виміряне означало б дозволити відповіді розійтися з фактом.
  {
    id: "lock50",
    q: "Чи готові замкнути частину грошей до 50 років?",
    opts: [
      ["no", "Ні — має лишатись доступ"],
      ["some", "Невелику частину — так"],
    ],
  },
  {
    id: "exit",
    q: "Фонд зі строком: досидите до кінця чи можете вийти раніше?",
    opts: [
      ["hold", "Досиджу — до тієї дати гроші не потрібні"],
      ["early", "Можу вийти раніше"],
    ],
  },
];

// Набори. `gives` — що набір оптимізує, `costs` — чим за це платить.
// Друге поле обовʼязкове й таке саме помітне, як перше: набір без ціни
// читався б як порада, а безкоштовних наборів не буває.
//
// `fits` — за яких відповідей набір НЕ суперечить обмеженню. Відсутнє
// питання = набору байдуже. Питання згадується лише там, де якась відповідь
// дає СПРАВЖНЮ суперечність: `fits` про суперечність, а не про перевагу, і
// «ліквідний» із `lock50: ["no"]` вигадав би конфлікт тому, хто відповів
// «так».
//
// Усі набори пишуть ОДИН І ТОЙ САМИЙ перелік із дванадцяти ключів, і це не
// охайність. Порожнє значення в PUT /settings означає «ПРИБРАТИ», тож
// набір, який мовчить про ціль НПФ, лишив би її від попереднього — і сума
// часток поїхала б без жодного сліду в UI. Тест у server_test.go тримає цю
// рівність переліків.
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
      target_npf_pct: "",
      limit_isin_pct: "25", limit_broker_pct: "60", limit_year_pct: "40",
      reserve_target_months: "3", reserve_fill_share_pct: "",
      reinvest_rank: "rate",
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
      target_npf_pct: "",
      limit_isin_pct: "25", limit_broker_pct: "50", limit_year_pct: "40",
      reserve_target_months: "6", reserve_fill_share_pct: "",
      reinvest_rank: "plan",
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
      target_npf_pct: "",
      limit_isin_pct: "35", limit_broker_pct: "60", limit_year_pct: "30",
      reserve_target_months: "3", reserve_fill_share_pct: "",
      reinvest_rank: "ladder",
    },
  },
  {
    key: "liquid",
    name: "Ліквідний",
    gives: "доступ до грошей: великий резерв і короткі строки, нічого ламати не доведеться",
    costs: "найнижча дохідність із восьми — помітна частина капіталу не працює взагалі",
    fits: { when: ["any"], cushion: ["no"] },
    values: {
      usd_target_share_pct: "25", eur_target_share_pct: "",
      target_bonds_pct: "45", target_funds_pct: "20", target_deposits_pct: "15",
      target_npf_pct: "",
      limit_isin_pct: "20", limit_broker_pct: "50", limit_year_pct: "50",
      reserve_target_months: "12", reserve_fill_share_pct: "25",
      reinvest_rank: "short",
    },
  },
  {
    key: "cushion",
    name: "Спершу подушка",
    gives: "запас на чорний день збирається сам: доки його бракує, частина вільних грошей іде туди, а не в папір",
    costs: "поки подушка набирається, портфель росте повільніше — відкладене не заробляє нічого",
    fits: { cushion: ["no"], when: ["any", "mid"] },
    values: {
      usd_target_share_pct: "20", eur_target_share_pct: "",
      target_bonds_pct: "55", target_funds_pct: "10", target_deposits_pct: "15",
      target_npf_pct: "",
      limit_isin_pct: "25", limit_broker_pct: "50", limit_year_pct: "40",
      reserve_target_months: "6", reserve_fill_share_pct: "40",
      reinvest_rank: "plan",
    },
  },
  {
    key: "pension",
    name: "Пенсійний якір",
    gives: "найбільший дохід першого року з усіх наборів, і не з ринку: внесок у НПФ повертає 18% ПДФО",
    costs: "гроші замкнені до 50 років, вийти не можна взагалі, а знижка працює лише проти офіційної зарплати",
    fits: { when: ["long"], lock50: ["some"], effort: ["few"] },
    values: {
      usd_target_share_pct: "10", eur_target_share_pct: "",
      target_bonds_pct: "45", target_funds_pct: "5", target_deposits_pct: "10",
      target_npf_pct: "25",
      limit_isin_pct: "30", limit_broker_pct: "60", limit_year_pct: "40",
      reserve_target_months: "3", reserve_fill_share_pct: "",
      reinvest_rank: "plan",
    },
  },
  {
    key: "term",
    name: "Фонд зі строком",
    gives: "обіцяна фондом дохідність вища за купон, а дата закриття й вікно купівлі відомі наперед",
    costs: "вийти до закриття можна лише з податком 23% замість 14%, а колись його перестануть продавати зовсім",
    fits: { exit: ["hold"], when: ["mid", "long"], effort: ["few"] },
    values: {
      usd_target_share_pct: "20", eur_target_share_pct: "",
      target_bonds_pct: "40", target_funds_pct: "40", target_deposits_pct: "10",
      target_npf_pct: "",
      // Ліміт на один папір дорівнює цілі фондів навмисно: сертифікат
      // рахується в концентрації окремим емітентом, і стеля 25 при цілі 40
      // означала б набір, який порушує сам себе з першого дня.
      limit_isin_pct: "40", limit_broker_pct: "60", limit_year_pct: "40",
      reserve_target_months: "3", reserve_fill_share_pct: "",
      reinvest_rank: "rate",
    },
  },
  {
    key: "bank",
    name: "Тільки банк",
    gives: "нічого не треба відкривати: вклади в банку, який уже є, без брокерського рахунку й довідника паперів",
    costs: "відсотки оподатковуються (19.5%), ставки нижчі за купон ОВДП, і майже все лежить в одній установі",
    fits: { effort: ["few"], when: ["any", "mid"] },
    values: {
      usd_target_share_pct: "20", eur_target_share_pct: "",
      target_bonds_pct: "", target_funds_pct: "", target_deposits_pct: "75",
      target_npf_pct: "",
      // Паперів немає — вимір концентрації по ISIN нічого не міряє. А от
      // ліміт на установу високий свідомо: набір і Є ставка на один банк,
      // і ліміт мусить це визнавати, а не кричати щодня. Ціна названа в
      // рядку «Платить», а не схована в число.
      limit_isin_pct: "", limit_broker_pct: "80", limit_year_pct: "40",
      reserve_target_months: "6", reserve_fill_share_pct: "30",
      reinvest_rank: "rate",
    },
  },
];

// Режими ранжування «Що купити». Живуть ТУТ, бо споживачів стало двоє:
// випадайка у формі «Інструменти реінвесту» й таблиця різниці нижче. Два
// списки тих самих чотирьох пар розійшлись би тихо.
export const RANKS = [
  ["plan", "під план"], ["rate", "за дохідністю"],
  ["short", "короткі"], ["ladder", "драбина"],
];

// Людські назви полів — для показу різниці перед записом.
const FIELD_LABEL = {
  usd_target_share_pct: "Цільова частка USD, %",
  eur_target_share_pct: "Цільова частка EUR, %",
  target_bonds_pct: "Ціль ОВДП, %",
  target_funds_pct: "Ціль фондів, %",
  target_deposits_pct: "Ціль вкладів, %",
  target_npf_pct: "Ціль НПФ, %",
  limit_isin_pct: "Макс. в одному папері, %",
  limit_broker_pct: "Макс. в одній установі, %",
  limit_year_pct: "Макс. погашень в один рік, %",
  reserve_target_months: "Ціль резерву, місяців",
  reserve_fill_share_pct: "З вільних у резерв, %",
  reinvest_rank: "Порядок у «Що купити»",
};

// Значення так, як його читає людина. Єдиний ключ із КОДОМ — reinvest_rank:
// «plan» у таблиці різниці читалося б як код, якого користувач ніде не
// бачив. Решта — числа, які кажуть самі за себе.
//
// Саме порівняння diff лишається на СИРИХ значеннях: інакше збережений
// «plan» не збігся б із набірним.
const shown = (k, v) => (k === "reinvest_rank"
  ? (RANKS.find(([code]) => code === v) || [, v])[1]
  : v);

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

  const quiz = QUESTIONS.map((q) => `<div class="mb">
    <div class="mb-xs">${esc(q.q)}</div>
    <div class="row-h">
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
        ? `<span class="t-warn">суперечить ${clash.length} з названих обмежень</span>`
        : ok.length
          ? `<span class="t-ok">не суперечить жодному з ${ok.length} названих</span>`
          : `<span class="muted">до названих обмежень байдужий</span>`;
    const why = [...clash.map((c) => `<div class="sub-xs t-warn">✕ ${esc(c.q)} → «${esc(c.a)}»</div>`),
      ...ok.map((c) => `<div class="sub-xs">✓ ${esc(c.q)} → «${esc(c.a)}»</div>`)].join("");
    return `<div class="rule-top">
      <div class="kv wrap">
        <b>${esc(p.name)}</b>${verdict}
      </div>
      <div class="sub">Дає: ${esc(p.gives)}</div>
      <div class="sub">Платить: ${esc(p.costs)}</div>
      ${why}
      <div class="mt-sm">
        ${diff.length
          ? `<button class="sm" data-preset="${p.key}">Показати, що зміниться (${diff.length})</button>`
          : `<span class="sub">усе вже стоїть саме так</span>`}
      </div>
      <div data-preview="${p.key}"></div>
    </div>`;
  }).join("");

  return `<div class="card">
    <h2 class="h-row">Готові набори налаштувань ${infoBtn("strategy")}</h2>
    <div class="muted mb">Це не поради, а іменовані комбінації тих самих
      цілей, лімітів і правил, що у формах «Політики» — з підписом, що кожна дає і чим за це
      платить. Питання нижче ні на що не впливають самі по собі: вони лише позначають, який набір
      не суперечить обмеженням, які ви назвали. Застосувати можна будь-який, і будь-яке число
      потім змінити руками.</div>
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
      // саме буде переписано, і лише потім писати. Набір чіпає дванадцять
      // налаштувань одразу, і «застосував і не помітив, що затер валютну
      // ціль» — помилка, яка знаходиться не одразу.
      //
      // «Прибрати» в третьому стовпчику — не окраса: порожнє значення
      // справді СТИРАЄ налаштування, і саме тому перехід між наборами не
      // лишає позаду чужого числа.
      const diff = Object.entries(p.values).filter(([k, v]) => (current[k] || "") !== v);
      box.innerHTML = `<div class="table-scroll mt-sm"><table><thead><tr>
          <th>Налаштування</th><th class="num">Зараз</th><th class="num">Стане</th></tr></thead><tbody>
          ${diff.map(([k, v]) => `<tr><td>${esc(FIELD_LABEL[k] || k)}</td>
            <td class="num muted">${current[k] ? esc(shown(k, current[k])) : "не задано"}</td>
            <td class="num"><b>${v ? esc(shown(k, v)) : "прибрати"}</b></td></tr>`).join("")}
        </tbody></table></div>
        <button class="sm mt-sm" data-apply="${p.key}">Застосувати «${esc(p.name)}»</button>`;
      box.querySelector("[data-apply]").addEventListener("click", async (e) => {
        e.target.disabled = true;
        await apply(ctx, { method: "PUT", path: "settings", body: p.values },
          `Застосовано «${p.name}»`);
      });
    }));
}
