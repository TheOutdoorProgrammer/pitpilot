#!/usr/bin/env bash
set -euo pipefail

# Bootstrap only. Starting collection is a separate exclusive-adapter handoff.
if [[ $EUID -ne 0 || $# -ne 1 ]]; then
  echo 'Usage: sudo scripts/picollector-install.sh /path/to/verified/picollector' >&2
  exit 1
fi
source_binary=$1
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
if [[ ! -f $source_binary || ! -x $source_binary ]]; then
  echo 'A verified executable is required.' >&2
  exit 1
fi
if [[ -e /opt/pitpilot/picollector/current || -L /opt/pitpilot/picollector/current ]]; then
  echo 'Already installed; use the signed updater.' >&2
  exit 1
fi
if ! id pitpilot-collector >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/pitpilot-collector --no-create-home --shell /usr/sbin/nologin pitpilot-collector
fi
install -d -m 0755 /opt/pitpilot/picollector/releases/bootstrap /var/lib/pitpilot-updater
install -d -m 0700 -o pitpilot-collector -g pitpilot-collector /var/lib/pitpilot-collector
install -d -m 0750 -o root -g pitpilot-collector /etc/pitpilot
install -m 0755 -o root -g root "$source_binary" /opt/pitpilot/picollector/releases/bootstrap/picollector
# Recovery must remain executable even when the newly activated reader cannot start.
install -m 0755 -o root -g root "$source_binary" /opt/pitpilot/picollector/updater
ln -s /opt/pitpilot/picollector/releases/bootstrap/picollector /opt/pitpilot/picollector/current
for unit in picollector.service picollector-update.service picollector-update.timer; do
  install -m 0644 -o root -g root "$script_dir/../deploy/picollector/$unit" "/etc/systemd/system/$unit"
done
systemctl daemon-reload
echo 'Installed without starting. Install private configuration, enroll with sudo and a private token file, and complete exclusive adapter handoff before enabling services.'
