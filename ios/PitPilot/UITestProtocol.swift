#if DEBUG
import Foundation

// This transport only exists in Debug and is activated explicitly by the UI test runner.
final class UITestProtocol: URLProtocol {
    private static let populated = ProcessInfo.processInfo.arguments.contains("--ui-testing-populated")
    private static let migration = ProcessInfo.processInfo.arguments.contains("--ui-testing-migration")
    private static let signals = ProcessInfo.processInfo.arguments.contains("--ui-testing-signals")
    static var vehicles: [[String: Any]] = {
        guard populated || migration || signals || ProcessInfo.processInfo.arguments.contains("--ui-testing-integrations") else { return [] }
        var vehicle: [String: Any] = ["id": "test-vehicle", "name": "Synthetic route truck", "make": "", "model": "", "year": 2002, "odometerMiles": 120000, "createdAt": "2026-01-01T00:00:00Z"]
        if migration {
            vehicle["vin"] = "SYNTHETIC-VIN"
            vehicle["odometerStatus"] = "estimated"
            vehicle["licensePlate"] = "TEST-ONLY"
            vehicle["extraFields"] = [["name": "Registration region", "value": "Synthetic region", "isRequired": false, "fieldType": 0]]
            vehicle["source"] = ["system": "lubelogger", "instance": "synthetic", "collection": "vehicles", "id": "1"]
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
    static let trips: [[String: Any]] = populated ? [[
        "id": "test-trip", "vehicleId": "test-vehicle", "title": "Synthetic park loop",
        "startedAt": "2026-01-01T12:00:00Z", "endedAt": "2026-01-01T12:03:00Z", "distanceMiles": 0.4,
        "points": [
            ["latitude": 40.7712, "longitude": -73.9744, "recordedAt": "2026-01-01T12:00:00Z"],
            ["latitude": 40.7720, "longitude": -73.9740, "recordedAt": "2026-01-01T12:01:00Z"],
            ["latitude": 40.7727, "longitude": -73.9730, "recordedAt": "2026-01-01T12:02:00Z"],
            ["latitude": 40.7733, "longitude": -73.9726, "recordedAt": "2026-01-01T12:03:00Z"]
        ]
    ]] : []
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
        let data = try! JSONSerialization.data(withJSONObject: response)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: code, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}

    private static let signalNow = Date()
    private static func signalTime(_ offset: TimeInterval) -> String { ISO8601DateFormatter().string(from: signalNow.addingTimeInterval(offset)) }
    private static func signalDay(_ offset: Int) -> String { String(signalTime(Double(offset) * 86400).prefix(10)) }
    private static var signalLatest: [String: Any] {
        guard signals else { return ["asOf": signalTime(0), "definitions": [], "series": [], "contexts": []] }
        return ["asOf": signalTime(0), "definitions": [
            ["metric": "fuel_level_pct", "label": "Fuel level", "unit": "%", "staleAfterSeconds": 900],
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
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        func value(_ name: String) -> String { query.first { $0.name == name }?.value ?? "" }
        let metric = value("metric")
        let statistic = value("statistic")
        let unit = ["fuel_level_pct": "%", "mil_on": "boolean", "fuel_system_1_status": "code", "retained_samples": "count"][metric] ?? "kPa"
        var response: [String: Any] = ["metric": metric, "unit": unit, "from": value("from"), "to": value("to"), "maxPoints": 120, "series": []]
        if ProcessInfo.processInfo.arguments.contains("--ui-testing-history-empty") { return response }
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
        response["series"] = [["source": metric == "fuel_level_pct" ? "smartcar" : "pi", "quality": "measured", "statistic": statistic, "points": points]]
        return response
    }
}
#endif
