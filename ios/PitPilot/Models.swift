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
    var subtitle: String { [year > 0 ? String(year) : "", make, model].filter { !$0.isEmpty }.joined(separator: " ") }
}

enum RecordKind: String, Codable, CaseIterable, Identifiable {
    case service, repair, upgrade, fuel, expense, note
    var id: String { rawValue }
    var label: String { rawValue.capitalized }
    var symbol: String {
        switch self {
        case .service: "wrench.and.screwdriver"
        case .repair: "wrench.adjustable"
        case .upgrade: "sparkles"
        case .fuel: "fuelpump"
        case .expense: "creditcard"
        case .note: "note.text"
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
}

struct Reminder: Codable, Identifiable {
    let id: String
    let vehicleId: String
    var title: String
    var dueDate: String?
    var dueOdometerMiles: Double?
    var completed: Bool
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
