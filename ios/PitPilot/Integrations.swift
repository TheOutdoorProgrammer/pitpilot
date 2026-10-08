import Foundation

struct VehicleDevice: Codable, Identifiable {
    let id: String
    let vehicleId: String
    let name: String
    let createdAt: String
    var autoUpdate: Bool
    var enrolledAt: String?
    var revokedAt: String?
    var lastSeenAt: String?
    var lastObservedAt: String?
    var lastUploadAt: String?
    var version: String?
    var queuedBatches: Int?
    var rejectedSamples: Int?
    var collectionState: String?
    var updateState: String?

    func status(at now: Date) -> String {
        if revokedAt != nil { return "Access revoked" }
        guard let seen = SignalFormat.date(lastSeenAt ?? "") else {
            return enrolledAt == nil ? "Waiting for pairing" : "Waiting for first check-in"
        }
        if seen.timeIntervalSince(now) > 5 * 60 { return "Check-in time unavailable" }
        return now.timeIntervalSince(seen) > 15 * 60 ? "Check-in overdue" : "Recently checked in"
    }
}

struct DevicePairing: Decodable, Identifiable {
    let device: VehicleDevice
    let enrollmentToken: String
    let expiresAt: String
    var id: String { device.id }
    var expiration: Date? { SignalFormat.date(expiresAt) }
}

struct SmartcarConfiguration: Decodable {
    let configured: Bool
    let mode: String
    let connectAvailable: Bool
    let pollIntervalSeconds: Int
}

struct SmartcarStatus: Decodable {
    let state: String
    var connectionId: String?
    var lastAttemptAt: String?
    var lastSuccessAt: String?
    var latestObservedAt: String?
    var nextAttemptAt: String?
    var errorCode: String?
    var supportedMetrics: [String] = []
    var unavailableSignals: Int?
    var unsupportedSignals: Int?

    var title: String {
        switch state {
        case "disconnected": "Not linked"
        case "awaiting_authorization": "Authorization needed"
        case "awaiting_selection": "Choose a vehicle"
        case "provisioning": "Waiting for vehicle data"
        case "connected": "Vehicle linked"
        case "temporary_error": "Update delayed"
        case "reconnect_required": "Reconnect needed"
        default: "Status unavailable"
        }
    }
    var explanation: String {
        switch state {
        case "disconnected": "Link your connected-car account to bring supported readings into this garage."
        case "awaiting_authorization": "Finish the secure sign-in to let Smartcar access the signals you approve."
        case "awaiting_selection": "Confirm which vehicle belongs in this garage. Nothing is assigned automatically."
        case "provisioning": "The link is saved. Your vehicle's manufacturer may need time before readings become available."
        case "connected": "Readings arrive when your vehicle and manufacturer make them available. A successful check does not mean the readings are live."
        case "temporary_error": "The last check could not finish. Saved readings remain available and the server will retry."
        case "reconnect_required": "Your connected-car account needs your attention. Reconnect to restore access."
        default: "Refresh to check this connection again."
        }
    }
    var canSync: Bool { ["provisioning", "connected", "temporary_error"].contains(state) }
    var canDisconnect: Bool { state != "disconnected" }
}

struct SmartcarSession: Decodable {
    let sessionId: String
    let authorizationUrl: URL
    let callbackScheme: String
    let expiresAt: String

    func validate(now: Date = .now) throws -> String {
        guard authorizationUrl.scheme == "https", authorizationUrl.host == "connect.smartcar.com",
              authorizationUrl.port == nil || authorizationUrl.port == 443,
              authorizationUrl.user == nil, authorizationUrl.password == nil, authorizationUrl.fragment == nil,
              ["/oauth/authorize", "/oauth/reauthenticate"].contains(authorizationUrl.path),
              callbackScheme.hasPrefix("sc"), UUID(uuidString: String(callbackScheme.dropFirst(2))) != nil,
              let expires = SignalFormat.date(expiresAt), expires > now,
              let query = URLComponents(url: authorizationUrl, resolvingAgainstBaseURL: false)?.queryItems,
              query.filter({ $0.name == "state" }).count == 1,
              let state = query.first(where: { $0.name == "state" })?.value, !state.isEmpty else {
            throw APIError.message("The server could not start a valid Smartcar sign-in. Refresh and try again.")
        }
        return state
    }

    func completion(_ callback: URL, now: Date = .now) throws -> [String: String] {
        let expectedState = try validate(now: now)
        guard let url = URLComponents(url: callback, resolvingAgainstBaseURL: false),
              url.scheme == callbackScheme, url.host == "callback", url.path.isEmpty || url.path == "/",
              url.user == nil, url.password == nil, url.port == nil, url.fragment == nil else {
            throw APIError.message("Smartcar returned an invalid sign-in response. Please start again.")
        }
        var values: [String: String] = [:]
        var seen = Set<String>()
        let keys = ["state": "state", "user_id": "userId", "vehicle_id": "vehicleId", "external_id": "externalId", "error": "error"]
        for item in url.queryItems ?? [] {
            guard seen.insert(item.name).inserted else { throw APIError.message("Smartcar returned a repeated sign-in field. Please start again.") }
            guard let key = keys[item.name] else { continue }
            guard let value = item.value, !value.isEmpty, value.utf8.count <= 2048 else { throw APIError.message("Smartcar returned an incomplete sign-in response. Please start again.") }
            values[key] = value
        }
        guard values["state"] == expectedState else { throw APIError.message("This Smartcar sign-in does not match your request. Please start again.") }
        return values
    }
}

struct SmartcarCandidate: Decodable, Identifiable {
    let candidateId: String
    let make: String
    let model: String
    let year: Int
    var id: String { candidateId }
    var title: String { [year > 0 ? String(year) : "", make, model].filter { !$0.isEmpty }.joined(separator: " ") }
}

struct SmartcarSelection: Decodable {
    let state: String
    let candidates: [SmartcarCandidate]
}

@MainActor
final class VehicleIntegrations: ObservableObject {
    @Published private(set) var devices: [VehicleDevice] = []
    @Published private(set) var configuration: SmartcarConfiguration?
    @Published private(set) var smartcar: SmartcarStatus?
    @Published private(set) var loading = false
    @Published private(set) var working = false
    @Published private(set) var loaded = false
    @Published var error: String?
    @Published var pairing: DevicePairing?
    @Published private(set) var candidates: [SmartcarCandidate] = []
    @Published private(set) var message: String?
    private let vehicleID: String
    private let client: APIClient
    private let browser: any SmartcarAuthenticating
    private var selectionSession: SmartcarSession?
    private var refreshID: UUID?
    private var actionTask: Task<Void, Never>?

    init(vehicleID: String, client: APIClient, browser: (any SmartcarAuthenticating)? = nil) {
        self.vehicleID = vehicleID
        self.client = client
        self.browser = browser ?? SmartcarBrowser()
    }

    func refresh() async {
        guard !Task.isCancelled else { return }
        let id = UUID()
        refreshID = id
        loading = true
        defer { if refreshID == id { loading = false } }
        do {
            async let devices: [VehicleDevice] = client.request("vehicles/\(vehicleID)/devices")
            async let configuration: SmartcarConfiguration = client.request("integrations/smartcar")
            let next = try await (devices, configuration)
            let status: SmartcarStatus?
            if next.1.configured { status = try await client.request("vehicles/\(vehicleID)/smartcar") }
            else { status = nil }
            try Task.checkCancellation()
            guard refreshID == id else { return }
            self.devices = next.0; self.configuration = next.1; smartcar = status
            loaded = true; error = nil
        } catch is CancellationError {} catch { if refreshID == id { self.error = error.localizedDescription } }
    }

    func pair(name: String) async {
        await perform {
            let grant: DevicePairing = try await self.client.request("vehicles/\(self.vehicleID)/devices", method: "POST", body: APIClient.body(["name": name.trimmingCharacters(in: .whitespacesAndNewlines)]))
            self.devices.append(grant.device)
            self.pairing = grant
        }
    }

    func autoUpdate(_ device: VehicleDevice, enabled: Bool) async {
        await perform {
            let updated: VehicleDevice = try await self.client.request("devices/\(device.id)", method: "PATCH", body: APIClient.body(["autoUpdate": enabled]))
            self.devices = self.devices.map { $0.id == updated.id ? updated : $0 }
        }
    }

    func revoke(_ device: VehicleDevice) async {
        await perform {
            _ = try await self.client.send("devices/\(device.id)", method: "DELETE")
            self.pairing = nil
            self.message = "Device access revoked. Previously saved readings remain available."
            await self.refresh()
        }
    }

    func connect(reconnect: Bool) async {
        await perform {
            self.candidates = []; self.selectionSession = nil
            let session: SmartcarSession = try await self.client.request("vehicles/\(self.vehicleID)/smartcar/sessions", method: "POST", body: APIClient.body(["intent": reconnect ? "reconnect" : "connect"]))
            _ = try session.validate()
            let callback = try await self.browser.authenticate(session)
            let selection: SmartcarSelection = try await self.client.request("vehicles/\(self.vehicleID)/smartcar/sessions/\(session.sessionId)/complete", method: "POST", body: APIClient.body(session.completion(callback)))
            if selection.state == "cancelled" { await self.refresh(); return }
            self.selectionSession = session; self.candidates = selection.candidates
            if selection.candidates.isEmpty { self.message = "No vehicle is ready to select. Refresh the connection status or start sign-in again." }
            await self.refresh()
            if !selection.candidates.isEmpty { self.smartcar = SmartcarStatus(state: "awaiting_selection") }
        }
    }

    func bind(_ candidate: SmartcarCandidate) async {
        guard let session = selectionSession, candidates.contains(where: { $0.id == candidate.id }) else { return }
        await perform {
            _ = try session.validate()
            let status: SmartcarStatus = try await self.client.request("vehicles/\(self.vehicleID)/smartcar/sessions/\(session.sessionId)/bind", method: "POST", body: APIClient.body(["candidateId": candidate.id]))
            self.smartcar = status; self.candidates = []; self.selectionSession = nil
            self.message = "Vehicle linked. Your first readings may take time to arrive."
        }
    }

    func sync() async {
        await perform {
            self.smartcar = try await self.client.request("vehicles/\(self.vehicleID)/smartcar/sync", method: "POST", body: APIClient.body([:]))
            self.message = "Update requested. Pull to refresh to check its progress."
        }
    }

    func disconnect() async {
        await perform {
            _ = try await self.client.send("vehicles/\(self.vehicleID)/smartcar", method: "DELETE")
            self.candidates = []; self.selectionSession = nil
            self.message = "Smartcar detached from this garage. Saved readings remain available."
            await self.refresh()
        }
    }

    func leave() {
        actionTask?.cancel(); browser.cancel()
        refreshID = nil; loading = false
        pairing = nil; candidates = []; selectionSession = nil
    }

    private func perform(_ action: @escaping @MainActor () async throws -> Void) async {
        guard !working, !Task.isCancelled else { return }
        working = true; error = nil; message = nil
        let task = Task { @MainActor in
            defer { self.working = false; self.actionTask = nil }
            do { try Task.checkCancellation(); try await action() }
            catch is CancellationError {} catch { if !Task.isCancelled { self.error = error.localizedDescription } }
        }
        actionTask = task
        await withTaskCancellationHandler { await task.value } onCancel: { task.cancel() }
    }
}
