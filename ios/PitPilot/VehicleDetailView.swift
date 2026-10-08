import SwiftUI
import MapKit

struct VehicleDetailView: View {
    @EnvironmentObject private var store: GarageStore
    @Environment(\.dismiss) private var dismiss
    private let initialVehicle: Vehicle
    private var vehicle: Vehicle { store.vehicles.first(where: { $0.id == initialVehicle.id }) ?? initialVehicle }
    init(vehicle: Vehicle) { initialVehicle = vehicle }
    @State private var selected = "Overview"
    private enum Sheet: String, Identifiable { case record, reminder, vehicle; var id: String { rawValue } }
    @State private var sheet: Sheet?
    @State private var confirmDeleteVehicle = false
    @State private var deleteRecord: VehicleRecord?
    @State private var search = ""
    @State private var filter = "all"
    @State private var completingReminder: Reminder?
    private var detail: VehicleDetail { store.detail(vehicle.id) }
    private var historyRecords: [VehicleRecord] {
        detail.records.filter { $0.kind != .plan && (filter == "all" || $0.kind.rawValue == filter) && $0.matches(search) }
            .sorted { left, right in
                if (left.pinned ?? false) != (right.pinned ?? false) { return left.pinned ?? false }
                if left.date != right.date { return left.date > right.date }
                return left.title.localizedStandardCompare(right.title) == .orderedAscending
            }
    }

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 22) {
                VehicleCard(vehicle: vehicle)
                if vehicle.source != nil || vehicle.vin != nil || vehicle.licensePlate != nil || vehicle.notes != nil || !(vehicle.tags ?? []).isEmpty || !(vehicle.extraFields ?? []).isEmpty {
                    DisclosureGroup("Vehicle details") {
                        VStack(alignment: .leading, spacing: 10) {
                            if let vin = vehicle.vin, !vin.isEmpty { LabeledContent("VIN", value: vin) }
                            if let plate = vehicle.licensePlate, !plate.isEmpty { LabeledContent("License plate", value: plate) }
                            if let notes = vehicle.notes, !notes.isEmpty { Text(notes).textSelection(.enabled) }
                            MetadataContent(tags: vehicle.tags, fields: vehicle.extraFields, source: vehicle.source)
                        }.font(.subheadline).fixedSize(horizontal: false, vertical: true).padding(.top, 10)
                    }.accessibilityIdentifier("vehicleInformation").padding(16).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 16))
                }
                if store.offline { OfflineBanner(date: store.cache.updatedAt) }
                if let error = store.error { Text(error).font(.callout).foregroundStyle(.orange) }
                Picker("Vehicle section", selection: $selected) {
                    ForEach(["Overview", "History", "Upcoming", "Trips"], id: \.self) { Text($0) }
                }.pickerStyle(.segmented)
                switch selected {
                case "Overview":
                    if let connection = store.connection {
                        NavigationLink {
                            VehicleIntegrationsView(vehicleID: vehicle.id, connection: connection)
                        } label: {
                            HStack(spacing: 14) {
                                Image(systemName: "antenna.radiowaves.left.and.right").font(.title2).foregroundStyle(PitStyle.amber)
                                VStack(alignment: .leading, spacing: 4) {
                                    Text("Vehicle integrations").font(.headline).foregroundStyle(.primary)
                                    Text("Pair a Pi or link Smartcar").font(.subheadline).foregroundStyle(.secondary)
                                }
                                Spacer()
                                Image(systemName: "chevron.right").foregroundStyle(PitStyle.amber)
                            }.padding(18).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 18))
                        }.buttonStyle(.plain).accessibilityIdentifier("vehicleIntegrations")
                    }
                    VehicleSignalsView(vehicleID: vehicle.id)
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
            .refreshable { await store.refresh(); await store.refreshDetail(vehicle.id); await store.refreshSignals(vehicle.id) }
            .sheet(item: $sheet) { item in
                switch item {
                case .record: AddRecordView(vehicle: vehicle)
                case .reminder: AddReminderView(vehicle: vehicle)
                case .vehicle: AddVehicleView(vehicle: vehicle)
                }
            }
            .sheet(item: $completingReminder) { reminder in CompleteReminderView(reminder: reminder, vehicle: vehicle) }
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
        LazyVStack(alignment: .leading, spacing: 16) {
            HStack {
                Text("The running record").font(.title3.weight(.bold))
                Spacer()
                Button("Log entry") { sheet = .record }.disabled(store.offline)
            }
            HStack {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                TextField("Search history and notes", text: $search).accessibilityIdentifier("historySearch")
                if !search.isEmpty { Button("Clear search", systemImage: "xmark.circle.fill") { search = "" }.labelStyle(.iconOnly) }
            }.padding(12).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 12))
            Picker("Record type", selection: $filter) {
                Text("All history").tag("all")
                ForEach(RecordKind.allCases.filter { $0 != .plan }) { Text($0.label).tag($0.rawValue) }
            }.pickerStyle(.menu).accessibilityIdentifier("historyFilter")
            Text("\(historyRecords.count) \(historyRecords.count == 1 ? "record" : "records")").font(.caption).foregroundStyle(.secondary)
            if detail.records.filter({ $0.kind != .plan }).isEmpty {
                ContentUnavailableView("A fresh start", systemImage: "wrench.and.screwdriver", description: Text("Log a service, repair, fuel stop, or note. Your vehicle's story starts here."))
            } else if historyRecords.isEmpty {
                ContentUnavailableView("No matching records", systemImage: "magnifyingglass", description: Text("Try another word or record type."))
            }
            ForEach(historyRecords) { record in
                recordLink(record)
            }
        }
    }

    private func recordLink(_ record: VehicleRecord) -> some View {
        NavigationLink { RecordDetailView(record: record, vehicle: vehicle) } label: { RecordRow(record: record) }
            .buttonStyle(.plain)
            .contextMenu { Button("Delete record", role: .destructive) { deleteRecord = record }.disabled(store.offline) }
    }

    private var reminders: some View {
        LazyVStack(alignment: .leading, spacing: 16) {
            let plans = detail.records.filter { $0.kind == .plan }
            if !plans.isEmpty {
                Text("Planned work").font(.title3.weight(.bold))
                Text("Estimates are separate from completed work and actual spending.").font(.subheadline).foregroundStyle(.secondary)
                ForEach(plans) { recordLink($0) }
            }
            HStack { Text("Stay ahead").font(.title3.weight(.bold)); Spacer(); Button("Add reminder") { sheet = .reminder }.disabled(store.offline) }
            if detail.reminders.isEmpty { ContentUnavailableView("Nothing on the horizon", systemImage: "calendar", description: Text("Set a due date or mileage for your next service. Reminders appear here when you open the app.")) }
            ForEach(detail.reminders.sorted { !$0.completed && $1.completed }) { reminder in
                HStack(alignment: .top, spacing: 14) {
                    Button {
                        if reminder.recurrence != nil && !reminder.completed { completingReminder = reminder }
                        else { Task { await store.complete(reminder) } }
                    } label: {
                        Image(systemName: reminder.completed ? "checkmark.circle.fill" : "circle").font(.title2).frame(width: 44, height: 44)
                    }.accessibilityLabel(reminder.completed ? "Mark \(reminder.title) incomplete" : "Complete \(reminder.title)").disabled(store.offline)
                    VStack(alignment: .leading, spacing: 5) {
                        Text(reminder.title).font(.headline).strikethrough(reminder.completed)
                        if let date = reminder.dueDate, !date.isEmpty { Text("Due \(date)").font(.subheadline).foregroundStyle(.secondary) }
                        if let miles = reminder.dueOdometerMiles { Text("At \(miles.formatted()) mi").font(.subheadline).foregroundStyle(miles <= vehicle.odometerMiles && !reminder.completed ? PitStyle.amber : .secondary) }
                        if let recurrence = reminder.recurrence {
                            Text(recurrence.label).font(.caption).foregroundStyle(.secondary)
                            Text(recurrence.fixedIntervals ? "Fixed schedule" : "From completion").font(.caption).foregroundStyle(.secondary)
                        }
                        if let notes = reminder.notes, !notes.isEmpty { Text(notes).font(.caption).foregroundStyle(.secondary).textSelection(.enabled) }
                        if let tags = reminder.tags, !tags.isEmpty { Text(tags.joined(separator: ", ")).font(.caption).foregroundStyle(.secondary) }
                        NavigationLink("Reminder details") { ReminderDetailView(reminder: reminder, vehicle: vehicle) }
                            .font(.caption).accessibilityLabel("Details for \(reminder.title)")
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

struct RecordRow: View {
    let record: VehicleRecord
    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: record.kind.symbol).foregroundStyle(PitStyle.amber).frame(width: 26).accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 6) {
                HStack(alignment: .firstTextBaseline) {
                    Text(record.title).font(.headline)
                    if record.pinned == true { Image(systemName: "pin.fill").font(.caption).accessibilityLabel("Pinned") }
                }
                Text("\(record.kind.label) · \(record.dateLabel)").font(.caption).foregroundStyle(.secondary)
                if let plan = record.plan {
                    Text("\(plan.status.replacingOccurrences(of: "-", with: " ").capitalized) · \(plan.priority.capitalized) priority").font(.caption).foregroundStyle(PitStyle.amber)
                }
                if !record.notes.isEmpty { Text(record.notes).font(.subheadline).foregroundStyle(.secondary).lineLimit(2) }
                if record.kind.hasOdometer {
                    Text("\(record.odometerMiles.formatted()) mi" + (record.kind == .odometer ? " · \(record.readingLabel)" : "")).font(.caption).foregroundStyle(.secondary)
                }
                if record.kind.hasCost {
                    Text((record.kind == .plan ? "Estimate " : "") + (Double(record.costCents) / 100).formatted(.currency(code: "USD"))).font(.subheadline.weight(.semibold))
                }
            }.frame(maxWidth: .infinity, alignment: .leading)
            Image(systemName: "chevron.right").font(.caption).foregroundStyle(.secondary).padding(.top, 4)
        }.padding(18).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 18))
    }
}

struct MetadataContent: View {
    var tags: [String]?
    var fields: [ExtraField]?
    var source: RecordSource?
    var body: some View {
        if let tags, !tags.isEmpty { LabeledContent("Tags", value: tags.joined(separator: ", ")) }
        ForEach(Array((fields ?? []).enumerated()), id: \.offset) { _, field in
            VStack(alignment: .leading, spacing: 4) {
                Text(field.name).font(.subheadline.weight(.semibold))
                Text(field.value.isEmpty ? "No value" : field.value).foregroundStyle(.secondary).textSelection(.enabled)
            }
        }
        if let source {
            VStack(alignment: .leading, spacing: 4) {
                Label(source.system == "lubelogger" ? "Imported from LubeLogger" : "Imported from \(source.system)", systemImage: "tray.and.arrow.down")
                Text("Source collection: \(source.collection)").font(.caption).foregroundStyle(.secondary)
                Text("Source record: \(source.id)").font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
            }
        }
    }
}

struct RecordDetailView: View {
    @EnvironmentObject private var store: GarageStore
    let record: VehicleRecord
    let vehicle: Vehicle
    @State private var editing = false
    private var current: VehicleRecord { store.detail(vehicle.id).records.first { $0.id == record.id } ?? record }
    var body: some View {
        List {
            if store.offline { OfflineBanner(date: store.cache.updatedAt).listRowBackground(PitStyle.panel) }
            Section {
                Text(current.title).font(.title2.weight(.bold)).textSelection(.enabled)
                LabeledContent("Type", value: current.kind.label)
                LabeledContent("Date", value: current.dateLabel)
                if current.pinned == true { Label("Pinned note", systemImage: "pin.fill") }
            }
            if let plan = current.plan {
                Section("Planned work") {
                    LabeledContent("Status", value: plan.status.replacingOccurrences(of: "-", with: " ").capitalized)
                    LabeledContent("Priority", value: plan.priority.capitalized)
                    LabeledContent("Work type", value: RecordKind(rawValue: plan.recordKind).label)
                    if let created = plan.createdAt, !created.isEmpty { LabeledContent("Created", value: created) }
                    if let modified = plan.modifiedAt, !modified.isEmpty { LabeledContent("Modified", value: modified) }
                    ForEach(plan.reminderIds ?? [], id: \.self) { id in
                        if let reminder = store.detail(vehicle.id).reminders.first(where: { $0.id == id }) {
                            NavigationLink(reminder.title) { ReminderDetailView(reminder: reminder, vehicle: vehicle) }
                        }
                    }
                    Text("This plan is separate from completed work. Its estimated cost is not actual spending.").font(.footnote).foregroundStyle(.secondary)
                }
            }
            if current.kind.hasOdometer {
                Section("Odometer") {
                    if let initial = current.initialOdometerMiles { LabeledContent("Initial reading", value: "\(initial.formatted()) mi") }
                    LabeledContent(current.kind == .odometer ? "Final reading" : "Reading", value: "\(current.odometerMiles.formatted()) mi")
                    if current.kind == .odometer { Text(current.readingLabel).foregroundStyle(current.odometerStatus == "estimated" ? PitStyle.amber : .secondary) }
                }
            }
            if current.kind.hasCost {
                Section(current.kind == .plan ? "Estimate" : "Cost") {
                    LabeledContent("USD", value: (Double(current.costCents) / 100).formatted(.currency(code: "USD")))
                    if let gallons = current.gallons { LabeledContent("US gallons", value: gallons.formatted()) }
                    if let fuel = current.fuel {
                        LabeledContent("Filled to full", value: fuel.fillToFull ? "Yes" : "No")
                        LabeledContent("Previous fill missed", value: fuel.missedFill ? "Yes" : "No")
                    }
                }
            }
            if !current.notes.isEmpty { Section("Notes") { Text(current.notes).textSelection(.enabled) } }
            if current.source != nil || !(current.tags ?? []).isEmpty || !(current.extraFields ?? []).isEmpty {
                Section("Record details") { MetadataContent(tags: current.tags, fields: current.extraFields, source: current.source) }
            }
            if !current.kind.isSupported { Text("Update the app to edit this record type. Its original information remains on your server.").font(.footnote).foregroundStyle(.secondary) }
        }.scrollContentBackground(.hidden).background(PitStyle.background)
            .navigationTitle(current.kind.label).navigationBarTitleDisplayMode(.inline)
            .toolbar { Button("Edit record") { editing = true }.disabled(store.offline || !current.kind.isSupported) }
            .sheet(isPresented: $editing) { AddRecordView(vehicle: vehicle, record: current) }
    }
}

struct ReminderDetailView: View {
    @EnvironmentObject private var store: GarageStore
    let reminder: Reminder
    let vehicle: Vehicle
    @State private var completing = false
    @State private var editing = false
    private var current: Reminder { store.detail(vehicle.id).reminders.first { $0.id == reminder.id } ?? reminder }
    var body: some View {
        List {
            Section {
                Text(current.title).font(.title2.weight(.bold))
                if let due = current.dueDate { LabeledContent("Due date", value: due) }
                if let miles = current.dueOdometerMiles { LabeledContent("Due reading", value: "\(miles.formatted()) mi") }
                if let recurrence = current.recurrence {
                    Text(recurrence.label)
                    Text(recurrence.fixedIntervals ? "Each completion advances the original schedule by one interval. An overdue reminder may still be due afterward." : "Each completion schedules the next reminder from the date and reading you enter.").font(.footnote).foregroundStyle(.secondary)
                }
                Button(current.completed ? "Mark incomplete" : "Complete reminder") {
                    if current.recurrence != nil && !current.completed { completing = true }
                    else { Task { await store.complete(current) } }
                }.disabled(store.offline)
            }
            if let notes = current.notes, !notes.isEmpty { Section("Notes") { Text(notes).textSelection(.enabled) } }
            Section("Details") { MetadataContent(tags: current.tags, fields: current.extraFields, source: current.source) }
            if let thresholds = current.thresholds {
                Section("Advance warning") {
                    if let value = thresholds.urgentDays { LabeledContent("Urgent within", value: "\(value) days") }
                    if let value = thresholds.veryUrgentDays { LabeledContent("Very urgent within", value: "\(value) days") }
                    if let value = thresholds.urgentMiles { LabeledContent("Urgent within", value: "\(value.formatted()) mi") }
                    if let value = thresholds.veryUrgentMiles { LabeledContent("Very urgent within", value: "\(value.formatted()) mi") }
                }
            }
            if let error = store.error { Text(error).foregroundStyle(.orange) }
        }.scrollContentBackground(.hidden).background(PitStyle.background)
            .navigationTitle("Reminder").navigationBarTitleDisplayMode(.inline)
            .toolbar { Button("Edit reminder") { editing = true }.disabled(store.offline) }
            .sheet(isPresented: $completing) { CompleteReminderView(reminder: current, vehicle: vehicle) }
            .sheet(isPresented: $editing) { AddReminderView(vehicle: vehicle, reminder: current) }
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
