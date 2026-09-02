#!/usr/bin/env bash
#
# Оновити вже розгорнутий oddinvestd у LXC з GitHub, без робочої станції:
# fetch main у bare-репозиторій контейнера + deploy/lxc-deploy.sh.
# Запускати на Proxmox-хості (root):
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/ODDsama/oddinvest/main/deploy/proxmox-update.sh)
#
# За потреби вкажи контейнер явно: CT=123 bash <(curl ...)
#
# Це запасний шлях. Звичайний деплой — `git push prod main` з робочої
# станції: post-receive хук у контейнері робить те саме, що й тут, але без
# заходу на Proxmox-хост і без походу в GitHub (див. README, «Деплой»).
# Сама збірка, підміна бінарника, restart, health і відкат живуть в одному
# місці — deploy/lxc-deploy.sh, і цей скрипт лише доносить туди свіжий main.
#
set -euo pipefail

CT="${CT:-$(pct list | awk '/oddinvest/{print $1}' | head -1)}"
if [ -z "$CT" ]; then
  echo "!! не знайшов LXC з іменем oddinvest — задай CT=<id>"
  exit 1
fi
echo "==> оновлюю oddinvestd у LXC $CT з GitHub"
pct exec "$CT" -- bash -lc '
  set -e
  BARE=/srv/git/oddinvest.git
  if [ ! -d "$BARE" ]; then
    echo "!! немає $BARE — контейнер ще на старій розкладці; спершу один раз:"
    echo "   bash <(curl -fsSL https://raw.githubusercontent.com/ODDsama/oddinvest/main/deploy/proxmox-git-setup.sh)"
    exit 1
  fi
  # Старий протокол git і HTTP/1.1 — не смак, а обхід. GitHub відсікає
  # АНОНІМНІ git-запити з адрес, які він обмежив (домашня IP за CGNAT —
  # саме така): GET info/refs проходить, а POST git-upload-pack за
  # протоколом v2 дістає 401 і git просить логін, якого в контейнера немає
  # й бути не має. За протоколом v0 той самий fetch з тієї самої адреси
  # проходить (перевірено 2026-09-02). Токен у контейнері був би другим
  # секретом, за яким треба стежити, заради читання публічного репозиторію.
  #
  # +main:main прямо в гілку: репозиторій bare, робоче дерево веде
  # lxc-deploy.sh через read-tree, тож «checked out branch» тут ні до чого.
  git -C "$BARE" -c protocol.version=0 -c http.version=HTTP/1.1 \
    fetch origin +main:main
  bash /opt/oddinvest-src/deploy/lxc-deploy.sh main
'
echo "==> готово"
