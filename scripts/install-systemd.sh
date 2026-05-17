#!/usr/bin/env bash
set -euo pipefail

BIN_NAME="SmartHome"
CONFIG_PATH="/etc/smarthome.conf"
SERVICE_PATH="/etc/systemd/system/smarthome.service"

if [[ $EUID -ne 0 ]]; then
  echo "Please run as root: sudo $0" >&2
  exit 1
fi

if [[ ! -x "./${BIN_NAME}" ]]; then
  echo "Binary ./${BIN_NAME} not found. Build it first:" >&2
  echo "  go build -o ${BIN_NAME} ." >&2
  exit 1
fi

id -u smarthome >/dev/null 2>&1 || useradd --system --home-dir /var/lib/smarthome --shell /usr/sbin/nologin smarthome
mkdir -p /var/lib/smarthome /var/log/smarthome
chown -R smarthome:smarthome /var/lib/smarthome /var/log/smarthome
install -m 0755 "./${BIN_NAME}" /usr/local/bin/${BIN_NAME}

if [[ ! -f "${CONFIG_PATH}" ]]; then
  install -m 0640 configs/smarthome.conf.example.json "${CONFIG_PATH}"
  chown root:smarthome "${CONFIG_PATH}"
  echo "Installed example config to ${CONFIG_PATH}. Edit it before starting the service."
fi

install -m 0644 systemd/smarthome.service "${SERVICE_PATH}"
systemctl daemon-reload
systemctl enable smarthome

echo "Installation complete. Next steps:"
echo "  sudo nano ${CONFIG_PATH}"
echo "  sudo systemctl start smarthome"
echo "  journalctl -u smarthome -f"
