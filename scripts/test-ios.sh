#!/usr/bin/env bash
set -euo pipefail
umask 077
# Each background command gets a process group that cancellation can stop.
set -m

cd "$(dirname "$0")/.."
device_type=com.apple.CoreSimulator.SimDeviceType.iPhone-17
device_id=""
active_child=""
derived_data=""

run_command() {
  local status=0
  "$@" &
  active_child=$!
  # Unlike a foreground external command, Bash's wait is interrupted by traps.
  wait "$active_child" || status=$?
  active_child=""
  return "$status"
}

stop_child() {
  if [[ -z "$active_child" ]]; then return; fi
  kill -TERM -- "-$active_child" 2>/dev/null || true
  for _ in {1..20}; do
    if ! kill -0 -- "-$active_child" 2>/dev/null; then break; fi
    sleep 0.1
  done
  kill -KILL -- "-$active_child" 2>/dev/null || true
  wait "$active_child" 2>/dev/null || true
  active_child=""
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  stop_child
  if [[ -n "$device_id" ]]; then
    xcrun simctl shutdown "$device_id" >/dev/null 2>&1 || true
    xcrun simctl delete "$device_id" || echo "Unable to remove test simulator $device_id" >&2
  fi
  if [[ -n "$derived_data" ]]; then rm -rf -- "$derived_data"; fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

run_key="${GITHUB_RUN_ID:-local-$$}-${GITHUB_RUN_ATTEMPT:-1}"
if [[ ! "$run_key" =~ ^(local-)?[0-9]+-[0-9]+$ ]]; then
  echo 'Invalid native test run identifier.' >&2
  exit 1
fi
temporary_root="${RUNNER_TEMP:-${TMPDIR:-/tmp}}"
results_root="$temporary_root/pitpilot-test-results/$run_key"
mkdir -p "$results_root" "$temporary_root/pitpilot-derived-data"
results_directory="$(mktemp -d "$results_root/run-XXXXXXXX")"
derived_data="$(mktemp -d "$temporary_root/pitpilot-derived-data/$run_key-XXXXXXXX")"

run_command ruby scripts/cleanup-ios-orphans.rb

compatible_runtime() {
  xcrun simctl list runtimes --json | jq -r --arg device "$device_type" '
    [.runtimes[]
      | select(.isAvailable == true)
      | select(any(.supportedDeviceTypes[]?; .identifier == $device))]
    | sort_by(.version | split(".") | map(tonumber))
    | last | .identifier // empty'
}

run_command xcodebuild -version
runtime="$(compatible_runtime)"
if [[ -z "$runtime" ]]; then
  echo 'The runner needs an installed iOS runtime supporting iPhone 17.' >&2
  xcrun simctl list runtimes
  exit 1
fi

device_id="$(xcrun simctl create "PitPilot-$run_key" "$device_type" "$runtime")"
echo "Testing with $device_type on $runtime ($device_id)"
# Build all test targets before paying for simulator boot or running any tests.
run_command xcodebuild build-for-testing \
  -project ios/PitPilot.xcodeproj \
  -scheme PitPilot \
  -destination "platform=iOS Simulator,id=$device_id" \
  -destination-timeout 120 \
  -derivedDataPath "$derived_data" \
  -resultBundlePath "$results_directory/Build.xcresult" \
  -parallel-testing-enabled NO
run_command xcrun simctl bootstatus "$device_id" -b
# iOS 26.5 caches disabled accessibility on first boot. Reboot only this fresh
# simulator after enabling it so XCTest receives the AX-loaded notification.
run_command xcrun simctl spawn "$device_id" defaults write com.apple.Accessibility AccessibilityEnabled -bool true
run_command xcrun simctl spawn "$device_id" defaults write com.apple.Accessibility ApplicationAccessibilityEnabled -int 1
run_command xcrun simctl spawn "$device_id" defaults write com.apple.Accessibility AutomationEnabled -int 1
run_command xcrun simctl shutdown "$device_id"
run_command xcrun simctl bootstatus "$device_id" -b
run_command xcodebuild test-without-building \
  -project ios/PitPilot.xcodeproj \
  -scheme PitPilot \
  -destination "platform=iOS Simulator,id=$device_id" \
  -destination-timeout 120 \
  -derivedDataPath "$derived_data" \
  -resultBundlePath "$results_directory/PitPilot.xcresult" \
  -parallel-testing-enabled NO
