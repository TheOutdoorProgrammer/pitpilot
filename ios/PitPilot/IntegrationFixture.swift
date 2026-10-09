#if DEBUG
import Foundation

enum IntegrationFixture {
    private static let lock = NSLock()
    private static var devices: [[String: Any]] = ProcessInfo.processInfo.arguments.contains("--ui-testing-device-stale") ? [[
        "id": "fixture-device", "vehicleId": "test-vehicle", "name": "Synthetic collector", "createdAt": "2026-01-01T00:00:00Z",
        "autoUpdate": true, "enrolledAt": "2026-01-01T00:00:00Z", "lastSeenAt": "2026-01-01T00:00:00Z", "version": "0.3.1",
        "queuedBatches": 12, "rejectedSamples": 2, "collectionState": "adapter_unavailable", "updateState": "rolled_back"
    ]] : []
    private static var state = ProcessInfo.processInfo.arguments.contains("--ui-testing-smartcar-reconnect") ? "reconnect_required" : "disconnected"
    private static let scheme = "sc00000000-0000-4000-8000-000000000000"
    private static func time(_ offset: TimeInterval = 0) -> String { ISO8601DateFormatter().string(from: Date().addingTimeInterval(offset)) }
    private static var status: [String: Any] {
        var result: [String: Any] = ["state": state, "supportedMetrics": [], "unavailableSignals": 0, "unsupportedSignals": 0]
        if ["connected", "provisioning", "reconnect_required"].contains(state) { result["connectionId"] = "synthetic-connection" }
        if state == "connected" {
            result["lastSuccessAt"] = time(); result["latestObservedAt"] = time(-10800); result["nextAttemptAt"] = time(3600)
            result["supportedMetrics"] = ["odometer_miles", "fuel_level_pct"]; result["unavailableSignals"] = 1
        }
        return result
    }

    static func reply(route: String, method: String, body: [String: Any]) -> (Int, Any)? {
        guard ProcessInfo.processInfo.arguments.contains("--ui-testing-integrations") else { return nil }
        lock.lock()
        defer { lock.unlock() }
        if route == "/api/v1/integrations/smartcar" {
            let configured = !ProcessInfo.processInfo.arguments.contains("--ui-testing-smartcar-unconfigured")
            var config: [String: Any] = ["configured": configured, "connectAvailable": configured, "pollIntervalSeconds": configured ? 3600 : 0]
            if configured { config["mode"] = "simulated" }
            return (200, config)
        }
        if route == "/api/v1/vehicles/test-vehicle/devices" {
            if method == "POST" {
                let device: [String: Any] = ["id": "fixture-device", "vehicleId": "test-vehicle", "name": body["name"] as? String ?? "Synthetic Pi", "createdAt": time(), "autoUpdate": true]
                devices.append(device)
                return (201, ["device": device, "enrollmentToken": "synthetic-pairing-token-not-a-real-secret", "expiresAt": time(600)])
            }
            return (200, devices)
        }
        if route == "/api/v1/devices/fixture-device", let index = devices.firstIndex(where: { $0["id"] as? String == "fixture-device" }) {
            if method == "DELETE" { devices[index]["revokedAt"] = time(); return (204, [:]) }
            if method == "PATCH" {
                if let enabled = body["autoUpdate"] { devices[index]["autoUpdate"] = enabled }
                if let enabled = body["gpsRecording"] { devices[index]["gpsRecording"] = enabled; devices[index]["gpsState"] = "disconnected" }
                return (200, devices[index])
            }
        }
        if route == "/api/v1/vehicles/test-vehicle/smartcar" {
            if ProcessInfo.processInfo.arguments.contains("--ui-testing-smartcar-unconfigured") { return (503, ["error": "smartcar is not configured"]) }
            if method == "DELETE" { state = "disconnected"; return (204, [:]) }
            return (200, status)
        }
        if route.hasSuffix("/smartcar/sessions") {
            state = "awaiting_authorization"
            return (201, ["sessionId": "synthetic-session", "authorizationUrl": "https://connect.smartcar.com/oauth/authorize?state=synthetic-state", "callbackScheme": scheme, "expiresAt": time(600)])
        }
        if route.hasSuffix("/smartcar/sessions/synthetic-session/complete") {
            state = "awaiting_selection"
            return (200, ["state": state, "candidates": [["candidateId": "synthetic-candidate", "make": "Sample", "model": "Roadster", "year": 2024]]])
        }
        if route.hasSuffix("/smartcar/sessions/synthetic-session/bind") { state = "provisioning"; return (200, status) }
        if route.hasSuffix("/smartcar/sync") { state = "connected"; return (202, status) }
        return nil
    }
}
#endif
