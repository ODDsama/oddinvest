#!/usr/bin/env bash
#
# Оновити вже розгорнутий oddinvestd у LXC: git pull + перезбірка + restart.
# Запускати на Proxmox-хості (root):
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/ODDsama/oddinvest/main/deploy/proxmox-update.sh)
#
# За потреби вкажи контейнер явно: CT=123 bash <(curl ...)
#
set -euo pipefail

CT="${CT:-$(pct list | awk '/oddinvest/{print $1}' | head -1)}"
if [ -z "$CT" ]; then
  echo "!! не знайшов LXC з іменем oddinvest — задай CT=<id>"
  exit 1
fi
echo "==> оновлюю oddinvestd у LXC $CT"
pct exec "$CT" -- bash -lc '
  set -e
  cd /opt/oddinvest-src
  # Старий протокол git і HTTP/1.1 — не смак, а обхід. GitHub відсікає
  # АНОНІМНІ git-запити з адрес, які він обмежив (домашня IP за CGNAT —
  # саме така): GET info/refs проходить, а POST git-upload-pack за
  # протоколом v2 дістає 401 і git просить логін, якого в контейнера немає
  # й бути не має. За протоколом v0 той самий pull з тієї самої адреси
  # проходить (перевірено 2026-09-02). Токен у контейнері був би другим
  # секретом, за яким треба стежити, заради читання публічного репозиторію.
  git -c protocol.version=0 -c http.version=HTTP/1.1 pull --ff-only
  export PATH=$PATH:/usr/local/go/bin GOTOOLCHAIN=local CGO_ENABLED=1

  # Тулчейн підтягується під go.mod. Без цього оновлення ламалось намертво
  # і мовчки: go.mod поїхав на 1.24, у контейнері лишався встановлений
  # 1.23.6, GOTOOLCHAIN=local забороняє довантажити потрібний — і збірка
  # падала на "go.mod requires go >= 1.24.0". Через set -e далі не йшло
  # нічого, тобто systemctl restart не виконувався, і в контейнері
  # лишався старий бінарник — а на екрані все виглядало так, ніби
  # оновлення просто не подіяло.
  #
  # sort -V, а не порівняння рядків: 1.23.6 більший за 1.24.0 лексично.
  need=$(sed -n "s/^go //p" go.mod | head -1)
  have=$(go version 2>/dev/null | cut -d" " -f3 | cut -c3-)
  if [ "$(printf "%s\n%s\n" "$need" "$have" | sort -V | tail -1)" != "$have" ]; then
    echo "-- Go $have старіший за потрібний $need — ставлю go$need"
    curl -fsSL "https://go.dev/dl/go$need.linux-amd64.tar.gz" -o /tmp/go.tgz
    rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz
    go version
  fi

  go build -o /usr/local/bin/oddinvestd ./cmd/oddinvestd
  systemctl restart oddinvestd
  sleep 2
  echo -n "service: "; systemctl is-active oddinvestd
  journalctl -u oddinvestd -n 8 --no-pager
'
echo "==> готово"
