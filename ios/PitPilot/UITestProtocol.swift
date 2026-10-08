#if DEBUG
import Foundation

// This transport only exists in Debug and is activated explicitly by the UI test runner.
final class UITestProtocol: URLProtocol {
    private static let populated = ProcessInfo.processInfo.arguments.contains("--ui-testing-populated")
    private static let migration = ProcessInfo.processInfo.arguments.contains("--ui-testing-migration")
    private static let signals = ProcessInfo.processInfo.arguments.contains("--ui-testing-signals")
    static var vehicles: [[String: Any]] = {
        guard populated || migration || signals else { return [] }
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

    private static func signalTime(_ offset: TimeInterval) -> String { ISO8601DateFormatter().string(from: Date().addingTimeInterval(offset)) }
    private static var signalLatest: [String: Any] {
        guard signals else { return ["asOf": signalTime(0), "definitions": [], "series": [], "contexts": []] }
        return ["asOf": signalTime(0), "definitions": [
            ["metric": "fuel_level_pct", "label": "Fuel level", "unit": "%", "staleAfterSeconds": 900],
            ["metric": "manifold_kpa", "label": "Manifold pressure", "unit": "kPa", "staleAfterSeconds": 900],
            ["metric": "rpm", "label": "Engine speed", "unit": "rpm", "staleAfterSeconds": 900]
        ], "series": [
            ["metric": "fuel_level_pct", "unit": "%", "source": "smartcar", "statistic": "snapshot", "quality": "measured", "stale": true,
             "latest": ["key": "synthetic-fuel", "metric": "fuel_level_pct", "unit": "%", "statistic": "snapshot", "quality": "measured", "value": 55, "calendarDate": "2026-10-08", "timezone": "unknown"]],
            ["metric": "manifold_kpa", "unit": "kPa", "source": "pi", "statistic": "sample", "quality": "measured", "stale": true,
             "latest": ["key": "synthetic-manifold", "metric": "manifold_kpa", "unit": "kPa", "statistic": "sample", "quality": "measured", "value": 42, "observedAt": signalTime(-3600)]],
            ["metric": "manifold_kpa", "unit": "kPa", "source": "pi", "statistic": "max", "quality": "measured", "stale": true,
             "latest": ["key": "synthetic-manifold-max", "metric": "manifold_kpa", "unit": "kPa", "statistic": "max", "quality": "measured", "value": 84, "periodStart": signalTime(-172800), "periodEnd": signalTime(-86400)]]
        ], "contexts": []]
    }

    private static func signalHistory(_ url: URL) -> [String: Any] {
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        func value(_ name: String) -> String { query.first { $0.name == name }?.value ?? "" }
        let metric = value("metric")
        let statistic = value("statistic")
        var response: [String: Any] = ["metric": metric, "unit": metric == "fuel_level_pct" ? "%" : "kPa", "from": value("from"), "to": value("to"), "maxPoints": 120, "series": []]
        if ProcessInfo.processInfo.arguments.contains("--ui-testing-history-empty") { return response }
        let point: [String: Any]
        if metric == "fuel_level_pct" {
            point = ["calendarDate": "2026-10-08", "timezone": "unknown", "minimum": 55, "maximum": 55, "mean": 55, "first": 55, "last": 55, "count": 1]
        } else if statistic == "sample" {
            point = ["bucketStart": signalTime(-7200), "bucketEnd": signalTime(-3600), "windowStart": signalTime(-5400), "windowEnd": signalTime(-3600), "minimum": 32, "maximum": 42, "mean": 37, "first": 32, "last": 42, "count": 2, "firstObservedAt": signalTime(-5400), "lastObservedAt": signalTime(-3600)]
        } else {
            point = ["bucketStart": signalTime(-172800), "bucketEnd": signalTime(-86400), "windowStart": signalTime(-172800), "windowEnd": signalTime(-86400), "minimum": 84, "maximum": 84, "mean": 84, "first": 84, "last": 84, "count": 1]
        }
        response["series"] = [["source": metric == "fuel_level_pct" ? "smartcar" : "pi", "quality": "measured", "statistic": statistic, "points": [point]]]
        return response
    }
}
#endif
