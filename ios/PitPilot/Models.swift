import Foundation
import CoreLocation

struct Vehicle: Codable, Identifiable, Hashable {
    let id: String
    var name: String
    var make: String
    var model: String
    var year: Int
    var odometerMiles: Double
    var createdAt: String
    var vin: String?
    var licensePlate: String?
    var notes: String?
    var tags: [String]?
    var extraFields: [ExtraField]?
    var source: RecordSource?
    var odometerStatus: String?
    var subtitle: String { [year > 0 ? String(year) : "", make, model].filter { !$0.isEmpty }.joined(separator: " ") }
}

struct RecordKind: RawRepresentable, Codable, Hashable, CaseIterable, Identifiable {
    let rawValue: String
    init(rawValue: String) { self.rawValue = rawValue }
    init(from decoder: Decoder) throws { rawValue = try decoder.singleValueContainer().decode(String.self) }
    func encode(to encoder: Encoder) throws { var container = encoder.singleValueContainer(); try container.encode(rawValue) }
    static let service = Self(rawValue: "service")
    static let repair = Self(rawValue: "repair")
    static let upgrade = Self(rawValue: "upgrade")
    static let fuel = Self(rawValue: "fuel")
    static let expense = Self(rawValue: "expense")
    static let note = Self(rawValue: "note")
    static let odometer = Self(rawValue: "odometer")
    static let plan = Self(rawValue: "plan")
    static let allCases: [Self] = [.service, .repair, .upgrade, .fuel, .expense, .note, .odometer, .plan]
    var id: String { rawValue }
    var label: String { self == .plan ? "Planned work" : rawValue.capitalized }
    var isSupported: Bool { Self.allCases.contains(self) }
    var hasCost: Bool { [.service, .repair, .upgrade, .fuel, .expense, .plan].contains(self) }
    var hasOdometer: Bool { [.service, .repair, .upgrade, .fuel, .expense, .odometer].contains(self) }
    var symbol: String {
        switch self {
        case .service: "wrench.and.screwdriver"
        case .repair: "wrench.adjustable"
        case .upgrade: "sparkles"
        case .fuel: "fuelpump"
        case .expense: "creditcard"
        case .note: "note.text"
        case .odometer: "gauge.with.needle"
        case .plan: "list.clipboard"
        default: "doc.text"
        }
    }
}

struct VehicleRecord: Codable, Identifiable {
    let id: String
    let vehicleId: String
    var kind: RecordKind
    var date: String
    var title: String
    var notes: String
    var odometerMiles: Double
    var costCents: Int
    var gallons: Double?
    var tags: [String]?
    var extraFields: [ExtraField]?
    var pinned: Bool?
    var initialOdometerMiles: Double?
    var odometerStatus: String?
    var plan: PlannedWork?
    var fuel: FuelDetails?
    var source: RecordSource?

    var dateLabel: String { date.isEmpty ? "Undated" : date }
    var readingLabel: String {
        switch odometerStatus {
        case "estimated": "Estimated reading"
        case "measured": "Measured reading"
        default: "Reading, method unspecified"
        }
    }
    func matches(_ query: String) -> Bool {
        let query = query.trimmingCharacters(in: .whitespacesAndNewlines)
        return query.isEmpty || ([title, notes] + (tags ?? []) + (extraFields ?? []).flatMap { [$0.name, $0.value] })
            .contains { $0.localizedCaseInsensitiveContains(query) }
    }
}

struct ExtraField: Codable, Hashable {
    var name: String
    var value: String
    var isRequired: Bool
    var fieldType: Int
}

struct RecordSource: Codable, Hashable {
    var system: String
    var instance: String
    var collection: String
    var id: String
}

struct PlannedWork: Codable {
    var status: String
    var priority: String
    var recordKind: String
    var createdAt: String?
    var modifiedAt: String?
    var reminderIds: [String]?
}

struct FuelDetails: Codable {
    var fillToFull: Bool
    var missedFill: Bool
}

struct Reminder: Codable, Identifiable {
    let id: String
    let vehicleId: String
    var title: String
    var dueDate: String?
    var dueOdometerMiles: Double?
    var completed: Bool
    var notes: String?
    var tags: [String]?
    var extraFields: [ExtraField]?
    var source: RecordSource?
    var recurrence: ReminderRecurrence?
    var thresholds: ReminderThresholds?
}

struct ReminderRecurrence: Codable {
    var miles: Double?
    var months: Int?
    var days: Int?
    var fixedIntervals: Bool
    var label: String {
        var parts: [String] = []
        if let miles { parts.append("\(miles.formatted()) miles") }
        if let months { parts.append("\(months) months") }
        if let days { parts.append("\(days) days") }
        return "Every " + parts.joined(separator: " or ")
    }
}

struct ReminderThresholds: Codable {
    var urgentDays: Int?
    var veryUrgentDays: Int?
    var urgentMiles: Double?
    var veryUrgentMiles: Double?
}

struct Trip: Codable, Identifiable {
    let id: String
    let vehicleId: String
    var title: String
    var startedAt: String
    var endedAt: String
    var distanceMiles: Double
    var points: [TripPoint]
}

struct TripPoint: Codable {
    let latitude: Double
    let longitude: Double
    let recordedAt: String
    var coordinate: CLLocationCoordinate2D { .init(latitude: latitude, longitude: longitude) }
}

struct VehicleDetail: Codable {
    var records: [VehicleRecord] = []
    var reminders: [Reminder] = []
    var trips: [Trip] = []
}

enum Input {
    static func moneyCents(_ value: String) -> Int? {
        let normalized = value.trimmingCharacters(in: .whitespacesAndNewlines)
            .replacingOccurrences(of: Locale.current.decimalSeparator ?? ".", with: ".")
        guard normalized.range(of: #"^[0-9]+(?:\.[0-9]{1,2})?$"#, options: .regularExpression) != nil,
              let decimal = Decimal(string: normalized, locale: Locale(identifier: "en_US_POSIX")),
              decimal <= Decimal(1_000_000_000) else { return nil }
        return NSDecimalNumber(decimal: decimal * 100).intValue
    }
    static func moneyText(_ cents: Int) -> String {
        let fraction = String(format: "%02d", cents % 100)
        return "\(cents / 100)\(Locale.current.decimalSeparator ?? ".")\(fraction)"
    }
    static func parseDate(_ value: String) -> Date? {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.dateFormat = "yyyy-MM-dd"
        formatter.isLenient = false
        return formatter.date(from: value)
    }
    static func number(_ value: String) -> Double? {
        let normalized = value.trimmingCharacters(in: .whitespacesAndNewlines)
            .replacingOccurrences(of: Locale.current.decimalSeparator ?? ".", with: ".")
        guard normalized.range(of: #"^[0-9]+(?:\.[0-9]+)?$"#, options: .regularExpression) != nil,
              let number = Double(normalized), number.isFinite else { return nil }
        return number
    }
    static func date(_ value: Date) -> String {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.string(from: value)
    }
}
