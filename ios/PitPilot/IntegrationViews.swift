import SwiftUI
import UniformTypeIdentifiers
import UIKit

struct VehicleIntegrationsView: View {
    @EnvironmentObject private var store: GarageStore
    @StateObject private var model: VehicleIntegrations
    @State private var showingPairForm = false
    @State private var deviceToRevoke: VehicleDevice?
    @State private var disconnectSmartcar = false

    init(vehicleID: String, connection: Connection) {
        _model = StateObject(wrappedValue: VehicleIntegrations(vehicleID: vehicleID, client: APIClient(connection: connection)))
    }

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 24) {
                VStack(alignment: .leading, spacing: 8) {
                    Label("CONNECTED GARAGE", systemImage: "antenna.radiowaves.left.and.right").font(.caption.weight(.bold)).tracking(2).foregroundStyle(PitStyle.amber)
                    Text("Bring your vehicle\ninto the picture.").font(.system(.largeTitle, design: .rounded, weight: .bold))
                    Text("Choose the connection that fits your vehicle. Readings keep their source and the time they were observed.").foregroundStyle(.secondary)
                }.fixedSize(horizontal: false, vertical: true)
                if let error = model.error {
                    Label(error, systemImage: "exclamationmark.circle").foregroundStyle(.orange).accessibilityIdentifier("integrationError")
                    Button("Refresh connections") { Task { await model.refresh() } }.disabled(model.loading || model.working)
                }
                if let message = model.message { Label(message, systemImage: "checkmark.circle").foregroundStyle(PitStyle.amber).accessibilityIdentifier("integrationMessage") }
                if model.loading && !model.loaded { ProgressView("Checking your connections…").frame(maxWidth: .infinity).padding(30) }
                if model.loaded {
                    piSection
                    smartcarSection
                }
            }.padding(20).frame(maxWidth: 760)
        }.frame(maxWidth: .infinity).background(PitStyle.background)
            .navigationTitle("Integrations").navigationBarTitleDisplayMode(.inline)
            .overlay(alignment: .topTrailing) { if model.working { ProgressView().padding().accessibilityLabel("Updating connection") } }
            .task { await model.refresh() }
            .refreshable { if !model.working { await model.refresh() } }
            .onDisappear { model.leave() }
            .sheet(isPresented: $showingPairForm, onDismiss: { model.pairing = nil }) { PairDeviceForm(model: model) }
            .confirmationDialog("Revoke this Pi's access?", isPresented: Binding(get: { deviceToRevoke != nil }, set: { if !$0 { deviceToRevoke = nil } }), titleVisibility: .visible) {
                if let device = deviceToRevoke { Button("Revoke access", role: .destructive) { deviceToRevoke = nil; Task { await model.revoke(device) } }.accessibilityIdentifier("confirmRevokePi") }
            } message: { Text("The device will stop sending new data to this garage. Saved readings stay here. Pair it again if you want to restore access.") }
            .confirmationDialog("Disconnect Smartcar?", isPresented: $disconnectSmartcar, titleVisibility: .visible) {
                Button("Disconnect Smartcar", role: .destructive) { Task { await model.disconnect() } }.accessibilityIdentifier("confirmDisconnectSmartcar")
            } message: { Text("Stops updates for this garage and keeps saved readings. This does not revoke your manufacturer's account access or disconnect other apps.") }
    }

    private var piSection: some View {
        VStack(alignment: .leading, spacing: 16) {
            Label("Raspberry Pi", systemImage: "cpu").font(.title2.weight(.bold))
            Text("Collect readings with a Pi and a supported OBD adapter. Pairing grants access; the first check-in confirms the collector has reached your server.").font(.subheadline).foregroundStyle(.secondary)
            if model.devices.isEmpty { Text("No Pi paired with this vehicle yet.").font(.callout) }
            ForEach(model.devices) { device in
                TimelineView(.periodic(from: .now, by: 60)) { context in
                    VStack(alignment: .leading, spacing: 12) {
                        Text(device.name).font(.headline)
                        Label(device.status(at: context.date), systemImage: device.revokedAt == nil ? "clock.arrow.circlepath" : "xmark.shield")
                            .font(.subheadline.weight(.medium)).foregroundStyle(device.revokedAt == nil ? PitStyle.amber : .secondary)
                        if let seen = SignalFormat.date(device.lastSeenAt ?? "") {
                            LabeledContent("Last check-in", value: seen.formatted(date: .abbreviated, time: .shortened))
                            Text("A check-in confirms contact with the collector, not the vehicle's ignition or live sensor availability.").font(.caption).foregroundStyle(.secondary)
                        }
                        if let version = device.version, !version.isEmpty { LabeledContent("Collector version", value: version) }
                        if let observed = SignalFormat.date(device.lastObservedAt ?? "") {
                            LabeledContent("Last reading", value: observed.formatted(date: .abbreviated, time: .shortened))
                        }
                        if let uploaded = SignalFormat.date(device.lastUploadAt ?? "") {
                            LabeledContent("Last upload", value: uploaded.formatted(date: .abbreviated, time: .shortened))
                        }
                        if let queued = device.queuedBatches { LabeledContent("Queued batches", value: queued.formatted()) }
                        if let rejected = device.rejectedSamples, rejected > 0 { Label("\(rejected.formatted()) samples rejected", systemImage: "exclamationmark.triangle").foregroundStyle(.orange) }
                        if let state = device.collectionState { LabeledContent("Collection", value: IntegrationLabel.collection(state)) }
                        if let state = device.updateState { LabeledContent("Updates", value: IntegrationLabel.update(state)) }
                        if device.revokedAt == nil {
                            Toggle("Automatic updates", isOn: Binding(get: { device.autoUpdate }, set: { value in Task { await model.autoUpdate(device, enabled: value) } }))
                                .disabled(model.working).accessibilityIdentifier("deviceAutoUpdate-\(device.id)")
                            Text("Allow the collector to install signed updates when it checks in. Turning this off pauses automatic installation.").font(.caption).foregroundStyle(.secondary)
                            if device.gpsRecording != nil {
                                Toggle("Record GPS trips", isOn: Binding(get: { device.gpsRecording == true }, set: { value in Task { await model.recordGPS(device, enabled: value) } }))
                                    .disabled(model.working || store.offline).accessibilityIdentifier("deviceGPS-\(device.id)")
                                Text("Save this vehicle's location and routes from a supported USB GPS receiver connected to the Pi. No phone location permission is needed. Turning this off stops new collection after the Pi checks in; saved routes remain.")
                                    .font(.caption).foregroundStyle(.secondary)
                            } else {
                                Text("Update your PitPilot server to enable GPS trip recording.").font(.caption).foregroundStyle(.secondary)
                            }
                            if let state = device.gpsState {
                                LabeledContent("GPS", value: GPSStatus.label(state))
                            }
                            Button("Revoke access", role: .destructive) { deviceToRevoke = device }.disabled(model.working).accessibilityIdentifier("revokePi-\(device.id)")
                        }
                    }.font(.subheadline).padding(16).background(PitStyle.background.opacity(0.6), in: RoundedRectangle(cornerRadius: 16))
                }
            }
            Button { showingPairForm = true } label: { Label("Pair a Pi", systemImage: "plus.circle.fill").frame(maxWidth: .infinity).padding(6) }
                .buttonStyle(.borderedProminent).foregroundStyle(.black).disabled(model.working).accessibilityIdentifier("pairPi")
        }.padding(20).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 22))
    }

    private var smartcarSection: some View {
        VStack(alignment: .leading, spacing: 16) {
            Label("Smartcar", systemImage: "car.side.and.exclamationmark").font(.title2.weight(.bold))
            if let config = model.configuration {
                if config.mode == "simulated" { Label("Simulated vehicle data", systemImage: "testtube.2").font(.subheadline.weight(.semibold)).foregroundStyle(.orange) }
                if !config.configured {
                    Text("Smartcar is not configured on your server yet.").font(.headline)
                    Text("Your server administrator needs to configure the Smartcar application before you can link a vehicle.").font(.subheadline).foregroundStyle(.secondary)
                } else if let status = model.smartcar {
                    Text(status.title).font(.headline).foregroundStyle(PitStyle.amber).accessibilityIdentifier("smartcarStatus")
                    Text(status.explanation).font(.subheadline).foregroundStyle(.secondary)
                    if let observed = SignalFormat.date(status.latestObservedAt ?? "") { LabeledContent("Latest observation", value: observed.formatted(date: .abbreviated, time: .shortened)) }
                    if let success = SignalFormat.date(status.lastSuccessAt ?? "") { LabeledContent("Last successful check", value: success.formatted(date: .abbreviated, time: .shortened)) }
                    if let next = SignalFormat.date(status.nextAttemptAt ?? "") { LabeledContent("Next check", value: next.formatted(date: .abbreviated, time: .shortened)) }
                    if !model.candidates.isEmpty {
                        Text("Choose the vehicle for this garage").font(.headline)
                        ForEach(model.candidates) { candidate in
                            Button { Task { await model.bind(candidate) } } label: {
                                HStack { Text(candidate.title.isEmpty ? "Vehicle details unavailable" : candidate.title); Spacer(); Image(systemName: "chevron.right") }.padding(12)
                            }.buttonStyle(.bordered).disabled(model.working).accessibilityIdentifier("smartcarCandidate-\(candidate.id)")
                        }
                    } else if config.connectAvailable && ["disconnected", "awaiting_authorization", "awaiting_selection", "reconnect_required"].contains(status.state) {
                        Button(status.state == "reconnect_required" ? "Reconnect Smartcar" : "Connect Smartcar") { Task { await model.connect(reconnect: status.state == "reconnect_required") } }
                            .buttonStyle(.borderedProminent).foregroundStyle(.black).disabled(model.working).accessibilityIdentifier("connectSmartcar")
                    } else if !config.connectAvailable { Text("Sign-in is not available on this server. Existing connections can still show their status.").font(.subheadline).foregroundStyle(.secondary) }
                    if status.canSync { Button("Check for updates") { Task { await model.sync() } }.disabled(model.working).accessibilityIdentifier("syncSmartcar") }
                    if !status.supportedMetrics.isEmpty {
                        DisclosureGroup("Available readings (\(status.supportedMetrics.count))") {
                            ForEach(status.supportedMetrics, id: \.self) { metric in Text(IntegrationLabel.metric(metric)).frame(maxWidth: .infinity, alignment: .leading).padding(.vertical, 3) }
                        }
                    }
                    if let count = status.unavailableSignals, count > 0 { Text("\(count) signals currently unavailable.").font(.caption).foregroundStyle(.secondary) }
                    if let count = status.unsupportedSignals, count > 0 { Text("\(count) signals are not supported by this vehicle.").font(.caption).foregroundStyle(.secondary) }
                    if status.canDisconnect { Button("Disconnect Smartcar", role: .destructive) { disconnectSmartcar = true }.disabled(model.working).accessibilityIdentifier("disconnectSmartcar") }
                }
            }
        }.font(.subheadline).padding(20).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 22))
    }
}

enum GPSStatus {
    static func label(_ state: String) -> String {
        switch state {
        case "disabled": "Recording off"
        case "disconnected": "GPS receiver not connected"
        case "waiting_clock": "Waiting for an accurate clock"
        case "waiting_fix": "Waiting for a satellite fix"
        case "fix": "Receiving GPS fixes"
        case "queue_full": "Upload queue full"
        case "paused": "Recording paused"
        default: "Status unavailable"
        }
    }
}

private struct PairDeviceForm: View {
    @ObservedObject var model: VehicleIntegrations
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    var body: some View {
        if let grant = model.pairing { PairingTokenView(grant: grant) }
        else {
            NavigationStack {
                Form {
                    Section("Name your collector") { TextField("For example, Truck Pi", text: $name).accessibilityIdentifier("piName") }
                    Section { Text("Creates a short-lived pairing token for the PitPilot collector. Keep the token private and use it only on your Pi.") }
                    if let error = model.error { Text(error).foregroundStyle(.orange) }
                }.navigationTitle("Pair a Pi").toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() }.disabled(model.working) }
                    ToolbarItem(placement: .confirmationAction) {
                        if model.working { ProgressView().accessibilityLabel("Creating pairing token") }
                        else {
                            Button("Create token") { Task { await model.pair(name: name) } }
                                .disabled(name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || name.utf8.count > 200)
                        }
                    }
                }
            }.interactiveDismissDisabled(model.working)
        }
    }
}

private struct PairingTokenView: View {
    let grant: DevicePairing
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    @State private var copied = false
    var body: some View {
        NavigationStack {
            TimelineView(.periodic(from: .now, by: 1)) { context in
                let valid = grant.expiration.map { $0 > context.date } ?? false
                ScrollView {
                    VStack(alignment: .leading, spacing: 22) {
                        Image(systemName: valid ? "key.horizontal.fill" : "clock.badge.exclamationmark").font(.system(size: 46)).foregroundStyle(PitStyle.amber).accessibilityHidden(true)
                        Text(valid ? "Ready for your Pi." : "This token has expired.").font(.system(.largeTitle, design: .rounded, weight: .bold))
                        Text(valid ? "Save this one-time token on your Pi in /root/pitpilot-enrollment with permissions 0600. It is shown only here and is not saved on this phone." : "Close this screen, revoke the pending device, and create a new pairing token.").foregroundStyle(.secondary)
                        if valid {
                            if scenePhase == .active { Text(grant.enrollmentToken).font(.system(.body, design: .monospaced)).textSelection(.enabled).privacySensitive().accessibilityIdentifier("pairingToken") }
                            else { Text("Pairing token hidden").foregroundStyle(.secondary) }
                            if let expiration = grant.expiration { Text("Expires \(expiration.formatted(date: .omitted, time: .shortened))").font(.caption) }
                            Button(copied ? "Token copied" : "Copy token") {
                                UIPasteboard.general.setItems([[UTType.utf8PlainText.identifier: grant.enrollmentToken]], options: [.localOnly: true, .expirationDate: grant.expiration ?? .now])
                                copied = true
                            }.buttonStyle(.borderedProminent).foregroundStyle(.black).disabled(scenePhase != .active)
                            Text("After installing and configuring the collector, run:").font(.callout)
                            Text("sudo picollector enroll --config /etc/pitpilot/collector.json --token-file /root/pitpilot-enrollment").font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                        }
                        Link("Collector setup guide", destination: URL(string: "https://github.com/TheOutdoorProgrammer/pitpilot/blob/main/docs/picollector.md")!)
                        Text("The device stays in “Waiting for pairing” until enrollment succeeds. A token alone does not mean it is collecting readings.").font(.callout).foregroundStyle(.secondary)
                    }.padding(24).frame(maxWidth: 620)
                }.frame(maxWidth: .infinity).background(PitStyle.background)
            }.navigationTitle(grant.device.name).navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
        }
    }
}

enum IntegrationLabel {
    static func metric(_ metric: String) -> String {
        ["odometer_miles": "Odometer", "fuel_level_pct": "Fuel level", "battery_soc_pct": "Battery charge", "oil_life_pct": "Oil life", "engine_rpm": "Engine speed", "tire_pressure_fl_psi": "Front left tire pressure", "tire_pressure_fr_psi": "Front right tire pressure", "tire_pressure_rl_psi": "Rear left tire pressure", "tire_pressure_rr_psi": "Rear right tire pressure"][metric] ?? metric.replacingOccurrences(of: "_", with: " ").capitalized
    }
    static func collection(_ state: String) -> String {
        switch state {
        case "starting": "Collector starting"
        case "waiting_clock": "Waiting for a reliable clock"
        case "collecting": "Collecting readings"
        case "adapter_unavailable": "Waiting for adapter"
        case "paused": "Collection paused"
        case "queue_full": "Upload queue full"
        default: "Not reported"
        }
    }
    static func update(_ state: String) -> String {
        switch state {
        case "idle": "No update in progress"
        case "healthy": "Current version healthy"
        case "checking": "Checking for updates"
        case "downloading": "Downloading update"
        case "applying": "Installing update"
        case "staged": "Update ready to install"
        case "rolled_back": "Previous version restored"
        case "failed": "Update needs attention"
        case "disabled": "Automatic updates paused"
        default: "Not reported"
        }
    }
}
