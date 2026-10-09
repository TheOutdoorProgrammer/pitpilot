#!/usr/bin/env bash
set +x
set -euo pipefail
umask 077

# Run from a detached systemd service. SSH loss must not leave the adapter idle.
state=/var/lib/pitpilot-handoff
reader=/opt/pitpilot/picollector/current
config=/etc/pitpilot/collector.json
old_unit=/etc/systemd/system/sonoma-collector.service
action=${1:-}
if [[ $EUID != 0 || $# != 1 || ! $action =~ ^(start|accept|rollback|recover)$ ]]; then
  echo 'Usage: sudo picollector-sonoma-handoff.sh start|accept|rollback|recover' >&2
  exit 1
fi
for command in jq flock systemctl systemd-run timeout tar runuser; do
  command -v "$command" >/dev/null || { echo "Missing $command" >&2; exit 1; }
done
install -d -m 0700 "$state"
exec 9>"$state/lock"
flock -w 60 9

phase() {
  printf '%s\n' "$1" > "$state/phase.new"
  sync -f "$state/phase.new"
  mv "$state/phase.new" "$state/phase"
  sync -f "$state"
}

# shellcheck disable=SC2329 # Invoked indirectly by the EXIT trap.
finish() {
  local result=$1
  trap - EXIT
  if (( result != 0 )); then rollback; fi
  exit "$result"
}

rollback() {
  systemctl disable --now picollector-update.timer
  systemctl stop picollector-update.service picollector.service
  [[ $(systemctl show picollector.service -p MainPID --value) == 0 ]]
  systemctl disable picollector.service
  if [[ -f $state/retired-sonoma-collector.service ]]; then
    systemctl unmask sonoma-collector.service
    install -m 0644 "$state/retired-sonoma-collector.service" "$old_unit"
    systemctl daemon-reload
  fi
  # Resume the live old queue, never restore a snapshot over acknowledged data.
  systemctl enable --now sonoma-collector.service
  systemctl is-active --quiet sonoma-collector.service
  phase rolled_back
  echo 'Old collector resumed. Native queue and enrollment retained for recovery.'
}

if [[ $action == recover ]]; then
  [[ -f $state/phase && $(<"$state/phase") == awaiting_acceptance ]] || exit 0
  rollback
  exit 0
fi
if [[ $action == rollback ]]; then
  rollback
  exit 0
fi
if [[ $action == accept ]]; then
  [[ -f $state/phase && $(<"$state/phase") == awaiting_acceptance ]]
  systemctl is-active --quiet picollector.service
  [[ $(systemctl show sonoma-collector.service -p MainPID --value) == 0 ]]
  systemctl enable picollector.service sonoma-rfcomm.service
  systemctl enable --now picollector-update.timer
  systemctl disable sonoma-collector.service
  if [[ -f $old_unit && ! -L $old_unit ]]; then
    mv "$old_unit" "$state/retired-sonoma-collector.service"
  fi
  systemctl mask sonoma-collector.service
  systemctl daemon-reload
  phase accepted
  systemctl stop picollector-handoff-recovery.timer
  echo 'Native collector and automatic updates enabled; old reader retired.'
  exit 0
fi

[[ ! -e $state/phase ]] || { echo 'Existing handoff state requires inspection, not another start.' >&2; exit 1; }
[[ -x $reader && -f $config && -f /var/lib/pitpilot-collector/identity.json ]]
[[ -f $old_unit && ! -L $old_unit ]]
[[ -f /etc/systemd/system/picollector.service.d/sonoma-handoff.conf ]]
systemctl is-active --quiet sonoma-collector.service
[[ $(systemctl show picollector.service -p MainPID --value) == 0 ]]
[[ $(jq -r .stateDirectory "$config") == /var/lib/pitpilot-collector ]]
queue=$(jq -er '.queue_path | select(startswith("/"))' /etc/sonoma/collector.json)
[[ -f $queue && ! -L $queue ]]
install -m 0755 "${BASH_SOURCE[0]}" /opt/pitpilot/picollector/handoff
phase awaiting_acceptance
trap 'finish "$?"' EXIT
systemd-run --unit=picollector-handoff-recovery --on-active=10min \
  /opt/pitpilot/picollector/handoff recover
systemctl stop sonoma-collector.service
[[ $(systemctl show sonoma-collector.service -p MainPID --value) == 0 ]]
tar -czf "$state/legacy-backup.tar.gz" -- \
  "$queue" /etc/sonoma /usr/local/bin/sonoma-collector "$old_unit" \
  /etc/systemd/system/sonoma-collector.service.d
sync -f "$state/legacy-backup.tar.gz"

# Upload-only opens no adapter and keeps the old event identity and sequence.
drained=false
deadline=$((SECONDS + 240))
while (( SECONDS < deadline )); do
  if timeout --kill-after=3 35 runuser -u sonoma -- \
    /usr/local/bin/sonoma-collector -config /etc/sonoma/collector.json -upload-only \
    > "$state/drain.log" 2>&1; then
    if jq -e -s 'any(.[]; .msg == "upload batch finished" and .count == 0)' "$state/drain.log" >/dev/null; then
      drained=true
      break
    fi
  fi
  sleep 3
done
[[ $drained == true ]] || { echo 'Old queue did not drain; retaining the old collector.' >&2; exit 1; }
systemctl start sonoma-rfcomm.service
systemctl start picollector.service
for _ in {1..12}; do
  pid=$(systemctl show picollector.service -p MainPID --value)
  if [[ $pid != 0 && -f /var/lib/pitpilot-collector/health.json ]] && \
    jq -e --argjson pid "$pid" '.pid == $pid and .queueReady == true' \
      /var/lib/pitpilot-collector/health.json >/dev/null; then
    echo 'Native reader started. Verify server observations and both delivery sinks, then accept within the recovery window.'
    exit 0
  fi
  sleep 5
done
echo 'Native reader did not become ready.' >&2
exit 1
