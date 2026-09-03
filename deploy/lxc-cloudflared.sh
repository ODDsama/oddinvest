#!/usr/bin/env bash
# Виводить oddinvestd у світ через Cloudflare Tunnel.
#
# Запускати як root УСЕРЕДИНІ контейнера (або `pct exec <CT> -- bash -s
# < deploy/lxc-cloudflared.sh` з Proxmox-хоста). Один раз.
#
# ЧОМУ ТУНЕЛЬ, А НЕ ПРОБРОС ПОРТУ. Домашня адреса за CGNAT — вхідних
# зʼєднань до неї не буває. Тунель ініціює зʼєднання зсередини, TLS
# термінує Cloudflare, і жоден порт назовні не відкривається.
#
# ЧОМУ СПЕРШУ ПАРОЛЬ. Скрипт відмовляється працювати, доки в
# /etc/oddinvestd.env немає ODDINVEST_AUTH_PASSWORD: двері без замка
# відкривати не можна (README → «Доступ ззовні»).
#
# Параметри: HOSTNAME=oddinvest.example.com (обовʼязково),
# TUNNEL=oddinvest (назва тунелю, типово).
set -euo pipefail

HOSTNAME="${HOSTNAME:?задай HOSTNAME=oddinvest.<домен>}"
TUNNEL="${TUNNEL:-oddinvest}"
ENV_FILE=/etc/oddinvestd.env

if ! grep -Eq '^ODDINVEST_AUTH_PASSWORD=.+' "$ENV_FILE"; then
  echo "!! у $ENV_FILE порожній ODDINVEST_AUTH_PASSWORD — спершу замок, потім двері" >&2
  exit 1
fi

echo "-- cloudflared"
if ! command -v cloudflared >/dev/null 2>&1; then
  install -m 0755 -d /usr/share/keyrings
  curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg \
    -o /usr/share/keyrings/cloudflare-main.gpg
  echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared bookworm main" \
    >/etc/apt/sources.list.d/cloudflared.list
  apt-get update -qq
  apt-get install -y -qq cloudflared
fi

# Єдиний інтерактивний крок: відкриває посилання, яке треба пройти в
# браузері під своїм акаунтом Cloudflare. Повторно — не питає.
if [ ! -f /root/.cloudflared/cert.pem ]; then
  echo "-- вхід у Cloudflare (посилання нижче відкрити в браузері)"
  cloudflared tunnel login
fi

echo "-- тунель $TUNNEL"
if ! cloudflared tunnel list | awk '{print $2}' | grep -qx "$TUNNEL"; then
  cloudflared tunnel create "$TUNNEL"
fi
UUID="$(cloudflared tunnel list | awk -v n="$TUNNEL" '$2==n{print $1}')"

install -d /etc/cloudflared
cat >/etc/cloudflared/config.yml <<CFG
tunnel: ${UUID}
credentials-file: /root/.cloudflared/${UUID}.json
ingress:
  - hostname: ${HOSTNAME}
    service: http://127.0.0.1:8080
    originRequest:
      # Заголовок, за яким сервіс ставить Secure на cookie сесії.
      httpHostHeader: ${HOSTNAME}
  - service: http_status:404
CFG

echo "-- DNS ${HOSTNAME} → тунель"
cloudflared tunnel route dns "$TUNNEL" "$HOSTNAME" || true

echo "-- systemd"
cloudflared service install 2>/dev/null || true
systemctl enable --now cloudflared
systemctl restart cloudflared
sleep 2
systemctl --no-pager --lines=5 status cloudflared || true

cat <<DONE

Готово: https://${HOSTNAME} → http://127.0.0.1:8080

Далі руками, у панелі Cloudflare (рекомендовано): Zero Trust → Access →
Applications → Add → Self-hosted, домен ${HOSTNAME}, політика Allow з
правилом «Emails = <свій акаунт>». Це другий замок перед застосунком.
DONE
