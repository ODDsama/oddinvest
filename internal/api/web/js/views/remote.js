// «Налаштування → Доступ ззовні»: замок і двері в одному місці.
//
// Три картки, і порядок у них не оформлення, а порядок дій: спершу пароль
// (замок), потім токен для Home Assistant (машина за тим самим замком), і
// аж тоді тунель (двері назовні). Саме тому картка тунелю відмовляється
// підключати, доки пароля немає, — і те саме перевіряє бекенд.
//
// ЧОГО ТУТ НЕМАЄ. Cloudflare Access — другий замок ПЕРЕД тунелем — не
// налаштовується звідси навмисно: він живе в панелі Cloudflare, керується
// політиками на рівні акаунта, і вигляд «ми ним керуємо» був би неправдою
// в момент, коли політика змінилась не з застосунку. README називає його
// окремим кроком.
//
// Секрети сюди НЕ повертаються ніколи: ані пароль, ані API-токен
// Cloudflare, ані токен HA (крім єдиної миті видачі). Стан описується
// прапорцями «є / немає», і саме тому сторінка не вміє «показати токен» —
// у базі його немає, там лише хеш.

import { esc } from "../format.js";
import { onSubmit, apply, confirmDialog } from "../forms.js";
import { text, field, formHTML } from "../fields.js";
import { infoBtn } from "../info.js";

// Стан тунелю словами. Ключі — status із Cloudflare API (healthy, degraded,
// down, inactive); порожньо = ще не питали або тунель не налаштований.
const TUNNEL_STATE = {
  healthy: ["t-ok", "працює"],
  degraded: ["t-warn", "працює нестабільно"],
  down: ["t-warn", "звʼязку немає"],
  inactive: ["muted", "жодного зʼєднання"],
};

function passwordCard() {
  return `<div class="card">
    <h2 class="h-row">Пароль ${infoBtn("setRemote")}</h2>
    <div class="note">Змінити пароль можна лише знаючи поточний — навіть із цього браузера.
      Після зміни всі інші пристрої вийдуть із застосунку.</div>
    ${formHTML({
    id: "pwForm", submit: "Змінити пароль",
    fields: [
      field("current", "Поточний пароль",
        { type: "password", required: true, autocomplete: "current-password" }),
      field("password", "Новий (від 8 символів)",
        { type: "password", required: true, autocomplete: "new-password" }),
      field("confirm", "Ще раз",
        { type: "password", required: true, autocomplete: "new-password" }),
    ],
  })}
  </div>`;
}

function tokenCard(auth) {
  const has = !!auth.has_token;
  return `<div class="card">
    <h2>Токен для Home Assistant</h2>
    <div class="note">Інтеграція ходить у REST — по кнопці «Оновити НБУ», повзунках цілей
      і кнопці «Отримано» у сповіщенні. Без токена всі чотири дістануть 401.
      У базі лишається лише хеш, тож показується він один раз.</div>
    <div class="pv-row"><span>Стан</span>
      <span class="${has ? "t-ok" : "muted"}">${has ? "виданий" : "не виданий"}</span></div>
    <div class="row-h mt-sm">
      <button type="button" id="btnToken">${has ? "Видати новий" : "Видати токен"}</button>
      ${has ? `<button type="button" class="quiet" id="btnTokenRevoke">Відкликати</button>` : ""}
    </div>
    <div class="mt-sm" id="tokenOut"></div>
  </div>`;
}

function tunnelCard(st) {
  const [cls, word] = TUNNEL_STATE[st.tunnel_status] || ["muted", "стан невідомий"];
  const rows = [];
  if (st.configured) {
    rows.push(`<div class="pv-row"><span>Адреса</span>
      <span><a class="lnk" href="${esc(st.public_url)}" target="_blank" rel="noreferrer"
        >${esc(st.hostname)}</a></span></div>`);
    rows.push(`<div class="pv-row"><span>Тунель</span>
      <span class="${cls}">${esc(word)}${st.running ? "" : " · конектор не запущений"}</span></div>`);
    if (st.public_ok) {
      rows.push(`<div class="pv-row"><span>Ззовні</span>
        <span class="t-ok">адреса відповідає</span></div>`);
    }
  }
  if (!st.cloudflared_found) {
    rows.push(`<div class="sub-xs t-warn">На сервері немає <code>cloudflared</code> — постав його
      в контейнері (<code>apt install cloudflared</code>) або задеплой заново: свіжий
      деплой ставить його сам.</div>`);
  }
  if (st.last_error) {
    rows.push(`<div class="sub-xs t-warn">${esc(st.last_error)}</div>`);
  }

  const form = st.configured
    ? `<div class="row-h mt-sm"><button type="button" class="warn" id="btnDisconnect">Відключити</button></div>`
    : formHTML({
      id: "tunForm", submit: "Підключити",
      fields: [
        field("api_token", "API-токен Cloudflare",
          { type: "password", required: true, autocomplete: "off" }),
        text("hostname", "Адреса", { ph: "oddinvest.example.com", required: true }),
      ],
    });

  return `<div class="card">
    <h2 class="h-row">Тунель Cloudflare ${infoBtn("setRemote")}</h2>
    <div class="note">Домашня адреса за CGNAT, тож проброс порту неможливий. Тунель дає
      публічний HTTPS без жодного відкритого порту: конектор сам зʼєднується назовні.
      Застосунок створить тунель, DNS-запис і запустить конектор — руками нічого не треба.</div>
    ${rows.join("")}
    ${form}
    ${st.configured ? "" : `<div class="sub-xs muted mt-sm">Токен береться в Cloudflare →
      My Profile → API Tokens → Create Token, з двома правами:
      <b>Account · Cloudflare Tunnel · Edit</b> і <b>Zone · DNS · Edit</b> на своїй зоні.
      Він лишається в базі (ним треба користуватись, а не звіряти) і назовні не віддається.</div>`}
    <div class="sub-xs muted mt-sm">Далі — <b>Cloudflare Access</b> руками, у панелі Cloudflare:
      Zero Trust → Access → Applications, політика «email = твій акаунт». Це другий замок
      перед застосунком, до якого перебір пароля не доходить.</div>
  </div>`;
}

/** Доступ ззовні. Сторінка не читає зведення взагалі — як і «Резервна
 *  копія», вона мусить працювати тоді, коли решта не працює. */
export async function remote(ctx, main) {
  const [auth, st] = await Promise.all([
    ctx.soft("auth", {}),
    ctx.soft("remote", {}),
  ]);
  main.innerHTML = passwordCard() + tokenCard(auth || {}) + tunnelCard(st || {});

  onSubmit(ctx, main.querySelector("#pwForm"), (f) => ({
    method: "PUT", path: "auth/password",
    body: {
      current: f.current.value, password: f.password.value, confirm: f.confirm.value,
    },
    msg: "Пароль змінено — інші пристрої вийшли",
  }));

  // Токен показується РІВНО тут і рівно раз: бекенд його більше не віддасть.
  // Тому не apply() з перезавантаженням сторінки, а власний запит — інакше
  // reload() стер би єдину копію токена з екрана.
  main.querySelector("#btnToken")?.addEventListener("click", async (e) => {
    if ((auth || {}).has_token
      && !await confirmDialog(ctx, "Старий токен перестане працювати. Видати новий?",
        { yes: "Видати", danger: false })) {
      return;
    }
    e.target.disabled = true;
    try {
      const res = await ctx.api("POST", "auth/token");
      main.querySelector("#tokenOut").innerHTML = `<div class="sub-xs">Токен (показується один раз):</div>
        <code class="secret">${esc(res.token)}</code>
        <div class="sub-xs muted">Вставити в Home Assistant: інтеграція ODD Invest →
          «Переналаштувати» → «Токен REST».</div>`;
    } catch (err) {
      ctx.toast(String(err.message || err), false);
    } finally {
      e.target.disabled = false;
    }
  });

  main.querySelector("#btnTokenRevoke")?.addEventListener("click", async () => {
    if (!await confirmDialog(ctx, "Відкликати токен? Home Assistant перестане писати в сервіс.",
      { yes: "Відкликати" })) return;
    await apply(ctx, { method: "DELETE", path: "auth/token" }, "Токен відкликано");
  });

  onSubmit(ctx, main.querySelector("#tunForm"), (f) => ({
    path: "remote/connect",
    body: { api_token: f.api_token.value.trim(), hostname: f.hostname.value.trim() },
    msg: "Тунель створюється — за пів хвилини онови сторінку",
  }));

  main.querySelector("#btnDisconnect")?.addEventListener("click", async () => {
    if (!await confirmDialog(ctx,
      "Відключити тунель? Адреса перестане працювати, тунель і DNS-запис буде видалено.",
      { yes: "Відключити" })) return;
    await apply(ctx, { path: "remote/disconnect" }, "Тунель відключено");
  });
}
