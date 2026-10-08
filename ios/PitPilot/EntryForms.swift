import SwiftUI

struct EntryToolbar: ToolbarContent {
    let working: Bool
    let valid: Bool
    let cancel: () -> Void
    let save: () -> Void
    var body: some ToolbarContent {
        ToolbarItem(placement: .cancellationAction) { Button("Cancel", action: cancel).disabled(working) }
        ToolbarItem(placement: .confirmationAction) {
            if working { ProgressView().accessibilityLabel("Saving") }
            else { Button("Save", action: save).fontWeight(.bold).disabled(!valid) }
        }
    }
}

struct AddVehicleView: View {
    @EnvironmentObject private var store: GarageStore
    @Environment(\.dismiss) private var dismiss
    var vehicle: Vehicle? = nil
    @State private var name = ""
    @State private var make = ""
    @State private var model = ""
    @State private var year = ""
    @State private var mileage = "0"
    @State private var working = false
    @State private var error: String?
    private var parsedYear: Int? { year.isEmpty ? 0 : Int(year) }
    private var valid: Bool {
        guard let year = parsedYear, Input.number(mileage) != nil else { return false }
        return !name.trimmingCharacters(in: .whitespaces).isEmpty && name.count <= 200 && (year == 0 || (1886...(Calendar.current.component(.year, from: Date()) + 2)).contains(year))
    }
    var body: some View {
        NavigationStack {
            Form {
                Section("Give it a name") { TextField("Vehicle name", text: $name).accessibilityIdentifier("vehicleName") }
                Section("Vehicle details") {
                    LabeledContent("Make") { TextField("Make", text: $make).multilineTextAlignment(.trailing) }
                    LabeledContent("Model") { TextField("Model", text: $model).multilineTextAlignment(.trailing) }
                    LabeledContent("Year") { TextField("Optional", text: $year).keyboardType(.numberPad).multilineTextAlignment(.trailing) }
                    LabeledContent("Odometer (mi)") { TextField("Odometer in miles", text: $mileage).keyboardType(.decimalPad).multilineTextAlignment(.trailing) }
                }
                if let error { Text(error).foregroundStyle(.red) }
            }.navigationTitle(vehicle == nil ? "Add vehicle" : "Edit vehicle").navigationBarTitleDisplayMode(.inline)
                .toolbar { EntryToolbar(working: working, valid: valid, cancel: { dismiss() }, save: save) }
                .interactiveDismissDisabled(working)
                .onAppear {
                    if let vehicle {
                        name = vehicle.name; make = vehicle.make; model = vehicle.model
                        year = vehicle.year == 0 ? "" : String(vehicle.year)
                        mileage = vehicle.odometerMiles.formatted(.number.grouping(.never))
                    }
                }
        }
    }
    private func save() {
        guard valid, let year = parsedYear, let miles = Input.number(mileage) else { return }
        working = true
        Task {
            do {
                let body: [String: Any] = ["name": name.trimmingCharacters(in: .whitespaces), "make": make, "model": model, "year": year, "odometerMiles": miles]
                if let vehicle { try await store.updateVehicle(vehicle.id, values: body) }
                else { try await store.createVehicle(body) }
                dismiss()
            } catch { self.error = error.localizedDescription }
            working = false
        }
    }
}

struct AddRecordView: View {
    @EnvironmentObject private var store: GarageStore
    @Environment(\.dismiss) private var dismiss
    let vehicle: Vehicle
    @State private var kind: RecordKind = .service
    @State private var title = ""
    @State private var notes = ""
    @State private var date = Date()
    @State private var mileage = ""
    @State private var cost = "0"
    @State private var gallons = ""
    @State private var working = false
    @State private var error: String?
    private var valid: Bool {
        !title.trimmingCharacters(in: .whitespaces).isEmpty && title.count <= 200 && Input.number(mileage) != nil && (Input.number(cost) ?? .infinity) <= 1_000_000_000 && (kind != .fuel || gallons.isEmpty || (Input.number(gallons) ?? 0) > 0)
    }
    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Picker("Type", selection: $kind) { ForEach(RecordKind.allCases) { kind in Label(kind.label, systemImage: kind.symbol).tag(kind) } }
                    TextField("What did you do?", text: $title)
                    DatePicker("Date", selection: $date, displayedComponents: .date)
                }
                Section("Reading and cost") {
                    LabeledContent("Odometer (mi)") { TextField("Odometer in miles", text: $mileage).keyboardType(.decimalPad).multilineTextAlignment(.trailing) }
                    LabeledContent("Cost (USD)") { TextField("Cost in USD", text: $cost).keyboardType(.decimalPad).multilineTextAlignment(.trailing) }
                    if kind == .fuel { LabeledContent("US gallons") { TextField("Optional", text: $gallons).keyboardType(.decimalPad).multilineTextAlignment(.trailing) } }
                }
                Section("Notes") { TextField("Parts, shop, or anything worth remembering", text: $notes, axis: .vertical).lineLimit(3...8) }
                if let error { Text(error).foregroundStyle(.red) }
            }.navigationTitle("Log entry").navigationBarTitleDisplayMode(.inline)
                .toolbar { EntryToolbar(working: working, valid: valid, cancel: { dismiss() }, save: save) }
                .interactiveDismissDisabled(working)
                .onAppear { if mileage.isEmpty { mileage = vehicle.odometerMiles.formatted(.number.grouping(.never)) } }
        }
    }
    private func save() {
        guard valid, let miles = Input.number(mileage), let cost = Input.number(cost) else { return }
        working = true
        Task {
            do {
                var body: [String: Any] = ["kind": kind.rawValue, "title": title.trimmingCharacters(in: .whitespaces), "notes": notes, "date": Input.date(date), "odometerMiles": miles, "costCents": Int((cost * 100).rounded())]
                if kind == .fuel, let amount = Input.number(gallons) { body["gallons"] = amount }
                try await store.createRecord(vehicleID: vehicle.id, values: body)
                dismiss()
            } catch { self.error = error.localizedDescription }
            working = false
        }
    }
}

struct AddReminderView: View {
    @EnvironmentObject private var store: GarageStore
    @Environment(\.dismiss) private var dismiss
    let vehicle: Vehicle
    @State private var title = ""
    @State private var useDate = true
    @State private var date = Date()
    @State private var useMileage = false
    @State private var mileage = ""
    @State private var working = false
    @State private var error: String?
    private var valid: Bool {
        !title.trimmingCharacters(in: .whitespaces).isEmpty && title.count <= 200 && (useDate || useMileage) && (!useMileage || Input.number(mileage) != nil)
    }
    var body: some View {
        NavigationStack {
            Form {
                Section("Next up") { TextField("Reminder title", text: $title) }
                Section("When is it due?") {
                    Toggle("Due by date", isOn: $useDate)
                    if useDate { DatePicker("Due date", selection: $date, displayedComponents: .date) }
                    Toggle("Due at mileage", isOn: $useMileage)
                    if useMileage { LabeledContent("Odometer (mi)") { TextField("Odometer in miles", text: $mileage).keyboardType(.decimalPad).multilineTextAlignment(.trailing) } }
                }
                Section { Text("Check Upcoming to see these reminders. Push notifications are not available in this release.").font(.footnote).foregroundStyle(.secondary) }
                if let error { Text(error).foregroundStyle(.red) }
            }.navigationTitle("Add reminder").navigationBarTitleDisplayMode(.inline)
                .toolbar { EntryToolbar(working: working, valid: valid, cancel: { dismiss() }, save: save) }
                .interactiveDismissDisabled(working)
        }
    }
    private func save() {
        guard valid else { return }
        working = true
        Task {
            do {
                var body: [String: Any] = ["title": title.trimmingCharacters(in: .whitespaces)]
                if useDate { body["dueDate"] = Input.date(date) }
                if useMileage { body["dueOdometerMiles"] = Input.number(mileage) }
                try await store.createReminder(vehicleID: vehicle.id, values: body)
                dismiss()
            } catch { self.error = error.localizedDescription }
            working = false
        }
    }
}
