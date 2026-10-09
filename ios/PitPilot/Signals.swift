import Foundation

struct SignalDefinition: Codable, Identifiable {
    let metric: String
    let label: String
    let unit: String
    let staleAfterSeconds: Int
    var description: String?
    var interpretation: String?
    var valueLabels: [String: String]?
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
    var dashboardMetrics: [String] {
        let featured = ["fuel_level_pct", "battery_soc_pct", "odometer_km", "adapter_voltage_v", "coolant_c", "rpm", "manifold_kpa", "speed_kph"]
        return metrics.sorted {
            let a = featured.firstIndex(of: $0) ?? featured.count, b = featured.firstIndex(of: $1) ?? featured.count
            return a == b ? label($0).localizedStandardCompare(label($1)) == .orderedAscending : a < b
        }
    }
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
    func initialHistoryDays(_ metric: String, statistic: String, now: Date = Date()) -> Int {
        let available = readings(metric).filter { $0.statistic == statistic }
        guard !available.isEmpty else { return 30 }
        let cutoff = now.addingTimeInterval(-30 * 86400)
        let cutoffDay = String(ISO8601DateFormatter().string(from: cutoff).prefix(10))
        let hasRecentValue = available.contains { reading in
            if let observed = reading.latest.referenceDate { return observed >= cutoff }
            // ISO calendar dates choose a useful range without inventing an observation time.
            if let day = reading.latest.calendarDate { return day >= cutoffDay }
            return false
        }
        return hasRecentValue ? 30 : 365
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

enum SignalChartKind: Equatable {
    case trend, state, category, bars

    static func resolve(unit: String, statistic: String) -> Self {
        if statistic == "count" { return .bars }
        if unit == "boolean" { return .state }
        if unit == "code" { return .category }
        if unit == "count" || statistic == "sum" { return .bars }
        return .trend
    }
}

// A day number is a chart coordinate, not an observation timestamp or a guessed timezone.
enum SignalCalendarDay {
    private static var calendar: Calendar {
        var value = Calendar(identifier: .gregorian)
        value.timeZone = TimeZone(secondsFromGMT: 0)!
        return value
    }
    static func number(_ string: String) -> Double? {
        let parts = string.split(separator: "-").compactMap { Int($0) }
        guard parts.count == 3,
              let date = calendar.date(from: DateComponents(year: parts[0], month: parts[1], day: parts[2])),
              calendar.component(.year, from: date) == parts[0],
              calendar.component(.month, from: date) == parts[1],
              calendar.component(.day, from: date) == parts[2] else { return nil }
        return floor(date.timeIntervalSince1970 / 86400)
    }
    static func label(_ number: Double) -> String {
        let formatter = DateFormatter()
        formatter.calendar = calendar
        formatter.timeZone = calendar.timeZone
        formatter.setLocalizedDateFormatFromTemplate("MMMd")
        return formatter.string(from: Date(timeIntervalSince1970: number.rounded() * 86400))
    }
}

struct SignalChartBucket: Identifiable {
    let point: SignalHistoryPoint
    let start: Double
    let end: Double
    let calendar: Bool
    var id: String { point.id }
    var center: Double { (start + end) / 2 }
    var state: String {
        guard point.count > 0, [0, 1].contains(point.minimum), [0, 1].contains(point.maximum), point.minimum <= point.maximum else { return "Unknown" }
        if point.minimum != point.maximum { return "Mixed" }
        return point.minimum == 1 ? "On" : "Off"
    }
    var category: String {
        guard point.count > 0, point.minimum.isFinite, point.maximum.isFinite,
              point.minimum.rounded() == point.minimum, point.maximum.rounded() == point.maximum else { return "Unknown" }
        return point.minimum == point.maximum ? "Code \(point.minimum.formatted(.number.precision(.fractionLength(0))))" : "Mixed"
    }
}

struct SignalChartVertex: Identifiable {
    let id: String
    let x: Double
    let value: Double
    let segment: String
}

struct SignalStateMark: Identifiable {
    let id: String
    let start: Double
    let end: Double
    let label: String
    let summary: Bool
}

struct SignalChartData {
    let buckets: [SignalChartBucket]
    let vertices: [SignalChartVertex]
    let calendar: Bool
    let kind: SignalChartKind
    let statistic: String
    let valueLabels: [String: String]?

    init(series: SignalHistorySeries, unit: String, calendar: Bool, maximumJoinGapSeconds: Int = 900, valueLabels: [String: String]? = nil) {
        self.valueLabels = valueLabels
        self.calendar = calendar
        statistic = series.statistic
        kind = .resolve(unit: unit, statistic: series.statistic)
        buckets = series.points.compactMap { point in
            if calendar {
                guard let day = point.calendarDate.flatMap(SignalCalendarDay.number) else { return nil }
                return SignalChartBucket(point: point, start: day, end: day, calendar: true)
            }
            guard point.calendarDate == nil,
                  let first = (point.windowStart ?? point.firstObservedAt).flatMap(SignalFormat.date),
                  let last = (point.windowEnd ?? point.lastObservedAt).flatMap(SignalFormat.date) else { return nil }
            return SignalChartBucket(point: point, start: first.timeIntervalSince1970, end: last.timeIntervalSince1970, calendar: false)
        }.sorted { $0.start < $1.start }
        var result: [SignalChartVertex] = []
        var segment = 0
        var previous: SignalChartBucket?
        var lastX: Double?
        for bucket in buckets {
            guard kind == .trend else { continue }
            var values: [(Double, Double)] = []
            if calendar || series.statistic != "sample" {
                values = [(bucket.center, bucket.point.mean)]
            } else {
                if let first = bucket.point.firstObservedAt.flatMap(SignalFormat.date) {
                    values.append((first.timeIntervalSince1970, bucket.point.first))
                }
                if bucket.point.lastObservedAt != bucket.point.firstObservedAt,
                   let last = bucket.point.lastObservedAt.flatMap(SignalFormat.date) {
                    values.append((last.timeIntervalSince1970, bucket.point.last))
                }
            }
            let missingBucket: Bool
            if let previous {
                if calendar { missingBucket = bucket.start - previous.end > 1 }
                else if series.statistic == "sample",
                        let end = previous.point.bucketEnd.flatMap(SignalFormat.date),
                        let start = bucket.point.bucketStart.flatMap(SignalFormat.date) {
                    missingBucket = start.timeIntervalSince(end) > 0.001
                } else { missingBucket = bucket.start > previous.end }
            } else { missingBucket = false }
            if missingBucket { segment += 1 }
            for (index, value) in values.enumerated() {
                // This is a conservative drawing limit, not a claim about source sampling cadence.
                if !calendar, series.statistic == "sample", let lastX, value.0 - lastX > Double(maximumJoinGapSeconds) { segment += 1 }
                result.append(SignalChartVertex(id: "\(bucket.id)/\(index)", x: value.0, value: value.1, segment: "\(series.id)/\(segment)"))
                lastX = value.0
            }
            previous = bucket
        }
        vertices = result
    }

    var stateMarks: [SignalStateMark] {
        buckets.flatMap { bucket -> [SignalStateMark] in
            let rawLabel = kind == .state ? bucket.state : bucket.category
            let label = rawLabel == "Mixed" || rawLabel == "Unknown" ? rawLabel : SignalFormat.value(bucket.point.minimum, unit: kind == .state ? "boolean" : "code", labels: valueLabels)
            if calendar || statistic != "sample" {
                return [SignalStateMark(id: bucket.id, start: bucket.center, end: bucket.center, label: label, summary: true)]
            }
            if bucket.end - bucket.start <= 900 {
                return [SignalStateMark(id: bucket.id, start: bucket.start, end: bucket.end, label: label, summary: false)]
            }
            var marks: [SignalStateMark] = []
            for (name, timestamp, value) in [("first", bucket.point.firstObservedAt, bucket.point.first), ("last", bucket.point.lastObservedAt, bucket.point.last)] {
                if let date = timestamp.flatMap(SignalFormat.date) {
                    let x = date.timeIntervalSince1970
                    marks.append(SignalStateMark(id: "\(bucket.id)/\(name)", start: x, end: x,
                        label: SignalFormat.value(value, unit: kind == .state ? "boolean" : "code", labels: valueLabels), summary: false))
                }
            }
            if marks.isEmpty || label == "Mixed" || label == "Unknown" {
                marks.append(SignalStateMark(id: bucket.id, start: bucket.center, end: bucket.center, label: label, summary: true))
            }
            return marks
        }
    }

    var xDomain: ClosedRange<Double> {
        let first = buckets.map { kind == .bars ? min($0.start, barBounds($0).lowerBound) : $0.start }.min() ?? 0
        let last = buckets.map { kind == .bars ? max($0.end, barBounds($0).upperBound) : $0.end }.max() ?? first
        let padding = calendar ? 0.5 : max((last - first) * 0.025, 1)
        return (first - padding)...(last + padding)
    }
    func barBounds(_ bucket: SignalChartBucket) -> ClosedRange<Double> {
        let nearest = buckets.map { abs($0.center - bucket.center) }.filter { $0 > 0 }.min()
        let period = calendar ? 1 : bucket.end - bucket.start
        let available = period > 0 ? period : nearest ?? 60
        let halfWidth = min(available, nearest ?? available) * 0.325
        return (bucket.center - halfWidth)...(bucket.center + halfWidth)
    }
    func yDomain(unit: String) -> ClosedRange<Double> {
        let minimum = buckets.map { $0.point.minimum }.min() ?? 0
        let maximum = buckets.map { $0.point.maximum }.max() ?? 1
        if unit == "%", minimum >= 0, maximum <= 100 { return 0...100 }
        let padding = max((maximum - minimum) * 0.12, max(abs(maximum) * 0.05, 1))
        if kind == .bars { return (minimum >= 0 ? 0 : minimum - padding)...max(1, maximum + padding) }
        return (minimum - padding)...(maximum + padding)
    }
    var axisValues: [Double] {
        let low = buckets.first?.start ?? 0
        let high = buckets.map(\.end).max() ?? low
        if high <= low { return [low] }
        return Array(Set((0...3).map { index in
            let value = low + (high - low) * Double(index) / 3
            return calendar ? value.rounded() : value
        })).sorted()
    }
    func xLabel(_ value: Double) -> String {
        if calendar { return SignalCalendarDay.label(value) }
        let date = Date(timeIntervalSince1970: value)
        if xDomain.upperBound - xDomain.lowerBound < 86400 { return date.formatted(date: .omitted, time: .shortened) }
        return date.formatted(.dateTime.month(.abbreviated).day())
    }
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
    let codes: [String]?
    let successfulReads: Int
    let unknown: Bool
    private enum CodingKeys: String, CodingKey { case `class`, codes, successfulReads, unknown }
    init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        self.class = try values.decode(String.self, forKey: .class)
        successfulReads = try values.decode(Int.self, forKey: .successfulReads)
        unknown = try values.decode(Bool.self, forKey: .unknown)
        codes = try values.decodeIfPresent([String].self, forKey: .codes)
        if !unknown, codes == nil {
            throw DecodingError.dataCorruptedError(forKey: .codes, in: values, debugDescription: "Known diagnostics require an explicit codes array")
        }
    }
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
    static func value(_ number: Double, unit: String, labels: [String: String]? = nil) -> String {
        if number.isFinite, number.rounded() == number, let label = labels?[String(format: "%.0f", locale: Locale(identifier: "en_US_POSIX"), number)] { return label }
        if unit == "boolean" { return number == 0 ? "Off" : number == 1 ? "On" : "Unknown" }
        if unit == "code" { return number.rounded() == number ? "Code \(number.formatted(.number.precision(.fractionLength(0))))" : "Unknown code" }
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
