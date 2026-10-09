import SwiftUI

struct GarageMenuView: View {
    @EnvironmentObject private var store: GarageStore
    @Environment(\.dismiss) private var dismiss
    var vehicle: Vehicle? = nil
    @State private var settings = false
    @State private var editing = false
    @State private var confirmClearGPS = false
    @State private var clearingGPS = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Label("Your garage, connected", systemImage: "steeringwheel")
                        .font(.headline).foregroundStyle(PitStyle.amber).padding(.vertical, 10)
                }
                Section("Integrations") {
                    if let connection = store.connection {
                        if let vehicle {
                            NavigationLink {
                                VehicleIntegrationsView(vehicleID: vehicle.id, connection: connection)
                            } label: {
                                Label("Vehicle integrations", systemImage: "antenna.radiowaves.left.and.right")
                            }.accessibilityIdentifier("vehicleIntegrations")
                            Text("Pi collection and Smartcar for \(vehicle.name)").font(.caption).foregroundStyle(.secondary)
                        } else {
                            ForEach(store.vehicles) { vehicle in
                                NavigationLink {
                                    VehicleIntegrationsView(vehicleID: vehicle.id, connection: connection)
                                } label: { Label(vehicle.name, systemImage: "car.side") }
                            }
                            if store.vehicles.isEmpty { Text("Add a vehicle to pair a Pi or connect Smartcar.").foregroundStyle(.secondary) }
                        }
                    }
                }
                Section("Manage") {
                    if vehicle != nil {
                        Button { editing = true } label: { Label("Edit vehicle", systemImage: "pencil") }.disabled(store.offline)
                    }
                    Button { settings = true } label: { Label("Settings", systemImage: "gearshape") }.accessibilityIdentifier("menuSettings")
                }
                if vehicle != nil {
                    Section("Location privacy") {
                        Button("Delete GPS location history", role: .destructive) { confirmClearGPS = true }
                            .disabled(store.offline || clearingGPS).accessibilityIdentifier("clearGPSHistory")
                        Text("Remove saved native GPS fixes and automatic trips. Turn off Record GPS trips in integrations to stop future collection.").font(.caption).foregroundStyle(.secondary)
                    }
                }
                if let error { Text(error).foregroundStyle(.orange) }
            }
            .scrollContentBackground(.hidden).background(PitStyle.background)
            .navigationTitle("Garage menu").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
            .sheet(isPresented: $settings) { SettingsView() }
            .sheet(isPresented: $editing) { if let vehicle { AddVehicleView(vehicle: store.vehicles.first { $0.id == vehicle.id } ?? vehicle) } }
            .confirmationDialog("Delete GPS location history?", isPresented: $confirmClearGPS, titleVisibility: .visible) {
                Button("Delete saved GPS history", role: .destructive) {
                    guard let vehicle else { return }
                    clearingGPS = true
                    Task {
                        do { try await store.clearLocationHistory(vehicle.id); dismiss() }
                        catch { self.error = error.localizedDescription; clearingGPS = false }
                    }
                }
            } message: { Text("Permanently removes native GPS fixes and automatic trips. Odometer readings and other trip sources stay unchanged. New GPS fixes can still arrive while recording is enabled.") }
        }
    }
}
