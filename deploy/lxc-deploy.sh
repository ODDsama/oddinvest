#!/usr/bin/env bash
#
# Розгорнути oddinvestd у ЦЬОМУ контейнері з ревізії bare-репозиторію:
#
#   lxc-deploy.sh [<rev>]        # типово main; GO_VER=1.24.0 — пришпилити тулчейн
#
# Єдине місце, де живе ланцюжок «checkout → Go → збірка → підміна бінарника
# → restart → перевірка → відкат». Його кличуть три входи: post-receive
# хук (deploy/lxc-post-receive) після `git push prod main`,
# proxmox-update.sh (fallback: fetch з GitHub у bare-репо без робочої
# станції) і proxmox-lxc.sh на свіжій провізії. Доти збірка була
# переписана у двох скриптах і вже раз розійшлась (див. коментар про Go
# нижче); третя копія розійшлась би так само.
#
# Запускати як root усередині контейнера. stdin не читається: хук отримує
# ним список refs, і без цього рядка перший же read у скрипті зʼїв би його.
set -euo pipefail
exec </dev/null

BARE=/srv/git/oddinvest.git
SRC=/opt/oddinvest-src
BIN=/usr/local/bin/oddinvestd
REV="${1:-main}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/}"
# /healthz з версією — для НОВОГО бінарника (internal/api/health.go).
HEALTHZ_URL="${HEALTHZ_URL:-http://127.0.0.1:8080/healthz}"
# База — та сама, що в unit-файлі (ODDINVEST_DB_PATH).
DB="${DB:-/var/lib/oddinvestd/oddinvest.db}"

# Git у хуку виставляє GIT_DIR=. — з ним checkout у чуже робоче дерево
# піде не туди. Обидва шляхи задаємо явно й лише через цю обгортку.
unset GIT_DIR GIT_WORK_TREE
g() { git --git-dir="$BARE" --work-tree="$SRC" "$@"; }

if [ ! -d "$BARE" ]; then
  echo "!! немає $BARE — контейнер ставить deploy/proxmox-lxc.sh на Proxmox-хості"
  exit 1
fi

sha="$(g rev-parse --verify "$REV^{commit}")"
echo "-- викладаю $(g log -1 --oneline "$sha")"
mkdir -p "$SRC"
# read-tree, а не checkout: checkout посунув би HEAD bare-репозиторію на
# detached-коміт, а reset --hard — саму гілку main. read-tree лише
# приводить індекс (він лежить у bare-репо) і робоче дерево до ревізії;
# файли, яких у новій ревізії немає, при цьому прибираються, бо індекс
# памʼятає попередній стан. Тому окремий clean не потрібен.
g read-tree -u --reset "$sha"

# ---------- тулчейн ----------
export PATH="$PATH:/usr/local/go/bin" GOTOOLCHAIN=local CGO_ENABLED=1
# Версія береться з go.mod, а не з константи: доти вона жила в ТРЬОХ місцях
# (go.mod, Dockerfile, скрипт провізії) і саме тому розійшлась — go.mod
# поїхав на 1.24, у контейнері лишався 1.23.6, GOTOOLCHAIN=local
# забороняє довантажити потрібний, і збірка падала на «go.mod requires
# go >= 1.24.0». Через set -e restart не виконувався, у контейнері стояв
# старий бінарник, а на екрані все виглядало так, ніби оновлення просто
# не подіяло. GO_VER лишається важелем на випадок, коли треба саме
# конкретний тулчейн — тоді вимагаємо рівно його, а не «не старіший».
#
# sort -V, а не порівняння рядків: 1.23.6 більший за 1.24.0 лексично.
need="${GO_VER:-$(sed -n 's/^go //p' "$SRC/go.mod" | head -1)}"
need="${need#go}"
have="$(go version 2>/dev/null | cut -d' ' -f3 | cut -c3- || true)"
if [ -n "${GO_VER:-}" ]; then
  ok=$([ "$have" = "$need" ] && echo 1 || true)
else
  ok=$([ -n "$have" ] && [ "$(printf '%s\n%s\n' "$need" "$have" | sort -V | tail -1)" = "$have" ] && echo 1 || true)
fi
if [ -z "$ok" ]; then
  echo "-- Go ${have:-відсутній}, потрібен $need — ставлю go$need"
  curl -fsSL "https://go.dev/dl/go$need.linux-amd64.tar.gz" -o /tmp/go.tgz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tgz
  rm -f /tmp/go.tgz
fi
echo "-- $(go version)"

# ---------- конектор тунелю ----------
# Ідемпотентно й тихо: у контейнерах, поставлених до появи «Доступу
# ззовні», cloudflared немає, а скрипт провізії заново не ганяють. Тунель
# без нього створиться, але зʼєднання не буде — і сторінка про це скаже.
# Невдача тут НЕ валить деплой (|| true): відсутній конектор — це втрачений
# доступ ззовні, а не втрачений застосунок.
if ! command -v cloudflared >/dev/null 2>&1; then
  echo "-- ставлю cloudflared"
  (
    set -e
    install -m 0755 -d /usr/share/keyrings
    curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg \
      -o /usr/share/keyrings/cloudflare-main.gpg
    echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared bookworm main" \
      >/etc/apt/sources.list.d/cloudflared.list
    DEBIAN_FRONTEND=noninteractive apt-get update -q
    DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends cloudflared
  ) || echo "!! cloudflared не поставився — доступ ззовні буде недоступний"
fi

# ---------- юніт ----------
# Ставиться з репозиторію на кожному деплої, тож зміна в
# deploy/systemd/oddinvestd.service їде на бойовий тим самим пушем. Доти
# старі контейнери латались sed-ом (право на 443, UMask) — по латці на
# кожне нове поле юніта, і кожна жила вічно.
UNIT_FILE=/etc/systemd/system/oddinvestd.service
if ! cmp -s "$SRC/deploy/systemd/oddinvestd.service" "$UNIT_FILE"; then
  echo "-- юніт з репозиторію"
  install -m 644 "$SRC/deploy/systemd/oddinvestd.service" "$UNIT_FILE"
  systemctl daemon-reload
fi

# ---------- збірка ----------
# У ТИМЧАСОВИЙ файл, а не одразу в $BIN: раніше збірка писала прямо в
# /usr/local/bin/oddinvestd, і невдала збірка лишала там обрізаний файл,
# з яким наступний перезапуск сервісу вже не піднімався. Тепер провал
# збірки завершує скрипт тут (set -e), живий бінарник не чіпається.
cd "$SRC"
echo "-- збірка"
# Версія збірки — коротке sha коміту: /healthz віддає її назад, і
# перевірка нижче переконується, що відповідає саме НОВИЙ бінарник.
ver="$(printf '%s' "$sha" | cut -c1-7)"
go build -ldflags "-X github.com/ODDsama/oddinvest/internal/api.Version=$ver"   -o "$BIN.new" ./cmd/oddinvestd

# ---------- підміна + restart ----------
# Які домиграційні копії були ДО рестарту: нова, що зʼявиться після нього,
# означає, що цей деплой змігрував базу, — і відкат мусить повернути саме
# її (див. «відкат» нижче).
pre_before="$(ls "$DB".pre-* 2>/dev/null | grep -v '\.tmp$' || true)"
if [ -x "$BIN" ]; then
  mv -f "$BIN" "$BIN.prev"
fi
mv -f "$BIN.new" "$BIN"
systemctl restart oddinvestd

# ---------- перевірка ----------
# НОВИЙ бінарник — через /healthz: 200 означає, що база відповідає
# (пінг + остання міграція), а версія в тілі має збігтись із щойно
# зібраним комітом — тобто відповідає саме він, а не щось інше на порту.
#
# 200 на «/» лишається для ВІДКОТУ: попередній бінарник може бути старшим
# за /healthz. Він теж щось доводить — main.go відкриває сховище й
# проганяє міграції ДО ListenAndServe, тож відповідь означає, що процес
# піднявся на цій базі.
healthy_new() {
  local _
  for _ in $(seq 1 30); do
    curl -fsS "$HEALTHZ_URL" 2>/dev/null | grep -q "\"version\":\"$ver\"" && return 0
    sleep 0.5
  done
  return 1
}
healthy() {
  local _
  for _ in $(seq 1 30); do
    curl -fsS -o /dev/null "$HEALTH_URL" 2>/dev/null && return 0
    sleep 0.5
  done
  return 1
}

if healthy_new; then
  echo "health: ok"
  echo "== на бойовому: $(g log -1 --oneline "$sha")"
  exit 0
fi

echo "!! health: $HEALTHZ_URL не віддав версію $ver за 15 с"
journalctl -u oddinvestd -n 20 --no-pager || true

# Відкат бінарника — не відкат схеми: down-міграцій немає
# (internal/store/migrate.go). Старий бінарник над новішою схемою тепер
# відмовляється стартувати (refuseNewerSchema) — раніше він піднімався й
# мовчки писав рядки, що ламають нові інваріанти. Тож якщо цей деплой
# змігрував базу (зʼявилась нова копія <db>.pre-<версія>), відкат
# повертає й саму базу з неї. Поточна база не зникає: вона лягає поруч
# як <db>.failed-<час>, тож записане новою версією за ці секунди можна
# дістати руками.
if [ -x "$BIN.prev" ]; then
  echo "-- відкат на попередній бінарник (новий лишаю в $BIN.failed)"
  mv -f "$BIN" "$BIN.failed"
  mv -f "$BIN.prev" "$BIN"
  pre_after="$(ls "$DB".pre-* 2>/dev/null | grep -v '\.tmp$' || true)"
  pre_new="$(comm -13 <(printf '%s\n' "$pre_before" | sort) <(printf '%s\n' "$pre_after" | sort) | grep . | head -1 || true)"
  if [ -n "$pre_new" ]; then
    systemctl stop oddinvestd
    stamp="$(date +%Y%m%d-%H%M%S)"
    echo "-- цей деплой змігрував базу: повертаю $pre_new (поточну лишаю в $DB.failed-$stamp)"
    mv -f "$DB" "$DB.failed-$stamp"
    # Журнали — разом із нею: без -wal у .failed бракувало б записаного
    # після останнього checkpoint.
    for j in wal shm; do if [ -e "$DB-$j" ]; then mv -f "$DB-$j" "$DB.failed-$stamp-$j"; fi; done
    cp -p "$pre_new" "$DB"
    chown oddinvestd:oddinvestd "$DB"
    chmod 600 "$DB"
  fi
  systemctl restart oddinvestd
  if healthy; then
    echo "відкат: попередня версія працює"
  else
    echo "!! попередня версія теж не піднялась — див. journalctl -u oddinvestd;"
    echo "!! якщо впала міграція, знімок бази лежить поруч: ls /var/lib/oddinvestd/*.pre-*"
  fi
fi
exit 1
