#!/usr/bin/env bash
#
# One-shot deploy of oddinvestd into a fresh Debian 12 LXC on a Proxmox VE host.
#
# Run on the Proxmox host shell (Datacenter > node > Shell), as root:
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/ODDsama/oddinvest/main/deploy/proxmox-lxc.sh)
#
# Override any tunable via env, e.g. different storage / bridge / MQTT:
#
#   STORAGE=local-zfs BRIDGE=vmbr0 \
#   MQTT_ADDR=tcp://192.168.1.10:1883 MQTT_USER=oddinvest MQTT_PASS=secret \
#   bash <(curl -fsSL https://raw.githubusercontent.com/ODDsama/oddinvest/main/deploy/proxmox-lxc.sh)
#
set -euo pipefail

# ---------- tunables ----------
CTID="${CTID:-$(pvesh get /cluster/nextid)}"
CTHOSTNAME="${CTHOSTNAME:-oddinvest}"
STORAGE="${STORAGE:-local-lvm}"          # rootfs storage (local-lvm, local-zfs, ...)
TEMPLATE_STORAGE="${TEMPLATE_STORAGE:-local}"
BRIDGE="${BRIDGE:-vmbr0}"
DISK_GB="${DISK_GB:-4}"
MEMORY_MB="${MEMORY_MB:-512}"
CORES="${CORES:-1}"
# Порожньо = взяти з go.mod (це вирішує deploy/lxc-deploy.sh). Задавати
# сюди щось варто лише тоді, коли треба саме конкретний тулчейн, — інакше
# версія знову почне жити окремим життям від go.mod.
GO_VER="${GO_VER:-}"
# MQTT — leave MQTT_ADDR empty to deploy with MQTT disabled for now.
MQTT_ADDR="${MQTT_ADDR:-}"
MQTT_USER="${MQTT_USER:-}"
MQTT_PASS="${MQTT_PASS:-}"
MQTT_PREFIX="${MQTT_PREFIX:-oddinvest}"

echo "==> CTID=$CTID host=$CTHOSTNAME storage=$STORAGE bridge=$BRIDGE"

# ---------- ensure Debian 12 template ----------
pveam update >/dev/null 2>&1 || true
TMPL="$(pveam available --section system | awk '/debian-12-standard/{print $2}' | sort -V | tail -1)"
if [ -z "$TMPL" ]; then echo "!! no debian-12-standard template found in pveam"; exit 1; fi
if ! pveam list "$TEMPLATE_STORAGE" 2>/dev/null | grep -q "$TMPL"; then
  echo "==> downloading template $TMPL to $TEMPLATE_STORAGE"
  pveam download "$TEMPLATE_STORAGE" "$TMPL"
fi

# ---------- create + start the container ----------
echo "==> creating LXC $CTID"
pct create "$CTID" "$TEMPLATE_STORAGE:vztmpl/$TMPL" \
  --hostname "$CTHOSTNAME" \
  --cores "$CORES" --memory "$MEMORY_MB" \
  --rootfs "$STORAGE:$DISK_GB" \
  --net0 "name=eth0,bridge=$BRIDGE,ip=dhcp" \
  --features nesting=1 \
  --unprivileged 1 \
  --onboot 1
pct start "$CTID"

# ---------- wait for network ----------
echo "==> waiting for container network"
for _ in $(seq 1 30); do
  pct exec "$CTID" -- bash -lc 'getent hosts go.dev >/dev/null 2>&1' && break
  sleep 2
done

# ---------- provisioning script (values expanded here on the host) ----------
cat >/tmp/oddinvest-provision.sh <<PROV
#!/usr/bin/env bash
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
echo "-- installing build deps"
apt-get update -q
apt-get install -y -q --no-install-recommends ca-certificates curl git gcc libc6-dev
# cloudflared — конектор тунелю. Ставиться ПАКЕТОМ, а не тягнеться демоном:
# демон його лише запускає дочірнім процесом (internal/tunnel), коли тунель
# підключають зі сторінки «Доступ ззовні». Ланцюг постачання лишається за
# apt, а не за нашим кодом.
install -m 0755 -d /usr/share/keyrings
curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg \
  -o /usr/share/keyrings/cloudflare-main.gpg
echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared bookworm main" \
  >/etc/apt/sources.list.d/cloudflared.list
apt-get update -q
apt-get install -y -q --no-install-recommends cloudflared
echo "-- fetching source"
# Розкладка та сама, що її робить proxmox-git-setup.sh для наявного
# контейнера: bare-репозиторій /srv/git/oddinvest.git (ціль для
# `git push prod main` з робочої станції) + робоче дерево /opt/oddinvest-src
# без власного .git + post-receive хук. Свіжий контейнер без хука знову
# оновлювався б лише one-liner-ом з хоста — саме від цього й відходимо.
#
# Протокол v0 і HTTP/1.1 — обхід обмеження GitHub на анонімні git-запити з
# деяких адрес (401 на POST git-upload-pack); довід — у proxmox-update.sh.
rm -rf /srv/git/oddinvest.git /opt/oddinvest-src
mkdir -p /srv/git /opt/oddinvest-src
git -c protocol.version=0 -c http.version=HTTP/1.1 clone --bare \
  https://github.com/ODDsama/oddinvest /srv/git/oddinvest.git
git -C /srv/git/oddinvest.git symbolic-ref HEAD refs/heads/main
git --git-dir=/srv/git/oddinvest.git --work-tree=/opt/oddinvest-src read-tree -u --reset main
install -m 755 /opt/oddinvest-src/deploy/lxc-post-receive /srv/git/oddinvest.git/hooks/post-receive
echo "-- service user + data dir"
id oddinvestd >/dev/null 2>&1 || useradd -r -s /usr/sbin/nologin oddinvestd
install -d -o oddinvestd -g oddinvestd /var/lib/oddinvestd
echo "-- env file"
cat >/etc/oddinvestd.env <<ENV
ODDINVEST_HTTP_ADDR=:8080
ODDINVEST_DB_PATH=/var/lib/oddinvestd/oddinvest.db
ODDINVEST_MQTT_ADDR=${MQTT_ADDR}
ODDINVEST_MQTT_USER=${MQTT_USER}
ODDINVEST_MQTT_PASS=${MQTT_PASS}
ODDINVEST_MQTT_PREFIX=${MQTT_PREFIX}
ENV
chmod 640 /etc/oddinvestd.env
echo "-- systemd unit"
cat >/etc/systemd/system/oddinvestd.service <<UNIT
[Unit]
Description=ODD Invest backend (oddinvestd)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=oddinvestd
Group=oddinvestd
EnvironmentFile=/etc/oddinvestd.env
ExecStart=/usr/local/bin/oddinvestd
Restart=on-failure
RestartSec=5
StateDirectory=oddinvestd
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/oddinvestd
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable oddinvestd
echo "-- toolchain + build + start (deploy/lxc-deploy.sh)"
# Тулчейн, збірку, старт і перевірку робить той самий скрипт, що й хук
# після git push, і proxmox-update.sh: одна логіка на три входи. GO_VER
# підставляє ХОСТ (тому без екранування) — порожній рядок означає «взяти
# з go.mod», і саме так lxc-deploy.sh його й трактує.
GO_VER="${GO_VER}" bash /opt/oddinvest-src/deploy/lxc-deploy.sh main
PROV

echo "==> provisioning inside container (build takes ~1-2 min)"
pct push "$CTID" /tmp/oddinvest-provision.sh /root/provision.sh
pct exec "$CTID" -- bash /root/provision.sh

# ---------- report ----------
IP="$(pct exec "$CTID" -- bash -lc "hostname -I | awk '{print \$1}'")"
echo
echo "======================================================================"
echo "  oddinvestd is running in LXC $CTID ($CTHOSTNAME)"
echo "  Web UI / REST : http://$IP:8080"
echo "  MQTT          : ${MQTT_ADDR:-<disabled>}"
echo "  Edit config   : pct exec $CTID -- nano /etc/oddinvestd.env"
echo "                  pct exec $CTID -- systemctl restart oddinvestd"
echo "  Logs          : pct exec $CTID -- journalctl -u oddinvestd -f"
echo "  Deploy        : git remote add prod root@$IP:/srv/git/oddinvest.git"
echo "                  git push prod main   (ключ: PUBKEY=... proxmox-git-setup.sh)"
echo "======================================================================"
