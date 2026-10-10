import Foundation

enum DisplayUnits: String, CaseIterable {
    case metric, imperial

    static let preferenceKey = "displayUnits"

    var title: String { self == .metric ? "Metric" : "Imperial (US)" }

    func value(_ number: Double, unit: String) -> Double {
        let conversion = conversion(for: unit)
        return number * conversion.scale + conversion.offset
    }

    func unit(_ sourceUnit: String) -> String { conversion(for: sourceUnit).unit }

    func formatted(_ number: Double, unit: String, labels: [String: String]? = nil) -> String {
        // Semantic codes refer to the original value, never a converted physical quantity.
        if number.isFinite, number.rounded() == number,
           let label = labels?[String(format: "%.0f", locale: Locale(identifier: "en_US_POSIX"), number)] { return label }
        return SignalFormat.value(value(number, unit: unit), unit: self.unit(unit))
    }

    func explanation(_ text: String, unit: String) -> String {
        guard self == .imperial else { return text }
        switch unit {
        case "L", "L/h": return text.replacingOccurrences(of: "liters", with: "US gallons")
        case "g/s": return text.replacingOccurrences(of: "grams per second", with: "pounds per minute")
        default: return text
        }
    }

    private struct Conversion {
        let unit: String
        var scale: Double = 1
        var offset: Double = 0
    }

    private func conversion(for source: String) -> Conversion {
        let metric: Conversion
        switch source {
        case "mi", "mile", "miles": metric = Conversion(unit: "km", scale: 1.609344)
        case "mph": metric = Conversion(unit: "km/h", scale: 1.609344)
        case "ft", "feet": metric = Conversion(unit: "m", scale: 0.3048)
        case "°F", "fahrenheit": metric = Conversion(unit: "°C", scale: 5 / 9, offset: -32 * 5 / 9)
        case "celsius": metric = Conversion(unit: "°C")
        case "psi": metric = Conversion(unit: "kPa", scale: 6.894757293168361)
        case "gal", "US gal": metric = Conversion(unit: "L", scale: 3.785411784)
        case "gal/h", "US gal/h": metric = Conversion(unit: "L/h", scale: 3.785411784)
        case "lb/min": metric = Conversion(unit: "g/s", scale: 453.59237 / 60)
        default: metric = Conversion(unit: source)
        }
        guard self == .imperial else { return metric }
        let imperial: Conversion
        switch metric.unit {
        case "km": imperial = Conversion(unit: "mi", scale: 1 / 1.609344)
        case "km/h": imperial = Conversion(unit: "mph", scale: 1 / 1.609344)
        case "m": imperial = Conversion(unit: "ft", scale: 1 / 0.3048)
        case "°C": imperial = Conversion(unit: "°F", scale: 9 / 5, offset: 32)
        case "kPa": imperial = Conversion(unit: "psi", scale: 1 / 6.894757293168361)
        case "Pa": imperial = Conversion(unit: "psi", scale: 1 / 6894.757293168361)
        case "L": imperial = Conversion(unit: "US gal", scale: 1 / 3.785411784)
        case "L/h": imperial = Conversion(unit: "US gal/h", scale: 1 / 3.785411784)
        case "g/s": imperial = Conversion(unit: "lb/min", scale: 60 / 453.59237)
        default: return metric
        }
        if source == imperial.unit { return Conversion(unit: source) }
        return Conversion(unit: imperial.unit, scale: metric.scale * imperial.scale,
                          offset: metric.offset * imperial.scale + imperial.offset)
    }
}
