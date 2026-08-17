// Розділ «Гроші» — де вони лежать і звідки взялись. Три сторінки.
//
// Баланси по брокерах (роздільні: гривня в одного не купує папір в
// іншого), рухи за період і податок за рік.
//
// Форм тут немає ЖОДНОЇ, і це головна зміна розділу. Доти «Гроші» несли
// і журнал, і чотири форми, і імпорт виписки — тобто відповідали і на «де
// мої гроші», і на «чим це записати». Друге питання забрав розділ
// «Записати»: воно одне на весь застосунок, а місць для нього було чотири.
//
// Резерв поїхав в «Активи»: він частина капіталу, а не рух рахунку, і
// питання «на скільки місяців вистачить» стоїть поруч із рештою того, що
// я маю, а не поруч із конвертаціями.

import { wireDisclosures } from "../disclosure.js";
import { onDelete } from "../forms.js";
import {
  walletHTML, brokerBalancesHTML, movesHTML, flowHTML, taxHTML, taxYear,
} from "./money-cards.js";

/** Скільки і де: гаманець по валютах і рахунки по брокерах. */
export async function balances(ctx, main) {
  main.innerHTML = `
    ${walletHTML(ctx)}
    ${brokerBalancesHTML(ctx)}`;
}

/** Куди воно рухалось: потік за період і журнал рухів. */
export async function flows(ctx, main) {
  const [deposits, conversions, flow] = await Promise.all([
    ctx.soft("deposits", []),
    ctx.soft("conversions", []),
    ctx.soft("cashflow", null),
  ]);
  main.innerHTML = `
    ${flowHTML(flow)}
    ${movesHTML(deposits, conversions)}`;
  onDelete(ctx, main, "[data-deldep]", (b) => ({
    path: "deposits/" + b.dataset.deldep, msg: "Рух видалено",
  }));
  onDelete(ctx, main, "[data-delconv]", (b) => ({
    path: "conversions/" + b.dataset.delconv, msg: "Конвертацію видалено",
  }));
  wireDisclosures(main);
}

/** Скільки з доходу забрала держава — грошима, а не ставкою.
 *
 *  Власна сторінка, а не картка в хвості рухів: сюди приходять раз на рік
 *  і цілеспрямовано, з декларацією перед очима. */
export async function tax(ctx, main) {
  const x = await ctx.soft("tax?year=" + taxYear(), null);
  main.innerHTML = taxHTML(x);

  main.querySelector("[data-tax-year]")?.addEventListener("change", (e) => {
    try { localStorage.setItem("oddinvest.taxYear", e.target.value); } catch (_) { /* приватний режим */ }
    ctx.reload();
  });
  // Вивантаження — тим самим шляхом, що й бекап у «Налаштуваннях»:
  // сирий запит через транспорт, далі blob у файл.
  main.querySelector("[data-tax-csv]")?.addEventListener("click", async (e) => {
    const y = e.currentTarget.dataset.taxCsv;
    try {
      const resp = await ctx.store.raw("export/csv?year=" + y);
      if (!resp.ok) throw new Error(await resp.text());
      const url = URL.createObjectURL(await resp.blob());
      const a = document.createElement("a");
      a.href = url;
      a.download = `oddinvest-${y}.csv`;
      a.click();
      URL.revokeObjectURL(url);
      ctx.toast(`Звіт за ${y} завантажено`);
    } catch (err) { ctx.toast(String(err.message || err), false); }
  });
  wireDisclosures(main);
}
