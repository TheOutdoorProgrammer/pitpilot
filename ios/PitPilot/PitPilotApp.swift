import SwiftUI

enum PitStyle {
    static let amber = Color(red: 1, green: 0.73, blue: 0.24)
    static let panel = Color(red: 0.12, green: 0.14, blue: 0.16)
    static let background = Color(red: 0.055, green: 0.065, blue: 0.075)
}

@main
struct PitPilotApp: App {
    @StateObject private var store = GarageStore()
    var body: some Scene {
        WindowGroup {
            NavigationStack {
                Group {
                    if store.connection == nil { ConnectionView() }
                    else { GarageView() }
                }
            }
            .environmentObject(store)
            .tint(PitStyle.amber)
            .preferredColorScheme(.dark)
        }
    }
}

struct ConnectionView: View {
    @EnvironmentObject private var store: GarageStore
    @State private var server = ""
    @State private var token = ""
    @State private var working = false
    @State private var error: String?
    @FocusState private var tokenFocused: Bool
    var body: some View {
        ScrollView {
                VStack(alignment: .leading, spacing: 30) {
                    VStack(alignment: .leading, spacing: 12) {
                        Image(systemName: "steeringwheel").font(.system(size: 64)).foregroundStyle(PitStyle.amber).accessibilityHidden(true)
                        Text("Your garage.\nEvery mile.").font(.system(.largeTitle, design: .rounded, weight: .black))
                        Text("Maintenance, memories, and the road ahead.").font(.title3).foregroundStyle(.secondary)
                    }.padding(.top, 24)
                    VStack(alignment: .leading, spacing: 16) {
                        Text("CONNECT TO PITPILOT").font(.caption.weight(.bold)).tracking(2).foregroundStyle(PitStyle.amber)
                        TextField("Server URL", text: $server).keyboardType(.URL).textContentType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled().accessibilityIdentifier("serverURL")
                        Divider()
                        SecureField("API token", text: $token).textContentType(.oneTimeCode).textInputAutocapitalization(.never).autocorrectionDisabled().focused($tokenFocused).accessibilityIdentifier("apiToken")
                        Text("Use your self-hosted server's HTTPS address and API token. Your token stays in this device's Keychain.").font(.footnote).foregroundStyle(.secondary)
                    }.padding(22).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 22))
                    if let error { Label(error, systemImage: "exclamationmark.circle").foregroundStyle(.red).font(.callout).accessibilityIdentifier("connectionError") }
                    Button {
                        tokenFocused = false
                        working = true
                        error = nil
                        Task {
                            do { try await store.connect(server: server, token: token) }
                            catch { self.error = error.localizedDescription }
                            working = false
                        }
                    } label: {
                        HStack { if working { ProgressView().tint(.black) }; Text(working ? "Connecting…" : "Open my garage").fontWeight(.bold); Spacer(); Image(systemName: "arrow.right") }.padding(10)
                    }.buttonStyle(.borderedProminent).foregroundStyle(.black).disabled(working || server.isEmpty || token.isEmpty).accessibilityIdentifier("connectButton")
                    Text("Your data lives on your server. Previously loaded vehicles and records are available offline after connecting.").font(.footnote).foregroundStyle(.secondary)
                }.padding(24).frame(maxWidth: 620)
            }.frame(maxWidth: .infinity).background(PitStyle.background).navigationTitle("PitPilot").navigationBarTitleDisplayMode(.inline)
                .onAppear {
                    if server.isEmpty, let configured = Bundle.main.object(forInfoDictionaryKey: "PITPILOT_BASE_URL") as? String, configured.hasPrefix("https://") { server = configured }
                }
    }
}

struct GarageView: View {
    @EnvironmentObject private var store: GarageStore
    private enum Sheet: String, Identifiable { case vehicle, settings; var id: String { rawValue } }
    @State private var sheet: Sheet?
    var body: some View {
        ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    HStack(alignment: .firstTextBaseline) {
                        Text("THE GARAGE").font(.caption.weight(.bold)).tracking(3).foregroundStyle(PitStyle.amber)
                        Spacer()
                        Text("\(store.vehicles.count) \(store.vehicles.count == 1 ? "vehicle" : "vehicles")").font(.caption).foregroundStyle(.secondary)
                    }
                    if store.offline { OfflineBanner(date: store.cache.updatedAt) }
                    if let error = store.error { Text(error).font(.callout).foregroundStyle(.orange) }
                    if store.refreshing && store.vehicles.isEmpty { ProgressView("Opening your garage…").frame(maxWidth: .infinity).padding(40) }
                    else if store.vehicles.isEmpty {
                        ContentUnavailableView {
                            Label("Make room for your first ride", systemImage: "car.side.hill.up")
                        } description: {
                            Text("Add a vehicle to start keeping its service history, fuel stops, and next maintenance in one place.")
                        } actions: { Button("Add a vehicle") { sheet = .vehicle }.buttonStyle(.borderedProminent).controlSize(.large).foregroundStyle(.black).disabled(store.offline).accessibilityIdentifier("emptyAddVehicle") }
                    }
                    ForEach(store.vehicles) { vehicle in
                        NavigationLink(value: vehicle.id) { VehicleCard(vehicle: vehicle) }.buttonStyle(.plain)
                    }
                }.padding(20).frame(maxWidth: 760)
            }.frame(maxWidth: .infinity).background(PitStyle.background)
                .navigationTitle("PitPilot")
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) { Button("Settings", systemImage: "gearshape") { sheet = .settings }.labelStyle(.iconOnly) }
                    ToolbarItem(placement: .topBarTrailing) { Button("Add vehicle", systemImage: "plus") { sheet = .vehicle }.labelStyle(.iconOnly).disabled(store.offline) }
                }
                .navigationDestination(for: String.self) { id in
                    if let vehicle = store.vehicles.first(where: { $0.id == id }) { VehicleDetailView(vehicle: vehicle) }
                }
                .refreshable { await store.refresh() }
                .task { await store.refresh() }
        .sheet(item: $sheet) { item in
            switch item { case .vehicle: AddVehicleView(); case .settings: SettingsView() }
        }
    }
}

struct VehicleCard: View {
    let vehicle: Vehicle
    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 5) {
                    Text(vehicle.name).font(.title2.weight(.bold)).foregroundStyle(.white)
                    Text(vehicle.subtitle.isEmpty ? "Your vehicle" : vehicle.subtitle).font(.subheadline).foregroundStyle(.secondary)
                }
                Spacer()
                Image(systemName: "car.side.fill").font(.system(size: 34)).foregroundStyle(PitStyle.amber).accessibilityHidden(true)
            }
            VStack(alignment: .leading, spacing: 4) {
                Text(vehicle.odometerStatus == "estimated" ? "Estimated odometer" : "Odometer")
                    .font(.caption.weight(.semibold)).foregroundStyle(vehicle.odometerStatus == "estimated" ? PitStyle.amber : .secondary)
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(vehicle.odometerMiles.formatted(.number.precision(.fractionLength(0)))).font(.system(.largeTitle, design: .rounded, weight: .heavy)).monospacedDigit().foregroundStyle(.white)
                    Text("mi").foregroundStyle(.secondary)
                    Spacer()
                    Image(systemName: "arrow.up.right").foregroundStyle(PitStyle.amber).accessibilityHidden(true)
                }
            }
            Rectangle().fill(PitStyle.amber).frame(height: 3)
        }.padding(24).background(LinearGradient(colors: [PitStyle.panel, PitStyle.panel.opacity(0.6)], startPoint: .topLeading, endPoint: .bottomTrailing), in: RoundedRectangle(cornerRadius: 24))
            .accessibilityElement(children: .combine)
    }
}

struct OfflineBanner: View {
    let date: Date?
    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Label("Saved on this phone", systemImage: "wifi.slash").font(.subheadline.weight(.semibold))
            if let date { Text("Last refreshed \(date.formatted(date: .abbreviated, time: .shortened)). Connect to make changes.").font(.caption) }
        }.frame(maxWidth: .infinity, alignment: .leading).padding().background(.orange.opacity(0.12), in: RoundedRectangle(cornerRadius: 14)).foregroundStyle(.orange)
    }
}

struct SettingsView: View {
    @EnvironmentObject private var store: GarageStore
    @Environment(\.dismiss) private var dismiss
    @State private var confirm = false
    @State private var error: String?
    var body: some View {
        NavigationStack {
            Form {
                Section("Your server") {
                    Text(store.connection?.server.absoluteString ?? "Not connected").textSelection(.enabled)
                    Text("API token stored securely in Keychain").font(.footnote).foregroundStyle(.secondary)
                }
                Section("On this phone") {
                    Text("Vehicles, records, reminders, and loaded trips are cached for offline reading. Changes require a server connection. Cached trip locations are protected and excluded from device backups.")
                    Button("Disconnect and clear saved data", role: .destructive) { confirm = true }
                }
                Section("About") {
                    LabeledContent("PitPilot", value: Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "0.1.1")
                    LabeledContent("Build", value: Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "1")
                    Text("A home for every mile.").foregroundStyle(.secondary)
                }
                if let error { Text(error).foregroundStyle(.red) }
            }.navigationTitle("Settings").toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
                .confirmationDialog("Disconnect from this garage?", isPresented: $confirm, titleVisibility: .visible) {
                    Button("Disconnect", role: .destructive) { do { try store.disconnect(); dismiss() } catch { self.error = error.localizedDescription } }
                } message: { Text("Removes the token and saved data from this phone. Your server's data stays intact.") }
        }
    }
}
