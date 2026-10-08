import Foundation
import OSLog
import CryptoKit

struct Connection: Codable {
    var server: URL
    var token: String
    var cacheIdentity: String {
        SHA256.hash(data: Data((server.absoluteString + "\n" + token).utf8)).map { String(format: "%02x", $0) }.joined()
    }

    static func validated(server: String, token: String) throws -> Connection {
        guard let url = URL(string: server.trimmingCharacters(in: .whitespacesAndNewlines)),
              url.scheme == "https", let host = url.host, !host.isEmpty,
              url.user == nil, url.password == nil, url.query == nil, url.fragment == nil,
              url.path.isEmpty || url.path == "/" else {
            throw APIError.message("Enter your server's HTTPS address, without a path or sign-in details.")
        }
        let cleanToken = token.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleanToken.isEmpty, !cleanToken.contains(where: \.isNewline) else {
            throw APIError.message("Enter a valid API token from your server.")
        }
        return Connection(server: url, token: cleanToken)
    }
}

enum APIError: LocalizedError {
    case message(String)
    var errorDescription: String? {
        switch self { case .message(let message): message }
    }
}

final class NoRedirect: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

struct APIClient {
    let connection: Connection
    private let session: URLSession
    private static let defaultSession: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("--ui-testing") { config.protocolClasses = [UITestProtocol.self] }
        #endif
        return URLSession(configuration: config, delegate: NoRedirect(), delegateQueue: nil)
    }()
    private static let logger = Logger(subsystem: "com.theoutdoorprogrammer.pitpilot", category: "api")

    init(connection: Connection, session: URLSession? = nil) {
        self.connection = connection
        self.session = session ?? Self.defaultSession
    }

    func request<T: Decodable>(_ route: String, method: String = "GET", body: Data? = nil, query: [URLQueryItem] = []) async throws -> T {
        let data = try await send(route, method: method, body: body, query: query)
        do { return try JSONDecoder().decode(T.self, from: data) }
        catch { throw APIError.message("The server returned data this app cannot read. Check that the app and server are up to date.") }
    }

    func send(_ route: String, method: String = "GET", body: Data? = nil, query: [URLQueryItem] = []) async throws -> Data {
        try Task.checkCancellation()
        let started = Date()
        var statusCode = 0
        var cancelled = false
        var failureKind: String?
        var components = URLComponents(url: connection.server.appendingPathComponent("api/v1/" + route), resolvingAgainstBaseURL: false)!
        if !query.isEmpty {
            components.queryItems = query
            // Go query parsing treats a literal plus as a space, including timezone offsets.
            components.percentEncodedQuery = components.percentEncodedQuery?.replacingOccurrences(of: "+", with: "%2B")
        }
        var request = URLRequest(url: components.url!)
        request.httpMethod = method
        request.httpBody = body
        request.timeoutInterval = 20
        request.setValue("Bearer \(connection.token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        let traceID = UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased()
        let spanID = String(UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased().prefix(16))
        request.setValue("00-\(traceID)-\(spanID)-01", forHTTPHeaderField: "traceparent")
        defer {
            if !cancelled, let operation = Self.operation(route: route, method: method) {
                let elapsed = min(120_000, max(0, Int(Date().timeIntervalSince(started) * 1000)))
                let eventStatus = statusCode
                let eventFailure = failureKind
                Task { await report(operation: operation, duration: elapsed, status: eventStatus, failureKind: eventFailure, traceparent: "00-\(traceID)-\(spanID)-01") }
            }
        }
        do {
            let (data, response) = try await session.data(for: request)
            try Task.checkCancellation()
            guard let response = response as? HTTPURLResponse else { throw APIError.message("The server did not send an HTTP response.") }
            statusCode = response.statusCode
            Self.logger.info("api_request method=\(method, privacy: .public) status=\(response.statusCode) trace_id=\(traceID, privacy: .public) span_id=\(spanID, privacy: .public)")
            switch response.statusCode {
            case 200..<300: return data
            case 401, 403: throw APIError.message("Your connection is not authorized. Check the API token in Settings.")
            case 404: throw APIError.message("This item no longer exists on the server. Refresh and try again.")
            case 409: throw APIError.message("This item changed on the server. Close this form, review the refreshed record, and try again.")
            case 400, 422: throw APIError.message("The server could not accept these values. Check the form and try again.")
            default: throw APIError.message("The server could not complete the request (\(response.statusCode)). Try again shortly.")
            }
        } catch {
            if error is CancellationError || (error as? URLError)?.code == .cancelled || Task.isCancelled {
                cancelled = true
                throw CancellationError()
            }
            if let transport = error as? URLError {
                let kind = Self.failureKind(for: transport.code)
                failureKind = kind
                Self.logger.error("api_transport_failure kind=\(kind, privacy: .public) trace_id=\(traceID, privacy: .public) span_id=\(spanID, privacy: .public)")
                throw APIError.message("Couldn't reach your server. Check your connection and try again. Saved data remains available.")
            }
            throw error
        }
    }

    static func failureKind(for code: URLError.Code) -> String {
        switch code {
        case .cannotFindHost, .dnsLookupFailed: return "dns"
        case .timedOut: return "timeout"
        case .cannotConnectToHost, .networkConnectionLost: return "connection"
        case .secureConnectionFailed, .serverCertificateHasBadDate, .serverCertificateUntrusted,
             .serverCertificateHasUnknownRoot, .serverCertificateNotYetValid,
             .clientCertificateRejected, .clientCertificateRequired: return "tls"
        case .notConnectedToInternet, .dataNotAllowed, .internationalRoamingOff: return "offline"
        default: return "other"
        }
    }

    static func body(_ values: [String: Any]) throws -> Data { try JSONSerialization.data(withJSONObject: values) }

    static func operation(route: String, method: String) -> String? {
        let parts = route.split(separator: "/")
        if parts.count == 4, parts[0] == "vehicles", parts[2] == "signals", method == "GET" {
            switch parts[3] { case "latest": return "signals.latest"; case "history": return "signals.history"; default: return nil }
        }
        if parts.count == 1 && parts[0] == "vehicles" { return method == "GET" ? "vehicles.list" : method == "POST" ? "vehicle.create" : nil }
        if parts.count == 2 {
            let resource: String
            switch parts[0] { case "vehicles": resource = "vehicle"; case "records": resource = "record"; case "reminders": resource = "reminder"; case "trips": resource = "trip"; default: return nil }
            switch method { case "GET": return resource == "vehicle" ? "vehicle.get" : nil; case "PATCH": return ["vehicle", "record", "reminder"].contains(resource) ? resource + ".update" : nil; case "DELETE": return resource + ".delete"; default: return nil }
        }
        if parts.count == 3 && parts[0] == "vehicles" {
            switch (parts[2], method) {
            case ("records", "GET"): return "records.list"
            case ("records", "POST"): return "record.create"
            case ("reminders", "GET"): return "reminders.list"
            case ("reminders", "POST"): return "reminder.create"
            case ("trips", "GET"): return "trips.list"
            case ("trips", "POST"): return "trip.create"
            default: return nil
            }
        }
        return nil
    }

    private func report(operation: String, duration: Int, status: Int, failureKind: String?, traceparent: String) async {
        var request = URLRequest(url: connection.server.appendingPathComponent("api/v1/client-events"))
        request.httpMethod = "POST"
        request.timeoutInterval = 5
        request.setValue("Bearer \(connection.token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue(traceparent, forHTTPHeaderField: "traceparent")
        var event: [String: Any] = ["operation": operation, "durationMs": duration, "statusCode": status]
        if let failureKind { event["failureKind"] = failureKind }
        request.httpBody = try? Self.body(event)
        // No recursive instrumentation, retry queue, or sensitive request metadata.
        do {
            let (_, response) = try await session.data(for: request)
            if let response = response as? HTTPURLResponse, response.statusCode != 204 {
                Self.logger.notice("client_event_delivery_failed status=\(response.statusCode)")
            }
        } catch { Self.logger.notice("client_event_delivery_failed transport=true") }
    }
}
