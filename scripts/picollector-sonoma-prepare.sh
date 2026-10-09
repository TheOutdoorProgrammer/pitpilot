#!/usr/bin/env bash
set +x
set -euo pipefail
umask 077

if [[ $EUID != 0 || $# != 4 ]]; then
  echo 'Usage: sudo picollector-sonoma-prepare.sh VERIFIED_BINARY SHA256 ENROLLMENT_TOKEN_FILE SERVER_URL' >&2
  exit 1
fi
binary=$1
digest=$2
token=$3
server=$4
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
[[ $digest =~ ^[a-f0-9]{64}$ && $server == https://* ]]
[[ $(uname -m) == aarch64 ]] || { echo 'This handoff supports the ARM64 Sonoma installation.' >&2; exit 1; }
command -v jq >/dev/null
printf '%s  %s\n' "$digest" "$binary" | sha256sum --check --status
[[ -f /etc/sonoma/collector.json && -f /etc/sonoma/telemetry.env ]]
[[ ! -e /etc/pitpilot/collector.json && ! -e /var/lib/pitpilot-collector/identity.json ]]
legacy_token=$(jq -er '.token_file | select(startswith("/"))' /etc/sonoma/collector.json)
[[ -f $legacy_token && ! -L $legacy_token ]]
bash "$script_dir/picollector-install.sh" "$binary"
install -m 0600 -o pitpilot-collector -g pitpilot-collector "$legacy_token" /etc/pitpilot/legacy-token
install -m 0600 -o root -g root /etc/sonoma/telemetry.env /etc/pitpilot/telemetry.env
jq --arg server "$server" '{
  server: $server, serialPort: .serial_port, baud: .baud,
  pollSeconds: .poll_seconds, queueLimit: .queue_limit,
  stateDirectory: "/var/lib/pitpilot-collector", timeSyncMarker: .time_sync_marker,
  updateManifestUrl: "https://github.com/TheOutdoorProgrammer/pitpilot/releases/latest/download/picollector_linux_arm64.update.json",
  legacy: {endpoint: .upload_url, deviceId: .device_id, tokenFile: "/etc/pitpilot/legacy-token"}
}' /etc/sonoma/collector.json > /etc/pitpilot/collector.json
chown root:pitpilot-collector /etc/pitpilot/collector.json
chmod 0640 /etc/pitpilot/collector.json
install -d -m 0755 /etc/systemd/system/picollector.service.d
install -m 0644 "$script_dir/../deploy/picollector/sonoma-handoff.conf" \
  /etc/systemd/system/picollector.service.d/sonoma-handoff.conf
systemctl daemon-reload
systemd-analyze verify picollector.service picollector-update.service picollector-update.timer
/opt/pitpilot/picollector/current enroll --config /etc/pitpilot/collector.json --token-file "$token"
rm -- "$token"
echo 'Prepared and enrolled. Old collector and networking are unchanged.'
