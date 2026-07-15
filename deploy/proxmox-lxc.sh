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
GO_VER="${GO_VER:-go1.23.6}"
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
echo "-- installing ${GO_VER}"
curl -fsSL "https://go.dev/dl/${GO_VER}.linux-amd64.tar.gz" -o /tmp/go.tgz
rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz
echo "-- building oddinvestd"
rm -rf /opt/oddinvest-src
git clone --depth 1 https://github.com/ODDsama/oddinvest /opt/oddinvest-src
cd /opt/oddinvest-src
export PATH="\$PATH:/usr/local/go/bin" GOTOOLCHAIN=local CGO_ENABLED=1
go build -o /usr/local/bin/oddinvestd ./cmd/oddinvestd
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
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/oddinvestd
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now oddinvestd
sleep 2
systemctl --no-pager --full status oddinvestd | head -n 12 || true
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
echo "======================================================================"
