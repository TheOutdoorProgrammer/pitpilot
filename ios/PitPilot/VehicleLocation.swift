import SwiftUI
import MapKit

struct VehicleLocationEnvelope: Codable {
    var location: VehicleLocation?
}

struct VehicleLocation: Codable {
    let latitude: Double
    let longitude: Double
    let recordedAt: String
    let source: String
    var speedKph: Double?
    var courseDegrees: Double?
    var altitudeMeters: Double?
    var satellites: Int?
    var hdop: Double?
    var fixQuality: Int?
    var accuracyMeters: Double?
    var coordinate: CLLocationCoordinate2D { CLLocationCoordinate2D(latitude: latitude, longitude: longitude) }
    func isStale(at now: Date) -> Bool {
        guard let time = SignalFormat.date(recordedAt) else { return true }
        return now.timeIntervalSince(time) > 15 * 60
    }
}

struct VehicleLocationView: View {
    @EnvironmentObject private var store: GarageStore
    let vehicleID: String
    @State private var fullMap = false
    private var fix: VehicleLocation? { store.cache.locations?[vehicleID]?.location }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label("Last known location", systemImage: "location").font(.headline)
                Spacer()
                if store.locationsRefreshing.contains(vehicleID) { ProgressView().accessibilityLabel("Refreshing location") }
            }
            if let fix, CLLocationCoordinate2DIsValid(fix.coordinate) {
                ZStack {
                    Map(initialPosition: .region(MKCoordinateRegion(center: fix.coordinate, latitudinalMeters: 1600, longitudinalMeters: 1600)), interactionModes: []) {
                        Marker("Last known location", coordinate: fix.coordinate).tint(PitStyle.amber)
                    }.id(fix.recordedAt).allowsHitTesting(false).accessibilityHidden(true)
                    // The disabled Map cannot supply the button's interactive surface.
                    Button { fullMap = true } label: {
                        ZStack(alignment: .topTrailing) {
                            Rectangle().fill(.clear).contentShape(Rectangle())
                            Image(systemName: "arrow.up.left.and.arrow.down.right")
                                .padding(10).background(.ultraThinMaterial, in: Circle()).padding(10)
                        }
                    }.buttonStyle(.plain).accessibilityLabel("Open last known vehicle location").accessibilityIdentifier("vehicleLocationMap")
                }.frame(height: 190).clipShape(RoundedRectangle(cornerRadius: 16))
                TimelineView(.periodic(from: .now, by: 60)) { context in
                    HStack(alignment: .top) {
                        Text(SignalFormat.date(fix.recordedAt)?.formatted(date: .abbreviated, time: .shortened) ?? "Unknown capture time")
                        Spacer()
                        if fix.isStale(at: context.date) { Label("Stale", systemImage: "clock").foregroundStyle(.orange) }
                    }.font(.caption).foregroundStyle(.secondary)
                }
                Text("Reported by \(fix.source == "pi-gps" ? "Pi GPS" : SignalFormat.source(fix.source)). This is a recorded fix, not a live location.")
                    .font(.caption).foregroundStyle(.secondary)
            } else if store.locationErrors[vehicleID] == nil {
                Label("No GPS fix recorded yet", systemImage: "location.slash").foregroundStyle(.secondary)
                Text("A connected GPS source can add location and automatic trips. Bluetooth engine readings alone do not provide a route.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let error = store.locationErrors[vehicleID] {
                Text("Location could not refresh. \(error)").font(.caption).foregroundStyle(.orange)
                Button("Retry location") { Task { await store.refreshLocation(vehicleID) } }.disabled(store.locationsRefreshing.contains(vehicleID))
            }
        }.padding(18).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 20))
            .task { await store.refreshLocation(vehicleID) }
            .sheet(isPresented: $fullMap) {
                if let fix { VehicleLocationMap(fix: fix) }
            }
    }
}

private struct VehicleLocationMap: View {
    @Environment(\.dismiss) private var dismiss
    let fix: VehicleLocation
    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                Map {
                    Marker("Last known location", coordinate: fix.coordinate).tint(PitStyle.amber)
                    if let radius = fix.accuracyMeters, radius > 0 { MapCircle(center: fix.coordinate, radius: radius).foregroundStyle(PitStyle.amber.opacity(0.12)) }
                }.mapControls { MapCompass(); MapScaleView() }
                VStack(alignment: .leading, spacing: 8) {
                    Text("Last known location").font(.headline)
                    Text(SignalFormat.date(fix.recordedAt)?.formatted(date: .abbreviated, time: .shortened) ?? "Capture time unavailable")
                    if let accuracy = fix.accuracyMeters { Text("Reported accuracy: \(accuracy.formatted(.number.precision(.fractionLength(0)))) m") }
                    Text("The vehicle may have moved since this fix. Map tiles require an internet connection.").font(.caption).foregroundStyle(.secondary)
                }.frame(maxWidth: .infinity, alignment: .leading).padding(20).background(PitStyle.panel)
            }.navigationTitle("Vehicle location").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
        }
    }
}
