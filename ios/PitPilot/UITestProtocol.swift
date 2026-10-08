#if DEBUG
import Foundation

// This transport only exists in Debug and is activated explicitly by the UI test runner.
final class UITestProtocol: URLProtocol {
    private static let populated = ProcessInfo.processInfo.arguments.contains("--ui-testing-populated")
    private static let migration = ProcessInfo.processInfo.arguments.contains("--ui-testing-migration")
    static var vehicles: [[String: Any]] = {
        guard populated || migration else { return [] }
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
}
#endif
