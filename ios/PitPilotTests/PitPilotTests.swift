import XCTest
@testable import PitPilot

final class PitPilotTests: XCTestCase {
    func testConnectionRejectsUnsafeURLsAndMissingToken() throws {
        for url in ["http://example.com", "https://name:secret@example.com", "https://example.com/path", "https://example.com?token=secret", "https://example.com#fragment", "not a url"] {
            XCTAssertThrowsError(try Connection.validated(server: url, token: "test"), url)
        }
        XCTAssertThrowsError(try Connection.validated(server: "https://example.com", token: " "))
        XCTAssertEqual(try Connection.validated(server: " https://example.com ", token: " test ").token, "test")
    }
    func testNumericInputRejectsNegativeAndNonNumeric() {
        XCTAssertNil(Input.number("-1"))
        XCTAssertNil(Input.number("nope"))
        XCTAssertNil(Input.number("12oops"))
        XCTAssertNil(Input.number(""))
        XCTAssertEqual(Input.number("1234"), 1234)
    }
    func testTripGapsAreNotInventedAsContinuousRoutes() {
        let points = [
            TripPoint(latitude: 40, longitude: -75, recordedAt: "2026-01-01T00:00:00Z"),
            TripPoint(latitude: 40.1, longitude: -75, recordedAt: "2026-01-01T00:01:00.123Z"),
            TripPoint(latitude: 40.2, longitude: -75, recordedAt: "2026-01-01T00:20:00Z"),
            TripPoint(latitude: 40.3, longitude: -75, recordedAt: "invalid"),
            TripPoint(latitude: 400, longitude: -75, recordedAt: "2026-01-01T00:21:00Z")
        ]
        XCTAssertEqual(TripSegments.split(points).map(\.count), [2, 1, 1])
    }
    func testDecodeAPIRecordAndOptionalFields() throws {
        let data = Data(#"{"id":"r","vehicleId":"v","kind":"fuel","date":"2026-10-08","title":"Fill up","notes":"","odometerMiles":120000.5,"costCents":4500,"gallons":null}"#.utf8)
        let record = try JSONDecoder().decode(VehicleRecord.self, from: data)
        XCTAssertEqual(record.kind, .fuel)
        XCTAssertEqual(record.costCents, 4500)
        XCTAssertNil(record.gallons)
    }
    func testTelemetryUsesBoundedOperationsWithoutIDs() {
        XCTAssertEqual(APIClient.operation(route: "vehicles/private-id/records", method: "GET"), "records.list")
        XCTAssertEqual(APIClient.operation(route: "records/private-id", method: "DELETE"), "record.delete")
        XCTAssertEqual(APIClient.operation(route: "records/private-id", method: "PATCH"), "record.update")
        XCTAssertNil(APIClient.operation(route: "client-events", method: "POST"))
        XCTAssertNil(APIClient.operation(route: "unknown/private-id", method: "GET"))
    }

    func testUnknownRecordKindDoesNotBreakHistoryOrCache() throws {
        let data = Data(#"[{"id":"future","vehicleId":"v","kind":"inspection","date":"2026-10-08","title":"Future record","notes":"Still readable","odometerMiles":120000,"costCents":0}]"#.utf8)
        let records = try JSONDecoder().decode([VehicleRecord].self, from: data)
        XCTAssertEqual(records.count, 1)
        XCTAssertEqual(records[0].kind.rawValue, "inspection")
        XCTAssertFalse(records[0].kind.isSupported)
        let roundTrip = try JSONDecoder().decode([VehicleRecord].self, from: JSONEncoder().encode(records))
        XCTAssertEqual(roundTrip[0].kind.rawValue, "inspection")
    }

    func testUndatedImportedNoteRoundTripsAndEditsWithoutInventingValues() throws {
        let vehicle = try JSONDecoder().decode(Vehicle.self, from: Data(#"{"id":"v","name":"Synthetic vehicle","make":"","model":"","year":2002,"odometerMiles":120000,"createdAt":"2026-01-01T00:00:00Z","vin":"SYNTHETIC-VIN","licensePlate":"TEST-ONLY","notes":"Original vehicle note","extraFields":[{"name":"Region","value":"Synthetic region","isRequired":false,"fieldType":0}],"source":{"system":"lubelogger","instance":"synthetic","collection":"vehicles","id":"1"}}"#.utf8))
        let cachedVehicle = try JSONDecoder().decode(Vehicle.self, from: JSONEncoder().encode(vehicle))
        XCTAssertEqual(cachedVehicle.vin, "SYNTHETIC-VIN")
        XCTAssertEqual(cachedVehicle.licensePlate, "TEST-ONLY")
        XCTAssertEqual(cachedVehicle.notes, "Original vehicle note")
        XCTAssertEqual(cachedVehicle.extraFields?.first?.value, "Synthetic region")
        XCTAssertEqual(cachedVehicle.source?.collection, "vehicles")
        var estimatedVehicle = cachedVehicle
        estimatedVehicle.odometerStatus = "estimated"
        XCTAssertEqual(try JSONDecoder().decode(Vehicle.self, from: JSONEncoder().encode(estimatedVehicle)).odometerStatus, "estimated")
        let record = try JSONDecoder().decode(VehicleRecord.self, from: Data(#"{"id":"n","vehicleId":"v","kind":"note","date":"","title":"Collector journal","notes":"First line\nSecond line","odometerMiles":0,"costCents":0,"pinned":true,"tags":["collector"],"extraFields":[{"name":"Sensor","value":"7.25","isRequired":false,"fieldType":2}],"source":{"system":"lubelogger","instance":"synthetic","collection":"notes","id":"12"}}"#.utf8))
        let cached = try JSONDecoder().decode(VehicleRecord.self, from: JSONEncoder().encode(record))
        XCTAssertEqual(cached.dateLabel, "Undated")
        XCTAssertFalse(cached.kind.hasCost)
        XCTAssertFalse(cached.kind.hasOdometer)
        XCTAssertEqual(cached.tags, ["collector"])
        XCTAssertEqual(cached.extraFields?.first?.fieldType, 2)
        XCTAssertEqual(cached.source?.id, "12")
        XCTAssertTrue(cached.matches("second line"))
        XCTAssertTrue(cached.matches("sensor"))
        var draft = RecordDraft(record: cached, mileage: 999)
        XCTAssertTrue(try draft.values(original: cached).isEmpty)
        draft.notes += "\nCorrection"
        let patch = try draft.values(original: cached)
        XCTAssertEqual(Set(patch.keys), ["notes"])
        XCTAssertEqual(patch["notes"] as? String, "First line\nSecond line\nCorrection")
    }

    func testRecordEditingPreservesPrecisionAndPlanRelationships() throws {
        let fuel = VehicleRecord(id: "f", vehicleId: "v", kind: .fuel, date: "2026-10-08", title: "Fuel", notes: "", odometerMiles: 123456.123456789, costCents: 1001, gallons: 11.123456789)
        var fuelDraft = RecordDraft(record: fuel, mileage: 0)
        fuelDraft.title = "Receipt corrected"
        XCTAssertEqual(Set(try fuelDraft.values(original: fuel).keys), ["title"])
        fuelDraft.cost = "10.23"
        XCTAssertEqual(try fuelDraft.values(original: fuel)["costCents"] as? Int, 1023)
        XCTAssertNil(Input.moneyCents("10.239"))
        XCTAssertNil(Input.moneyCents("-1"))
        XCTAssertEqual(Input.moneyCents(Input.moneyText(999_999_999)), 999_999_999)
        let plan = VehicleRecord(id: "p", vehicleId: "v", kind: .plan, date: "", title: "Planned repair", notes: "", odometerMiles: 0, costCents: 5000, plan: PlannedWork(status: "planned", priority: "critical", recordKind: "repair", createdAt: "2026-01-01T00:00:00Z", reminderIds: ["linked-reminder"]))
        var planDraft = RecordDraft(record: plan, mileage: 0)
        planDraft.planStatus = "in-progress"
        let patch = try planDraft.values(original: plan)
        XCTAssertEqual(Set(patch.keys), ["plan"])
        let details = try XCTUnwrap(patch["plan"] as? [String: Any])
        XCTAssertEqual(Set(details.keys), ["status"])
        XCTAssertEqual(details["status"] as? String, "in-progress")
    }

    func testImportedReadingsAndReminderRecurrenceSurviveCache() throws {
        let record = try JSONDecoder().decode(VehicleRecord.self, from: Data(#"{"id":"o","vehicleId":"v","kind":"odometer","date":"2026-10-08","title":"Daily reading","notes":"Estimated from collector","odometerMiles":145100.25,"initialOdometerMiles":145000,"odometerStatus":"estimated","costCents":0,"extraFields":[{"name":"Battery","value":"12.4","isRequired":false,"fieldType":2}]}"#.utf8))
        XCTAssertEqual(record.readingLabel, "Estimated reading")
        XCTAssertFalse(record.kind.hasCost)
        var draft = RecordDraft(record: record, mileage: 0)
        draft.extraFields[0].value = "12.5"
        let patch = try draft.values(original: record)
        XCTAssertEqual(Set(patch.keys), ["extraFields"])
        let reminder = try JSONDecoder().decode(Reminder.self, from: Data(#"{"id":"r","vehicleId":"v","title":"Oil","dueOdometerMiles":150000,"completed":false,"notes":"Preserved","recurrence":{"miles":5000,"fixedIntervals":true},"thresholds":{"urgentMiles":500,"veryUrgentMiles":100}}"#.utf8))
        let cached = try JSONDecoder().decode(Reminder.self, from: JSONEncoder().encode(reminder))
        XCTAssertEqual(cached.recurrence?.miles, 5000)
        XCTAssertEqual(cached.recurrence?.fixedIntervals, true)
        XCTAssertEqual(cached.thresholds?.urgentMiles, 500)
        XCTAssertEqual(cached.notes, "Preserved")
    }
    func testCachedGarageIsBoundToServerAndToken() throws {
        let first = try Connection.validated(server: "https://first.example.com", token: "first-token")
        let changedToken = try Connection.validated(server: "https://first.example.com", token: "second-token")
        let changedServer = try Connection.validated(server: "https://second.example.com", token: "first-token")
        let cache = GarageCache(connectionDigest: first.cacheIdentity)
        XCTAssertTrue(cache.belongs(to: first))
        XCTAssertFalse(cache.belongs(to: changedToken))
        XCTAssertFalse(cache.belongs(to: changedServer))
        XCTAssertFalse(GarageCache().belongs(to: first))
        XCTAssertFalse(String(data: try JSONEncoder().encode(cache), encoding: .utf8)!.contains("first-token"))
    }

    func testCancellingAnInFlightRequestDoesNotReportATransportFailure() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let started = expectation(description: "Request reached URLSession")
        let reported = expectation(description: "Cancellation must not be reported")
        reported.isInverted = true
        fixture.configure(.hold, started: started, reported: reported)
        let request = Task { try await fixture.client.send("vehicles") }
        await fulfillment(of: [started], timeout: 2)
        request.cancel()
        do { _ = try await request.value; XCTFail("Cancelled request succeeded") }
        catch { XCTAssertTrue(error is CancellationError, "Unexpected error: \(error)") }
        await fulfillment(of: [reported], timeout: 0.2)
    }

    func testAlreadyCancelledRequestDoesNotStartOrReport() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let started = expectation(description: "Cancelled task must not start a request")
        let reported = expectation(description: "Cancelled task must not report")
        started.isInverted = true
        reported.isInverted = true
        fixture.configure(.success, started: started, reported: reported)
        let request = Task {
            withUnsafeCurrentTask { $0?.cancel() }
            return try await fixture.client.send("vehicles")
        }
        do { _ = try await request.value; XCTFail("Cancelled request succeeded") }
        catch { XCTAssertTrue(error is CancellationError) }
        await fulfillment(of: [started, reported], timeout: 0.2)
    }

    func testURLSessionCancellationIsPreservedWithoutCancellingTheCaller() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let reported = expectation(description: "URL cancellation must not report")
        reported.isInverted = true
        fixture.configure(.failure(.cancelled), reported: reported)
        do { _ = try await fixture.client.send("vehicles"); XCTFail("Cancelled request succeeded") }
        catch { XCTAssertTrue(error is CancellationError) }
        XCTAssertFalse(Task.isCancelled)
        await fulfillment(of: [reported], timeout: 0.2)
    }

    func testGenuineTransportFailureReportsOnlyBoundedClassification() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let reported = expectation(description: "Offline failure reported")
        fixture.configure(.failure(.notConnectedToInternet), reported: reported)
        do { _ = try await fixture.client.send("vehicles"); XCTFail("Offline request succeeded") }
        catch {
            XCTAssertFalse(error is CancellationError)
            XCTAssertTrue(error.localizedDescription.contains("Couldn't reach your server"))
        }
        await fulfillment(of: [reported], timeout: 2)
        let event = try XCTUnwrap(fixture.events.first)
        XCTAssertEqual(Set(event.keys), ["operation", "durationMs", "statusCode", "failureKind"])
        XCTAssertEqual(event["operation"] as? String, "vehicles.list")
        XCTAssertEqual(event["statusCode"] as? Int, 0)
        XCTAssertEqual(event["failureKind"] as? String, "offline")
        let classifications: [(URLError.Code, String)] = [(.cannotFindHost, "dns"), (.dnsLookupFailed, "dns"), (.timedOut, "timeout"), (.cannotConnectToHost, "connection"), (.networkConnectionLost, "connection"), (.serverCertificateUntrusted, "tls"), (.secureConnectionFailed, "tls"), (.dataNotAllowed, "offline"), (.unknown, "other")]
        for (code, kind) in classifications {
            XCTAssertEqual(APIClient.failureKind(for: code), kind)
        }
    }

    @MainActor
    func testCancelledRefreshPreservesExistingStateAndCanRefreshAgain() async throws {
        for detail in [false, true] {
            for wasOffline in [false, true] {
                let fixture = HTTPFixture()
                defer { fixture.close() }
                let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
                defer { try? FileManager.default.removeItem(at: directory) }
                let vehicle = Vehicle(id: "saved", name: "Cached ride", make: "", model: "", year: 2002, odometerMiles: 123, createdAt: "2026-01-01T00:00:00Z")
                let reminder = Reminder(id: "saved-reminder", vehicleId: vehicle.id, title: "Oil", dueDate: "2026-12-01", completed: false)
                let date = Date(timeIntervalSince1970: 123)
                let cache = GarageCache(vehicles: [vehicle], details: [vehicle.id: VehicleDetail(reminders: [reminder])], updatedAt: date, connectionDigest: fixture.connection.cacheIdentity)
                let store = GarageStore(connection: fixture.connection, cache: cache, offline: wasOffline, cacheURL: directory.appendingPathComponent("garage.json"), session: fixture.session)
                store.error = "Existing status"
                let started = expectation(description: "Refresh started")
                started.expectedFulfillmentCount = detail ? 3 : 1
                fixture.configure(.hold, started: started)
                let refresh = Task {
                    if detail { await store.refreshDetail(vehicle.id) }
                    else { await store.refresh() }
                }
                await fulfillment(of: [started], timeout: 2)
                refresh.cancel()
                await refresh.value
                XCTAssertEqual(store.offline, wasOffline)
                XCTAssertEqual(store.error, "Existing status")
                XCTAssertEqual(store.vehicles, [vehicle])
                XCTAssertEqual(store.cache.updatedAt, date)
                XCTAssertEqual(store.detail(vehicle.id).reminders.map(\.id), [reminder.id])
                XCTAssertFalse(store.refreshing)
                fixture.configure(.success)
                await store.refresh()
                XCTAssertFalse(store.offline)
                XCTAssertNil(store.error)
                XCTAssertTrue(store.vehicles.isEmpty)
            }
        }
    }

    @MainActor
    func testGenuineOfflineRefreshStillMarksOfflineAndRecovers() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = GarageStore(connection: fixture.connection, cache: GarageCache(), cacheURL: directory.appendingPathComponent("garage.json"), session: fixture.session)
        fixture.configure(.failure(.notConnectedToInternet))
        await store.refresh()
        XCTAssertTrue(store.offline)
        XCTAssertNotNil(store.error)
        fixture.configure(.success)
        await store.refresh()
        XCTAssertFalse(store.offline)
        XCTAssertNil(store.error)
    }

    @MainActor
    func testReplacementRefreshSupersedesAnInFlightRefresh() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = GarageStore(connection: fixture.connection, cache: GarageCache(), offline: true, cacheURL: directory.appendingPathComponent("garage.json"), session: fixture.session)
        store.error = "Previous connection error"
        let started = expectation(description: "Original request started")
        fixture.configure(.hold, started: started)
        let original = Task { await store.refresh() }
        await fulfillment(of: [started], timeout: 2)
        XCTAssertTrue(store.refreshing)
        let replacementStarted = expectation(description: "Replacement request was not dropped")
        fixture.configure(.success, started: replacementStarted)
        let replacement = Task { await store.refresh() }
        await fulfillment(of: [replacementStarted], timeout: 2)
        await replacement.value
        original.cancel()
        await original.value
        XCTAssertFalse(store.refreshing)
        XCTAssertFalse(store.offline)
        XCTAssertNil(store.error)
        XCTAssertNotNil(store.cache.updatedAt)
    }

    @MainActor
    func testAlreadyCancelledRefreshDoesNotCancelAHealthyRefresh() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = GarageStore(connection: fixture.connection, cache: GarageCache(), cacheURL: directory.appendingPathComponent("garage.json"), session: fixture.session)
        let started = expectation(description: "Healthy request started")
        let stopped = expectation(description: "Healthy request must not be cancelled")
        stopped.isInverted = true
        fixture.configure(.hold, started: started, stopped: stopped)
        let original = Task { await store.refresh() }
        await fulfillment(of: [started], timeout: 2)
        let cancelled = Task {
            withUnsafeCurrentTask { $0?.cancel() }
            await store.refresh()
        }
        await cancelled.value
        await fulfillment(of: [stopped], timeout: 0.2)
        XCTAssertTrue(store.refreshing)
        XCTAssertFalse(store.offline)
        XCTAssertNil(store.error)
        fixture.configure(.hold)
        original.cancel()
        await original.value
    }
}

private final class HTTPFixture {
    enum Response { case hold, success, failure(URLError.Code) }
    let connection: Connection
    let session: URLSession
    var client: APIClient { APIClient(connection: connection, session: session) }
    private let lock = NSLock()
    private var response: Response = .success
    private var started: XCTestExpectation?
    private var reported: XCTestExpectation?
    private var stopped: XCTestExpectation?
    private var recordedEvents: [[String: Any]] = []
    var events: [[String: Any]] { lock.lock(); defer { lock.unlock() }; return recordedEvents }

    init() {
        connection = Connection(server: URL(string: "https://\(UUID().uuidString.lowercased()).example.test")!, token: "synthetic-test-token")
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [FixtureProtocol.self]
        session = URLSession(configuration: configuration)
        FixtureProtocol.register(self)
    }

    func close() { session.invalidateAndCancel(); FixtureProtocol.remove(self) }

    func configure(_ response: Response, started: XCTestExpectation? = nil, reported: XCTestExpectation? = nil, stopped: XCTestExpectation? = nil) {
        lock.lock()
        defer { lock.unlock() }
        self.response = response
        self.started = started
        self.reported = reported
        self.stopped = stopped
    }

    func stop() {
        lock.lock()
        let stopped = self.stopped
        lock.unlock()
        stopped?.fulfill()
    }

    func receive(_ transport: URLProtocol) {
        lock.lock()
        let response = self.response
        let started = self.started
        let reported = self.reported
        lock.unlock()
        if transport.request.url?.path == "/api/v1/client-events" {
            var data = transport.request.httpBody ?? Data()
            if let stream = transport.request.httpBodyStream {
                stream.open()
                defer { stream.close() }
                var buffer = [UInt8](repeating: 0, count: 1024)
                while stream.hasBytesAvailable {
                    let count = stream.read(&buffer, maxLength: buffer.count)
                    if count <= 0 { break }
                    data.append(buffer, count: count)
                }
            }
            if let event = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] {
                lock.lock(); recordedEvents.append(event); lock.unlock()
            }
            respond(transport, status: 204)
            reported?.fulfill()
            return
        }
        started?.fulfill()
        switch response {
        case .hold: break
        case .success: respond(transport, status: 200)
        case .failure(let code): transport.client?.urlProtocol(transport, didFailWithError: URLError(code))
        }
    }

    private func respond(_ transport: URLProtocol, status: Int) {
        transport.client?.urlProtocol(transport, didReceive: HTTPURLResponse(url: transport.request.url!, statusCode: status, httpVersion: nil, headerFields: nil)!, cacheStoragePolicy: .notAllowed)
        transport.client?.urlProtocol(transport, didLoad: Data("[]".utf8))
        transport.client?.urlProtocolDidFinishLoading(transport)
    }
}

private final class FixtureProtocol: URLProtocol {
    private static let lock = NSLock()
    private static var fixtures: [String: HTTPFixture] = [:]
    static func register(_ fixture: HTTPFixture) { lock.lock(); defer { lock.unlock() }; fixtures[fixture.connection.server.host!] = fixture }
    static func remove(_ fixture: HTTPFixture) { lock.lock(); defer { lock.unlock() }; fixtures.removeValue(forKey: fixture.connection.server.host!) }
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.lock.lock()
        let fixture = Self.fixtures[request.url!.host!]
        Self.lock.unlock()
        guard let fixture else { client?.urlProtocol(self, didFailWithError: URLError(.cancelled)); return }
        fixture.receive(self)
    }
    override func stopLoading() {
        Self.lock.lock()
        let fixture = Self.fixtures[request.url!.host!]
        Self.lock.unlock()
        fixture?.stop()
    }
}
