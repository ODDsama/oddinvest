#!/usr/bin/env bash
#
# Одноразово перевести вже розгорнутий контейнер на деплой через git push:
# bare-репозиторій /srv/git/oddinvest.git + post-receive хук + (за бажання)
# SSH-ключ робочої станції для root. Запускати на Proxmox-хості (root):
#
#   CT=106 PUBKEY="ssh-ed25519 AAAA… user@host" \
#   bash <(curl -fsSL https://raw.githubusercontent.com/ODDsama/oddinvest/main/deploy/proxmox-git-setup.sh)
#
# Ідемпотентний: повторний запуск нічого не ламає (ключ не дублюється,
# bare-репо не переклонюється). Після нього на робочій станції:
#
#   git remote add prod root@<ip контейнера>:/srv/git/oddinvest.git
#   git push prod main          # хук збирає, рестартує і перевіряє сам
#
# Старий клон /opt/oddinvest-src відсувається в /opt/oddinvest-src.old, а
# не видаляється: якщо щось піде не так, є до чого повернутись.
set -euo pipefail

CT="${CT:-$(pct list | awk '/oddinvest/{print $1}' | head -1)}"
if [ -z "$CT" ]; then
  echo "!! не знайшов LXC з іменем oddinvest — задай CT=<id>"
  exit 1
fi
PUBKEY="${PUBKEY:-}"
echo "==> налаштовую git-деплой у LXC $CT"
pct exec "$CT" -- env "PUBKEY=$PUBKEY" bash -lc '
  set -e
  BARE=/srv/git/oddinvest.git
  SRC=/opt/oddinvest-src

  if [ ! -d "$BARE" ]; then
    echo "-- bare-клон з GitHub у $BARE"
    mkdir -p /srv/git
    # Старий протокол git і HTTP/1.1 — не смак, а обхід. GitHub відсікає
    # АНОНІМНІ git-запити з адрес, які він обмежив (домашня IP за CGNAT —
    # саме така): GET info/refs проходить, а POST git-upload-pack за
    # протоколом v2 дістає 401 і git просить логін, якого в контейнера
    # немає й бути не має. За протоколом v0 той самий clone з тієї самої
    # адреси проходить (перевірено 2026-09-02). Токен у контейнері був би
    # другим секретом, за яким треба стежити, заради читання публічного
    # репозиторію. Пушу з робочої станції це не стосується — він іде по SSH
    # прямо в контейнер.
    git -c protocol.version=0 -c http.version=HTTP/1.1 clone --bare \
      https://github.com/ODDsama/oddinvest "$BARE"
  fi
  git -C "$BARE" symbolic-ref HEAD refs/heads/main

  if [ -d "$SRC/.git" ]; then
    echo "-- старий клон $SRC → $SRC.old"
    rm -rf "$SRC.old"
    mv "$SRC" "$SRC.old"
  fi
  mkdir -p "$SRC"
  git --git-dir="$BARE" --work-tree="$SRC" read-tree -u --reset main
  install -m 755 "$SRC/deploy/lxc-post-receive" "$BARE/hooks/post-receive"
  echo "-- хук: $BARE/hooks/post-receive"

  if [ -n "$PUBKEY" ]; then
    install -d -m 700 /root/.ssh
    touch /root/.ssh/authorized_keys
    chmod 600 /root/.ssh/authorized_keys
    if grep -qxF "$PUBKEY" /root/.ssh/authorized_keys; then
      echo "-- ключ уже в authorized_keys"
    else
      echo "$PUBKEY" >> /root/.ssh/authorized_keys
      echo "-- ключ додано в /root/.ssh/authorized_keys"
    fi
  fi

  ip=$(hostname -I | awk "{print \$1}")
  echo
  echo "На робочій станції:"
  echo "  git remote add prod root@$ip:/srv/git/oddinvest.git"
  echo "  git push prod main"
'
echo "==> готово"
