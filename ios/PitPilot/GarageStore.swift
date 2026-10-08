import Foundation
import Security

struct SecureConnection {
    private var query: [String: Any] {
        var account = "connection"
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("--ui-testing") { account = "ui-test-connection" }
        #endif
        return [kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "com.theoutdoorprogrammer.pitpilot", kSecAttrAccount as String: account]
    }
    func load() throws -> Connection? {
        var read = query
        read[kSecReturnData as String] = true
        read[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(read as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data else { throw APIError.message("Unable to read your secure connection. Unlock your phone and try again.") }
        return try JSONDecoder().decode(Connection.self, from: data)
    }
    func save(_ connection: Connection) throws {
        let data = try JSONEncoder().encode(connection)
        let status = SecItemUpdate(query as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if status == errSecSuccess { return }
        guard status == errSecItemNotFound else { throw APIError.message("Unable to update your secure connection.") }
        var insert = query
        insert[kSecValueData as String] = data
        insert[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        guard SecItemAdd(insert as CFDictionary, nil) == errSecSuccess else { throw APIError.message("Unable to save your secure connection.") }
    }
    func remove() throws {
        let status = SecItemDelete(query as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw APIError.message("Unable to remove your secure connection.") }
    }
}

struct GarageCache: Codable {
    var vehicles: [Vehicle] = []
    var details: [String: VehicleDetail] = [:]
    var updatedAt: Date?
    var connectionDigest: String?
}

@MainActor
final class GarageStore: ObservableObject {
    @Published private(set) var connection: Connection?
    @Published private(set) var cache = GarageCache()
    @Published private(set) var refreshing = false
    @Published private(set) var offline = false
    @Published var error: String?
    private let secure = SecureConnection()
    private let cacheURL: URL
    private let session: URLSession?
    private var refreshTask: Task<[Vehicle], Error>?
    private var refreshID: UUID?

    init() {
        session = nil
        var filename = "garage.json"
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("--ui-testing") { filename = "garage-ui-test.json" }
        #endif
        cacheURL = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appendingPathComponent(filename)
        do {
            connection = try secure.load()
            if let connection, let data = try? Data(contentsOf: cacheURL),
               let saved = try? JSONDecoder().decode(GarageCache.self, from: data),
               saved.belongs(to: connection) {
                cache = saved
                offline = true
            }
        } catch { self.error = error.localizedDescription }
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("--ui-testing"),
           !ProcessInfo.processInfo.arguments.contains("--ui-testing-preserve-cache") {
            connection = nil
            cache = GarageCache()
            offline = false
        }
        #endif
    }

    init(connection: Connection, cache: GarageCache, offline: Bool = false, cacheURL: URL, session: URLSession) {
        self.cacheURL = cacheURL
        self.session = session
        self.connection = connection
        self.cache = cache
        self.offline = offline
    }

    var vehicles: [Vehicle] { cache.vehicles }
    func detail(_ id: String) -> VehicleDetail { cache.details[id] ?? VehicleDetail() }
    private var client: APIClient? { connection.map { APIClient(connection: $0, session: session) } }
    private func isCurrent(_ client: APIClient) -> Bool {
        connection?.server == client.connection.server && connection?.token == client.connection.token
    }

    func connect(server: String, token: String) async throws {
        let next = try Connection.validated(server: server, token: token)
        let vehicles: [Vehicle] = try await APIClient(connection: next).request("vehicles")
        try secure.save(next)
        connection = next
        cache = GarageCache(vehicles: vehicles, updatedAt: Date(), connectionDigest: next.cacheIdentity)
        offline = false
        error = nil
        persist()
    }

    func disconnect() throws {
        // Delete cached locations before dropping the credential so failure remains recoverable.
        if FileManager.default.fileExists(atPath: cacheURL.path) { try FileManager.default.removeItem(at: cacheURL) }
        try secure.remove()
        connection = nil
        cache = GarageCache()
        offline = false
        error = nil
    }

    func refresh() async {
        guard !Task.isCancelled, let client else { return }
        // A replacement view or pull gesture must not be dropped while an old task unwinds.
        refreshTask?.cancel()
        let id = UUID()
        let task = Task<[Vehicle], Error> { try await client.request("vehicles") }
        refreshID = id
        refreshTask = task
        refreshing = true
        defer {
            if refreshID == id {
                refreshing = false
                refreshTask = nil
                refreshID = nil
            }
        }
        do {
            let vehicles = try await withTaskCancellationHandler {
                try await task.value
            } onCancel: { task.cancel() }
            try Task.checkCancellation()
            guard isCurrent(client), refreshID == id else { return }
            cache.vehicles = vehicles
            let ids = Set(cache.vehicles.map(\.id))
            cache.details = cache.details.filter { ids.contains($0.key) }
            cache.updatedAt = Date()
            offline = false
            error = nil
            persist()
        } catch is CancellationError {
            // SwiftUI cancels view-owned refreshes when navigation changes their lifetime.
        } catch { if isCurrent(client), refreshID == id { offline = true; self.error = error.localizedDescription } }
    }

    func refreshDetail(_ id: String) async {
        guard let client else { return }
        do {
            async let records: [VehicleRecord] = client.request("vehicles/\(id)/records")
            async let reminders: [Reminder] = client.request("vehicles/\(id)/reminders")
            async let trips: [Trip] = client.request("vehicles/\(id)/trips")
            let detail = try await VehicleDetail(records: records, reminders: reminders, trips: trips)
            try Task.checkCancellation()
            guard isCurrent(client) else { return }
            cache.details[id] = detail
            cache.updatedAt = Date()
            offline = false
            error = nil
            persist()
        } catch is CancellationError {
            // Keep the last known connection state and cached detail intact.
        } catch { if isCurrent(client) { offline = true; self.error = error.localizedDescription } }
    }

    func createVehicle(_ values: [String: Any]) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        let vehicle: Vehicle = try await client.request("vehicles", method: "POST", body: APIClient.body(values))
        guard isCurrent(client) else { return }
        cache.vehicles.append(vehicle)
        persist()
    }

    func updateVehicle(_ id: String, values: [String: Any]) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        let vehicle: Vehicle = try await client.request("vehicles/\(id)", method: "PATCH", body: APIClient.body(values))
        guard isCurrent(client) else { return }
        if let index = cache.vehicles.firstIndex(where: { $0.id == id }) { cache.vehicles[index] = vehicle }
        persist()
    }

    func deleteVehicle(_ id: String) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        _ = try await client.send("vehicles/\(id)", method: "DELETE")
        guard isCurrent(client) else { return }
        cache.vehicles.removeAll { $0.id == id }
        cache.details.removeValue(forKey: id)
        persist()
    }

    func createRecord(vehicleID: String, values: [String: Any]) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        let record: VehicleRecord = try await client.request("vehicles/\(vehicleID)/records", method: "POST", body: APIClient.body(values))
        guard isCurrent(client) else { return }
        var detail = detail(vehicleID)
        detail.records.insert(record, at: 0)
        cache.details[vehicleID] = detail
        persist()
        await refresh()
    }

    func createReminder(vehicleID: String, values: [String: Any]) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        let reminder: Reminder = try await client.request("vehicles/\(vehicleID)/reminders", method: "POST", body: APIClient.body(values))
        guard isCurrent(client) else { return }
        var detail = detail(vehicleID)
        detail.reminders.append(reminder)
        cache.details[vehicleID] = detail
        persist()
    }

    func complete(_ reminder: Reminder) async {
        guard let client else { return }
        do {
            let _: Reminder = try await client.request("reminders/\(reminder.id)", method: "PATCH", body: APIClient.body(["completed": !reminder.completed]))
            guard isCurrent(client) else { return }
            await refreshDetail(reminder.vehicleId)
        } catch { if isCurrent(client) { self.error = error.localizedDescription } }
    }

    func deleteRecord(_ record: VehicleRecord) async {
        guard let client else { return }
        do {
            _ = try await client.send("records/\(record.id)", method: "DELETE")
            guard isCurrent(client) else { return }
            cache.details[record.vehicleId]?.records.removeAll { $0.id == record.id }
            persist()
        } catch { if isCurrent(client) { self.error = error.localizedDescription } }
    }

    private func persist() {
        do {
            try FileManager.default.createDirectory(at: cacheURL.deletingLastPathComponent(), withIntermediateDirectories: true)
            try JSONEncoder().encode(cache).write(to: cacheURL, options: [.atomic, .completeFileProtection])
            var excluded = cacheURL
            var values = URLResourceValues()
            values.isExcludedFromBackup = true
            try excluded.setResourceValues(values)
        } catch { self.error = "Your changes are saved on the server, but this phone couldn't cache them for offline access." }
    }
}

extension GarageCache {
    func belongs(to connection: Connection) -> Bool { connectionDigest == connection.cacheIdentity }
}
