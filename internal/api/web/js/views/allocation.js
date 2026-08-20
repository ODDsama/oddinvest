// Скільки чого за стратегією: ціль кожного виду в ГРИВНЯХ і те, наскільки
// вона закрита.
//
// ЧИМ ЦЕ ВІДРІЗНЯЄТЬСЯ ВІД «СТРУКТУРИ ЗА ВИДОМ» (views/risk.js). Питання
// різні, і саме тому карток дві. «Структура» стоїть у «Портфелі» й
// відповідає на «ЧИМ Я РИЗИКУЮ»: її одиниця — відсоток капіталу, і читають
// її, щоб побачити перекіс. Ця стоїть у «Що купити» й відповідає на
// «СКІЛЬКИ ЩЕ ДОКЛАСТИ»: її одиниця — гривня, і читають її перед тим, як
// витратити гроші.
//
// Числа в обох з ОДНОГО джерела (rebalance у зведенні), тож розійтися вони
// не можуть за побудовою. Різні в них лише питання, порядок рядків і
// одиниця — а це рівно те, чого не можна злити в одну картку, не
// зіпсувавши обидві.
//
// ЧОГО ТУТ НЕМАЄ І НЕ БУДЕ: РОЗПОДІЛУ ВІЛЬНИХ ГРОШЕЙ. Спокуса написати «з
// 3 000 ₴ вільних — 2 000 у резерв, 1 000 в ОВДП» велика, і виглядало б це
// найкориснішим рядком на екрані. Але пропорція, за якою ділити, ніде не
// задана: користувач ставив ЦІЛІ, а не черговість, і будь-який поділ був би
// правилом, яке застосунок вигадав за нього. Тому числом навпроти рядка
// стоїть «бракує до цілі» — факт, а не рішення, — а скільки з вільних піти
// куди, вирішує людина, дивлячись на порядок рядків.
//
// ПОРЯДОК РЯДКІВ. Резерв завжди перший, і не тому, що «важливіший»: його
// ціль АБСОЛЮТНА (місяці витрат), а не частка капіталу, тож у відсоткових
// пунктах вона не міряється взагалі й із рештою рядків непорівнянна.
// Поставити його в спільне сортування означало б упорядкувати за числом,
// якого в нього немає. Аргумент той самий, з якого buildRebalance
// (state_rebalance.go) навмисно не дає резерву target_pct.
//
// Види між собою — за дефіцитом у ВІДСОТКОВИХ ПУНКТАХ, спадно. Саме за
// в.п., а не за відсотком заповнення, і це не смак: це ТА САМА міра, якою
// planScore у handlers_reinvest.go ранжує поради в списку ПІД цією карткою.
// Два різні порядки на одному екрані означали б, що карта суперечить
// списку, — а тоді котрийсь із них зайвий.
//
// Закриті й перебрані цілі йдуть донизу: докладати туди нема чого.

import { esc, pct, plural, uah2 as fmtUAH } from "../format.js";
import { infoBtn } from "../info.js";
import { needsSetting } from "../components.js";
import { KIND_GROUP } from "../constants.js";
import { routeFor } from "../routes.js";

// Сталий порядок при однаковому дефіциті. Без нього два види з однаковим
// розривом ставали б у порядку, у якому їх поклав бекенд, — а це порядок
// збірки, не сенсу, і мінявся б він мовчки.
const KIND_ORDER = ["bonds", "funds", "deposits", "npf"];

// Резерв — із summary.reserve, а не з рядка ребалансу.
//
// У ребалансі його рядок є, але БЕЗ цілі, і це зроблено навмисно: ціль
// резерву задана в місяцях витрат, і виведена з них частка капіталу давала
// б число, яке нічого не означає (на живих даних — «385% капіталу»). Ціль у
// ГРИВНЯХ при цьому цілком осмислена, і лежить вона саме тут: target_uah,
// uah і gap_uah приходять уже точними.
function reserveRow(s) {
  const r = s.reserve;
  const now = (r && r.uah) || s.reserve_uah || 0;
  const target = (r && r.target_uah) || 0;
  if (!(target > 0)) {
    // Резерв є, цілі немає — рядок довідковий. Сховати його означало б
    // показати неповну картину саме тому, хто ще не задав витрати.
    return now > 0 ? { key: "reserve", title: "Резерв", nowUAH: now, noTarget: true } : null;
  }
  const months = r.target_months || 0;
  return {
    key: "reserve",
    title: "Резерв",
    nowUAH: now,
    targetUAH: target,
    fillPct: (now / target) * 100,
    deficitUAH: r.gap_uah || 0,
    basis: `ціль ${months} ${plural(months, "місяць", "місяці", "місяців")} витрат`,
  };
}

// Рядки видів. Резерв звідси ВИКИДАЄТЬСЯ: він уже прийшов зі свого джерела
// вище, і без цього фільтра стояв би в картці двічі — раз із ціллю в
// гривнях, раз без жодної.
function kindRows(s) {
  const rows = (s.rebalance || [])
    .filter((r) => r.dimension === "kind" && r.key !== "reserve");
  const gapPP = (r) => (r.target_pct || 0) - (r.current_pct || 0);
  const ord = (r) => {
    const i = KIND_ORDER.indexOf(r.key);
    return i < 0 ? KIND_ORDER.length : i;
  };
  const withTarget = rows.filter((r) => r.target_pct > 0).map((r) => ({
    key: r.key,
    title: KIND_GROUP[r.key] || r.key,
    nowUAH: r.current_uah || 0,
    targetUAH: r.target_uah || 0,
    fillPct: r.fill_pct || 0,
    deficitUAH: r.deficit_uah || 0,
    basis: `ціль ${r.target_pct}% капіталу`,
    unitUAH: r.bond_cost_uah || 0,
    feasible: r.feasible !== false,
    row: r,
  }));
  withTarget.sort((a, b) => {
    // Закрите — донизу, і це перший критерій: рядок, у який нема чого
    // докладати, не має стояти над тим, у який є.
    const ca = a.deficitUAH > 0 ? 0 : 1, cb = b.deficitUAH > 0 ? 0 : 1;
    if (ca !== cb) return ca - cb;
    const d = gapPP(b.row) - gapPP(a.row);
    if (Math.abs(d) > 0.01) return d;
    return ord(a) - ord(b);
  });
  const noTarget = rows
    .filter((r) => !(r.target_pct > 0) && (r.current_uah || 0) > 0)
    .map((r) => ({
      key: r.key, title: KIND_GROUP[r.key] || r.key,
      nowUAH: r.current_uah || 0, noTarget: true,
    }));
  return { withTarget, noTarget };
}

// Один рядок. Смужка тим самим кольоровим правилом, що й у «Структурі»:
// перебір — попередження, розрив — інформація, закрито — успіх. Однакове
// питання, розфарбоване по-різному на двох сторінках, читалось би як два
// різні виміри.
function rowHTML(r) {
  if (r.noTarget) {
    return `<div class="mb"><div class="kv">
      <span><b>${esc(r.title)}</b> <span class="muted">цілі немає</span></span>
      <span class="muted">${fmtUAH(r.nowUAH)}</span></div>
      <div class="sub">${r.key === "reserve"
        ? "ціль резерву задається місяцями витрат — «Політика → Резерв»"
        : "показано довідково: частка є, цілі під неї не ставили"}</div></div>`;
  }
  const over = r.nowUAH - r.targetUAH;
  const short = r.deficitUAH > 0;
  const color = over > 0.005 ? "var(--oi-warn)" : short ? "var(--oi-info)" : "var(--oi-ok)";
  const bar = Math.max(0, Math.min(100, r.fillPct));
  const tail = short
    ? `<span class="t-warn">бракує ${fmtUAH(r.deficitUAH)}</span>`
    : over > 0.005
      ? `<span class="t-warn">перебір ${fmtUAH(over)}</span>`
      : `<span class="t-ok">ціль закрита ✅</span>`;
  // Найдешевший вхід — лише там, де він існує І де ще є що добирати. У НПФ
  // його немає взагалі (внести можна будь-яку суму), і нуль там означає саме
  // це, а не «не дізнались»; а під закритою ціллю ціна кроку — просто шум:
  // крок нікуди робити.
  const unit = short && r.unitUAH > 0
    ? (r.feasible
      ? ` · найдешевший вхід ${fmtUAH(r.unitUAH)}`
      : ` · <span class="t-warn">⚠ найдешевший вхід ${fmtUAH(r.unitUAH)} — це більше
          за всю цільову суму</span>`)
    : "";
  return `<div class="mb">
    <div class="kv"><b>${esc(r.title)}</b><b>${pct(r.fillPct, 0)}</b></div>
    <div class="progress mt-xs mb-xs"><span style="--oi-fill:${bar}%;--oi-c:${color}"></span></div>
    <div class="sub">є ${fmtUAH(r.nowUAH)} з ${fmtUAH(r.targetUAH)} · ${esc(r.basis)}
      · ${tail}${unit}</div>
  </div>`;
}

/** Картка «Скільки чого за стратегією» для сторінки «Що купити». */
export function allocationCardHTML(ctx) {
  const s = ctx.summary || {};
  const res = reserveRow(s);
  const { withTarget, noTarget } = kindRows(s);
  const targeted = (res && !res.noTarget ? [res] : []).concat(withTarget);
  if (!targeted.length) {
    return needsSetting(`Скільки чого за стратегією ${infoBtn("allocation")}`,
      "Цілей ще немає, тож і рахувати заповнення нема від чого. Частки за видом "
      + "(ОВДП / фонди / вклади / НПФ) задаються в «Частках і межах», ціль резерву — "
      + "місяцями витрат у «Резерві». Задай хоч одну — і тут з'явиться, скільки в неї "
      + "треба грошей і скільки вже стоїть.",
    routeFor("policy/mix"));
  }

  const rest = (res && res.noTarget ? [res] : []).concat(noTarget);
  const needSum = targeted.reduce((a, r) => a + (r.deficitUAH || 0), 0);
  const free = s.account_uah || 0;
  // Сума цілей за видом — те саме число й те саме формулювання, що в
  // «Структурі»: нерозподілене показуємо, а не розтягуємо до сотні мовчки.
  const targetSum = withTarget.reduce((a, r) => a + ((r.row.target_pct) || 0), 0);

  const money = needSum > 0
    ? `<div class="sub">Бракує разом <b>${fmtUAH(needSum)}</b>; вільних на рахунках ${
      fmtUAH(free)}.${free < needSum
      ? " За раз усе не закриється — порядок рядків і є відповідь, з чого починати."
      : " Вистачає на все одразу; що саме брати — у списку нижче."}</div>`
    : `<div class="sub t-ok">Усі задані цілі закриті. Нові гроші вже нічого не «добирають» —
       вибір у списку нижче лишається за дохідністю.</div>`;

  return `<div class="card">
    <h2 class="h-row">Скільки чого за стратегією ${infoBtn("allocation")}</h2>
    <div class="note">Ціль кожного виду в гривнях і те, скільки в ній уже стоїть.
      Закрите й перебране — донизу, решта за розривом до цілі у ВІДСОТКОВИХ ПУНКТАХ
      капіталу: це той самий порядок, яким ранжований список під карткою, тож вони не
      можуть радити різне. Скільки з вільних грошей піти куди, застосунок не ділить:
      пропорції ти ніде не задавав, і вигадати її за тебе було б порадою, а не
      підрахунком.</div>
    ${targeted.map(rowHTML).join("")}
    ${rest.length ? `<div class="rule-top">${rest.map(rowHTML).join("")}</div>` : ""}
    ${money}
    <div class="sub">Ціль у гривнях рахується від СЬОГОДНІШНЬОГО капіталу: докупиш —
      і вона підросте разом із ним. Ціль резерву так не робить — вона в місяцях витрат,
      тобто абсолютна.${targetSum > 0 && targetSum < 99.5
      ? ` Цілями за видом розподілено ${targetSum.toFixed(0)}% капіталу — решта ${
        (100 - targetSum).toFixed(0)}% лишається на твій розсуд.`
      : targetSum > 100.5
        ? ` Цілі за видом у сумі дають ${targetSum.toFixed(0)}% — більше за капітал,
            тож усі одразу недосяжні.` : ""}
      Ті самі числа у відсотках капіталу — <a class="lnk"
      href="${routeFor("portfolio/structure")}">Портфель → Структура</a>.</div>
  </div>`;
}
