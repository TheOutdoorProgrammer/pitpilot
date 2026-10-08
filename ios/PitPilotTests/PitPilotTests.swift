import XCTest
@testable import PitPilot

final class PitPilotTests: XCTestCase {
    func testDenseCalendarTrendPreservesDaySpacingGapsZeroAndSparseAxis() throws {
        let start = try XCTUnwrap(SignalFormat.date("2026-05-01T00:00:00Z"))
        let points = (0..<94).filter { ![23, 24, 25, 61, 62].contains($0) }.map { index in
            let day = String(ISO8601DateFormatter().string(from: start.addingTimeInterval(Double(index) * 86400)).prefix(10))
            let fuel = index == 93 ? 0 : 95 - Double(index % 19) * 5
            return SignalHistoryPoint(minimum: fuel, maximum: fuel, mean: fuel, first: fuel, last: fuel, count: 1, calendarDate: day, timezone: "unknown")
        }
        let series = SignalHistorySeries(source: "smartcar", quality: "measured", statistic: "snapshot", points: points)
        let chart = SignalChartData(series: series, unit: "%", calendar: true)
        XCTAssertEqual(chart.kind, .trend)
        XCTAssertEqual(chart.vertices.count, 89)
        XCTAssertEqual(chart.vertices.last?.value, 0)
        XCTAssertEqual(Set(chart.vertices.map(\.segment)).count, 3)
        XCTAssertEqual(chart.vertices[23].x - chart.vertices[22].x, 4)
        XCTAssertNotEqual(chart.vertices[23].segment, chart.vertices[22].segment)
        XCTAssertEqual(chart.axisValues.count, 4)
        XCTAssertEqual(chart.yDomain(unit: "%"), 0...100)
        XCTAssertEqual(chart.xDomain.upperBound - chart.xDomain.lowerBound, 94)
        XCTAssertTrue(points.allSatisfy { $0.firstObservedAt == nil && $0.windowStart == nil })
        XCTAssertNil(SignalCalendarDay.number("2026-02-30"))
        let estimated = SignalHistorySeries(source: "pi", quality: "estimated", statistic: "snapshot", points: points)
        let other = SignalChartData(series: estimated, unit: "%", calendar: true)
        XCTAssertNotEqual(chart.vertices.first?.segment, other.vertices.first?.segment)
    }

    func testBooleanAndCodeBucketsNeverBecomeFractionalStates() {
        let mixed = SignalHistoryPoint(minimum: 0, maximum: 1, mean: 0.375, first: 0, last: 1, count: 8, calendarDate: "2026-10-08")
        let off = SignalHistoryPoint(minimum: 0, maximum: 0, mean: 0, first: 0, last: 0, count: 1, calendarDate: "2026-10-07")
        let on = SignalHistoryPoint(minimum: 1, maximum: 1, mean: 1, first: 1, last: 1, count: 1, calendarDate: "2026-10-06")
        let invalid = SignalHistoryPoint(minimum: 0.5, maximum: 0.5, mean: 0.5, first: 0.5, last: 0.5, count: 1, calendarDate: "2026-10-05")
        let series = SignalHistorySeries(source: "pi", quality: "measured", statistic: "snapshot", points: [mixed, off, on, invalid])
        let chart = SignalChartData(series: series, unit: "boolean", calendar: true)
        XCTAssertEqual(chart.kind, .state)
        XCTAssertTrue(chart.vertices.isEmpty)
        XCTAssertEqual(chart.buckets.map(\.state), ["Unknown", "On", "Off", "Mixed"])
        let codes = SignalHistoryPoint(minimum: 2, maximum: 4, mean: 3, first: 2, last: 4, count: 2, calendarDate: "2026-10-08")
        let codeChart = SignalChartData(series: SignalHistorySeries(source: "pi", quality: "measured", statistic: "sample", points: [codes]), unit: "code", calendar: true)
        XCTAssertEqual(codeChart.kind, .category)
        XCTAssertTrue(codeChart.vertices.isEmpty)
        XCTAssertEqual(codeChart.buckets.first?.category, "Mixed")
        XCTAssertEqual(SignalChartKind.resolve(unit: "boolean", statistic: "count"), .bars)
    }

    func testCountBarsPreserveAveragePeriodValueWithoutInventingTotals() {
        let point = SignalHistoryPoint(minimum: 10, maximum: 20, mean: 15, first: 10, last: 20, count: 2, calendarDate: "2026-10-08")
        let series = SignalHistorySeries(source: "pi", quality: "measured", statistic: "sum", points: [point])
        let chart = SignalChartData(series: series, unit: "count", calendar: true)
        XCTAssertEqual(chart.kind, .bars)
        XCTAssertEqual(chart.buckets.first?.point.mean, 15)
        XCTAssertEqual(chart.buckets.first?.point.count, 2)
        XCTAssertEqual(chart.buckets.first?.point.maximum, 20)
        XCTAssertTrue(chart.vertices.isEmpty)
        XCTAssertEqual(SignalChartKind.resolve(unit: "L", statistic: "sum"), .bars)
        XCTAssertEqual(SignalChartKind.resolve(unit: "kPa", statistic: "max"), .trend)
        let zero = SignalHistoryPoint(minimum: 0, maximum: 0, mean: 0, first: 0, last: 0, count: 1, calendarDate: "2026-10-07")
        let zeroChart = SignalChartData(series: SignalHistorySeries(source: "pi", quality: "measured", statistic: "sum", points: [zero, point]), unit: "count", calendar: true)
        XCTAssertEqual(zeroChart.yDomain(unit: "count").lowerBound, 0)
        let bounds = zeroChart.barBounds(zeroChart.buckets[0])
        XCTAssertEqual(bounds.upperBound - bounds.lowerBound, 0.65, accuracy: 0.000001)
        XCTAssertLessThan(bounds.upperBound, zeroChart.barBounds(zeroChart.buckets[1]).lowerBound)
        let timestamp = SignalHistoryPoint(windowStart: "2026-10-08T00:00:00Z", windowEnd: "2026-10-09T00:00:00Z",
            minimum: 10, maximum: 20, mean: 15, first: 10, last: 20, count: 2)
        let daily = SignalChartData(series: SignalHistorySeries(source: "pi", quality: "measured", statistic: "sum", points: [timestamp]), unit: "count", calendar: false)
        let dailyBounds = daily.barBounds(daily.buckets[0])
        XCTAssertEqual(dailyBounds.upperBound - dailyBounds.lowerBound, 86400 * 0.65, accuracy: 0.001)
    }

    func testLongUniformBooleanWindowShowsEndpointsWithoutHeldState() {
        let point = SignalHistoryPoint(bucketStart: "2026-10-08T10:00:00Z", bucketEnd: "2026-10-08T12:00:00Z",
            windowStart: "2026-10-08T10:00:00Z", windowEnd: "2026-10-08T12:00:00Z",
            minimum: 0, maximum: 0, mean: 0, first: 0, last: 0, count: 2,
            firstObservedAt: "2026-10-08T10:00:00Z", lastObservedAt: "2026-10-08T12:00:00Z")
        let series = SignalHistorySeries(source: "pi", quality: "measured", statistic: "sample", points: [point])
        let chart = SignalChartData(series: series, unit: "boolean", calendar: false)
        XCTAssertEqual(chart.stateMarks.count, 2)
        XCTAssertTrue(chart.stateMarks.allSatisfy { $0.start == $0.end && $0.label == "Off" && !$0.summary })
        XCTAssertEqual(chart.stateMarks[1].start - chart.stateMarks[0].start, 7200)
        let summary = SignalChartData(series: SignalHistorySeries(source: "pi", quality: "measured", statistic: "max", points: [point]), unit: "boolean", calendar: false)
        XCTAssertEqual(summary.stateMarks.count, 1)
        XCTAssertTrue(summary.stateMarks[0].summary)
        XCTAssertEqual(summary.stateMarks[0].start, summary.stateMarks[0].end)
    }

    func testTimestampTrendBreaksMissingBucketsAndLongUnknownGaps() throws {
        func point(_ start: String, _ end: String, _ first: String, _ last: String) -> SignalHistoryPoint {
            SignalHistoryPoint(bucketStart: start, bucketEnd: end, windowStart: first, windowEnd: last,
                minimum: 30, maximum: 60, mean: 42, first: 35, last: 50, count: 8, firstObservedAt: first, lastObservedAt: last)
        }
        let points = [
            point("2026-10-08T12:00:00Z", "2026-10-08T12:05:00Z", "2026-10-08T12:00:30Z", "2026-10-08T12:04:00Z"),
            point("2026-10-08T12:05:00Z", "2026-10-08T12:10:00Z", "2026-10-08T12:05:30Z", "2026-10-08T12:09:00Z"),
            point("2026-10-08T12:15:00Z", "2026-10-08T12:20:00Z", "2026-10-08T12:15:30Z", "2026-10-08T12:19:00Z"),
            point("2026-10-08T12:20:00Z", "2026-10-08T13:00:00Z", "2026-10-08T12:20:30Z", "2026-10-08T12:59:00Z")
        ]
        let series = SignalHistorySeries(source: "pi", quality: "measured", statistic: "sample", points: points)
        let chart = SignalChartData(series: series, unit: "kPa", calendar: false)
        XCTAssertEqual(chart.vertices.count, 8)
        XCTAssertEqual(chart.vertices[0].segment, chart.vertices[3].segment)
        XCTAssertNotEqual(chart.vertices[3].segment, chart.vertices[4].segment)
        XCTAssertNotEqual(chart.vertices[6].segment, chart.vertices[7].segment)
        XCTAssertEqual(chart.vertices[0].x, try XCTUnwrap(SignalFormat.date(points[0].firstObservedAt!)).timeIntervalSince1970)
        XCTAssertEqual(chart.buckets.first?.point.minimum, 30)
        XCTAssertEqual(chart.buckets.first?.point.maximum, 60)
    }

    func testUnknownDiagnosticsAllowOmittedCodesWithoutClaimingNoFaults() throws {
        for json in [#"{"class":"stored","successfulReads":0,"unknown":true}"#, #"{"class":"stored","codes":null,"successfulReads":0,"unknown":true}"#] {
            let diagnostic = try JSONDecoder().decode(SignalDiagnostic.self, from: Data(json.utf8))
            XCTAssertTrue(diagnostic.unknown)
            XCTAssertNil(diagnostic.codes)
        }
        XCTAssertThrowsError(try JSONDecoder().decode(SignalDiagnostic.self, from: Data(#"{"class":"stored","codes":null,"successfulReads":1,"unknown":false}"#.utf8)))
        let known = try JSONDecoder().decode(SignalDiagnostic.self, from: Data(#"{"class":"stored","codes":[],"successfulReads":1,"unknown":false}"#.utf8))
        XCTAssertFalse(known.unknown)
        XCTAssertEqual(known.codes, [])
    }

    private var signalFixture: Data {
        Data(#"{"asOf":"2026-10-08T12:00:00Z","definitions":[{"metric":"fuel_level_pct","label":"Fuel level","unit":"%","staleAfterSeconds":900},{"metric":"rpm","label":"Engine speed","unit":"rpm","staleAfterSeconds":900}],"series":[{"metric":"fuel_level_pct","unit":"%","source":"smartcar","statistic":"snapshot","quality":"measured","stale":true,"latest":{"key":"synthetic","metric":"fuel_level_pct","unit":"%","statistic":"snapshot","quality":"measured","value":0,"calendarDate":"2026-10-08","timezone":"unknown"}}]}"#.utf8)
    }

    func testSignalSnapshotsKeepUnknownTimeAndActualZero() throws {
        let signals = try JSONDecoder().decode(LatestSignals.self, from: signalFixture)
        XCTAssertEqual(signals.metrics, ["fuel_level_pct"])
        let reading = try XCTUnwrap(signals.series.first)
        XCTAssertNil(reading.latest.referenceDate)
        XCTAssertNil(reading.latest.observedAt)
        XCTAssertEqual(reading.latest.value, 0)
        XCTAssertEqual(SignalFormat.value(reading.latest.value, unit: reading.unit), "0 %")
        XCTAssertTrue(reading.latest.timeLabel.contains("Time and timezone unknown"))
        XCTAssertTrue(reading.isStale(at: Date(), definition: signals.definition("fuel_level_pct")))
        let history = try JSONDecoder().decode(SignalHistory.self, from: Data(#"{"metric":"fuel_level_pct","unit":"%","from":"2026-10-01T00:00:00Z","to":"2026-10-09T00:00:00Z","maxPoints":120,"series":[{"source":"smartcar","quality":"measured","statistic":"snapshot","points":[{"calendarDate":"2026-10-08","timezone":"unknown","minimum":0,"maximum":0,"mean":0,"first":0,"last":0,"count":1}]}]}"#.utf8))
        let point = try XCTUnwrap(history.series.first?.points.first)
        XCTAssertNil(point.windowStart)
        XCTAssertNil(point.firstObservedAt)
        XCTAssertEqual(point.calendarDate, "2026-10-08")
    }

    func testHistoryInitiallyIncludesOlderCalendarSnapshotsWithoutAssigningTime() throws {
        let now = try XCTUnwrap(SignalFormat.date("2026-10-08T12:00:00Z"))
        let oldSnapshot = LatestSignal(metric: "fuel_level_pct", unit: "%", source: "smartcar", statistic: "snapshot", quality: "measured",
            latest: SignalObservation(key: "old", metric: "fuel_level_pct", unit: "%", statistic: "snapshot", quality: "measured", value: 55, calendarDate: "2026-08-15", timezone: "unknown"), stale: true)
        let recentSample = LatestSignal(metric: "fuel_level_pct", unit: "%", source: "pi", statistic: "sample", quality: "measured",
            latest: SignalObservation(key: "recent", metric: "fuel_level_pct", unit: "%", statistic: "sample", quality: "measured", value: 42, observedAt: "2026-10-08T11:00:00Z"), stale: true)
        let signals = LatestSignals(asOf: "2026-10-08T12:00:00Z", definitions: [], series: [oldSnapshot, recentSample])
        XCTAssertEqual(signals.initialHistoryDays("fuel_level_pct", statistic: "snapshot", now: now), 365)
        XCTAssertEqual(signals.initialHistoryDays("fuel_level_pct", statistic: "sample", now: now), 30)
        XCTAssertEqual(signals.initialHistoryDays("missing", statistic: "sample", now: now), 30)
        XCTAssertNil(oldSnapshot.latest.referenceDate)
        let fresh = try JSONDecoder().decode(LatestSignals.self, from: signalFixture)
        XCTAssertEqual(fresh.initialHistoryDays("fuel_level_pct", statistic: "snapshot", now: now), 30)
    }

    func testSignalStalenessAdvancesWhenCachedWithoutErasingValue() throws {
        let observed = try XCTUnwrap(SignalFormat.date("2026-10-08T12:00:00.125Z"))
        let definition = SignalDefinition(metric: "rpm", label: "Engine speed", unit: "rpm", staleAfterSeconds: 900)
        let reading = LatestSignal(metric: "rpm", unit: "rpm", source: "pi", statistic: "sample", quality: "measured",
            latest: SignalObservation(key: "synthetic", metric: "rpm", unit: "rpm", statistic: "sample", quality: "measured", value: 0, observedAt: "2026-10-08T12:00:00.125Z"), stale: false)
        XCTAssertFalse(reading.isStale(at: observed.addingTimeInterval(30), definition: definition))
        XCTAssertTrue(reading.isStale(at: observed.addingTimeInterval(901), definition: definition))
        XCTAssertEqual(reading.latest.value, 0)
        var cache = GarageCache()
        cache.signals = ["synthetic-vehicle": LatestSignals(asOf: "2026-10-08T12:00:01Z", definitions: [definition], series: [reading])]
        let restored = try JSONDecoder().decode(GarageCache.self, from: JSONEncoder().encode(cache))
        XCTAssertEqual(restored.signals?["synthetic-vehicle"]?.series.first?.latest.value, 0)
        let old = try JSONDecoder().decode(GarageCache.self, from: Data(#"{"vehicles":[],"details":{}}"#.utf8))
        XCTAssertNil(old.signals)
        XCTAssertNil(old.signalHistory)
    }

    @MainActor
    func testSignalRefreshFailureAndCancellationKeepGarageOnlineAndCached() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        var cache = GarageCache()
        cache.signals = ["synthetic": try JSONDecoder().decode(LatestSignals.self, from: signalFixture)]
        let store = GarageStore(connection: fixture.connection, cache: cache, cacheURL: directory.appendingPathComponent("garage.json"), session: fixture.session)
        let timeoutReported = expectation(description: "Signal timeout telemetry completed before session teardown")
        fixture.configure(.failure(.timedOut), reported: timeoutReported)
        await store.refreshSignals("synthetic")
        await fulfillment(of: [timeoutReported], timeout: 2)
        XCTAssertFalse(store.offline)
        XCTAssertNil(store.error)
        XCTAssertNotNil(store.signalErrors["synthetic"])
        XCTAssertEqual(store.signals("synthetic")?.series.first?.latest.value, 0)
        fixture.configure(.failure(.cancelled))
        await store.refreshSignals("synthetic")
        XCTAssertFalse(store.offline)
        XCTAssertTrue(store.signalsRefreshing.isEmpty)
        let recoveryReported = expectation(description: "Signal recovery telemetry completed before session teardown")
        fixture.configure(.json(signalFixture), reported: recoveryReported)
        await store.refreshSignals("synthetic")
        await fulfillment(of: [recoveryReported], timeout: 2)
        XCTAssertNil(store.signalErrors["synthetic"])
        XCTAssertEqual(store.signals("synthetic")?.series.count, 1)
    }

    func testSignalHistoryUsesEncodedQueryAndBoundedTelemetryOperation() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let reported = expectation(description: "History telemetry completed before session teardown")
        fixture.configure(.success, reported: reported)
        let _: [Vehicle] = try await fixture.client.request("vehicles/private-id/signals/history", query: [URLQueryItem(name: "metric", value: "fuel_level_pct"), URLQueryItem(name: "from", value: "2026-10-08T00:00:00+01:00")])
        await fulfillment(of: [reported], timeout: 2)
        let url = try XCTUnwrap(fixture.requests.first)
        XCTAssertEqual(url.path, "/api/v1/vehicles/private-id/signals/history")
        let query = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems)
        XCTAssertEqual(query.first { $0.name == "from" }?.value, "2026-10-08T00:00:00+01:00")
        XCTAssertTrue(url.absoluteString.contains("%2B01:00"))
        XCTAssertEqual(APIClient.operation(route: "vehicles/private-id/signals/history", method: "GET"), "signals.history")
        XCTAssertEqual(APIClient.operation(route: "vehicles/private-id/signals/latest", method: "GET"), "signals.latest")
        XCTAssertNil(APIClient.operation(route: "vehicles/private-id/signals/arbitrary", method: "GET"))
    }

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
    enum Response { case hold, success, failure(URLError.Code), json(Data) }
    let connection: Connection
    let session: URLSession
    var client: APIClient { APIClient(connection: connection, session: session) }
    private let lock = NSLock()
    private var response: Response = .success
    private var started: XCTestExpectation?
    private var reported: XCTestExpectation?
    private var stopped: XCTestExpectation?
    private var recordedEvents: [[String: Any]] = []
    private var recordedRequests: [URL] = []
    var events: [[String: Any]] { lock.lock(); defer { lock.unlock() }; return recordedEvents }
    var requests: [URL] { lock.lock(); defer { lock.unlock() }; return recordedRequests }

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
        if let url = transport.request.url { lock.lock(); recordedRequests.append(url); lock.unlock() }
        switch response {
        case .hold: break
        case .success: respond(transport, status: 200)
        case .json(let data): respond(transport, status: 200, data: data)
        case .failure(let code): transport.client?.urlProtocol(transport, didFailWithError: URLError(code))
        }
    }

    private func respond(_ transport: URLProtocol, status: Int, data: Data = Data("[]".utf8)) {
        transport.client?.urlProtocol(transport, didReceive: HTTPURLResponse(url: transport.request.url!, statusCode: status, httpVersion: nil, headerFields: nil)!, cacheStoragePolicy: .notAllowed)
        transport.client?.urlProtocol(transport, didLoad: data)
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
