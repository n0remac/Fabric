#!/bin/bash
set -euo pipefail

# Run after building both binaries into deploy/bin/. Existing configuration and
# firmware are preserved; token rotation is an explicit fabricctl operation.
project_dir=$(cd -- "$(dirname -- "$0")/.." && pwd)
operator=${SUDO_USER:-pi}
operator_dir=$(getent passwd "$operator" | cut -d: -f6)
if [[ $EUID -ne 0 ]]; then
  echo "Run with sudo to install the Fabric service." >&2
  exit 1
fi
if [[ ! -x "$project_dir/deploy/bin/fabricd" || ! -x "$project_dir/deploy/bin/fabricctl" ]]; then
  echo "Build deploy/bin/fabricd and deploy/bin/fabricctl first." >&2
  exit 1
fi
if [[ -z "$operator_dir" || "$operator" == root ]]; then
  echo "Run through sudo as the operator who will publish firmware." >&2
  exit 1
fi
if ! systemctl is-active --quiet fabricd.service && [[ -n $(ss -H -ltn '( sport = :8080 )') ]]; then
  echo "Port 8080 is already in use outside fabricd.service. Stop the existing Fabric tmux process before enabling the systemd service." >&2
  exit 1
fi
if ! getent passwd fabric >/dev/null; then
  useradd --system --user-group --home-dir /var/lib/fabric --shell /usr/sbin/nologin fabric
fi
install -d -o root -g fabric -m 0750 /etc/fabric /etc/fabric/pages
install -d -o fabric -g fabric -m 0750 /var/lib/fabric /var/lib/fabric/firmware
install -d -o "$operator" -g "$(id -gn "$operator")" -m 0700 "$operator_dir/.config/fabric"
install -d -o "$operator" -g "$(id -gn "$operator")" -m 0700 "$operator_dir/.config/fabric/nodes"
if systemctl is-active --quiet fabricd.service; then
  systemctl stop fabricd.service
fi
install -o root -g root -m 0755 "$project_dir/deploy/bin/fabricd" /usr/local/bin/fabricd
install -o root -g root -m 0755 "$project_dir/deploy/bin/fabricctl" /usr/local/bin/fabricctl
rm -f /etc/fabric/pages/*.json
for page in "$project_dir"/pages/*.json; do
  install -o root -g fabric -m 0640 "$page" "/etc/fabric/pages/$(basename "$page")"
done
if [[ ! -e /etc/fabric/fabric.env ]]; then
  install -o root -g fabric -m 0640 "$project_dir/deploy/fabric.env" /etc/fabric/fabric.env
fi
if ! grep -q '^FABRIC_NODES_FILE=' /etc/fabric/fabric.env; then
  echo 'FABRIC_NODES_FILE=/etc/fabric/nodes.json' >> /etc/fabric/fabric.env
fi
if [[ ! -e /etc/fabric/firmware-access.json ]]; then
  /usr/local/bin/fabricctl token-issue --id pi-publisher --role publisher \
    --output "$operator_dir/.config/fabric/publisher.token"
  /usr/local/bin/fabricctl token-issue --id x3-personal --role reader \
    --output "$operator_dir/.config/fabric/x3.token"
  chown "$operator:$(id -gn "$operator")" "$operator_dir/.config/fabric/publisher.token" "$operator_dir/.config/fabric/x3.token"
fi
chown root:fabric /etc/fabric/firmware-access.json
chmod 0640 /etc/fabric/firmware-access.json
if [[ ! -e /etc/fabric/nodes.json ]]; then
  /usr/local/bin/fabricctl node import-firmware --id x3-pocket --type xteink-x3 --name 'Pocket Reader' \
    --credential-id x3-personal --firmware-channel dev
  /usr/local/bin/fabricctl node import-firmware --id pi-home --type raspberry-pi --name 'Pi Publisher' \
    --credential-id pi-publisher
  /usr/local/bin/fabricctl node create --id simulator-browser --type browser --name 'Simulator Browser' \
    --permissions pages.read,actions.invoke --output "$operator_dir/.config/fabric/nodes/simulator.token"
  chown "$operator:$(id -gn "$operator")" "$operator_dir/.config/fabric/nodes/simulator.token"
fi
chown root:fabric /etc/fabric/nodes.json
chmod 0640 /etc/fabric/nodes.json
install -o root -g root -m 0644 "$project_dir/deploy/fabricd.service" /etc/systemd/system/fabricd.service
systemctl daemon-reload
systemctl enable --now fabricd.service
echo "Fabric installed. Tokens are in $operator_dir/.config/fabric; token contents were not printed."
