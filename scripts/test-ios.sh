#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
device_type=com.apple.CoreSimulator.SimDeviceType.iPhone-17
device_id=""

cleanup() {
  status=$?
  trap - EXIT
  if [[ -n "$device_id" ]]; then
    xcrun simctl shutdown "$device_id" >/dev/null 2>&1 || true
    xcrun simctl delete "$device_id" || echo "Unable to remove test simulator $device_id" >&2
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

compatible_runtime() {
  xcrun simctl list runtimes --json | jq -r --arg device "$device_type" '
    [.runtimes[]
      | select(.isAvailable == true)
      | select(any(.supportedDeviceTypes[]?; .identifier == $device))]
    | sort_by(.version | split(".") | map(tonumber))
    | last | .identifier // empty'
}

xcodebuild -version
runtime="$(compatible_runtime)"
if [[ -z "$runtime" ]]; then
  echo 'The runner needs an installed iOS runtime supporting iPhone 17.' >&2
  xcrun simctl list runtimes
  exit 1
fi

device_id="$(xcrun simctl create "PitPilot-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}" "$device_type" "$runtime")"
echo "Testing with $device_type on $runtime ($device_id)"
xcrun simctl bootstatus "$device_id" -b
# iOS 26.5 caches disabled accessibility on first boot. Reboot only this fresh
# simulator after enabling it so XCTest receives the AX-loaded notification.
xcrun simctl spawn "$device_id" defaults write com.apple.Accessibility AccessibilityEnabled -bool true
xcrun simctl spawn "$device_id" defaults write com.apple.Accessibility ApplicationAccessibilityEnabled -int 1
xcrun simctl spawn "$device_id" defaults write com.apple.Accessibility AutomationEnabled -int 1
xcrun simctl shutdown "$device_id"
xcrun simctl bootstatus "$device_id" -b
xcodebuild test \
  -project ios/PitPilot.xcodeproj \
  -scheme PitPilot \
  -destination "platform=iOS Simulator,id=$device_id" \
  -destination-timeout 120 \
  -parallel-testing-enabled NO
