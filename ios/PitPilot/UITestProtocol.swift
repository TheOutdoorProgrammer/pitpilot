#if DEBUG
import Foundation
import UIKit

// This transport only exists in Debug and is activated explicitly by the UI test runner.
final class UITestProtocol: URLProtocol {
    private static let populated = ProcessInfo.processInfo.arguments.contains("--ui-testing-populated")
    private static let migration = ProcessInfo.processInfo.arguments.contains("--ui-testing-migration")
    private static let signals = ProcessInfo.processInfo.arguments.contains("--ui-testing-signals")
    private static let cockpit = ProcessInfo.processInfo.arguments.contains("--ui-testing-cockpit")
    static var vehicles: [[String: Any]] = {
        guard populated || migration || signals || cockpit || ProcessInfo.processInfo.arguments.contains("--ui-testing-integrations") else { return [] }
        var vehicle: [String: Any] = ["id": "test-vehicle", "name": "Synthetic route truck", "make": "", "model": "", "year": 2002, "odometerMiles": 120000, "createdAt": "2026-01-01T00:00:00Z"]
        if migration {
            vehicle["vin"] = "SYNTHETIC-VIN"
            vehicle["odometerStatus"] = "estimated"
            vehicle["licensePlate"] = "TEST-ONLY"
            vehicle["extraFields"] = [["name": "Registration region", "value": "Synthetic region", "isRequired": false, "fieldType": 0]]
            vehicle["source"] = ["system": "lubelogger", "instance": "synthetic", "collection": "vehicles", "id": "1"]
        }
        if cockpit { vehicle["photoRevision"] = "synthetic-photo"; vehicle["vin"] = "SYNTHETIC-VIN"; vehicle["licensePlate"] = "TEST-ONLY" }
        return [vehicle]
    }()
    static var records: [[String: Any]] = migration ? [
        ["id": "test-note", "vehicleId": "test-vehicle", "kind": "note", "title": "Synthetic collector journal", "date": "", "notes": "Migration acceptance note", "odometerMiles": 0, "costCents": 0, "pinned": true, "tags": ["collector"], "extraFields": [["name": "Sensor provenance", "value": "Synthetic sensor", "isRequired": false, "fieldType": 0]], "source": ["system": "lubelogger", "instance": "synthetic", "collection": "notes", "id": "12"]],
        ["id": "test-reading", "vehicleId": "test-vehicle", "kind": "odometer", "title": "Synthetic daily reading", "date": "2026-10-08", "notes": "Collector estimate", "odometerMiles": 120100, "initialOdometerMiles": 120000, "odometerStatus": "estimated", "costCents": 0],
        ["id": "test-plan", "vehicleId": "test-vehicle", "kind": "plan", "title": "Synthetic planned repair", "date": "", "notes": "Work has not happened", "odometerMiles": 0, "costCents": 10000, "plan": ["status": "planned", "priority": "critical", "recordKind": "repair", "reminderIds": ["test-reminder"]]]
    ] : populated ? [[
        "id": "test-record", "vehicleId": "test-vehicle", "kind": "service", "title": "Synthetic oil service",
        "date": "2026-01-01", "notes": "UI acceptance fixture", "odometerMiles": 120000, "costCents": 4500
    ]] : []
    static var reminders: [[String: Any]] = migration ? [[
        "id": "test-reminder", "vehicleId": "test-vehicle", "title": "Synthetic recurring oil", "dueOdometerMiles": 125000, "completed": false,
        "recurrence": ["miles": 5000, "fixedIntervals": false], "notes": "Recurring migration fixture"
    ]] : populated ? [[
        "id": "test-reminder", "vehicleId": "test-vehicle", "title": "Synthetic tire inspection",
        "dueDate": "2026-12-01", "completed": false
    ]] : []
    static var trips: [[String: Any]] = populated || cockpit ? [[
        "id": "test-trip", "vehicleId": "test-vehicle", "title": "Synthetic park loop",
        "startedAt": "2026-01-01T12:00:00Z", "endedAt": "2026-01-01T12:03:00Z", "distanceMiles": 0.4,
        "source": "pi-gps", "distanceQuality": "derived", "recordedPointCount": 4,
        "points": [
            ["latitude": 40.7712, "longitude": -73.9744, "recordedAt": "2026-01-01T12:00:00Z"],
            ["latitude": 40.7720, "longitude": -73.9740, "recordedAt": "2026-01-01T12:01:00Z"],
            ["latitude": 40.7727, "longitude": -73.9730, "recordedAt": "2026-01-01T12:02:00Z"],
            ["latitude": 40.7733, "longitude": -73.9726, "recordedAt": "2026-01-01T12:03:00Z"]
        ]
    ]] : []
    static var location: [String: Any]? = cockpit ? ["latitude": 40.7733, "longitude": -73.9726, "recordedAt": "2026-01-01T12:03:00Z", "source": "pi-gps", "accuracyMeters": 8] : nil
    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "pitpilot.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        if ProcessInfo.processInfo.arguments.contains("--ui-testing-offline") {
            client?.urlProtocol(self, didFailWithError: URLError(.notConnectedToInternet))
            return
        }
        let route = request.url!.path
        var code = 200
        var response: Any = []
        var body: [String: Any] = [:]
        var binary: Data?
        if let data = request.httpBody { body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:] }
        else if let stream = request.httpBodyStream {
            stream.open()
            defer { stream.close() }
            var data = Data()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable { let count = stream.read(&buffer, maxLength: buffer.count); if count <= 0 { break }; data.append(buffer, count: count) }
            body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        }
        if request.value(forHTTPHeaderField: "Authorization") != "Bearer test-token" { code = 401; response = ["error": "Unauthorized"] }
        else if route == "/api/v1/client-events" { code = 204 }
        else if route == "/api/v1/vehicles/test-vehicle/photo", !Self.vehicles.isEmpty {
            if request.httpMethod == "DELETE" { Self.vehicles[0].removeValue(forKey: "photoRevision"); response = Self.vehicles[0] }
            else if request.httpMethod == "PUT" { Self.vehicles[0]["photoRevision"] = "synthetic-updated-photo"; response = Self.vehicles[0] }
            else {
                binary = UIGraphicsImageRenderer(size: CGSize(width: 320, height: 160)).image { context in
                    UIColor.darkGray.setFill(); context.fill(CGRect(x: 0, y: 0, width: 320, height: 160))
                    UIColor.orange.setFill(); context.fill(CGRect(x: 60, y: 50, width: 200, height: 60))
                }.jpegData(compressionQuality: 0.8)
            }
        }
        else if route == "/api/v1/vehicles/test-vehicle/location" { response = ["location": Self.location as Any? ?? NSNull()] }
        else if route == "/api/v1/vehicles/test-vehicle/location-history", request.httpMethod == "DELETE" {
            Self.location = nil; Self.trips.removeAll { $0["source"] as? String == "pi-gps" }; code = 204
        }
        else if route == "/api/v1/vehicles/test-vehicle", request.httpMethod == "PATCH", !Self.vehicles.isEmpty {
            for (key, value) in body { Self.vehicles[0][key] = value }; response = Self.vehicles[0]
        }
        else if route == "/api/v1/trips/test-trip", request.httpMethod == "DELETE" { Self.trips.removeAll(); code = 204 }
        else if let fixture = IntegrationFixture.reply(route: route, method: request.httpMethod ?? "GET", body: body) { code = fixture.0; response = fixture.1 }
        else if route.hasSuffix("/signals/latest") { response = Self.signalLatest }
        else if route.hasSuffix("/signals/history") {
            if ProcessInfo.processInfo.arguments.contains("--ui-testing-history-error") { code = 503; response = ["error": "Synthetic unavailable"] }
            else { response = Self.signalHistory(request.url!) }
        }
        else if route == "/api/v1/vehicles" {
            if request.httpMethod == "POST" {
                body["id"] = "test-vehicle"; body["createdAt"] = "2026-01-01T00:00:00Z"
                Self.vehicles.append(body); response = body; code = 201
            } else { response = Self.vehicles }
        } else if route.hasSuffix("/records") {
            if request.httpMethod == "POST" { body["id"] = "test-record"; body["vehicleId"] = "test-vehicle"; if body["odometerMiles"] == nil { body["odometerMiles"] = 0 }; if body["costCents"] == nil { body["costCents"] = 0 }; Self.records.append(body); response = body; code = 201 }
            else { response = Self.records }
        } else if route.hasSuffix("/reminders") {
            if request.httpMethod == "POST" { body["id"] = "test-reminder"; body["vehicleId"] = "test-vehicle"; body["completed"] = false; Self.reminders.append(body); response = body; code = 201 }
            else { response = Self.reminders }
        } else if route == "/api/v1/reminders/test-reminder", request.httpMethod == "PATCH", !Self.reminders.isEmpty {
            if Self.migration, body["completed"] as? Bool == true {
                if let miles = body["completionOdometerMiles"] as? Double,
                   let expected = body["expectedDueOdometerMiles"] as? Double,
                   expected == (Self.reminders[0]["dueOdometerMiles"] as? NSNumber)?.doubleValue {
                    Self.reminders[0]["dueOdometerMiles"] = miles + 5000
                    Self.reminders[0]["completed"] = false
                    response = Self.reminders[0]
                } else { code = 409; response = ["error": "Stale schedule"] }
            } else {
                Self.reminders[0]["completed"] = body["completed"]
                response = Self.reminders[0]
            }
        } else if route.hasPrefix("/api/v1/records/"), request.httpMethod == "PATCH",
                  let index = Self.records.firstIndex(where: { $0["id"] as? String == request.url!.lastPathComponent }) {
            for (key, value) in body { Self.records[index][key] = value }
            response = Self.records[index]
        } else if route.hasSuffix("/trips") {
            response = Self.trips
        }
        let data = binary ?? (try! JSONSerialization.data(withJSONObject: response))
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: code, httpVersion: nil, headerFields: ["Content-Type": binary == nil ? "application/json" : "image/jpeg"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}

    private static let signalNow = Date()
    private static func signalTime(_ offset: TimeInterval) -> String { ISO8601DateFormatter().string(from: signalNow.addingTimeInterval(offset)) }
    private static func signalDay(_ offset: Int) -> String { String(signalTime(Double(offset) * 86400).prefix(10)) }
    private static var signalLatest: [String: Any] {
        guard signals else { return ["asOf": signalTime(0), "definitions": [], "series": [], "contexts": []] }
        var result: [String: Any] = ["asOf": signalTime(0), "definitions": [
            ["metric": "fuel_level_pct", "label": "Fuel level", "unit": "%", "staleAfterSeconds": 900, "description": "Fuel remaining as a percentage of tank capacity.", "interpretation": "Slopes and movement can affect the reported level. Compare readings under similar conditions."],
            ["metric": "manifold_kpa", "label": "Manifold pressure", "unit": "kPa", "staleAfterSeconds": 900],
            ["metric": "mil_on", "label": "Malfunction indicator", "unit": "boolean", "staleAfterSeconds": 900],
            ["metric": "fuel_system_1_status", "label": "Fuel system status", "unit": "code", "staleAfterSeconds": 900],
            ["metric": "retained_samples", "label": "Recorded samples", "unit": "count", "staleAfterSeconds": 900],
            ["metric": "rpm", "label": "Engine speed", "unit": "rpm", "staleAfterSeconds": 900]
        ], "series": [
            ["metric": "fuel_level_pct", "unit": "%", "source": "smartcar", "statistic": "snapshot", "quality": "measured", "stale": true,
             "latest": ["key": "synthetic-fuel", "metric": "fuel_level_pct", "unit": "%", "statistic": "snapshot", "quality": "measured", "value": 0, "calendarDate": signalDay(-47), "timezone": "unknown"]],
            ["metric": "mil_on", "unit": "boolean", "source": "pi", "statistic": "sample", "quality": "measured", "stale": true,
             "latest": ["key": "synthetic-mil", "metric": "mil_on", "unit": "boolean", "statistic": "sample", "quality": "measured", "value": 1, "observedAt": signalTime(-3600)]],
            ["metric": "fuel_system_1_status", "unit": "code", "source": "pi", "statistic": "sample", "quality": "measured", "stale": true,
             "latest": ["key": "synthetic-status", "metric": "fuel_system_1_status", "unit": "code", "statistic": "sample", "quality": "measured", "value": 4, "observedAt": signalTime(-3600)]],
            ["metric": "retained_samples", "unit": "count", "source": "pi", "statistic": "sum", "quality": "measured", "stale": true,
             "latest": ["key": "synthetic-count", "metric": "retained_samples", "unit": "count", "statistic": "sum", "quality": "measured", "value": 130, "periodStart": signalTime(-172800), "periodEnd": signalTime(-86400)]],
            ["metric": "manifold_kpa", "unit": "kPa", "source": "pi", "statistic": "sample", "quality": "measured", "stale": true,
             "latest": ["key": "synthetic-manifold", "metric": "manifold_kpa", "unit": "kPa", "statistic": "sample", "quality": "measured", "value": 39, "observedAt": signalTime(-3400)]],
            ["metric": "manifold_kpa", "unit": "kPa", "source": "pi", "statistic": "max", "quality": "measured", "stale": true,
             "latest": ["key": "synthetic-manifold-max", "metric": "manifold_kpa", "unit": "kPa", "statistic": "max", "quality": "measured", "value": 84, "periodStart": signalTime(-172800), "periodEnd": signalTime(-86400)]]
        ], "contexts": []]
        if ProcessInfo.processInfo.arguments.contains("--ui-testing-speed") {
            var definitions = result["definitions"] as! [[String: Any]]
            definitions.append(["metric": "speed_kph", "label": "Vehicle speed", "unit": "km/h", "staleAfterSeconds": 60,
                "description": "Instantaneous road speed reported by the vehicle.", "interpretation": "A parked reading can be zero even when an earlier driving period had a nonzero average."])
            result["definitions"] = definitions
            var series = result["series"] as! [[String: Any]]
            series.append(["metric": "speed_kph", "unit": "km/h", "source": "pi", "statistic": "sample", "quality": "measured", "stale": true,
                "latest": ["key": "synthetic-speed", "metric": "speed_kph", "unit": "km/h", "statistic": "sample", "quality": "measured", "value": 0, "observedAt": signalTime(-1685)]])
            series.append(["metric": "speed_kph", "unit": "km/h", "source": "lubelogger", "statistic": "mean", "quality": "derived", "stale": true,
                "latest": ["key": "synthetic-speed-mean", "metric": "speed_kph", "unit": "km/h", "statistic": "mean", "quality": "derived", "value": 34.2, "periodStart": signalTime(-4 * 86400), "periodEnd": signalTime(-3 * 86400)]])
            result["series"] = series
        }
        return result
    }

    private static func signalHistory(_ url: URL) -> [String: Any] {
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        func value(_ name: String) -> String { query.first { $0.name == name }?.value ?? "" }
        let metric = value("metric")
        let statistic = value("statistic")
        if metric == "speed_kph" { return speedHistory(from: value("from"), to: value("to")) }
        let unit = ["fuel_level_pct": "%", "mil_on": "boolean", "fuel_system_1_status": "code", "retained_samples": "count"][metric] ?? "kPa"
        var response: [String: Any] = ["metric": metric, "unit": unit, "from": value("from"), "to": value("to"), "maxPoints": 120, "series": []]
        if ProcessInfo.processInfo.arguments.contains("--ui-testing-history-empty") { return response }
        if statistic == "all" {
            let statistics = metric == "fuel_level_pct" ? ["snapshot"] : metric == "manifold_kpa" ? ["sample", "max"] : metric == "retained_samples" ? ["sum"] : ["sample"]
            var combined: [[String: Any]] = []
            for statistic in statistics {
                var components = URLComponents(url: url, resolvingAgainstBaseURL: false)!
                components.queryItems = query.filter { $0.name != "statistic" } + [URLQueryItem(name: "statistic", value: statistic)]
                combined += signalHistory(components.url!)["series"] as? [[String: Any]] ?? []
            }
            if metric == "fuel_level_pct" {
                for (index, source) in ["pi", "smartcar"].enumerated() {
                    let start = signalTime(-48 * 86400 + Double(index * 300))
                    let end = signalTime(-48 * 86400 + Double(index * 300 + 240))
                    if start >= value("from"), end <= value("to") {
                        combined.append(["source": source, "quality": "measured", "statistic": "sample", "unit": "%", "points": [[
                            "bucketStart": start, "bucketEnd": end, "windowStart": start, "windowEnd": end,
                            "firstObservedAt": start, "lastObservedAt": end, "minimum": 21, "maximum": 28,
                            "mean": 24, "first": 28, "last": 21, "count": 12]]])
                    }
                }
            }
            response["series"] = combined
            return response
        }
        var points: [[String: Any]] = []
        if metric == "fuel_level_pct" {
            for index in 0..<94 where ![23, 24, 25, 61, 62].contains(index) {
                let day = signalDay(-140 + index)
                guard day >= String(value("from").prefix(10)), day <= String(value("to").prefix(10)) else { continue }
                let fuel = index == 93 ? 0 : 94 - Double(index % 19) * 4.8
                points.append(["calendarDate": day, "timezone": "unknown", "minimum": fuel, "maximum": fuel, "mean": fuel, "first": fuel, "last": fuel, "count": 1])
            }
        } else if metric == "retained_samples" {
            for index in 0..<8 where index != 4 {
                let amount = Double(40 + index * 10)
                points.append(["bucketStart": signalTime(Double(-9 + index) * 86400), "bucketEnd": signalTime(Double(-8 + index) * 86400),
                    "windowStart": signalTime(Double(-9 + index) * 86400), "windowEnd": signalTime(Double(-8 + index) * 86400),
                    "minimum": index == 0 ? 0 : amount - 20, "maximum": index == 0 ? 0 : amount + 20, "mean": index == 0 ? 0 : amount,
                    "first": index == 0 ? 0 : amount - 20, "last": index == 0 ? 0 : amount + 20, "count": 2])
            }
        } else if metric == "mil_on" || metric == "fuel_system_1_status" {
            for index in 0..<8 where index != 4 {
                let start = Double(-7200 + index * 450)
                let low = metric == "mil_on" ? Double(index % 2) : Double(index % 2 == 0 ? 2 : 4)
                let high = index == 2 ? (metric == "mil_on" ? 1.0 : 4.0) : low
                points.append(["bucketStart": signalTime(start), "bucketEnd": signalTime(start + 450), "windowStart": signalTime(start + 30), "windowEnd": signalTime(start + 420),
                    "minimum": low, "maximum": high, "mean": (low + high) / 2, "first": low, "last": high, "count": 8,
                    "firstObservedAt": signalTime(start + 30), "lastObservedAt": signalTime(start + 420)])
            }
        } else if statistic == "sample" {
            for index in 0..<16 where index != 8 {
                let start = Double(-7200 + index * 240)
                let first = 34 + Double(index % 5) * 7
                let last = first + 5
                points.append(["bucketStart": signalTime(start), "bucketEnd": signalTime(start + 240), "windowStart": signalTime(start + 30), "windowEnd": signalTime(start + 200),
                    "minimum": first - 3, "maximum": last + 3, "mean": (first + last) / 2, "first": first, "last": last, "count": 8,
                    "firstObservedAt": signalTime(start + 30), "lastObservedAt": signalTime(start + 200)])
            }
        } else {
            points = [["bucketStart": signalTime(-172800), "bucketEnd": signalTime(-86400), "windowStart": signalTime(-172800), "windowEnd": signalTime(-86400), "minimum": 78, "maximum": 84, "mean": 81, "first": 78, "last": 84, "count": 2]]
        }
        response["series"] = [["source": metric == "fuel_level_pct" ? "lubelogger" : "pi", "quality": "measured", "statistic": statistic, "unit": unit, "points": points]]
        return response
    }

    private static func speedHistory(from: String, to: String) -> [String: Any] {
        let requestedStart = SignalFormat.date(from)!, requestedEnd = SignalFormat.date(to)!
        let width = requestedEnd.timeIntervalSince(requestedStart) / 120
        func included(_ timestamp: String) -> Bool { timestamp >= from && timestamp < to }
        var oldSamples: [(Date, Double)] = []
        var averages: [[String: Any]] = []
        for index in 0..<2 {
            let start = signalTime(Double(-5 + index) * 86400), end = signalTime(Double(-4 + index) * 86400)
            let at = signalTime(Double(-5 + index) * 86400 + 64800)
            let value = index == 0 ? 98.0 : 0.0, mean = index == 0 ? 72.4 : 34.2
            if included(at) {
                oldSamples.append((SignalFormat.date(at)!, value))
            }
            if included(start) {
                let bucket = min(119, max(0, Int(SignalFormat.date(end)!.timeIntervalSince(requestedStart) / width)))
                let bucketStart = requestedStart.addingTimeInterval(Double(bucket) * width)
                averages.append(["bucketStart": ISO8601DateFormatter().string(from: bucketStart), "bucketEnd": ISO8601DateFormatter().string(from: min(bucketStart.addingTimeInterval(width), requestedEnd)), "windowStart": start, "windowEnd": end,
                    "minimum": mean, "maximum": mean, "mean": mean, "first": mean, "last": mean, "count": 1])
            }
        }
        var raw: [(Date, Double)] = []
        for index in 0..<32 {
            let offset = Double(-7200 + index * 60 + (index >= 16 ? 3600 : 0))
            let maximum = Double(30 + (index % 8) * 10)
            for sample in 0..<12 {
                let value = sample <= 5 ? maximum * (0.4 + 0.6 * Double(sample) / 5) : maximum * (1 - 0.8 * Double(sample - 5) / 6)
                let stopped = (index == 15 || index == 31) && sample == 11
                raw.append((SignalFormat.date(signalTime(offset + Double(sample * 5)))!, stopped ? 0 : value))
            }
        }
        let samples = reducedSpeedSamples(oldSamples, from: requestedStart, to: requestedEnd, maxPoints: 120)
        let recovered = reducedSpeedSamples(raw, from: requestedStart, to: requestedEnd, maxPoints: 120)
        let summaryBuckets = Dictionary(grouping: averages) { $0["bucketStart"] as! String }
        let reducedAverages = summaryBuckets.keys.sorted().map { key -> [String: Any] in
            let periods = summaryBuckets[key]!
            var result = periods[0]
            let values = periods.map { $0["mean"] as! Double }
            result["minimum"] = values.min()!; result["maximum"] = values.max()!
            result["mean"] = values.reduce(0, +) / Double(values.count)
            result["last"] = values.last!; result["count"] = values.count
            result["windowStart"] = periods.map { $0["windowStart"] as! String }.min()!
            result["windowEnd"] = periods.map { $0["windowEnd"] as! String }.max()!
            return result
        }
        return ["metric": "speed_kph", "unit": "km/h", "from": from, "to": to, "maxPoints": 120, "series": [
            ["source": "lubelogger", "quality": "measured", "statistic": "sample", "unit": "km/h", "points": samples],
            ["source": "lubelogger", "quality": "derived", "statistic": "mean", "unit": "km/h", "points": reducedAverages],
            ["source": "pi", "quality": "measured", "statistic": "sample", "unit": "km/h", "points": recovered]
        ]]
    }

    static func reducedSpeedSamples(_ samples: [(Date, Double)], from: Date, to: Date, maxPoints: Int) -> [[String: Any]] {
        let width = to.timeIntervalSince(from) / Double(maxPoints)
        let included = samples.filter { $0.0 >= from && $0.0 < to }.sorted { $0.0 < $1.0 }
        let buckets = Dictionary(grouping: included) { min(maxPoints - 1, Int($0.0.timeIntervalSince(from) / width)) }
        let formatter = ISO8601DateFormatter()
        return buckets.keys.sorted().map { index in
            let points = buckets[index]!, first = points.first!, last = points.last!
            let minimum = points.reduce(first) { $1.1 < $0.1 ? $1 : $0 }
            let maximum = points.reduce(first) { $1.1 > $0.1 ? $1 : $0 }
            let gap = zip(points, points.dropFirst()).map { pair in pair.1.0.timeIntervalSince(pair.0.0) }.max() ?? 0
            let bucketStart = from.addingTimeInterval(Double(index) * width)
            return ["bucketStart": formatter.string(from: bucketStart), "bucketEnd": formatter.string(from: min(bucketStart.addingTimeInterval(width), to)),
                "windowStart": formatter.string(from: first.0), "windowEnd": formatter.string(from: last.0),
                "minimum": minimum.1, "maximum": maximum.1, "mean": points.map { $0.1 }.reduce(0, +) / Double(points.count),
                "first": first.1, "last": last.1, "count": points.count,
                "firstObservedAt": formatter.string(from: first.0), "lastObservedAt": formatter.string(from: last.0),
                "minimumObservedAt": formatter.string(from: minimum.0), "maximumObservedAt": formatter.string(from: maximum.0), "maxGapSeconds": gap]
        }
    }
}
#endif
