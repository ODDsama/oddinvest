// Три числа про місячний внесок — і один словник на весь застосунок.
//
// Доти одне число `ContribM` розходилось документом пʼятьма копіями
// (month_target_uah, settings.monthly_target_uah, forecast.contrib_plan,
// rows[].contrib_monthly, rows[].required_monthly реалістичного рядка), а
// поруч стояли plan_provides_uah і actual_monthly_uah. Сім чисел, три
// різні сенси й жодного спільного слова — тож на одному екрані «До цілі
// бракує ще 70 270» і «Скільки треба вносити: 70 270» читались як два
// різні показники, хоч це одне значення.
//
// Тут вони дістають імена, і саме звідси їх бере кожен розділ. Модуль без
// DOM: web-imports-check.mjs справді імпортує кожен файл.

/** Канонічні підписи. Міняти текст — тільки тут: у шести місцях, що їх
 *  читають, він мусить збігатися до слова, інакше «наскрізно» знову стане
 *  збігом, а не властивістю. */
export const CONTRIB = {
  need: { label: "Треба", hint: "щоб вийти на мету — уся сума, з нуля" },
  plan: { label: "План", hint: "скільки джерела доходу дають щомісяця" },
  actual: { label: "Факт", hint: "виміряний темп поповнень" },
  // «Треба мінус те, що з плану ДОХОДИТЬ до мети» — а не «мінус план».
  // Підпис казав друге, і це було неправдою щоразу, коли план спершу
  // наповнює подушку й цілі: бракувало майже всієї суми при чималому плані.
  gap: { label: "Бракує", hint: "треба мінус те, що з плану доходить до мети" },
};

/** Число або null — ніколи 0 замість «немає». Нуль тут буває законним
 *  значенням («план виводить на мету із запасом» — це gap === 0), і
 *  плутати його з відсутністю даних не можна. */
const num = (v) => (typeof v === "number" && isFinite(v) && v > 0 ? v : null);

/** Три показники внеску з документа зведення.
 *
 *  Кожне числове поле — number | null. Викликачі гілкуються на has*, а НЕ
 *  на істинність числа: gap === 0 означає «вистачає з запасом» і мусить
 *  малюватись, а не зникати.
 *
 *  → { need, needLo, needHi, plan, actual, actualMonths, gap,
 *      goal, date, months, hasGoal, hasPlan, hasActual } */
export function contribTriad(ctx) {
  const s = (ctx && ctx.summary) || {};
  const f = s.forecast || {};
  const rows = (f.rows || []).filter((r) => r.key !== "actual");
  const real = rows.find((r) => r.key === "realistic") || {};

  const plan = num(s.plan_provides_uah);
  const actual = num(s.actual_monthly_uah);

  // ТРЕБА — сума з нуля, план не віднімається.
  //
  // Запасний шлях для старого бекенда не має брехати. Якщо поля немає,
  // required_monthly годиться ЛИШЕ коли плану теж немає: тоді вони
  // тотожні за побудовою. Є план — краще не показати нічого, ніж
  // підставити під написом ТРЕБА нестачу: саме ця підміна й породила всю
  // цю роботу.
  const totals = rows.map((r) => r.required_total_monthly).filter((v) => num(v) != null);
  let need = num(real.required_total_monthly);
  let needLo = null, needHi = null;
  if (totals.length) {
    needLo = Math.min(...totals);
    needHi = Math.max(...totals);
  } else if (plan == null) {
    need = num(real.required_monthly);
    const legacy = rows.map((r) => r.required_monthly).filter((v) => num(v) != null);
    if (legacy.length) {
      needLo = Math.min(...legacy);
      needHi = Math.max(...legacy);
    }
  }

  // БРАКУЄ — похідне, і воно НЕ num(): нуль тут змістовний.
  const rawGap = f.contrib_plan;
  const gap = typeof rawGap === "number" && isFinite(rawGap) && rawGap >= 0 ? rawGap : null;

  // ДОХОДИТЬ ДО МЕТИ — скільки з плану лишається капіталу після подушки,
  // цілей і боргів (forecast.plan_to_goal, рахує бекенд). Нуль змістовний:
  // «план є, але до мети з нього не доходить нічого», — тож не num().
  const rawToGoal = f.plan_to_goal;
  const toGoal = typeof rawToGoal === "number" && isFinite(rawToGoal) && rawToGoal > 0 ? rawToGoal : 0;

  return {
    need, needLo, needHi, plan, actual, gap, toGoal,
    actualMonths: num(s.actual_months),
    goal: num(f.goal_amount),
    date: f.date || "",
    months: num(f.months),
    hasGoal: need != null,
    hasPlan: plan != null,
    hasActual: actual != null,
  };
}

/** Рядок «скільки з плану доходить до мети» — текстом, без розмітки.
 *
 *  Потрібен там, де ПЛАН і БРАКУЄ стоять поруч: без нього «план 28 154,
 *  бракує 104 697 до потрібних 104 985» читається як помилка розрахунку,
 *  хоч обидва числа правильні. Різницю зʼїдають подушка, цілі й
 *  обовʼязкове за боргами — прогноз наповнює їх першими.
 *
 *  Порожній рядок — пояснювати нема чого: плану чи мети немає, нестачі
 *  немає, або до мети доходить увесь план. fmt — форматер грошей
 *  викликача: модуль без DOM і без власного форматування. */
export function toGoalText(t, fmt) {
  if (!t.hasPlan || !t.hasGoal || !(t.gap > 0) || t.plan - t.toGoal < 1) return "";
  const first = "подушка, цілі й обовʼязкове за боргами наповнюються першими";
  return t.toGoal > 0
    ? `з плану до мети капіталу доходить ${fmt(t.toGoal)}/міс — ${first}`
    : `з плану до мети капіталу не доходить нічого — ${first}`;
}

/** Яку частку від ПОТРІБНОГО покриває v, %. null — коли ділити нема на що
 *  або нема чого ділити.
 *
 *  Єдиний захист від ділення на нуль у застосунку: смуга прогресу
 *  малюється лише тоді, коли тут не null.
 *
 *  Стелі 100% тут більше немає, і це не послаблення. Стеля стояла, щоб
 *  смуга не вилазила за межі, тобто була вимогою МАЛЮВАННЯ, записаною в
 *  арифметиці, — а «більше, ніж треба» доводилось говорити словами поруч.
 *  Тепер обрізає progressBar() (components.js), і надлишок він не ховає, а
 *  показує сегментом. Рівно те саме про covered_pct сказано на бекенді
 *  (state.go:984-986): смугу обрізає той, хто малює.
 *
 *  Наслідок для викликачів: значення БУВАЄ більшим за 100, і надрукований
 *  відсоток тепер це показує. */
export function shareOfNeed(v, need) {
  if (!(need > 0) || v == null || !isFinite(v)) return null;
  return (v / need) * 100;
}
