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
  git pull --ff-only
  export PATH=$PATH:/usr/local/go/bin GOTOOLCHAIN=local CGO_ENABLED=1
  go build -o /usr/local/bin/oddinvestd ./cmd/oddinvestd
  systemctl restart oddinvestd
  sleep 2
  echo -n "service: "; systemctl is-active oddinvestd
  journalctl -u oddinvestd -n 8 --no-pager
'
echo "==> готово"
