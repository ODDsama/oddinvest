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

# Git у хуку виставляє GIT_DIR=. — з ним checkout у чуже робоче дерево
# піде не туди. Обидва шляхи задаємо явно й лише через цю обгортку.
unset GIT_DIR GIT_WORK_TREE
g() { git --git-dir="$BARE" --work-tree="$SRC" "$@"; }

if [ ! -d "$BARE" ]; then
  echo "!! немає $BARE — спершу deploy/proxmox-git-setup.sh на Proxmox-хості"
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

# ---------- збірка ----------
# У ТИМЧАСОВИЙ файл, а не одразу в $BIN: раніше збірка писала прямо в
# /usr/local/bin/oddinvestd, і невдала збірка лишала там обрізаний файл,
# з яким наступний перезапуск сервісу вже не піднімався. Тепер провал
# збірки завершує скрипт тут (set -e), живий бінарник не чіпається.
cd "$SRC"
echo "-- збірка"
go build -o "$BIN.new" ./cmd/oddinvestd

# ---------- підміна + restart ----------
if [ -x "$BIN" ]; then
  mv -f "$BIN" "$BIN.prev"
fi
mv -f "$BIN.new" "$BIN"
systemctl restart oddinvestd

# ---------- перевірка ----------
# 200 на «/» — не просто «процес живий»: main.go відкриває сховище (і
# проганяє міграції) ДО ListenAndServe, тож відповідь означає, що
# міграції пройшли і сервер слухає. Окремого /api/health немає навмисно.
healthy() {
  local _
  for _ in $(seq 1 30); do
    curl -fsS -o /dev/null "$HEALTH_URL" 2>/dev/null && return 0
    sleep 0.5
  done
  return 1
}

if healthy; then
  echo "health: ok"
  echo "== на бойовому: $(g log -1 --oneline "$sha")"
  exit 0
fi

echo "!! health: сервер не відповів на $HEALTH_URL за 15 с"
journalctl -u oddinvestd -n 20 --no-pager || true

# Відкат бінарника — не відкат схеми: down-міграцій немає
# (internal/store/migrate.go), і якщо нова версія вже мігрувала базу,
# попередня може не піднятись на новій схемі. На цей випадок перед
# міграцією сховище робить знімок <db>.pre-<version> поруч із базою.
if [ -x "$BIN.prev" ]; then
  echo "-- відкат на попередній бінарник (новий лишаю в $BIN.failed)"
  mv -f "$BIN" "$BIN.failed"
  mv -f "$BIN.prev" "$BIN"
  systemctl restart oddinvestd
  if healthy; then
    echo "відкат: попередня версія працює"
  else
    echo "!! попередня версія теж не піднялась — див. journalctl -u oddinvestd;"
    echo "!! якщо впала міграція, знімок бази лежить поруч: ls /var/lib/oddinvestd/*.pre-*"
  fi
fi
exit 1
