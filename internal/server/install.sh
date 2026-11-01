#!/bin/sh
# Nolyx Orbit agent installer. Usage:
#   curl -fsSL {{APP_URL}}/install.sh | sudo ORBIT_TOKEN=orb_enr_... sh
set -eu

ORBIT_URL="${ORBIT_URL:-{{APP_URL}}}"
: "${ORBIT_TOKEN:?set ORBIT_TOKEN to an enrollment token from Settings → Agents}"

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }
[ "$(uname -s)" = "Linux" ] || { echo "the Orbit agent supports Linux only" >&2; exit 1; }
command -v systemctl >/dev/null || { echo "systemd is required (or run the agent container instead)" >&2; exit 1; }

case "$ORBIT_URL" in
  https://*) ;;
  *) [ "${ORBIT_INSECURE:-}" = "1" ] || { echo "ORBIT_URL is not https; set ORBIT_INSECURE=1 to allow plain HTTP (trusted networks only)" >&2; exit 1; } ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

echo "→ downloading orbit-agent ($ARCH)"
curl -fsSL "$ORBIT_URL/download/orbit-agent-linux-$ARCH" -o /usr/local/bin/orbit-agent.new
chmod 0755 /usr/local/bin/orbit-agent.new
mv /usr/local/bin/orbit-agent.new /usr/local/bin/orbit-agent

install -d -m 0700 /etc/orbit-agent /var/lib/orbit-agent
umask 077
cat > /etc/orbit-agent/env <<ENV
ORBIT_URL=$ORBIT_URL
ORBIT_TOKEN=$ORBIT_TOKEN
ORBIT_REGION=${ORBIT_REGION:-}
ORBIT_INSECURE=${ORBIT_INSECURE:-}
ENV

cat > /etc/systemd/system/orbit-agent.service <<'UNIT'
[Unit]
Description=Nolyx Orbit agent
After=network-online.target docker.service
Wants=network-online.target

[Service]
EnvironmentFile=/etc/orbit-agent/env
ExecStart=/usr/local/bin/orbit-agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now orbit-agent
echo "✓ orbit-agent installed. Logs: journalctl -u orbit-agent -f"
