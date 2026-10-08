import SwiftUI
import MapKit

struct VehicleDetailView: View {
    @EnvironmentObject private var store: GarageStore
    @Environment(\.dismiss) private var dismiss
    private let initialVehicle: Vehicle
    private var vehicle: Vehicle { store.vehicles.first(where: { $0.id == initialVehicle.id }) ?? initialVehicle }
    init(vehicle: Vehicle) { initialVehicle = vehicle }
    @State private var selected = "History"
    private enum Sheet: String, Identifiable { case record, reminder, vehicle; var id: String { rawValue } }
    @State private var sheet: Sheet?
    @State private var confirmDeleteVehicle = false
    @State private var deleteRecord: VehicleRecord?
    private var detail: VehicleDetail { store.detail(vehicle.id) }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                VehicleCard(vehicle: vehicle)
                if store.offline { OfflineBanner(date: store.cache.updatedAt) }
                if let error = store.error { Text(error).font(.callout).foregroundStyle(.orange) }
                Picker("Vehicle section", selection: $selected) {
                    ForEach(["History", "Upcoming", "Trips"], id: \.self) { Text($0) }
                }.pickerStyle(.segmented)
                switch selected {
                case "Upcoming": reminders
                case "Trips": trips
                default: history
                }
            }.padding(20).frame(maxWidth: 760)
        }.frame(maxWidth: .infinity).background(PitStyle.background)
            .navigationTitle(vehicle.name).navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Button("Log service or expense", systemImage: "wrench.and.screwdriver") { sheet = .record }
                        Button("Add reminder", systemImage: "calendar.badge.plus") { sheet = .reminder }
                        Button("Edit vehicle", systemImage: "pencil") { sheet = .vehicle }
                        Button("Delete vehicle", systemImage: "trash", role: .destructive) { confirmDeleteVehicle = true }
                    } label: { Image(systemName: "plus").accessibilityLabel("Add to vehicle") }.disabled(store.offline)
                }
            }
            .task { await store.refreshDetail(vehicle.id) }
            .refreshable { await store.refresh(); await store.refreshDetail(vehicle.id) }
            .sheet(item: $sheet) { item in
                switch item {
                case .record: AddRecordView(vehicle: vehicle)
                case .reminder: AddReminderView(vehicle: vehicle)
                case .vehicle: AddVehicleView(vehicle: vehicle)
                }
            }
            .confirmationDialog("Delete \(vehicle.name)?", isPresented: $confirmDeleteVehicle, titleVisibility: .visible) {
                Button("Delete vehicle and history", role: .destructive) {
                    Task {
                        do { try await store.deleteVehicle(vehicle.id); dismiss() }
                        catch { store.error = error.localizedDescription }
                    }
                }
            } message: { Text("This permanently deletes the vehicle, its records, reminders, and trips from your server.") }
            .confirmationDialog("Delete this record?", isPresented: Binding(get: { deleteRecord != nil }, set: { if !$0 { deleteRecord = nil } }), titleVisibility: .visible) {
                if let record = deleteRecord { Button("Delete record", role: .destructive) { Task { await store.deleteRecord(record) }; deleteRecord = nil } }
            } message: { Text("This removes the record from your server and cannot be undone.") }
    }

    private var history: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                Text("The running record").font(.title3.weight(.bold))
                Spacer()
                Button("Log entry") { sheet = .record }.disabled(store.offline)
            }
            if detail.records.isEmpty {
                ContentUnavailableView("A fresh start", systemImage: "wrench.and.screwdriver", description: Text("Log a service, repair, fuel stop, or note. Your vehicle's story starts here."))
            }
            ForEach(detail.records.sorted { $0.date > $1.date }) { record in
                VStack(alignment: .leading, spacing: 12) {
                    HStack(alignment: .top) {
                        Image(systemName: record.kind.symbol).foregroundStyle(PitStyle.amber).frame(width: 26).accessibilityHidden(true)
                        VStack(alignment: .leading, spacing: 4) {
                            Text(record.title).font(.headline)
                            Text("\(record.kind.label) · \(record.date)").font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer(minLength: 4)
                        Text((Double(record.costCents) / 100).formatted(.currency(code: "USD"))).font(.subheadline.weight(.semibold))
                    }
                    if !record.notes.isEmpty { Text(record.notes).font(.subheadline).foregroundStyle(.secondary).textSelection(.enabled) }
                    HStack {
                        Text("\(record.odometerMiles.formatted(.number.precision(.fractionLength(0)))) mi").font(.caption).foregroundStyle(.secondary)
                        if let gallons = record.gallons { Text("· \(gallons.formatted()) gal").font(.caption).foregroundStyle(.secondary) }
                        Spacer()
                        Menu { Button("Delete record", role: .destructive) { deleteRecord = record } } label: { Image(systemName: "ellipsis").frame(width: 44, height: 30).accessibilityLabel("Record actions") }.disabled(store.offline)
                    }
                }.padding(18).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 18))
            }
        }
    }

    private var reminders: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack { Text("Stay ahead").font(.title3.weight(.bold)); Spacer(); Button("Add reminder") { sheet = .reminder }.disabled(store.offline) }
            if detail.reminders.isEmpty { ContentUnavailableView("Nothing on the horizon", systemImage: "calendar", description: Text("Set a due date or mileage for your next service. Reminders appear here when you open the app.")) }
            ForEach(detail.reminders.sorted { !$0.completed && $1.completed }) { reminder in
                HStack(alignment: .top, spacing: 14) {
                    Button { Task { await store.complete(reminder) } } label: {
                        Image(systemName: reminder.completed ? "checkmark.circle.fill" : "circle").font(.title2).frame(width: 44, height: 44)
                    }.accessibilityLabel(reminder.completed ? "Mark \(reminder.title) incomplete" : "Complete \(reminder.title)").disabled(store.offline)
                    VStack(alignment: .leading, spacing: 5) {
                        Text(reminder.title).font(.headline).strikethrough(reminder.completed)
                        if let date = reminder.dueDate, !date.isEmpty { Text("Due \(date)").font(.subheadline).foregroundStyle(.secondary) }
                        if let miles = reminder.dueOdometerMiles { Text("At \(miles.formatted()) mi").font(.subheadline).foregroundStyle(miles <= vehicle.odometerMiles && !reminder.completed ? PitStyle.amber : .secondary) }
                    }.padding(.vertical, 5)
                    Spacer(minLength: 0)
                }.padding(14).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 18))
            }
        }
    }

    private var trips: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Miles with a memory").font(.title3.weight(.bold))
            if detail.trips.isEmpty {
                ContentUnavailableView("No recorded trips yet", systemImage: "map", description: Text("Trips uploaded to your server appear here. Automatic Pi and Smartcar trip collection is still on the roadmap; this app doesn't track your location."))
            }
            ForEach(detail.trips) { trip in
                NavigationLink { TripView(trip: trip) } label: {
                    HStack {
                        Image(systemName: "point.topleft.down.to.point.bottomright.curvepath").font(.title2).foregroundStyle(PitStyle.amber)
                        VStack(alignment: .leading, spacing: 5) {
                            Text(trip.title.isEmpty ? "Recorded drive" : trip.title).font(.headline)
                            Text(String(trip.startedAt.prefix(10))).font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Text("\(trip.distanceMiles.formatted(.number.precision(.fractionLength(1)))) mi").font(.subheadline)
                        Image(systemName: "chevron.right").font(.caption).foregroundStyle(.secondary)
                    }.padding(18).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 18))
                }.buttonStyle(.plain)
            }
        }
    }
}

struct TripView: View {
    let trip: Trip
    private var validPoints: [TripPoint] { trip.points.filter { CLLocationCoordinate2DIsValid($0.coordinate) } }
    // Sparse samples must not be presented as a continuous observed route.
    var segments: [[TripPoint]] { TripSegments.split(trip.points) }
    var body: some View {
        VStack(spacing: 0) {
            if validPoints.isEmpty {
                ContentUnavailableView("No location samples", systemImage: "location.slash", description: Text("This trip has a distance record but no recorded route."))
            } else {
                Map {
                    ForEach(Array(segments.enumerated()), id: \.offset) { _, points in
                        if points.count > 1 { MapPolyline(coordinates: points.map(\.coordinate)).stroke(PitStyle.amber, lineWidth: 5) }
                        else if let point = points.first { Annotation("Sample", coordinate: point.coordinate) { Circle().fill(PitStyle.amber).frame(width: 8, height: 8) } }
                    }
                    if let first = validPoints.first { Marker("Start", systemImage: "flag", coordinate: first.coordinate).tint(.green) }
                    if let last = validPoints.last, validPoints.count > 1 { Marker("Finish", systemImage: "flag.checkered", coordinate: last.coordinate).tint(PitStyle.amber) }
                }.mapControls { MapCompass(); MapScaleView() }.accessibilityLabel("Recorded trip route")
            }
            VStack(alignment: .leading, spacing: 8) {
                Text("\(trip.distanceMiles.formatted(.number.precision(.fractionLength(1)))) miles").font(.system(.title, design: .rounded, weight: .bold))
                Text("\(trip.points.count) recorded location samples").font(.subheadline)
                Text("Lines connect recorded samples; they do not prove which road was taken. Gaps longer than five minutes are left disconnected. Map tiles may need an internet connection.").font(.caption).foregroundStyle(.secondary)
            }.frame(maxWidth: .infinity, alignment: .leading).padding(20).background(PitStyle.panel)
        }.navigationTitle(trip.title.isEmpty ? "Recorded drive" : trip.title).navigationBarTitleDisplayMode(.inline)
    }
}

enum TripSegments {
    static func split(_ points: [TripPoint]) -> [[TripPoint]] {
        var result: [[TripPoint]] = []
        let iso = ISO8601DateFormatter()
        func date(_ text: String) -> Date? {
            iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let value = iso.date(from: text) { return value }
            iso.formatOptions = [.withInternetDateTime]
            return iso.date(from: text)
        }
        for point in points {
            guard CLLocationCoordinate2DIsValid(point.coordinate) else { continue }
            if let previous = result.last?.last,
               let before = date(previous.recordedAt), let after = date(point.recordedAt),
               after.timeIntervalSince(before) >= 0, after.timeIntervalSince(before) <= 300 {
                result[result.count - 1].append(point)
            } else { result.append([point]) }
        }
        return result
    }
}
