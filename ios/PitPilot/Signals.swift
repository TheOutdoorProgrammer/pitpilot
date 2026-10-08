import Foundation

struct SignalDefinition: Codable, Identifiable {
    let metric: String
    let label: String
    let unit: String
    let staleAfterSeconds: Int
    var id: String { metric }
}

struct SignalObservation: Codable {
    let key: String
    let metric: String
    let unit: String
    let statistic: String
    let quality: String
    let value: Double
    var observedAt: String?
    var periodStart: String?
    var periodEnd: String?
    var sourceRevision: String?
    var calendarDate: String?
    var timezone: String?
    var referenceDate: Date? { SignalFormat.date(observedAt ?? periodEnd ?? "") }
    var timeLabel: String {
        if let calendarDate { return "\(calendarDate) · Time and timezone unknown" }
        if let observedAt, let date = SignalFormat.date(observedAt) {
            return date.formatted(date: .abbreviated, time: .shortened)
        }
        return SignalFormat.interval(periodStart, periodEnd)
    }
}

struct LatestSignal: Codable, Identifiable {
    let metric: String
    let unit: String
    let source: String
    let statistic: String
    let quality: String
    let latest: SignalObservation
    let stale: Bool
    var id: String { [metric, source, statistic, quality].joined(separator: "/") }
    func isStale(at now: Date, definition: SignalDefinition?) -> Bool {
        guard statistic == "sample", let date = latest.referenceDate, let definition else { return true }
        return stale || now.timeIntervalSince(date) > Double(definition.staleAfterSeconds)
    }
}

struct LatestSignals: Codable {
    let asOf: String
    let definitions: [SignalDefinition]
    let series: [LatestSignal]
    var contexts: [SourcedSignalContext]?
    var metrics: [String] { Array(Set(series.map(\.metric))).sorted { label($0).localizedStandardCompare(label($1)) == .orderedAscending } }
    func label(_ metric: String) -> String { definitions.first { $0.metric == metric }?.label ?? metric.replacingOccurrences(of: "_", with: " ").capitalized }
    func definition(_ metric: String) -> SignalDefinition? { definitions.first { $0.metric == metric } }
    func readings(_ metric: String) -> [LatestSignal] {
        series.filter { $0.metric == metric }.sorted {
            if SignalFormat.rank($0.statistic) != SignalFormat.rank($1.statistic) { return SignalFormat.rank($0.statistic) < SignalFormat.rank($1.statistic) }
            if let left = $0.latest.referenceDate, let right = $1.latest.referenceDate, left != right { return left > right }
            let leftTime = $0.latest.calendarDate ?? ""
            let rightTime = $1.latest.calendarDate ?? ""
            if leftTime != rightTime { return leftTime > rightTime }
            return $0.id < $1.id
        }
    }
}

struct SignalHistory: Codable {
    let metric: String
    let unit: String
    let from: String
    let to: String
    let maxPoints: Int
    let series: [SignalHistorySeries]
}

struct SignalHistorySeries: Codable, Identifiable {
    let source: String
    let quality: String
    let statistic: String
    let points: [SignalHistoryPoint]
    var id: String { [source, quality, statistic].joined(separator: "/") }
    var label: String { "\(SignalFormat.source(source)) · \(quality.capitalized)" }
}

struct SignalHistoryPoint: Codable, Identifiable {
    var bucketStart: String?
    var bucketEnd: String?
    var windowStart: String?
    var windowEnd: String?
    let minimum: Double
    let maximum: Double
    let mean: Double
    let first: Double
    let last: Double
    let count: Int
    var firstObservedAt: String?
    var lastObservedAt: String?
    var calendarDate: String?
    var timezone: String?
    var id: String { calendarDate ?? bucketStart ?? "unknown" }
}

struct SourcedSignalContext: Codable, Identifiable {
    let source: String
    let context: SignalContext
    var id: String { source + "/" + context.key }
}

struct SignalContext: Codable {
    let key: String
    let kind: String
    var observedAt: String?
    var periodStart: String?
    var periodEnd: String?
    var calendarDate: String?
    var timezone: String?
    var coverage: SignalCoverage?
    var diagnostic: SignalDiagnostic?
    var segment: SignalSegment?
    var location: SignalLocation?
    var timeLabel: String {
        if let calendarDate { return "\(calendarDate) · Time and timezone unknown" }
        if let observedAt, let date = SignalFormat.date(observedAt) { return date.formatted(date: .abbreviated, time: .shortened) }
        return SignalFormat.interval(periodStart, periodEnd)
    }
}
struct SignalCoverage: Codable {
    var firstObservedAt: String?
    var lastObservedAt: String?
    let retainedSamples: Int
    let skippedIntervals: Int
}
struct SignalDiagnostic: Codable {
    let `class`: String
    let codes: [String]
    let successfulReads: Int
    let unknown: Bool
}
struct SignalSegment: Codable {
    let startedAt: String
    let endedAt: String
    let state: String
    let samples: Int
    var distanceKm: Double?
    let speedCoverageSeconds: Double
    let runningSeconds: Double
    let idleSeconds: Double
    var topSpeedKph: Double?
}
struct SignalLocation: Codable {
    let latitude: Double
    let longitude: Double
    let type: String
    var accuracyMeters: Double?
}

enum SignalFormat {
    static func date(_ string: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = formatter.date(from: string) { return date }
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.date(from: string)
    }
    static func interval(_ start: String?, _ end: String?) -> String {
        guard let start, let end, let first = date(start), let last = date(end) else { return "Time unavailable" }
        return "\(first.formatted(date: .abbreviated, time: .shortened)) to \(last.formatted(date: .abbreviated, time: .shortened))"
    }
    static func value(_ number: Double, unit: String) -> String {
        if unit == "boolean" { return number == 0 ? "Off" : number == 1 ? "On" : "Unknown" }
        let label: String
        switch unit {
        case "percent": label = "%"
        case "fahrenheit": label = "°F"
        case "celsius": label = "°C"
        case "count": label = ""
        default: label = unit
        }
        return number.formatted(.number.precision(.fractionLength(0...2))) + (label.isEmpty ? "" : " \(label)")
    }
    static func source(_ source: String) -> String {
        switch source { case "pi": return "Raspberry Pi"; case "smartcar": return "Smartcar"; case "lubelogger": return "LubeLogger"; default: return source.capitalized }
    }
    static func statistic(_ statistic: String) -> String {
        switch statistic {
        case "sample": return "Reading"
        case "snapshot": return "Snapshot"
        case "min": return "Period minimum"
        case "max": return "Period maximum"
        case "mean": return "Period average"
        case "sum": return "Period total"
        case "count": return "Period count"
        default: return statistic.capitalized
        }
    }
    static func rank(_ statistic: String) -> Int { ["sample", "snapshot", "mean", "min", "max", "sum", "count"].firstIndex(of: statistic) ?? 7 }
}
