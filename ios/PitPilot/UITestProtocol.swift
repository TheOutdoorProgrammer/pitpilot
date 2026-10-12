#if DEBUG
import Foundation
import UIKit

// This transport only exists in Debug and is activated explicitly by the UI test runner.
final class UITestProtocol: URLProtocol {
    private static let populated = ProcessInfo.processInfo.arguments.contains("--ui-testing-populated")
    private static let migration = ProcessInfo.processInfo.arguments.contains("--ui-testing-migration")
    private static let signals = ProcessInfo.processInfo.arguments.contains("--ui-testing-signals")
    private static let cockpit = ProcessInfo.processInfo.arguments.contains("--ui-testing-cockpit")
    private static let smartcarLocation = ProcessInfo.processInfo.arguments.contains("--ui-testing-smartcar-location")
    static var vehicles: [[String: Any]] = {
        guard populated || migration || signals || cockpit || smartcarLocation || ProcessInfo.processInfo.arguments.contains("--ui-testing-integrations") else { return [] }
        var vehicle: [String: Any] = ["id": "test-vehicle", "name": "Synthetic route truck", "make": "", "model": "", "year": 2002, "odometerMiles": 120000, "createdAt": "2026-01-01T00:00:00Z"]
        if migration {
            vehicle["vin"] = "SYNTHETIC-VIN"
            vehicle["odometerStatus"] = "estimated"
            vehicle["licensePlate"] = "TEST-ONLY"
            vehicle["extraFields"] = [["name": "Registration region", "value": "Synthetic region", "isRequired": false, "fieldType": 0]]
            vehicle["source"] = ["system": "lubelogger", "instance": "synthetic", "collection": "vehicles", "id": "1"]
        }
        if cockpit { vehicle["photoRevision"] = "synthetic-photo"; vehicle["vin"] = "SYNTHETIC-VIN"; vehicle["licensePlate"] = "TEST-ONLY" }
        if smartcarLocation {
            return [vehicle, ["id": "smartcar-vehicle", "name": "Synthetic connected car", "make": "", "model": "", "year": 2019, "odometerMiles": 45000, "createdAt": "2026-01-01T00:00:00Z"]]
        }
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
    static var location: [String: Any]? = cockpit ? ["latitude": 40.7733, "longitude": -73.9726, "recordedAt": "2026-01-01T12:03:00Z", "source": "pi-gps", "locationType": "gps", "accuracyMeters": 8] : nil
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
        else if route == "/api/v1/vehicles/smartcar-vehicle/location", Self.smartcarLocation {
            response = ["location": ["latitude": 40.774, "longitude": -73.973, "recordedAt": "2026-01-01T14:00:00Z", "source": "smartcar", "locationType": "LAST_PARKED"]]
        }
        else if Self.smartcarLocation, ["/api/v1/vehicles/smartcar-vehicle/trips", "/api/v1/vehicles/smartcar-vehicle/records", "/api/v1/vehicles/smartcar-vehicle/reminders"].contains(route) { response = [] }
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
        else if route.hasSuffix("/signals/previews") { response = Self.signalPreviews(request.url!) }
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

    private static let signalNow = Date(timeIntervalSince1970: floor(Date().timeIntervalSince1970))
    private static func signalTime(_ offset: TimeInterval) -> String { ISO8601DateFormatter().string(from: signalNow.addingTimeInterval(offset)) }
    private static func signalDay(_ offset: Int) -> String { String(signalTime(Double(offset) * 86400).prefix(10)) }
    private static var signalLatest: [String: Any] {
        guard signals else { return ["asOf": signalTime(0), "definitions": [], "series": [], "contexts": []] }
        if ProcessInfo.processInfo.arguments.contains("--ui-testing-dense-drive") { return SparseDriveFixture.latest(now: signalNow, dense: true) }
        if ProcessInfo.processInfo.arguments.contains("--ui-testing-sparse-drives") { return SparseDriveFixture.latest(now: signalNow) }
        return ["asOf": signalTime(0), "definitions": [
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
    }

    private static func signalHistory(_ url: URL) -> [String: Any] {
        if ProcessInfo.processInfo.arguments.contains("--ui-testing-dense-drive") { return SparseDriveFixture.history(url: url, now: signalNow, dense: true) }
        if ProcessInfo.processInfo.arguments.contains("--ui-testing-sparse-drives") { return SparseDriveFixture.history(url: url, now: signalNow) }
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        func value(_ name: String) -> String { query.first { $0.name == name }?.value ?? "" }
        let metric = value("metric")
        let statistic = value("statistic")
        let unit = ["fuel_level_pct": "%", "mil_on": "boolean", "fuel_system_1_status": "code", "retained_samples": "count"][metric] ?? "kPa"
        var response: [String: Any] = ["metric": metric, "unit": unit, "from": value("from"), "to": value("to"), "maxPoints": Int(value("maxPoints")) ?? 120, "series": []]
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

    private static func signalPreviews(_ url: URL) -> [String: Any] {
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let metrics = query.first { $0.name == "metrics" }?.value?.split(separator: ",").map(String.init) ?? []
        let budget = min(10, max(1, Int(query.first { $0.name == "maxPoints" }?.value ?? "") ?? 10))
        let dense = ProcessInfo.processInfo.arguments.contains("--ui-testing-dense-drive")
        let drive = dense || ProcessInfo.processInfo.arguments.contains("--ui-testing-sparse-drives")
        let previews: [[String: Any]] = metrics.map { metric in
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            var from = signalNow.addingTimeInterval(-30 * 86400)
            var to = signalNow
            var truncated = false
            let slow = ["fuel_level_pct", "retained_samples"].contains(metric)
            if drive {
                let samples = dense ? DenseDriveFixture.samples(metric: metric, now: signalNow) : SparseDriveFixture.samples(metric: metric, now: signalNow)
                let boundary = samples.indices.dropFirst().last { samples[$0].date.timeIntervalSince(samples[$0 - 1].date) > 900 } ?? 0
                let session = Array(samples[boundary...])
                truncated = session.count > 512
                from = session.suffix(512).first!.date
                to = session.last!.date.addingTimeInterval(0.001)
            }
            var components = URLComponents(url: url, resolvingAgainstBaseURL: false)!
            components.queryItems = [URLQueryItem(name: "metric", value: metric), URLQueryItem(name: "statistic", value: "all"),
                URLQueryItem(name: "from", value: formatter.string(from: from)), URLQueryItem(name: "to", value: formatter.string(from: to)),
                URLQueryItem(name: "maxPoints", value: String(budget)), URLQueryItem(name: "binning", value: "time")]
            var history = signalHistory(components.url!)
            let series = history["series"] as? [[String: Any]] ?? []
            func lastDate(_ item: [String: Any]) -> String {
                let point = (item["points"] as? [[String: Any]])?.last ?? [:]
                return point["lastObservedAt"] as? String ?? point["windowEnd"] as? String ?? point["calendarDate"] as? String ?? ""
            }
            var selected = series.filter { !(($0["points"] as? [[String: Any]]) ?? []).isEmpty }.max { lastDate($0) < lastDate($1) }
            if var item = selected, let points = item["points"] as? [[String: Any]], points.count > budget {
                // The legacy fixture contains pre-aggregated points, so merge counts rather than averaging its averages.
                let groups = Dictionary(grouping: points.indices) { $0 * budget / points.count }
                item["points"] = groups.keys.sorted().map { key -> [String: Any] in
                    let values = groups[key]!.map { points[$0] }
                    var point = values.first!
                    let count = values.reduce(0) { $0 + (($1["count"] as? Int) ?? 1) }
                    point["bucketIndex"] = key
                    point["count"] = count
                    point["mean"] = values.reduce(0.0) { $0 + (($1["mean"] as? Double) ?? 0) * Double(($1["count"] as? Int) ?? 1) } / Double(count)
                    point["minimum"] = values.compactMap { $0["minimum"] as? Double }.min()
                    point["maximum"] = values.compactMap { $0["maximum"] as? Double }.max()
                    for field in ["bucketEnd", "windowEnd", "lastObservedAt", "last"] { point[field] = values.last![field] }
                    return point
                }
                selected = item
            }
            history["series"] = selected.map { [$0] } ?? []
            if let points = selected?["points"] as? [[String: Any]],
               let first = points.first, let last = points.last,
               let start = first["firstObservedAt"] as? String ?? first["windowStart"] as? String,
               let end = last["lastObservedAt"] as? String ?? last["windowEnd"] as? String {
                history["from"] = start
                history["to"] = SignalFormat.date(end).map { formatter.string(from: $0.addingTimeInterval(0.001)) } ?? end
            }
            if drive { history["from"] = formatter.string(from: from); history["to"] = formatter.string(from: to) }
            let discrete = ["mil_on", "fuel_system_1_status", "retained_samples"].contains(metric)
            return ["metric": metric, "scope": selected == nil ? "empty" : slow ? "recent_trend" : "latest_session",
                "aggregation": discrete ? "last" : "sample_mean", "selection": "latest_series", "truncated": truncated, "history": history]
        }
        return ["asOf": signalTime(0), "previews": previews]
    }
}

// Exercise sparse recording sessions against the same window and bucket budget the client requests.
enum SparseDriveFixture {
    struct Sample {
        let date: Date
        let value: Double
    }

    static func samples(metric: String, now: Date) -> [Sample] {
        [(-172800, 60), (-93600, 80), (-7200, 620), (-900, 100)].flatMap { offset, count in
            (0..<count).map { index in
                let wave = sin(Double(index) * 0.13)
                let timing = index % 97 == 45 ? 44.0 : index % 83 == 27 ? -4.0 : 18 + wave * 12
                let speed = index % 80 < 10 ? 0.0 : 46 + wave * 30
                return Sample(date: now.addingTimeInterval(Double(offset + index * 4)), value: metric == "speed_kph" ? speed : timing)
            }
        }
    }

    static func latest(now: Date, dense: Bool = false) -> [String: Any] {
        let definitions = [("timing_advance_deg", "Timing advance", "deg"), ("speed_kph", "Vehicle speed", "km/h")]
        let series: [[String: Any]] = definitions.map { metric, _, unit in
            let sample = (dense ? DenseDriveFixture.samples(metric: metric, now: now) : samples(metric: metric, now: now)).last!
            return ["metric": metric, "unit": unit, "source": "pi", "statistic": "sample", "quality": "measured", "stale": false,
                "latest": ["key": "synthetic-" + metric, "metric": metric, "unit": unit, "statistic": "sample", "quality": "measured",
                           "value": sample.value, "observedAt": timestamp(sample.date)]]
        }
        return ["asOf": timestamp(now), "definitions": definitions.map { metric, label, unit in
            ["metric": metric, "label": label, "unit": unit, "staleAfterSeconds": 900,
             "description": metric == "timing_advance_deg" ? "Ignition timing advance reported for the reference cylinder." : "Vehicle speed reported at the observation time."] as [String: Any]
        }, "series": series, "contexts": []]
    }

    static func history(url: URL, now: Date, dense: Bool = false) -> [String: Any] {
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        func value(_ name: String) -> String { query.first { $0.name == name }?.value ?? "" }
        let metric = value("metric")
        let unit = metric == "speed_kph" ? "km/h" : "deg"
        let budget = max(1, Int(value("maxPoints")) ?? 120)
        let from = SignalFormat.date(value("from")) ?? now.addingTimeInterval(-30 * 86400)
        let to = SignalFormat.date(value("to")) ?? now
        let selected = (dense ? DenseDriveFixture.samples(metric: metric, now: now) : samples(metric: metric, now: now)).filter { $0.date >= from && $0.date <= to }
        var response: [String: Any] = ["metric": metric, "unit": unit, "from": timestamp(from), "to": timestamp(to), "maxPoints": budget, "series": []]
        guard !selected.isEmpty else { return response }
        if value("binning") == "time" {
            let width = max(0.001, to.timeIntervalSince(from) / Double(budget))
            let groups = Dictionary(grouping: selected) { min(budget - 1, Int($0.date.timeIntervalSince(from) / width)) }
            let points = groups.keys.sorted().map { key -> [String: Any] in
                let readings = groups[key]!
                var point = Self.point(readings, index: key)
                point["bucketStart"] = timestamp(from.addingTimeInterval(Double(key) * width))
                point["bucketEnd"] = timestamp(from.addingTimeInterval(Double(key + 1) * width))
                return point
            }
            response["series"] = [["source": "pi", "quality": "measured", "statistic": "sample", "unit": unit, "points": points]]
            return response
        }
        let gaps = (1..<selected.count).filter { selected[$0].date.timeIntervalSince(selected[$0 - 1].date) > 900 }.sorted {
            let left = selected[$0].date.timeIntervalSince(selected[$0 - 1].date)
            let right = selected[$1].date.timeIntervalSince(selected[$1 - 1].date)
            return left == right ? $0 < $1 : left > right
        }
        let boundaries = [0] + gaps.prefix(budget - 1).sorted() + [selected.count]
        let segments = zip(boundaries, boundaries.dropFirst()).map { Array(selected[$0..<$1]) }
        let totalBudget = min(budget, selected.count)
        let remaining = totalBudget - segments.count
        let capacity = selected.count - segments.count
        let shares = segments.map { capacity == 0 ? 0 : Double(remaining * ($0.count - 1)) / Double(capacity) }
        var allocations = shares.map { 1 + Int($0) }
        let remainders = shares.indices.sorted {
            let left = shares[$0] - floor(shares[$0]), right = shares[$1] - floor(shares[$1])
            return left == right ? $0 < $1 : left > right
        }
        for index in remainders.prefix(totalBudget - allocations.reduce(0, +)) { allocations[index] += 1 }
        var points: [[String: Any]] = []
        for (index, segment) in segments.enumerated() {
            let groups = Dictionary(grouping: segment.indices) { $0 * allocations[index] / segment.count }
            for key in groups.keys.sorted() {
                let readings = groups[key]!.map { segment[$0] }
                points.append(point(readings, index: points.count))
            }
        }
        response["series"] = [["source": "pi", "quality": "measured", "statistic": "sample", "unit": unit, "points": points]]
        return response
    }

    private static func point(_ readings: [Sample], index: Int) -> [String: Any] {
        let first = readings.first!, last = readings.last!
        let minimum = readings.min { $0.value < $1.value }!, maximum = readings.max { $0.value < $1.value }!
        let maxGap = zip(readings, readings.dropFirst()).map { $1.date.timeIntervalSince($0.date) }.max() ?? 0
        return ["bucketIndex": index, "bucketStart": timestamp(first.date), "bucketEnd": timestamp(last.date),
            "windowStart": timestamp(first.date), "windowEnd": timestamp(last.date),
            "firstObservedAt": timestamp(first.date), "lastObservedAt": timestamp(last.date),
            "minimumObservedAt": timestamp(minimum.date), "maximumObservedAt": timestamp(maximum.date), "maxGapSeconds": maxGap,
            "minimum": minimum.value, "maximum": maximum.value, "first": first.value, "last": last.value,
            "mean": readings.map(\.value).reduce(0, +) / Double(readings.count), "count": readings.count]
    }

    private static func timestamp(_ date: Date) -> String { ISO8601DateFormatter().string(from: date) }
}

enum DenseDriveFixture {
    // Older sessions force a real month-wide domain; the recent drive is only 30 minutes of that domain.
    static func samples(metric: String, now: Date) -> [SparseDriveFixture.Sample] {
        [(-28 * 86400, 12), (-14 * 86400, 12), (-2100, 900)].flatMap { offset, count in
            (0..<count).map { index in
                let wave = sin(Double(index) * 0.13)
                let timing = index % 97 == 45 ? 44.0 : index % 83 == 27 ? -4.0 : 18 + wave * 12
                let speed = index % 80 < 10 ? 0.0 : 46 + wave * 30
                return SparseDriveFixture.Sample(date: now.addingTimeInterval(Double(offset + index * 2)), value: metric == "speed_kph" ? speed : timing)
            }
        }
    }
}
#endif
