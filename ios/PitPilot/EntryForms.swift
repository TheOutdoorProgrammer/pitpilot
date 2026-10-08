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
                        mileage = String(vehicle.odometerMiles)
                    }
                }
        }
    }
    private func save() {
        guard valid, let year = parsedYear, let miles = Input.number(mileage) else { return }
        working = true
        Task {
            do {
                var body: [String: Any] = [:]
                let cleanName = name.trimmingCharacters(in: .whitespaces)
                if vehicle?.name != cleanName { body["name"] = cleanName }
                if vehicle?.make != make { body["make"] = make }
                if vehicle?.model != model { body["model"] = model }
                if vehicle?.year != year { body["year"] = year }
                if vehicle?.odometerMiles != miles { body["odometerMiles"] = miles }
                if let vehicle { if !body.isEmpty { try await store.updateVehicle(vehicle.id, values: body) } }
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
    let record: VehicleRecord?
    @State private var draft: RecordDraft
    @State private var working = false
    @State private var error: String?
    init(vehicle: Vehicle, record: VehicleRecord? = nil) {
        self.vehicle = vehicle
        self.record = record
        _draft = State(initialValue: RecordDraft(record: record, mileage: vehicle.odometerMiles))
    }
    var body: some View {
        NavigationStack {
            Form {
                Section {
                    if record == nil {
                        Picker("Type", selection: $draft.kind) { ForEach(RecordKind.allCases) { kind in Label(kind.label, systemImage: kind.symbol).tag(kind) } }
                    } else { LabeledContent("Type", value: draft.kind.label) }
                    TextField("What did you do?", text: $draft.title)
                    if draft.kind == .note || draft.kind == .plan { Toggle("Include a date", isOn: $draft.includeDate) }
                    if draft.includeDate || (draft.kind != .note && draft.kind != .plan) {
                        DatePicker("Date", selection: $draft.date, displayedComponents: .date)
                    }
                    if draft.kind == .note { Toggle("Pinned", isOn: $draft.pinned) }
                }
                if draft.kind.hasOdometer {
                    Section("Reading") {
                        LabeledContent("Odometer (mi)") { TextField("Odometer in miles", text: $draft.mileage).keyboardType(.decimalPad).multilineTextAlignment(.trailing) }
                        if draft.kind == .odometer {
                            LabeledContent("Initial reading (mi)") { TextField("Optional initial reading", text: $draft.initialMileage).keyboardType(.decimalPad).multilineTextAlignment(.trailing) }
                            Picker("Reading method", selection: $draft.odometerStatus) {
                                Text("Unspecified").tag("unknown"); Text("Measured").tag("measured"); Text("Estimated").tag("estimated")
                            }
                        }
                    }
                }
                if draft.kind.hasCost {
                    Section(draft.kind == .plan ? "Estimated cost" : "Cost") {
                        LabeledContent("Cost (USD)") { TextField("Cost in USD", text: $draft.cost).keyboardType(.decimalPad).multilineTextAlignment(.trailing) }
                        Text("Use up to two decimal places for USD amounts.").font(.footnote).foregroundStyle(.secondary)
                        if draft.kind == .fuel {
                            LabeledContent("US gallons") { TextField("Optional", text: $draft.gallons).keyboardType(.decimalPad).multilineTextAlignment(.trailing) }
                            Toggle("Filled to full", isOn: $draft.fillToFull)
                            Toggle("Previous fill was missed", isOn: $draft.missedFill)
                        }
                    }
                }
                if draft.kind == .plan {
                    Section("Planned work") {
                        Picker("Status", selection: $draft.planStatus) { ForEach(RecordDraft.planStatuses, id: \.self) { Text($0.replacingOccurrences(of: "-", with: " ").capitalized).tag($0) } }
                        Picker("Priority", selection: $draft.planPriority) { ForEach(RecordDraft.planPriorities, id: \.self) { Text($0.capitalized).tag($0) } }
                        Picker("Work type", selection: $draft.planKind) { ForEach(RecordKind.allCases.filter { $0 != .plan }) { Text($0.label).tag($0.rawValue) } }
                        Text("Planned costs are estimates. Marking a plan done does not create a service record or add actual spending.").font(.footnote).foregroundStyle(.secondary)
                    }
                }
                Section("Notes") { TextField("Parts, shop, or anything worth remembering", text: $draft.notes, axis: .vertical).lineLimit(3...12).accessibilityIdentifier("recordNotes") }
                if !draft.extraFields.isEmpty {
                    Section("Custom fields") {
                        ForEach(draft.extraFields.indices, id: \.self) { index in
                            LabeledContent(draft.extraFields[index].name) { TextField("Value", text: $draft.extraFields[index].value, axis: .vertical).multilineTextAlignment(.trailing) }
                        }
                    }
                }
                if let tags = record?.tags, !tags.isEmpty { Section("Tags") { Text(tags.joined(separator: ", ")) } }
                if let error { Text(error).foregroundStyle(.red) }
            }.navigationTitle(record == nil ? "Log entry" : "Edit record").navigationBarTitleDisplayMode(.inline)
                .toolbar { EntryToolbar(working: working, valid: draft.valid && !store.offline, cancel: { dismiss() }, save: save) }
                .interactiveDismissDisabled(working)
        }
    }
    private func save() {
        guard draft.valid else { return }
        working = true
        Task {
            do {
                let body = try draft.values(original: record)
                if let record { if !body.isEmpty { try await store.updateRecord(record, values: body) } }
                else { try await store.createRecord(vehicleID: vehicle.id, values: body) }
                dismiss()
            } catch { self.error = error.localizedDescription }
            working = false
        }
    }
}

struct RecordDraft {
    static let planStatuses = ["planned", "in-progress", "testing", "blocked", "done"]
    static let planPriorities = ["low", "normal", "high", "critical"]
    var kind: RecordKind
    var title: String
    var notes: String
    var date: Date
    var includeDate: Bool
    var mileage: String
    var initialMileage: String
    var odometerStatus: String
    var cost: String
    var gallons: String
    var pinned: Bool
    var fillToFull: Bool
    var missedFill: Bool
    var planStatus: String
    var planPriority: String
    var planKind: String
    var extraFields: [ExtraField]

    init(record: VehicleRecord?, mileage: Double) {
        kind = record?.kind ?? .service
        title = record?.title ?? ""
        notes = record?.notes ?? ""
        date = record.flatMap { Input.parseDate($0.date) } ?? Date()
        includeDate = record.map { !$0.date.isEmpty } ?? true
        self.mileage = String(record?.odometerMiles ?? mileage)
        initialMileage = record?.initialOdometerMiles.map { String($0) } ?? ""
        odometerStatus = record?.odometerStatus.flatMap { $0.isEmpty ? nil : $0 } ?? "unknown"
        cost = Input.moneyText(record?.costCents ?? 0)
        gallons = record?.gallons.map { String($0) } ?? ""
        pinned = record?.pinned ?? false
        fillToFull = record?.fuel?.fillToFull ?? false
        missedFill = record?.fuel?.missedFill ?? false
        planStatus = record?.plan?.status ?? "planned"
        planPriority = record?.plan?.priority ?? "normal"
        planKind = record?.plan?.recordKind ?? "service"
        extraFields = record?.extraFields ?? []
    }

    var valid: Bool {
        kind.isSupported && !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && title.utf8.count <= 200 && notes.utf8.count <= 20_000
        && (!kind.hasOdometer || (Input.number(mileage) ?? .infinity) <= 10_000_000)
        && (!kind.hasCost || Input.moneyCents(cost) != nil)
        && (kind != .fuel || gallons.isEmpty || ((Input.number(gallons) ?? 0) > 0 && (Input.number(gallons) ?? .infinity) <= 10_000))
        && (kind != .odometer || initialMileage.isEmpty || (Input.number(initialMileage) ?? .infinity) <= 10_000_000)
        && extraFields.allSatisfy { $0.value.utf8.count <= 20_000 }
    }

    func values(original: VehicleRecord?) throws -> [String: Any] {
        guard valid else { throw APIError.message("Check the record's title, reading, and cost.") }
        var result: [String: Any] = [:]
        let cleanTitle = title.trimmingCharacters(in: .whitespacesAndNewlines)
        let dateText = includeDate || (kind != .note && kind != .plan) ? Input.date(date) : ""
        if original == nil { result["kind"] = kind.rawValue }
        if original?.title != cleanTitle { result["title"] = cleanTitle }
        if original?.notes != notes { result["notes"] = notes }
        if original?.date != dateText { result["date"] = dateText }
        if kind.hasOdometer, let value = Input.number(mileage), original?.odometerMiles != value { result["odometerMiles"] = value }
        if kind.hasCost, let cents = Input.moneyCents(cost), original?.costCents != cents { result["costCents"] = cents }
        if kind == .fuel {
            let amount = Input.number(gallons)
            if original?.gallons != amount { result["gallons"] = amount.map { $0 as Any } ?? NSNull() }
            if original == nil || (original?.fuel?.fillToFull ?? false) != fillToFull || (original?.fuel?.missedFill ?? false) != missedFill {
                result["fuel"] = ["fillToFull": fillToFull, "missedFill": missedFill]
            }
        }
        if kind == .note, (original?.pinned ?? false) != pinned || (kind == .note && original == nil) { result["pinned"] = pinned }
        if kind == .odometer {
            let initial = Input.number(initialMileage)
            if original?.initialOdometerMiles != initial { result["initialOdometerMiles"] = initial.map { $0 as Any } ?? NSNull() }
            if original == nil || RecordDraft(record: original, mileage: 0).odometerStatus != odometerStatus { result["odometerStatus"] = odometerStatus }
        }
        if extraFields != (original?.extraFields ?? []) {
            result["extraFields"] = try JSONSerialization.jsonObject(with: JSONEncoder().encode(extraFields))
        }
        if kind == .plan, original?.plan?.status != planStatus || original?.plan?.priority != planPriority || original?.plan?.recordKind != planKind {
            var plan: [String: Any] = [:]
            if original?.plan?.status != planStatus { plan["status"] = planStatus }
            if original?.plan?.priority != planPriority { plan["priority"] = planPriority }
            if original?.plan?.recordKind != planKind { plan["recordKind"] = planKind }
            result["plan"] = plan
        }
        // PATCH omits untouched metadata and numbers; the server owns provenance and unknown fields.
        return result
    }
}

struct AddReminderView: View {
    @EnvironmentObject private var store: GarageStore
    @Environment(\.dismiss) private var dismiss
    let vehicle: Vehicle
    var reminder: Reminder? = nil
    @State private var title = ""
    @State private var useDate = true
    @State private var date = Date()
    @State private var useMileage = false
    @State private var mileage = ""
    @State private var working = false
    @State private var error: String?
    @State private var notes = ""
    @State private var loaded = false
    private var valid: Bool {
        !title.trimmingCharacters(in: .whitespaces).isEmpty && title.count <= 200 && (useDate || useMileage) && (!useMileage || Input.number(mileage) != nil)
    }
    var body: some View {
        NavigationStack {
            Form {
                Section("Next up") { TextField("Reminder title", text: $title) }
                Section("When is it due?") {
                    Toggle("Due by date", isOn: $useDate).disabled(reminder?.recurrence != nil)
                    if useDate { DatePicker("Due date", selection: $date, displayedComponents: .date) }
                    Toggle("Due at mileage", isOn: $useMileage).disabled(reminder?.recurrence != nil)
                    if useMileage { LabeledContent("Odometer (mi)") { TextField("Odometer in miles", text: $mileage).keyboardType(.decimalPad).multilineTextAlignment(.trailing) } }
                }
                if let recurrence = reminder?.recurrence {
                    Section("Repeats") {
                        Text(recurrence.label)
                        Text(recurrence.fixedIntervals ? "Anchored to the existing due schedule." : "Scheduled from each completion.")
                        Text("Editing the due value preserves this recurrence and its advance-warning thresholds.").font(.footnote).foregroundStyle(.secondary)
                    }
                }
                Section("Notes") { TextField("Reminder notes", text: $notes, axis: .vertical).lineLimit(3...8) }
                Section { Text("Check Upcoming to see these reminders. Push notifications are not available in this release.").font(.footnote).foregroundStyle(.secondary) }
                if let error { Text(error).foregroundStyle(.red) }
            }.navigationTitle(reminder == nil ? "Add reminder" : "Edit reminder").navigationBarTitleDisplayMode(.inline)
                .toolbar { EntryToolbar(working: working, valid: valid && notes.utf8.count <= 20_000 && !store.offline, cancel: { dismiss() }, save: save) }
                .interactiveDismissDisabled(working)
                .onAppear {
                    guard !loaded else { return }
                    loaded = true
                    if let reminder {
                        title = reminder.title; notes = reminder.notes ?? ""
                        useDate = reminder.dueDate != nil
                        date = reminder.dueDate.flatMap(Input.parseDate) ?? Date()
                        useMileage = reminder.dueOdometerMiles != nil
                        mileage = reminder.dueOdometerMiles.map { String($0) } ?? ""
                    }
                }
        }
    }
    private func save() {
        guard valid else { return }
        working = true
        Task {
            do {
                let dateText = useDate ? Input.date(date) : nil
                let miles = useMileage ? Input.number(mileage) : nil
                var body: [String: Any] = [:]
                let cleanTitle = title.trimmingCharacters(in: .whitespaces)
                if reminder?.title != cleanTitle { body["title"] = cleanTitle }
                if (reminder?.notes ?? "") != notes { body["notes"] = notes }
                if reminder?.dueDate != dateText { body["dueDate"] = dateText.map { $0 as Any } ?? NSNull() }
                if reminder?.dueOdometerMiles != miles { body["dueOdometerMiles"] = miles.map { $0 as Any } ?? NSNull() }
                if let reminder { if !body.isEmpty { try await store.updateReminder(reminder, values: body) } }
                else { try await store.createReminder(vehicleID: vehicle.id, values: body) }
                dismiss()
            } catch { self.error = error.localizedDescription }
            working = false
        }
    }
}

struct CompleteReminderView: View {
    @EnvironmentObject private var store: GarageStore
    @Environment(\.dismiss) private var dismiss
    let reminder: Reminder
    let vehicle: Vehicle
    @State private var date = Date()
    @State private var mileage = ""
    @State private var working = false
    @State private var error: String?
    private var valid: Bool { reminder.dueOdometerMiles == nil || (Input.number(mileage) ?? .infinity) <= 10_000_000 }
    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Text(reminder.title).font(.headline)
                    if let recurrence = reminder.recurrence {
                        Text(recurrence.label)
                        Text(recurrence.fixedIntervals ? "Advance the original schedule by one interval. If more than one interval was missed, the next occurrence may remain overdue." : "Schedule the next occurrence from your completion date and reading.").font(.footnote).foregroundStyle(.secondary)
                    }
                }
                Section("Completion") {
                    if reminder.dueDate != nil { DatePicker("Completed on", selection: $date, displayedComponents: .date) }
                    if reminder.dueOdometerMiles != nil {
                        TextField("Reading at completion (mi)", text: $mileage).keyboardType(.decimalPad).accessibilityIdentifier("completionMileage")
                        Text("Current vehicle reading: \(vehicle.odometerMiles.formatted()) mi. Enter the reading when the work was completed.").font(.footnote).foregroundStyle(.secondary)
                    }
                    Text("This advances the reminder. Add a service record separately to save the work and its cost.").font(.footnote).foregroundStyle(.secondary)
                }
                if let error { Text(error).foregroundStyle(.red) }
            }.navigationTitle("Complete reminder").navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() }.disabled(working) }
                    ToolbarItem(placement: .confirmationAction) {
                        if working { ProgressView() }
                        else { Button("Complete", action: save).disabled(!valid || store.offline) }
                    }
                }.interactiveDismissDisabled(working)
        }
    }
    private func save() {
        guard valid else { return }
        working = true
        Task {
            do {
                var values: [String: Any] = ["completed": true]
                if let due = reminder.dueDate { values["completionDate"] = Input.date(date); values["expectedDueDate"] = due }
                if let due = reminder.dueOdometerMiles { values["completionOdometerMiles"] = Input.number(mileage); values["expectedDueOdometerMiles"] = due }
                try await store.updateReminder(reminder, values: values)
                dismiss()
            } catch {
                self.error = error.localizedDescription
                await store.refreshDetail(vehicle.id)
            }
            working = false
        }
    }
}
