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
    static let currentSignalCacheGeneration = 1
    var vehicles: [Vehicle] = []
    var details: [String: VehicleDetail] = [:]
    var updatedAt: Date?
    var connectionDigest: String?
    var signals: [String: LatestSignals]?
    var signalHistory: [String: SignalHistory]?
    var locations: [String: VehicleLocationEnvelope]?
    var hiddenDashboardMetrics: [String: Set<String>]?
    var signalCacheGeneration: Int? = GarageCache.currentSignalCacheGeneration

    @discardableResult mutating func invalidateOutdatedSignalCache() -> Bool {
        guard signalCacheGeneration != Self.currentSignalCacheGeneration else { return false }
        // Recovered historical readings used the native Pi source, so source-name filtering cannot remove every retired reading.
        signals = nil
        signalHistory = nil
        signalCacheGeneration = Self.currentSignalCacheGeneration
        return true
    }
}

@MainActor
final class GarageStore: ObservableObject {
    @Published private(set) var connection: Connection?
    @Published private(set) var cache = GarageCache()
    @Published private(set) var refreshing = false
    @Published private(set) var offline = false
    @Published var error: String?
    @Published private(set) var dashboardPreferenceError: String?
    @Published private(set) var signalErrors: [String: String] = [:]
    @Published private(set) var signalsRefreshing: Set<String> = []
    @Published private(set) var locationsRefreshing: Set<String> = []
    @Published private(set) var locationErrors: [String: String] = [:]
    @Published private(set) var tripsPaging: Set<String> = []
    @Published private(set) var tripsHaveMore: [String: Bool] = [:]
    private let secure = SecureConnection()
    private let cacheURL: URL
    private let session: URLSession?
    private var refreshTask: Task<[Vehicle], Error>?
    private var refreshID: UUID?
    private var signalRefreshIDs: [String: UUID] = [:]
    private var tripRevisions: [String: UUID] = [:]
    private var locationRevisions: [String: UUID] = [:]
    private var vehicleRevisions: [String: UUID] = [:]
    private var disconnecting = false

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
                invalidateOutdatedSignalCache()
            }
        } catch { self.error = error.localizedDescription }
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("--ui-testing"),
           !ProcessInfo.processInfo.arguments.contains("--ui-testing-preserve-cache") {
            UserDefaults.standard.removeObject(forKey: DisplayUnits.preferenceKey)
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
        invalidateOutdatedSignalCache()
    }

    private func invalidateOutdatedSignalCache() {
        if cache.invalidateOutdatedSignalCache() {
            persist(errorMessage: "Old saved readings are hidden, but this phone couldn't save the cache update. Connect to refresh your readings.")
        }
    }

    var vehicles: [Vehicle] { cache.vehicles }
    func detail(_ id: String) -> VehicleDetail { cache.details[id] ?? VehicleDetail() }
    func signals(_ id: String) -> LatestSignals? { cache.signals?[id] }

    func reportDisplayUnitsChange() async {
        guard !offline, let client else { return }
        await client.reportDisplayUnitsChange()
    }

    func dashboardMetricVisible(_ metric: String, vehicleID: String) -> Bool {
        !(cache.hiddenDashboardMetrics?[vehicleID]?.contains(metric) ?? false)
    }

    func setDashboardMetric(_ metric: String, vehicleID: String, visible: Bool) {
        let previous = cache.hiddenDashboardMetrics
        if cache.hiddenDashboardMetrics == nil { cache.hiddenDashboardMetrics = [:] }
        var hidden = cache.hiddenDashboardMetrics?[vehicleID] ?? []
        if visible { hidden.remove(metric) } else { hidden.insert(metric) }
        cache.hiddenDashboardMetrics?[vehicleID] = hidden
        if persist(errorMessage: nil) {
            dashboardPreferenceError = nil
        } else {
            cache.hiddenDashboardMetrics = previous
            dashboardPreferenceError = "This phone couldn't fully save your dashboard choice. The previous selection is shown for now. Please try again."
        }
    }
    private var client: APIClient? { disconnecting ? nil : connection.map { APIClient(connection: $0, session: session) } }
    private func isCurrent(_ client: APIClient) -> Bool {
        !disconnecting && connection?.server == client.connection.server && connection?.token == client.connection.token
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

    func disconnect() async throws {
        guard !disconnecting else { return }
        disconnecting = true
        defer { disconnecting = false }
        // Delete cached locations before dropping the credential so failure remains recoverable.
        if FileManager.default.fileExists(atPath: cacheURL.path) { try FileManager.default.removeItem(at: cacheURL) }
        try await VehiclePhotoCache.shared.clear()
        try secure.remove()
        connection = nil
        cache = GarageCache()
        offline = false
        error = nil
        signalErrors = [:]
        signalRefreshIDs = [:]
        signalsRefreshing = []
        locationsRefreshing = []; locationErrors = [:]; tripsPaging = []; tripsHaveMore = [:]
        tripRevisions = [:]; locationRevisions = [:]; vehicleRevisions = [:]
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
            cache.signals = cache.signals?.filter { ids.contains($0.key) }
            cache.locations = cache.locations?.filter { ids.contains($0.key) }
            cache.hiddenDashboardMetrics = cache.hiddenDashboardMetrics?.filter { ids.contains($0.key) }
            cache.signalHistory = cache.signalHistory?.filter { key, _ in ids.contains(String(key.split(separator: "/", maxSplits: 1).first ?? "")) }
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
        let tripRevision = tripRevisions[id]
        do {
            async let records: [VehicleRecord] = client.request("vehicles/\(id)/records")
            async let reminders: [Reminder] = client.request("vehicles/\(id)/reminders")
            async let trips: [Trip] = client.request("vehicles/\(id)/trips")
            let detail = try await VehicleDetail(records: records, reminders: reminders, trips: trips)
            try Task.checkCancellation()
            guard isCurrent(client), tripRevisions[id] == tripRevision else { return }
            cache.details[id] = detail
            tripsHaveMore[id] = detail.trips.count == 50
            cache.updatedAt = Date()
            offline = false
            error = nil
            persist()
        } catch is CancellationError {
            // Keep the last known connection state and cached detail intact.
        } catch { if isCurrent(client), tripRevisions[id] == tripRevision { offline = true; self.error = error.localizedDescription } }
    }

    func refreshSignals(_ id: String) async {
        guard !Task.isCancelled, let client else { return }
        let requestID = UUID()
        signalRefreshIDs[id] = requestID
        signalsRefreshing.insert(id)
        defer {
            if signalRefreshIDs[id] == requestID {
                signalRefreshIDs.removeValue(forKey: id)
                signalsRefreshing.remove(id)
            }
        }
        do {
            let response: LatestSignals = try await client.request("vehicles/\(id)/signals/latest")
            try Task.checkCancellation()
            guard isCurrent(client), signalRefreshIDs[id] == requestID else { return }
            if cache.signals?[id]?.historyRevision != response.historyRevision {
                cache.signalHistory = cache.signalHistory?.filter { !$0.key.hasPrefix(id + "/") }
            }
            if cache.signals == nil { cache.signals = [:] }
            cache.signals?[id] = response
            signalErrors.removeValue(forKey: id)
            persist()
        } catch is CancellationError {
        } catch {
            if isCurrent(client), signalRefreshIDs[id] == requestID { signalErrors[id] = error.localizedDescription }
        }
    }

    func refreshLocation(_ id: String) async {
        guard !locationsRefreshing.contains(id), let client else { return }
        let revision = locationRevisions[id]
        locationsRefreshing.insert(id)
        defer { if locationRevisions[id] == revision { locationsRefreshing.remove(id) } }
        do {
            let response: VehicleLocationEnvelope = try await client.request("vehicles/\(id)/location")
            try Task.checkCancellation()
            guard isCurrent(client), locationRevisions[id] == revision else { return }
            if cache.locations == nil { cache.locations = [:] }
            cache.locations?[id] = response
            locationErrors.removeValue(forKey: id)
            persist()
        } catch is CancellationError { }
        catch { if isCurrent(client), locationRevisions[id] == revision { locationErrors[id] = error.localizedDescription } }
    }

    func loadMoreTrips(_ id: String) async {
        guard !tripsPaging.contains(id), let client, let last = detail(id).trips.sorted(by: Trip.newestFirst).last else { return }
        let revision = tripRevisions[id]
        tripsPaging.insert(id)
        defer { tripsPaging.remove(id) }
        do {
            let page: [Trip] = try await client.request("vehicles/\(id)/trips", query: [URLQueryItem(name: "limit", value: "50"), URLQueryItem(name: "before", value: last.startedAt), URLQueryItem(name: "beforeId", value: last.id)])
            try Task.checkCancellation()
            guard isCurrent(client), tripRevisions[id] == revision else { return }
            var ids = Set(detail(id).trips.map(\.id))
            cache.details[id]?.trips.append(contentsOf: page.filter { ids.insert($0.id).inserted })
            tripsHaveMore[id] = page.count == 50
            error = nil; persist()
        } catch is CancellationError { }
        catch { if isCurrent(client), tripRevisions[id] == revision { self.error = error.localizedDescription } }
    }

    func deleteTrip(_ trip: Trip) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        _ = try await client.send("trips/\(trip.id)", method: "DELETE")
        guard isCurrent(client) else { throw CancellationError() }
        tripRevisions[trip.vehicleId] = UUID()
        cache.details[trip.vehicleId]?.trips.removeAll { $0.id == trip.id }
        if trip.source == "pi-gps" { invalidateNativeGPSCache(trip.vehicleId) }
        persist()
        if trip.source == "pi-gps" {
            await refreshLocation(trip.vehicleId)
            await refreshSignals(trip.vehicleId)
        }
    }

    func clearLocationHistory(_ vehicleID: String) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        _ = try await client.send("vehicles/\(vehicleID)/location-history", method: "DELETE")
        guard isCurrent(client) else { throw CancellationError() }
        tripRevisions[vehicleID] = UUID()
        invalidateNativeGPSCache(vehicleID)
        cache.details[vehicleID]?.trips.removeAll { $0.source == "pi-gps" }
        persist()
        await refreshDetail(vehicleID)
        await refreshLocation(vehicleID)
        await refreshSignals(vehicleID)
    }

    private func invalidateNativeGPSCache(_ vehicleID: String) {
        locationRevisions[vehicleID] = UUID()
        locationsRefreshing.remove(vehicleID)
        locationErrors.removeValue(forKey: vehicleID)
        signalRefreshIDs.removeValue(forKey: vehicleID)
        signalsRefreshing.remove(vehicleID)
        if cache.locations?[vehicleID]?.location?.source == "pi-gps" { cache.locations?.removeValue(forKey: vehicleID) }
        // Collection details also retain coordinates. Drop native GPS copies until re-read, including replies already in flight.
        cache.signals?[vehicleID]?.contexts?.removeAll { $0.source == "pi" && $0.context.location?.type == "gps" }
    }

    func cachedSignalHistory(vehicleID: String, metric: String, statistic: String, days: Int, now: Date = Date()) -> SignalHistory? {
        if let saved = cache.signalHistory?[historyKey(vehicleID, metric, statistic, days)] { return saved }
        guard statistic == "all", days > 0 else { return nil }
        let start = now.addingTimeInterval(-Double(days) * 86400)
        let formatter = ISO8601DateFormatter()
        let firstDay = String(formatter.string(from: start).prefix(10))
        let lastDay = String(formatter.string(from: now).prefix(10))
        var recovered: [SignalHistory] = []
        // Older app versions saved each statistic separately. Reuse one range per statistic without rebucketing or writing a synthetic cache entry.
        for kind in ["sample", "snapshot", "mean", "min", "max", "sum", "count"] {
            for range in [365, 30, 7] where range <= days {
                guard let saved = cache.signalHistory?[historyKey(vehicleID, metric, kind, range)], saved.metric == metric,
                      let from = SignalFormat.date(saved.from), let to = SignalFormat.date(saved.to),
                      from <= now, to >= start else { continue }
                let series = saved.series.filter { $0.statistic == kind }.compactMap { source -> SignalHistorySeries? in
                    let points = source.points.filter { point in
                        if let day = point.calendarDate { return day >= firstDay && day <= lastDay }
                        guard let first = (point.windowStart ?? point.firstObservedAt ?? point.bucketStart).flatMap(SignalFormat.date),
                              let last = (point.windowEnd ?? point.lastObservedAt ?? point.bucketEnd).flatMap(SignalFormat.date) else { return false }
                        // A partially overlapping aggregate cannot be trimmed without inventing replacement values.
                        return first >= start && last <= now && first <= last
                    }
                    guard !points.isEmpty else { return nil }
                    return SignalHistorySeries(source: source.source, quality: source.quality, statistic: source.statistic, points: points, unit: source.unit ?? saved.unit)
                }
                guard !series.isEmpty else { continue }
                recovered.append(SignalHistory(metric: metric, unit: saved.unit,
                    from: formatter.string(from: max(start, from)), to: formatter.string(from: min(now, to)),
                    maxPoints: saved.maxPoints, series: series))
                break
            }
        }
        guard !recovered.isEmpty else { return nil }
        let unit = cache.signals?[vehicleID]?.definition(metric)?.unit
            ?? recovered.first(where: { $0.series.contains { $0.statistic != "count" } })?.unit
            ?? recovered[0].unit
        return SignalHistory(metric: metric, unit: unit, from: recovered.map(\.from).min()!, to: recovered.map(\.to).max()!,
            maxPoints: recovered.map(\.maxPoints).max()!, series: recovered.flatMap(\.series))
    }

    func signalHistory(vehicleID: String, metric: String, statistic: String, days: Int) async throws -> SignalHistory {
        guard let client else { throw APIError.message("Connect to your server first.") }
        let revision = vehicleRevisions[vehicleID]
        let historyRevision = cache.signals?[vehicleID]?.historyRevision
        let end = Date()
        let start = end.addingTimeInterval(-Double(days) * 86400)
        let formatter = ISO8601DateFormatter()
        let response: SignalHistory = try await client.request("vehicles/\(vehicleID)/signals/history", query: [
            URLQueryItem(name: "metric", value: metric), URLQueryItem(name: "statistic", value: statistic),
            URLQueryItem(name: "from", value: formatter.string(from: start)), URLQueryItem(name: "to", value: formatter.string(from: end)),
            URLQueryItem(name: "maxPoints", value: "120")
        ])
        try Task.checkCancellation()
        guard isCurrent(client), vehicleRevisions[vehicleID] == revision,
              cache.signals?[vehicleID]?.historyRevision == historyRevision else { throw CancellationError() }
        if cache.signalHistory == nil { cache.signalHistory = [:] }
        cache.signalHistory?[historyKey(vehicleID, metric, statistic, days)] = response
        persist()
        return response
    }

    private func historyKey(_ vehicle: String, _ metric: String, _ statistic: String, _ days: Int) -> String {
        [vehicle, metric, statistic, String(days)].joined(separator: "/")
    }

    @discardableResult
    func createVehicle(_ values: [String: Any]) async throws -> Vehicle {
        guard let client else { throw APIError.message("Connect to your server first.") }
        let vehicle: Vehicle = try await client.request("vehicles", method: "POST", body: APIClient.body(values))
        guard isCurrent(client) else { throw CancellationError() }
        cache.vehicles.append(vehicle)
        persist()
        return vehicle
    }

    func updateVehicle(_ id: String, values: [String: Any]) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        let vehicle: Vehicle = try await client.request("vehicles/\(id)", method: "PATCH", body: APIClient.body(values))
        guard isCurrent(client) else { throw CancellationError() }
        if let index = cache.vehicles.firstIndex(where: { $0.id == id }) { cache.vehicles[index] = vehicle }
        persist()
    }

    func updateVehiclePhoto(_ id: String, jpeg: Data?) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        let data = try await client.send("vehicles/\(id)/photo", method: jpeg == nil ? "DELETE" : "PUT", body: jpeg, contentType: "image/jpeg")
        let vehicle = try JSONDecoder().decode(Vehicle.self, from: data)
        guard isCurrent(client) else { throw CancellationError() }
        try await VehiclePhotoCache.shared.remove(vehicleID: id, connection: client.connection)
        guard isCurrent(client) else { throw CancellationError() }
        if let index = cache.vehicles.firstIndex(where: { $0.id == id }) { cache.vehicles[index] = vehicle }
        persist()
    }

    func deleteVehicle(_ id: String) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        _ = try await client.send("vehicles/\(id)", method: "DELETE")
        guard isCurrent(client) else { return }
        // Replies started before deletion cannot restore private history or resurrect the vehicle in the garage.
        vehicleRevisions[id] = UUID(); tripRevisions[id] = UUID(); locationRevisions[id] = UUID()
        signalRefreshIDs.removeValue(forKey: id)
        signalsRefreshing.remove(id); locationsRefreshing.remove(id); tripsPaging.remove(id)
        tripsHaveMore.removeValue(forKey: id); locationErrors.removeValue(forKey: id)
        refreshID = nil; refreshTask?.cancel(); refreshTask = nil; refreshing = false
        cache.vehicles.removeAll { $0.id == id }
        cache.details.removeValue(forKey: id)
        cache.signals?.removeValue(forKey: id)
        cache.locations?.removeValue(forKey: id)
        cache.hiddenDashboardMetrics?.removeValue(forKey: id)
        cache.signalHistory = cache.signalHistory?.filter { !$0.key.hasPrefix(id + "/") }
        signalErrors.removeValue(forKey: id)
        persist()
        try await VehiclePhotoCache.shared.remove(vehicleID: id, connection: client.connection)
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

    func updateRecord(_ record: VehicleRecord, values: [String: Any]) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        let updated: VehicleRecord = try await client.request("records/\(record.id)", method: "PATCH", body: APIClient.body(values))
        guard isCurrent(client) else { return }
        if let index = cache.details[record.vehicleId]?.records.firstIndex(where: { $0.id == record.id }) {
            cache.details[record.vehicleId]?.records[index] = updated
        }
        persist()
        await refresh()
    }

    func updateReminder(_ reminder: Reminder, values: [String: Any]) async throws {
        guard let client else { throw APIError.message("Connect to your server first.") }
        let updated: Reminder = try await client.request("reminders/\(reminder.id)", method: "PATCH", body: APIClient.body(values))
        guard isCurrent(client) else { return }
        if let index = cache.details[reminder.vehicleId]?.reminders.firstIndex(where: { $0.id == reminder.id }) {
            cache.details[reminder.vehicleId]?.reminders[index] = updated
        }
        persist()
    }

    func complete(_ reminder: Reminder) async {
        guard let client else { return }
        do {
            try await updateReminder(reminder, values: ["completed": !reminder.completed])
        } catch { if isCurrent(client) { self.error = error.localizedDescription } }
    }

    func deleteRecord(_ record: VehicleRecord) async {
        guard let client else { return }
        do {
            _ = try await client.send("records/\(record.id)", method: "DELETE")
            guard isCurrent(client) else { return }
            cache.details[record.vehicleId]?.records.removeAll { $0.id == record.id }
            persist()
            await refresh()
        } catch { if isCurrent(client) { self.error = error.localizedDescription } }
    }

    @discardableResult private func persist(errorMessage: String? = "Your changes are saved on the server, but this phone couldn't cache them for offline access.") -> Bool {
        do {
            try FileManager.default.createDirectory(at: cacheURL.deletingLastPathComponent(), withIntermediateDirectories: true)
            try JSONEncoder().encode(cache).write(to: cacheURL, options: [.atomic, .completeFileProtection])
            var excluded = cacheURL
            var values = URLResourceValues()
            values.isExcludedFromBackup = true
            try excluded.setResourceValues(values)
            return true
        } catch {
            if let errorMessage { self.error = errorMessage }
            return false
        }
    }
}

extension GarageCache {
    func belongs(to connection: Connection) -> Bool { connectionDigest == connection.cacheIdentity }
}
