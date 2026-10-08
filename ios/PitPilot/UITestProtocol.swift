#if DEBUG
import Foundation

// This transport only exists in Debug and is activated explicitly by the UI test runner.
final class UITestProtocol: URLProtocol {
    static var vehicles: [[String: Any]] = []
    static var records: [[String: Any]] = []
    static var reminders: [[String: Any]] = []
    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "pitpilot.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
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
            if request.httpMethod == "POST" { body["id"] = "test-record"; body["vehicleId"] = "test-vehicle"; Self.records.append(body); response = body; code = 201 }
            else { response = Self.records }
        } else if route.hasSuffix("/reminders") {
            if request.httpMethod == "POST" { body["id"] = "test-reminder"; body["vehicleId"] = "test-vehicle"; body["completed"] = false; Self.reminders.append(body); response = body; code = 201 }
            else { response = Self.reminders }
        }
        let data = try! JSONSerialization.data(withJSONObject: response)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: code, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
#endif
