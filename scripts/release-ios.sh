#!/usr/bin/env bash
set -euo pipefail
umask 077

: "${RUNNER_TEMP:?}" "${APPLE_TEAM_ID:?}" "${PROFILE_NAME:?}" "${PROFILE_UUID:?}"
: "${SIGNING_KEYCHAIN:?}" "${SIGNING_KEYCHAIN_PASSWORD:?}" "${BUILD_NUMBER:?}"

archive="$RUNNER_TEMP/PitPilot.xcarchive"
export_dir="$RUNNER_TEMP/pitpilot-export"
signing_config="$RUNNER_TEMP/pitpilot-signing.xcconfig"
export_options="$RUNNER_TEMP/pitpilot-export.plist"
archive_keychain_state="$RUNNER_TEMP/pitpilot-archive-keychain-state.json"
ruby scripts/release-keychain.rb save "$archive_keychain_state"
trap 'ruby scripts/release-keychain.rb restore "$archive_keychain_state"' EXIT
security unlock-keychain -p "$SIGNING_KEYCHAIN_PASSWORD" "$SIGNING_KEYCHAIN"
security default-keychain -d user -s "$SIGNING_KEYCHAIN"

cat > "$signing_config" <<EOF
DEVELOPMENT_TEAM[sdk=iphoneos*] = $APPLE_TEAM_ID
CODE_SIGN_STYLE[sdk=iphoneos*] = Manual
CODE_SIGN_IDENTITY[sdk=iphoneos*] = Apple Distribution
PROVISIONING_PROFILE_SPECIFIER[sdk=iphoneos*] = $PROFILE_NAME
OTHER_CODE_SIGN_FLAGS[sdk=iphoneos*] = --keychain $SIGNING_KEYCHAIN
EOF

xcodebuild archive \
  -project ios/PitPilot.xcodeproj \
  -scheme PitPilot \
  -archivePath "$archive" \
  -destination 'generic/platform=iOS' \
  -xcconfig "$signing_config" \
  CURRENT_PROJECT_VERSION="$BUILD_NUMBER" \
  PITPILOT_BASE_URL="${PITPILOT_BASE_URL:-}" \
  FLEDGE_BASE_URL="${FLEDGE_BASE_URL:-}"

cat > "$export_options" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>method</key><string>release-testing</string>
  <key>teamID</key><string>$APPLE_TEAM_ID</string>
  <key>signingStyle</key><string>manual</string>
  <key>signingCertificate</key><string>Apple Distribution</string>
  <key>provisioningProfiles</key><dict>
    <key>com.theoutdoorprogrammer.pitpilot</key><string>$PROFILE_NAME</string>
  </dict>
  <key>destination</key><string>export</string>
  <key>manageAppVersionAndBuildNumber</key><false/>
</dict></plist>
EOF

xcodebuild -exportArchive -archivePath "$archive" \
  -exportPath "$export_dir" -exportOptionsPlist "$export_options"

unpacked="$RUNNER_TEMP/pitpilot-verified"
ditto -x -k "$export_dir/PitPilot.ipa" "$unpacked"
app="$unpacked/Payload/PitPilot.app"
profile="$RUNNER_TEMP/pitpilot-profile.plist"
entitlements="$RUNNER_TEMP/pitpilot-entitlements.plist"
codesign --verify --deep --strict "$app"
codesign -d --entitlements :- "$app" > "$entitlements" 2>/dev/null
security cms -D -i "$app/embedded.mobileprovision" > "$profile"
test "$(plutil -extract UUID raw -o - "$profile")" = "$PROFILE_UUID"
test "$(plutil -extract Entitlements.application-identifier raw -o - "$profile")" = "$APPLE_TEAM_ID.com.theoutdoorprogrammer.pitpilot"
test "$(plutil -extract Entitlements.get-task-allow raw -o - "$profile")" = false
test "$(plutil -extract application-identifier raw -o - "$entitlements")" = "$APPLE_TEAM_ID.com.theoutdoorprogrammer.pitpilot"
test "$(plutil -extract get-task-allow raw -o - "$entitlements")" = false
test "$(plutil -extract CFBundleVersion raw -o - "$app/Info.plist")" = "$BUILD_NUMBER"
echo 'Verified PitPilot Ad Hoc archive and build number.'
