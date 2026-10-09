import XCTest
import UIKit
import ImageIO
import UniformTypeIdentifiers
@testable import PitPilot

final class PitPilotTests: XCTestCase {
    @MainActor
    func testDashboardMetricChoicesPersistPerVehicleWithoutDroppingData() throws {
        let fixture = HTTPFixture(); defer { fixture.close() }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let url = directory.appendingPathComponent("garage.json")
        var cache = GarageCache(connectionDigest: fixture.connection.cacheIdentity)
        cache.signals = ["first": try JSONDecoder().decode(LatestSignals.self, from: signalFixture)]
        let store = GarageStore(connection: fixture.connection, cache: cache, offline: true, cacheURL: url, session: fixture.session)
        XCTAssertTrue(store.dashboardMetricVisible("fuel_level_pct", vehicleID: "first"))
        store.setDashboardMetric("fuel_level_pct", vehicleID: "first", visible: false)
        XCTAssertFalse(store.dashboardMetricVisible("fuel_level_pct", vehicleID: "first"))
        XCTAssertTrue(store.dashboardMetricVisible("fuel_level_pct", vehicleID: "second"))
        let saved = try JSONDecoder().decode(GarageCache.self, from: Data(contentsOf: url))
        XCTAssertEqual(saved.hiddenDashboardMetrics?["first"], ["fuel_level_pct"])
        XCTAssertEqual(saved.signals?["first"]?.series.count, 1)
        XCTAssertTrue(saved.belongs(to: fixture.connection))
        let restored = GarageStore(connection: fixture.connection, cache: saved, offline: true, cacheURL: url, session: fixture.session)
        XCTAssertFalse(restored.dashboardMetricVisible("fuel_level_pct", vehicleID: "first"))
        restored.setDashboardMetric("fuel_level_pct", vehicleID: "first", visible: true)
        XCTAssertTrue(restored.dashboardMetricVisible("fuel_level_pct", vehicleID: "first"))
        XCTAssertTrue(restored.dashboardMetricVisible("new_metric", vehicleID: "first"))
        XCTAssertNil(try JSONDecoder().decode(GarageCache.self, from: Data(#"{"vehicles":[],"details":{}}"#.utf8)).hiddenDashboardMetrics)
    }

    func testUnifiedTimelineKeepsDenseSparseAndIntradaySourceSemantics() throws {
        let base = try XCTUnwrap(SignalFormat.date("2026-05-01T00:00:00Z"))
        let days = (0..<94).filter { ![23, 24, 25, 61, 62].contains($0) }.map { index in
            let day = String(ISO8601DateFormatter().string(from: base.addingTimeInterval(Double(index) * 86400)).prefix(10))
            let value = index == 93 ? 0.0 : 94 - Double(index % 19) * 4.8
            return SignalHistoryPoint(minimum: value, maximum: value, mean: value, first: value, last: value, count: 1, calendarDate: day, timezone: "unknown")
        }
        func sample(_ timestamp: String, _ value: Double) -> SignalHistoryPoint {
            SignalHistoryPoint(bucketStart: timestamp, bucketEnd: timestamp, windowStart: timestamp, windowEnd: timestamp,
                minimum: value, maximum: value, mean: value, first: value, last: value, count: 1,
                firstObservedAt: timestamp, lastObservedAt: timestamp)
        }
        let historical = SignalHistorySeries(source: "lubelogger", quality: "measured", statistic: "snapshot", points: days)
        let oldSamples = SignalHistorySeries(source: "lubelogger", quality: "measured", statistic: "sample", points: [sample("2026-08-04T10:00:00Z", 20), sample("2026-08-10T10:00:00Z", 10)])
        let pi = SignalHistorySeries(source: "pi", quality: "measured", statistic: "sample", points: [sample("2026-10-08T12:00:00Z", 60), sample("2026-10-08T12:00:30Z", 59)])
        let smartcar = SignalHistorySeries(source: "smartcar", quality: "estimated", statistic: "sample", points: [sample("2026-10-08T12:00:00Z", 61)])
        let maximum = SignalHistorySeries(source: "lubelogger", quality: "measured", statistic: "max", points: [days[0]])
        let history = SignalHistory(metric: "fuel_level_pct", unit: "%", from: "2026-01-01T00:00:00Z", to: "2026-10-09T00:00:00Z", maxPoints: 120, series: [historical, oldSamples, pi, smartcar, maximum])
        let chart = SignalChartData(history: history, statistic: "trend")
        XCTAssertEqual(chart.buckets.count, 94)
        XCTAssertEqual(chart.vertices.count, 94)
        XCTAssertEqual(chart.buckets.filter { $0.calendar }.count, 89)
        XCTAssertTrue(chart.hasCalendarDays)
        XCTAssertEqual(chart.sparseVertices.count, 10)
        XCTAssertTrue(chart.sparseVertices.contains { $0.segment.hasPrefix("source-gap/pi/") })
        XCTAssertEqual(Set(chart.buckets.map(\.id)).count, chart.buckets.count)
        XCTAssertEqual(chart.yDomain(unit: "%"), 0...100)
        XCTAssertLessThanOrEqual(chart.axisValues.count, 4)
        XCTAssertFalse(chart.buckets.contains { $0.origin?.statistic == "max" })
        XCTAssertEqual(chart.vertices.filter { $0.id.hasPrefix("pi/") }.map(\.x), [
            try XCTUnwrap(SignalFormat.date("2026-10-08T12:00:00Z")).timeIntervalSince1970,
            try XCTUnwrap(SignalFormat.date("2026-10-08T12:00:30Z")).timeIntervalSince1970
        ])
        XCTAssertTrue(chart.vertices.contains { $0.estimated })
        XCTAssertTrue(chart.buckets.filter { $0.calendar }.allSatisfy { $0.point.firstObservedAt == nil && $0.point.windowStart == nil && $0.point.timezone == "unknown" })
        XCTAssertEqual(SignalChartData(history: history, statistic: "max").buckets.count, 1)
    }

    func testUnifiedStateHistoryKeepsMixedStatesAndCountUnitsSeparate() {
        let mixed = SignalHistoryPoint(minimum: 0, maximum: 1, mean: 0.375, first: 0, last: 1, count: 8, calendarDate: "2026-10-08")
        let history = SignalHistory(metric: "mil_on", unit: "boolean", from: "", to: "", maxPoints: 120, series: [
            SignalHistorySeries(source: "lubelogger", quality: "measured", statistic: "snapshot", points: [mixed]),
            SignalHistorySeries(source: "pi", quality: "measured", statistic: "count", points: [mixed], unit: "count")
        ])
        let trend = SignalChartData(history: history, statistic: "trend")
        XCTAssertEqual(trend.kind, .state)
        XCTAssertEqual(trend.stateMarks.map(\.label), ["Mixed"])
        XCTAssertTrue(trend.vertices.isEmpty)
        XCTAssertTrue(trend.sparseVertices.isEmpty)
        XCTAssertEqual(trend.axisValues.count, 1)
        XCTAssertEqual(history.displayedUnit("count"), "count")
        XCTAssertEqual(SignalChartData(history: history, statistic: "count").kind, .bars)
    }

    @MainActor
    func testDashboardMetricSaveFailureRestoresPreviousChoice() throws {
        let fixture = HTTPFixture(); defer { fixture.close() }
        let blockedParent = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: blockedParent) }
        try Data("file blocks cache directory".utf8).write(to: blockedParent)
        let store = GarageStore(connection: fixture.connection, cache: GarageCache(), cacheURL: blockedParent.appendingPathComponent("garage.json"), session: fixture.session)
        store.setDashboardMetric("fuel_level_pct", vehicleID: "vehicle", visible: false)
        XCTAssertTrue(store.dashboardMetricVisible("fuel_level_pct", vehicleID: "vehicle"))
        XCTAssertNotNil(store.dashboardPreferenceError)
        XCTAssertNil(store.error)
        XCTAssertEqual(try Data(contentsOf: blockedParent), Data("file blocks cache directory".utf8))
    }

    @MainActor
    func testGPSHistoryDeletionRefreshesSmartcarFallbackAndRejectsStaleCoordinates() async throws {
        let fixture = HTTPFixture(); defer { fixture.close() }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let piLocation = Data(#"{"location":{"latitude":1,"longitude":2,"recordedAt":"2026-01-02T00:00:00Z","source":"pi-gps"}}"#.utf8)
        let smartcarLocation = Data(#"{"location":{"latitude":3,"longitude":4,"recordedAt":"2026-01-01T00:00:00Z","source":"smartcar"}}"#.utf8)
        var signals = try JSONDecoder().decode(LatestSignals.self, from: signalFixture)
        signals.contexts = try JSONDecoder().decode([SourcedSignalContext].self, from: Data(#"[{"source":"pi","context":{"key":"native","kind":"location","observedAt":"2026-01-02T00:00:00Z","location":{"latitude":1,"longitude":2,"type":"gps"}}},{"source":"smartcar","context":{"key":"connected","kind":"location","observedAt":"2026-01-01T00:00:00Z","location":{"latitude":3,"longitude":4,"type":"gps"}}}]"#.utf8))
        let staleSignals = try JSONEncoder().encode(signals)
        var expectedSignals = signals
        expectedSignals.contexts?.removeAll { $0.source == "pi" }
        var cache = GarageCache()
        cache.locations = ["vehicle": try JSONDecoder().decode(VehicleLocationEnvelope.self, from: piLocation)]
        cache.signals = ["vehicle": signals]
        let store = GarageStore(connection: fixture.connection, cache: cache, cacheURL: directory.appendingPathComponent("garage.json"), session: fixture.session)
        let started = expectation(description: "Old coordinate requests held"); started.expectedFulfillmentCount = 2
        let reported = expectation(description: "GPS deletion and refresh telemetry completed"); reported.expectedFulfillmentCount = 8
        fixture.configure(.hold, started: started, reported: reported)
        let oldLocation = Task { await store.refreshLocation("vehicle") }
        let oldSignals = Task { await store.refreshSignals("vehicle") }
        await fulfillment(of: [started], timeout: 3)
        fixture.configure(.routes([
            "/api/v1/vehicles/vehicle/location-history": Data(),
            "/api/v1/vehicles/vehicle/records": Data("[]".utf8),
            "/api/v1/vehicles/vehicle/reminders": Data("[]".utf8),
            "/api/v1/vehicles/vehicle/trips": Data("[]".utf8),
            "/api/v1/vehicles/vehicle/location": smartcarLocation,
            "/api/v1/vehicles/vehicle/signals/latest": try JSONEncoder().encode(expectedSignals)
        ]), reported: reported)
        try await store.clearLocationHistory("vehicle")
        fixture.replyHeld(route: "/api/v1/vehicles/vehicle/location", data: piLocation)
        fixture.replyHeld(route: "/api/v1/vehicles/vehicle/signals/latest", data: staleSignals)
        await oldLocation.value; await oldSignals.value
        await fulfillment(of: [reported], timeout: 3)
        XCTAssertEqual(store.cache.locations?["vehicle"]?.location?.source, "smartcar")
        XCTAssertEqual(store.signals("vehicle")?.contexts?.map(\.source), ["smartcar"])
        XCTAssertFalse(store.offline)
        let saved = try JSONDecoder().decode(GarageCache.self, from: Data(contentsOf: directory.appendingPathComponent("garage.json")))
        XCTAssertEqual(saved.locations?["vehicle"]?.location?.source, "smartcar")
        XCTAssertEqual(saved.signals?["vehicle"]?.contexts?.map(\.source), ["smartcar"])
    }

    @MainActor
    func testDeletingPiTripKeepsSmartcarPositionWhenRefreshFails() async throws {
        let fixture = HTTPFixture(); defer { fixture.close() }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let location = VehicleLocationEnvelope(location: VehicleLocation(latitude: 3, longitude: 4, recordedAt: "2026-01-01T00:00:00Z", source: "smartcar"))
        var trip = Trip(id: "trip", vehicleId: "vehicle", title: "Synthetic", startedAt: "2026-01-01T00:00:00Z", endedAt: "2026-01-01T00:01:00Z", distanceMiles: 1, points: [])
        trip.source = "pi-gps"
        var signals = try JSONDecoder().decode(LatestSignals.self, from: signalFixture)
        signals.contexts = try JSONDecoder().decode([SourcedSignalContext].self, from: Data(#"[{"source":"pi","context":{"key":"native","kind":"location","observedAt":"2026-01-01T00:00:00Z","location":{"latitude":1,"longitude":2,"type":"gps"}}},{"source":"smartcar","context":{"key":"connected","kind":"location","observedAt":"2026-01-01T00:00:00Z","location":{"latitude":3,"longitude":4,"type":"gps"}}}]"#.utf8))
        var cache = GarageCache(); cache.locations = ["vehicle": location]; cache.signals = ["vehicle": signals]
        cache.details["vehicle"] = VehicleDetail(trips: [trip])
        let store = GarageStore(connection: fixture.connection, cache: cache, cacheURL: directory.appendingPathComponent("garage.json"), session: fixture.session)
        let reported = expectation(description: "Trip deletion and failed refreshes reported"); reported.expectedFulfillmentCount = 3
        fixture.configure(.routes(["/api/v1/trips/trip": Data()]), reported: reported)
        try await store.deleteTrip(trip)
        await fulfillment(of: [reported], timeout: 3)
        XCTAssertTrue(store.detail("vehicle").trips.isEmpty)
        XCTAssertEqual(store.cache.locations?["vehicle"]?.location?.source, "smartcar")
        XCTAssertEqual(store.signals("vehicle")?.contexts?.map(\.source), ["smartcar"])
        XCTAssertNotNil(store.locationErrors["vehicle"]); XCTAssertNotNil(store.signalErrors["vehicle"])
        XCTAssertFalse(store.offline)
    }

    func testVehicleProfilePhotoRetryDoesNotReapplyAcknowledgedOdometer() throws {
        var live = try JSONDecoder().decode(Vehicle.self, from: Data(#"{"id":"vehicle","name":"Synthetic","make":"Example","model":"Truck","year":2020,"odometerMiles":100,"odometerStatus":"measured","vin":"VIN-A","licensePlate":"PLATE-A","createdAt":"2026-01-01T00:00:00Z"}"#.utf8))
        var acknowledged = VehicleProfileDraft(vehicle: live)
        var form = acknowledged
        form.odometerMiles = 120; form.licensePlate = "PLATE-B"
        let firstPatch = form.changes(since: acknowledged)
        XCTAssertEqual(Set(firstPatch.keys), ["odometerMiles", "licensePlate"])
        XCTAssertEqual(firstPatch["odometerMiles"] as? Double, 120)
        XCTAssertFalse(form.changes(since: acknowledged).isEmpty, "A failed metadata request must remain retryable before acknowledgment")

        acknowledged = form
        live.odometerMiles = 121.75; live.odometerStatus = "estimated"; live.name = "Changed elsewhere"
        XCTAssertFalse(form.changes(since: VehicleProfileDraft(vehicle: live)).isEmpty, "A live-cache baseline would reproduce the bug")
        XCTAssertTrue(form.changes(since: acknowledged).isEmpty, "Photo failure and new Pi distance must not resend the already acknowledged reading")

        form.vin = "VIN-B"
        XCTAssertEqual(Set(form.changes(since: acknowledged).keys), ["vin"])
        form.odometerMiles = 125
        XCTAssertEqual(form.changes(since: acknowledged)["odometerMiles"] as? Double, 125, "An explicit subsequent correction must still be sent")
    }

    func testVehicleProfilePreservesUntouchedConcurrentFieldsAndSupportsClearing() throws {
        let vehicle = try JSONDecoder().decode(Vehicle.self, from: Data(#"{"id":"vehicle","name":"Synthetic","make":"Example","model":"Truck","year":2020,"odometerMiles":100.125,"vin":"VIN-A","licensePlate":"PLATE-A","createdAt":"2026-01-01T00:00:00Z"}"#.utf8))
        let baseline = VehicleProfileDraft(vehicle: vehicle)
        var form = baseline
        form.name = " Renamed "
        form.licensePlate = "  "
        let patch = form.changes(since: baseline)
        XCTAssertEqual(Set(patch.keys), ["name", "licensePlate"])
        XCTAssertEqual(patch["name"] as? String, "Renamed")
        XCTAssertEqual(patch["licensePlate"] as? String, "")
        XCTAssertNil(patch["odometerMiles"]); XCTAssertNil(patch["odometerStatus"]); XCTAssertNil(patch["vin"])
        let create = form.changes(since: nil)
        XCTAssertEqual(create["odometerMiles"] as? Double, 100.125)
        XCTAssertEqual(create["year"] as? Int, 2020)
        XCTAssertTrue(form.changes(since: form).isEmpty, "After creation is acknowledged, a photo retry must not resend profile fields")
    }

    @MainActor
    func testVehicleDeletionRejectsLatePrivateHistoryAndErrorReplies() async throws {
        for status in [200, 503] {
            let fixture = HTTPFixture()
            defer { fixture.close() }
            let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
            defer { try? FileManager.default.removeItem(at: directory) }
            let vehicle = try JSONDecoder().decode(Vehicle.self, from: Data(#"{"id":"vehicle","name":"Synthetic","make":"","model":"","year":0,"odometerMiles":0,"createdAt":"2026-01-01T00:00:00Z"}"#.utf8))
            let trip = Trip(id: "trip", vehicleId: "vehicle", title: "Synthetic", startedAt: "2026-01-01T00:00:00Z", endedAt: "2026-01-01T00:01:00Z", distanceMiles: 1, points: [])
            var cache = GarageCache(vehicles: [vehicle])
            cache.details[vehicle.id] = VehicleDetail(records: [VehicleRecord(id: "record", vehicleId: vehicle.id, kind: .service, date: "2026-01-01", title: "Synthetic", notes: "", odometerMiles: 0, costCents: 0)], trips: [trip])
            let store = GarageStore(connection: fixture.connection, cache: cache, cacheURL: directory.appendingPathComponent("garage.json"), session: fixture.session)
            let started = expectation(description: "Pre-deletion requests started"); started.expectedFulfillmentCount = 8
            let reported = expectation(description: "Noncancelled request telemetry completed"); reported.expectedFulfillmentCount = 8
            fixture.configure(.hold, started: started, reported: reported)
            let garage = Task { await store.refresh() }
            let detail = Task { await store.refreshDetail(vehicle.id) }
            let location = Task { await store.refreshLocation(vehicle.id) }
            let signals = Task { await store.refreshSignals(vehicle.id) }
            let paging = Task { await store.loadMoreTrips(vehicle.id) }
            let history = Task { try await store.signalHistory(vehicleID: vehicle.id, metric: "rpm", statistic: "sample", days: 30) }
            await fulfillment(of: [started], timeout: 3)
            fixture.configure(.status(204), reported: reported)
            try await store.deleteVehicle(vehicle.id)
            await garage.value
            XCTAssertFalse(store.refreshing)

            fixture.replyHeld(route: "/api/v1/vehicles/vehicle/records", data: try JSONEncoder().encode(cache.details[vehicle.id]!.records))
            fixture.replyHeld(route: "/api/v1/vehicles/vehicle/reminders")
            // Finish the other detail children before failing trips so this tests stale errors, not sibling cancellation.
            let detailChildrenCompleted = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in fixture.events.count >= 3 }, object: nil)
            await fulfillment(of: [detailChildrenCompleted], timeout: 3)
            fixture.replyHeld(route: "/api/v1/vehicles/vehicle/trips", status: status, data: try JSONEncoder().encode([trip]))
            fixture.replyHeld(route: "/api/v1/vehicles/vehicle/location", status: status, data: Data(#"{"location":{"latitude":1,"longitude":2,"recordedAt":"2026-01-01T00:00:00Z","source":"pi-gps"}}"#.utf8))
            fixture.replyHeld(route: "/api/v1/vehicles/vehicle/signals/latest", status: status, data: signalFixture)
            fixture.replyHeld(route: "/api/v1/vehicles/vehicle/signals/history", status: status, data: Data(#"{"metric":"rpm","unit":"rpm","from":"2026-01-01T00:00:00Z","to":"2026-01-02T00:00:00Z","maxPoints":120,"series":[]}"#.utf8))
            await detail.value; await location.value; await signals.value; await paging.value
            do { _ = try await history.value; XCTFail("Deleted vehicle history must not be returned for caching") } catch { }
            await fulfillment(of: [reported], timeout: 3)
            XCTAssertTrue(store.vehicles.isEmpty)
            XCTAssertNil(store.cache.details[vehicle.id]); XCTAssertNil(store.cache.locations?[vehicle.id]); XCTAssertNil(store.signals(vehicle.id))
            XCTAssertNil(store.cachedSignalHistory(vehicleID: vehicle.id, metric: "rpm", statistic: "sample", days: 30))
            XCTAssertFalse(store.offline); XCTAssertNil(store.error); XCTAssertNil(store.locationErrors[vehicle.id]); XCTAssertNil(store.signalErrors[vehicle.id])
            let persisted = try JSONDecoder().decode(GarageCache.self, from: Data(contentsOf: directory.appendingPathComponent("garage.json")))
            XCTAssertTrue(persisted.vehicles.isEmpty); XCTAssertTrue(persisted.details.isEmpty)
        }
    }

    func testVehiclePhotoNormalizationBoundsPixelsAndStripsLocationMetadata() throws {
        let format = UIGraphicsImageRendererFormat(); format.scale = 1
        let image = UIGraphicsImageRenderer(size: CGSize(width: 2400, height: 800), format: format).image { context in
            UIColor.orange.setFill(); context.fill(CGRect(x: 0, y: 0, width: 2400, height: 800))
        }
        let input = NSMutableData()
        let destination = try XCTUnwrap(CGImageDestinationCreateWithData(input as CFMutableData, UTType.jpeg.identifier as CFString, 1, nil))
        CGImageDestinationAddImage(destination, try XCTUnwrap(image.cgImage), [
            kCGImagePropertyGPSDictionary: [kCGImagePropertyGPSLatitude: 1.0, kCGImagePropertyGPSLatitudeRef: "N", kCGImagePropertyGPSLongitude: 2.0, kCGImagePropertyGPSLongitudeRef: "E"],
            kCGImagePropertyTIFFDictionary: [kCGImagePropertyTIFFMake: "Synthetic private camera"]
        ] as CFDictionary)
        XCTAssertTrue(CGImageDestinationFinalize(destination))
        let original = try XCTUnwrap(CGImageSourceCreateWithData(input as CFData, nil))
        XCTAssertNotNil((CGImageSourceCopyPropertiesAtIndex(original, 0, nil) as? [CFString: Any])?[kCGImagePropertyGPSDictionary])
        let jpeg = try VehiclePhotoData.normalizedJPEG(input as Data)
        XCTAssertLessThanOrEqual(jpeg.count, VehiclePhotoData.maximumBytes)
        let source = try XCTUnwrap(CGImageSourceCreateWithData(jpeg as CFData, nil))
        XCTAssertEqual(CGImageSourceGetType(source) as String?, UTType.jpeg.identifier)
        let properties = try XCTUnwrap(CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any])
        XCTAssertEqual(properties[kCGImagePropertyPixelWidth] as? Int, 1600)
        XCTAssertLessThanOrEqual(try XCTUnwrap(properties[kCGImagePropertyPixelHeight] as? Int), 1600)
        XCTAssertNil(properties[kCGImagePropertyGPSDictionary])
        XCTAssertNil((properties[kCGImagePropertyTIFFDictionary] as? [CFString: Any])?[kCGImagePropertyTIFFMake])
        XCTAssertThrowsError(try VehiclePhotoData.normalizedJPEG(Data("not an image".utf8)))
        XCTAssertThrowsError(try VehiclePhotoData.normalizedJPEG(Data(repeating: 0, count: 32 * 1024 * 1024 + 1)))
    }

    func testStateValueLabelsPreserveUnknownAndMixedSemantics() throws {
        let labels = ["0": "Spark", "1": "Compression"]
        XCTAssertEqual(SignalFormat.value(0, unit: "boolean", labels: labels), "Spark")
        XCTAssertEqual(SignalFormat.value(1, unit: "boolean", labels: labels), "Compression")
        XCTAssertEqual(SignalFormat.value(0.5, unit: "boolean", labels: labels), "Unknown")
        XCTAssertEqual(SignalFormat.value(99, unit: "code", labels: ["2": "Closed loop"]), "Code 99")
        let series = try JSONDecoder().decode(SignalHistorySeries.self, from: Data(#"{"source":"pi","statistic":"sample","quality":"measured","points":[{"windowStart":"2026-01-01T00:00:00Z","windowEnd":"2026-01-01T00:01:00Z","minimum":0,"maximum":0,"first":0,"last":0,"mean":0,"count":1},{"windowStart":"2026-01-01T00:02:00Z","windowEnd":"2026-01-01T00:03:00Z","minimum":0,"maximum":1,"first":0,"last":1,"mean":0.5,"count":2}]}"#.utf8))
        let chart = SignalChartData(series: series, unit: "boolean", calendar: false, valueLabels: labels)
        XCTAssertEqual(chart.stateMarks.map(\.label), ["Spark", "Mixed"])
        XCTAssertTrue(chart.vertices.isEmpty)
    }

    func testGPSFixStalenessAndSourceDoNotImplyVehicleOffline() throws {
        let envelope = try JSONDecoder().decode(VehicleLocationEnvelope.self, from: Data(#"{"location":{"latitude":1,"longitude":2,"recordedAt":"2026-01-01T00:00:00Z","source":"pi-gps","satellites":8}}"#.utf8))
        let fix = try XCTUnwrap(envelope.location)
        let captured = try XCTUnwrap(SignalFormat.date(fix.recordedAt))
        XCTAssertFalse(fix.isStale(at: captured.addingTimeInterval(899)))
        XCTAssertTrue(fix.isStale(at: captured.addingTimeInterval(901)))
        XCTAssertNil(try JSONDecoder().decode(VehicleLocationEnvelope.self, from: Data(#"{"location":null}"#.utf8)).location)
        XCTAssertEqual(GPSStatus.label("disconnected"), "GPS receiver not connected")
        let points = [TripPoint(latitude: 1, longitude: 2, recordedAt: "2026-01-01T00:00:00Z"), TripPoint(latitude: 1, longitude: 2, recordedAt: "2026-01-01T00:02:01Z")]
        XCTAssertEqual(TripSegments.split(points, maximumGap: 120).count, 2)
        let oldDevice = try JSONDecoder().decode(VehicleDevice.self, from: Data(#"{"id":"device","vehicleId":"vehicle","name":"Synthetic Pi","createdAt":"2026-01-01T00:00:00Z","autoUpdate":true}"#.utf8))
        XCTAssertNil(oldDevice.gpsRecording); XCTAssertNil(oldDevice.gpsState)
        let oldDefinition = try JSONDecoder().decode(SignalDefinition.self, from: Data(#"{"metric":"rpm","label":"Engine speed","unit":"rpm","staleAfterSeconds":900}"#.utf8))
        XCTAssertNil(oldDefinition.description); XCTAssertNil(oldDefinition.interpretation); XCTAssertNil(oldDefinition.valueLabels)
    }

    @MainActor
    func testTripPagingUsesStableCursorAndDeduplicatesExistingTrips() async throws {
        let fixture = HTTPFixture(); defer { fixture.close() }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let trip = Trip(id: "b", vehicleId: "vehicle", title: "Synthetic", startedAt: "2026-01-01T00:00:00+01:00", endedAt: "2026-01-01T00:01:00+01:00", distanceMiles: 1, points: [])
        let older = Trip(id: "a", vehicleId: trip.vehicleId, title: trip.title, startedAt: trip.startedAt, endedAt: trip.endedAt, distanceMiles: trip.distanceMiles, points: [])
        var cache = GarageCache(); cache.details["vehicle"] = VehicleDetail(records: [], reminders: [], trips: [trip])
        let store = GarageStore(connection: fixture.connection, cache: cache, cacheURL: directory.appendingPathComponent("garage.json"), session: fixture.session)
        let reported = expectation(description: "Paging telemetry completed")
        fixture.configure(.json(try JSONEncoder().encode([trip, older])), reported: reported)
        await store.loadMoreTrips("vehicle")
        await fulfillment(of: [reported], timeout: 2)
        XCTAssertEqual(store.detail("vehicle").trips.map(\.id), ["b", "a"])
        XCTAssertEqual(store.tripsHaveMore["vehicle"], false)
        let query = try XCTUnwrap(URLComponents(url: try XCTUnwrap(fixture.requests.first), resolvingAgainstBaseURL: false)?.queryItems)
        XCTAssertEqual(query.first { $0.name == "before" }?.value, trip.startedAt)
        XCTAssertEqual(query.first { $0.name == "beforeId" }?.value, "b")
        XCTAssertEqual(query.first { $0.name == "limit" }?.value, "50")
    }

    @MainActor
    func testLocationFailurePreservesSavedFixWithoutMarkingGarageOffline() async throws {
        let fixture = HTTPFixture(); defer { fixture.close() }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        var cache = GarageCache()
        cache.locations = ["vehicle": VehicleLocationEnvelope(location: VehicleLocation(latitude: 1, longitude: 2, recordedAt: "2026-01-01T00:00:00Z", source: "pi-gps"))]
        cache.details["vehicle"] = VehicleDetail(records: [VehicleRecord(id: "record", vehicleId: "vehicle", kind: .service, date: "2026-01-01", title: "Synthetic service", notes: "", odometerMiles: 100, costCents: 0)])
        let store = GarageStore(connection: fixture.connection, cache: cache, cacheURL: directory.appendingPathComponent("garage.json"), session: fixture.session)
        let reported = expectation(description: "Location failure telemetry completed")
        fixture.configure(.failure(.timedOut), reported: reported)
        await store.refreshLocation("vehicle")
        await fulfillment(of: [reported], timeout: 2)
        XCTAssertFalse(store.offline); XCTAssertNil(store.error)
        XCTAssertNotNil(store.locationErrors["vehicle"])
        XCTAssertEqual(store.cache.locations?["vehicle"]?.location?.recordedAt, "2026-01-01T00:00:00Z")
        XCTAssertTrue(store.locationsRefreshing.isEmpty)
        let unavailableReported = expectation(description: "Old server location telemetry completed")
        fixture.configure(.status(404), reported: unavailableReported)
        await store.refreshLocation("vehicle")
        await fulfillment(of: [unavailableReported], timeout: 2)
        XCTAssertFalse(store.offline); XCTAssertNil(store.error)
        XCTAssertTrue(store.locationErrors["vehicle"]?.contains("Update the PitPilot server") == true)
        XCTAssertEqual(store.cache.locations?["vehicle"]?.location?.recordedAt, "2026-01-01T00:00:00Z")
        XCTAssertEqual(store.detail("vehicle").records.map(\.id), ["record"])
    }

    func testPhotoAndGPSOperationsUseBoundedTelemetryNames() {
        XCTAssertEqual(APIClient.operation(route: "vehicles/private-id/photo", method: "GET"), "vehicle.photo.get")
        XCTAssertEqual(APIClient.operation(route: "vehicles/private-id/photo", method: "PUT"), "vehicle.photo.put")
        XCTAssertEqual(APIClient.operation(route: "vehicles/private-id/photo", method: "DELETE"), "vehicle.photo.delete")
        XCTAssertEqual(APIClient.operation(route: "vehicles/private-id/location", method: "GET"), "vehicle.location.get")
        XCTAssertEqual(APIClient.operation(route: "vehicles/private-id/location-history", method: "DELETE"), "vehicle.location.delete")
    }

    func testAuthenticatedPhotoCacheIsPartitionedAndInvalidated() async throws {
        let first = HTTPFixture(), second = HTTPFixture()
        defer { first.close(); second.close() }
        var vehicle = try JSONDecoder().decode(Vehicle.self, from: Data(#"{"id":"synthetic-photo-vehicle","name":"Synthetic","make":"","model":"","year":0,"odometerMiles":0,"createdAt":"2026-01-01T00:00:00Z"}"#.utf8))
        vehicle.photoRevision = "synthetic-revision"
        let cache = VehiclePhotoCache()
        let jpeg = try XCTUnwrap(UIGraphicsImageRenderer(size: CGSize(width: 10, height: 10)).image { context in
            UIColor.orange.setFill(); context.fill(CGRect(x: 0, y: 0, width: 10, height: 10))
        }.jpegData(compressionQuality: 0.8))
        let firstReported = expectation(description: "First photo telemetry"); firstReported.expectedFulfillmentCount = 2
        let secondReported = expectation(description: "Other connection photo telemetry")
        first.configure(.json(jpeg), reported: firstReported); second.configure(.json(jpeg), reported: secondReported)
        do {
            let downloaded = try await cache.image(vehicle: vehicle, client: first.client)
            let saved = try await cache.image(vehicle: vehicle, client: first.client)
            XCTAssertEqual(downloaded, jpeg); XCTAssertEqual(saved, jpeg)
            XCTAssertEqual(first.requests.count, 1)
            _ = try await cache.image(vehicle: vehicle, client: second.client)
            XCTAssertEqual(second.requests.count, 1)
            try await cache.remove(vehicleID: vehicle.id, connection: first.connection)
            _ = try await cache.image(vehicle: vehicle, client: first.client)
            XCTAssertEqual(first.requests.count, 2)
            await fulfillment(of: [firstReported, secondReported], timeout: 2)
            XCTAssertEqual(first.requestHeaders.first?["Authorization"], "Bearer synthetic-test-token")
            XCTAssertEqual(first.requestHeaders.first?["Accept"], "image/jpeg")
        } catch {
            try? await cache.remove(vehicleID: vehicle.id, connection: first.connection)
            try? await cache.remove(vehicleID: vehicle.id, connection: second.connection)
            throw error
        }
        try await cache.remove(vehicleID: vehicle.id, connection: first.connection)
        try await cache.remove(vehicleID: vehicle.id, connection: second.connection)
    }

    func testHistoryUsesCaptureTimeAcrossOffsetsAndKeepsUnknownPrecision() throws {
        var a = VehicleRecord(id: "a", vehicleId: "v", kind: .odometer, date: "2026-10-08", title: "Z actual", notes: "", odometerMiles: 101, costCents: 0)
        a.recordedAt = "2026-10-08T23:30:00-04:00"
        var b = VehicleRecord(id: "b", vehicleId: "v", kind: .odometer, date: "2026-10-09", title: "A older", notes: "", odometerMiles: 100, costCents: 0)
        b.recordedAt = "2026-10-09T02:00:00Z"
        var unknown = VehicleRecord(id: "unknown", vehicleId: "v", kind: .odometer, date: "2026-10-08", title: "Unknown capture time", notes: "", odometerMiles: 100, costCents: 0)
        unknown.createdAt = "2026-10-09T03:00:00Z"
        for records in [[a,b,unknown],[unknown,b,a],[b,a,unknown]] {
            XCTAssertEqual(records.sorted(by: VehicleRecord.newestFirst).map(\.id), ["a","b","unknown"])
        }
        XCTAssertEqual(unknown.dateLabel, "2026-10-08")
        let draft = RecordDraft(record: unknown, mileage: 0)
        XCTAssertFalse(draft.includeTime)
        XCTAssertNil(try draft.values(original: unknown)["recordedAt"])
    }

    func testNewMeasuredReadingIncludesExplicitTimeAndKeepsOriginalTimestampOnEdit() throws {
        var draft = RecordDraft(record: nil, mileage: 101)
        draft.kind = .odometer; draft.title = "Dashboard reading"
        XCTAssertEqual(draft.odometerStatus, "measured")
        XCTAssertTrue(draft.includeTime)
        let body = try draft.values(original: nil)
        XCTAssertNotNil(body["recordedAt"])
        var saved = VehicleRecord(id: "r", vehicleId: "v", kind: .odometer, date: Input.date(draft.date), title: draft.title, notes: "", odometerMiles: 101, costCents: 0, odometerStatus: "measured")
        saved.recordedAt = body["recordedAt"] as? String
        var edit = RecordDraft(record: saved, mileage: 101)
        edit.title = "Corrected title"
        XCTAssertEqual(Set(try edit.values(original: saved).keys), ["title"])
    }

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
        XCTAssertGreaterThan(Set(daily.axisValues.map(daily.xLabel)).count, 1)
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
        XCTAssertEqual(signals.initialHistoryDays("fuel_level_pct", statistic: "all", now: now), 365)
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

extension PitPilotTests {
    private var integrationSession: SmartcarSession {
        SmartcarSession(sessionId: "synthetic-session", authorizationUrl: URL(string: "https://connect.smartcar.com/oauth/authorize?state=expected-state")!, callbackScheme: "sc00000000-0000-4000-8000-000000000000", expiresAt: "2099-01-01T00:00:00Z")
    }

    func testSmartcarCallbackAllowlistAndStateValidation() throws {
        let session = integrationSession
        let callback = URL(string: "\(session.callbackScheme)://callback?state=expected-state&user_id=synthetic-user&vehicle_id=synthetic-vehicle&external_id=synthetic-external&access_token=must-not-forward")!
        let body = try session.completion(callback)
        XCTAssertEqual(body, ["state": "expected-state", "userId": "synthetic-user", "vehicleId": "synthetic-vehicle", "externalId": "synthetic-external"])
        for suffix in ["state=wrong", "state=expected-state&state=expected-state", "state=expected-state&user_id=a&user_id=b", "state=expected-state&error=", "user_id=synthetic-user"] {
            XCTAssertThrowsError(try session.completion(URL(string: "\(session.callbackScheme)://callback?\(suffix)")!))
        }
        for callback in ["other://callback?state=expected-state", "\(session.callbackScheme)://evil?state=expected-state", "\(session.callbackScheme)://callback/other?state=expected-state", "\(session.callbackScheme)://callback?state=expected-state#fragment"] {
            XCTAssertThrowsError(try session.completion(URL(string: callback)!))
        }
        XCTAssertEqual(try session.completion(URL(string: "\(session.callbackScheme)://callback?state=expected-state&error=access_denied")!)["error"], "access_denied")
    }

    func testSmartcarSessionRejectsUntrustedOrExpiredAuthorization() throws {
        for url in ["http://connect.smartcar.com/oauth/authorize?state=x", "https://connect.smartcar.com.evil.test/oauth/authorize?state=x", "https://connect.smartcar.com:444/oauth/authorize?state=x", "https://user@connect.smartcar.com/oauth/authorize?state=x", "https://connect.smartcar.com/other?state=x", "https://connect.smartcar.com/oauth/authorize", "https://connect.smartcar.com/oauth/authorize?state=a&state=b"] {
            let session = SmartcarSession(sessionId: "test", authorizationUrl: URL(string: url)!, callbackScheme: integrationSession.callbackScheme, expiresAt: integrationSession.expiresAt)
            XCTAssertThrowsError(try session.validate(), url)
        }
        let expired = SmartcarSession(sessionId: "test", authorizationUrl: integrationSession.authorizationUrl, callbackScheme: integrationSession.callbackScheme, expiresAt: "2000-01-01T00:00:00Z")
        XCTAssertThrowsError(try expired.validate())
        let wrongScheme = SmartcarSession(sessionId: "test", authorizationUrl: integrationSession.authorizationUrl, callbackScheme: "https", expiresAt: integrationSession.expiresAt)
        XCTAssertThrowsError(try wrongScheme.validate())
    }

    func testPiContactStatusDoesNotInventConnectivity() throws {
        var device = VehicleDevice(id: "device", vehicleId: "vehicle", name: "Synthetic Pi", createdAt: "2026-01-01T00:00:00Z", autoUpdate: true)
        let now = try XCTUnwrap(SignalFormat.date("2026-01-02T12:00:00Z"))
        XCTAssertEqual(device.status(at: now), "Waiting for pairing")
        device.enrolledAt = "2026-01-01T00:00:00Z"
        XCTAssertEqual(device.status(at: now), "Waiting for first check-in")
        device.lastSeenAt = "2026-01-02T11:59:00Z"
        XCTAssertEqual(device.status(at: now), "Recently checked in")
        device.lastSeenAt = "2026-01-02T11:00:00Z"
        XCTAssertEqual(device.status(at: now), "Check-in overdue")
        device.lastSeenAt = "2026-01-03T12:00:00Z"
        XCTAssertEqual(device.status(at: now), "Check-in time unavailable")
        device.revokedAt = "2026-01-02T12:00:00Z"
        XCTAssertEqual(device.status(at: now), "Access revoked")
        XCTAssertEqual(IntegrationLabel.collection("queue_full"), "Upload queue full")
        XCTAssertEqual(IntegrationLabel.update("rolled_back"), "Previous version restored")
    }

    func testSmartcarStateActionsAndIntegrationTelemetryAreBounded() {
        for state in ["disconnected", "awaiting_authorization", "awaiting_selection", "reconnect_required", "unknown"] {
            XCTAssertFalse(SmartcarStatus(state: state).canSync)
        }
        XCTAssertTrue(SmartcarStatus(state: "provisioning").canSync)
        XCTAssertEqual(SmartcarStatus(state: "provisioning").title, "Waiting for vehicle data")
        XCTAssertEqual(SmartcarStatus(state: "temporary_error").title, "Update delayed")
        XCTAssertEqual(APIClient.operation(route: "vehicles/private-vehicle/devices", method: "POST"), "device.create")
        XCTAssertEqual(APIClient.operation(route: "devices/private-device", method: "DELETE"), "device.revoke")
        XCTAssertEqual(APIClient.operation(route: "vehicles/private-vehicle/smartcar/sessions/private-session/complete", method: "POST"), "smartcar.session.complete")
        XCTAssertEqual(APIClient.operation(route: "vehicles/private-vehicle/smartcar/sessions/private-session/bind", method: "POST"), "smartcar.session.bind")
        XCTAssertNil(APIClient.operation(route: "vehicles/private-vehicle/smartcar/sessions/private-session/secret", method: "POST"))
    }

    func testSmartcarUnavailableCapabilitiesDecodeWithoutInventingReadings() throws {
        for metrics in ["", ",\"supportedMetrics\":null", ",\"supportedMetrics\":[]"] {
            let data = Data("{\"state\":\"provisioning\",\"unavailableSignals\":3,\"unsupportedSignals\":0\(metrics)}".utf8)
            let status = try JSONDecoder().decode(SmartcarStatus.self, from: data)
            XCTAssertEqual(status.title, "Waiting for vehicle data")
            XCTAssertTrue(status.supportedMetrics.isEmpty)
            XCTAssertEqual(status.unavailableSignals, 3)
            XCTAssertNil(status.latestObservedAt)
            XCTAssertNil(status.lastSuccessAt)
        }
        let candidate = try JSONDecoder().decode(SmartcarCandidate.self, from: Data("{\"candidateId\":\"synthetic-candidate\",\"make\":\"\",\"model\":\"\",\"year\":0}".utf8))
        XCTAssertTrue(candidate.title.isEmpty)
    }

    @MainActor
    func testCancelledSmartcarBrowserIsNotAnIntegrationError() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let reported = expectation(description: "Session creation telemetry delivered")
        fixture.configure(.json(try APIClient.body(["sessionId": integrationSession.sessionId, "authorizationUrl": integrationSession.authorizationUrl.absoluteString, "callbackScheme": integrationSession.callbackScheme, "expiresAt": integrationSession.expiresAt])), reported: reported)
        let model = VehicleIntegrations(vehicleID: "synthetic-vehicle", client: fixture.client, browser: CancellingSmartcarBrowser())
        await model.connect(reconnect: false)
        await fulfillment(of: [reported], timeout: 2)
        XCTAssertNil(model.error)
        XCTAssertFalse(model.working)
        XCTAssertTrue(model.candidates.isEmpty)
        XCTAssertEqual(fixture.requests.count, 1)
    }

    @MainActor
    func testPairingTokenIsClearedWhenLeavingIntegrationScreen() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let reported = expectation(description: "Pairing telemetry delivered")
        fixture.configure(.json(try APIClient.body(["device": ["id": "synthetic-device", "vehicleId": "synthetic-vehicle", "name": "Synthetic Pi", "createdAt": "2026-01-01T00:00:00Z", "autoUpdate": true], "enrollmentToken": "synthetic-one-time-token", "expiresAt": "2099-01-01T00:00:00Z"])), reported: reported)
        let model = VehicleIntegrations(vehicleID: "synthetic-vehicle", client: fixture.client, browser: CancellingSmartcarBrowser())
        await model.pair(name: "Synthetic Pi")
        await fulfillment(of: [reported], timeout: 2)
        XCTAssertEqual(model.pairing?.enrollmentToken, "synthetic-one-time-token")
        XCTAssertEqual(model.devices.count, 1)
        model.leave()
        XCTAssertNil(model.pairing)
        XCTAssertEqual(model.devices.first?.status(at: .now), "Waiting for pairing")
    }

    @MainActor
    func testLeavingCancelsPendingPairingWithoutPublishingASecretOrError() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let started = expectation(description: "Pairing request started")
        let stopped = expectation(description: "Pairing transport cancelled")
        fixture.configure(.hold, started: started, stopped: stopped)
        let model = VehicleIntegrations(vehicleID: "synthetic-vehicle", client: fixture.client, browser: CancellingSmartcarBrowser())
        let request = Task { await model.pair(name: "Synthetic Pi") }
        await fulfillment(of: [started], timeout: 2)
        model.leave()
        await request.value
        await fulfillment(of: [stopped], timeout: 2)
        XCTAssertNil(model.pairing)
        XCTAssertNil(model.error)
        XCTAssertFalse(model.working)
        XCTAssertTrue(fixture.events.isEmpty)
    }

    @MainActor
    func testDeclinedSmartcarConsentRefreshesExistingBindingWithoutAnError() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let reported = expectation(description: "Session, completion and three refresh telemetry events delivered")
        reported.expectedFulfillmentCount = 5
        fixture.configure(.routes([
            "/api/v1/vehicles/synthetic-vehicle/smartcar/sessions": try APIClient.body(["sessionId": integrationSession.sessionId, "authorizationUrl": integrationSession.authorizationUrl.absoluteString, "callbackScheme": integrationSession.callbackScheme, "expiresAt": integrationSession.expiresAt]),
            "/api/v1/vehicles/synthetic-vehicle/smartcar/sessions/synthetic-session/complete": try APIClient.body(["state": "cancelled", "candidates": []]),
            "/api/v1/vehicles/synthetic-vehicle/devices": Data("[]".utf8),
            "/api/v1/integrations/smartcar": try APIClient.body(["configured": true, "mode": "simulated", "connectAvailable": true, "pollIntervalSeconds": 3600]),
            "/api/v1/vehicles/synthetic-vehicle/smartcar": try APIClient.body(["state": "connected", "connectionId": "existing-binding", "supportedMetrics": []])
        ]), reported: reported)
        let model = VehicleIntegrations(vehicleID: "synthetic-vehicle", client: fixture.client, browser: DecliningSmartcarBrowser())
        await model.connect(reconnect: true)
        await fulfillment(of: [reported], timeout: 2)
        XCTAssertNil(model.error)
        XCTAssertNil(model.message)
        XCTAssertTrue(model.candidates.isEmpty)
        XCTAssertEqual(model.smartcar?.connectionId, "existing-binding")
        XCTAssertEqual(model.smartcar?.state, "connected")
    }

    @MainActor
    func testDisabledSmartcarDoesNotRequestUnavailableStatusOrHidePiDevices() async throws {
        let fixture = HTTPFixture()
        defer { fixture.close() }
        let reported = expectation(description: "Device and configuration telemetry delivered")
        reported.expectedFulfillmentCount = 2
        fixture.configure(.routes([
            "/api/v1/vehicles/synthetic-vehicle/devices": Data("[{\"id\":\"synthetic-device\",\"vehicleId\":\"synthetic-vehicle\",\"name\":\"Synthetic Pi\",\"createdAt\":\"2026-01-01T00:00:00Z\",\"autoUpdate\":true}]".utf8),
            "/api/v1/integrations/smartcar": Data("{\"configured\":false,\"connectAvailable\":false,\"pollIntervalSeconds\":0}".utf8)
        ]), reported: reported)
        let model = VehicleIntegrations(vehicleID: "synthetic-vehicle", client: fixture.client, browser: CancellingSmartcarBrowser())
        await model.refresh()
        await fulfillment(of: [reported], timeout: 2)
        XCTAssertTrue(model.loaded)
        XCTAssertNil(model.error)
        XCTAssertNil(model.smartcar)
        XCTAssertEqual(model.configuration?.configured, false)
        XCTAssertNil(model.configuration?.mode)
        XCTAssertEqual(model.devices.first?.name, "Synthetic Pi")
        XCTAssertEqual(fixture.requests.count, 2)
        XCTAssertFalse(fixture.requests.contains { $0.path.hasSuffix("/synthetic-vehicle/smartcar") })
    }
}

@MainActor
private final class CancellingSmartcarBrowser: SmartcarAuthenticating {
    func authenticate(_ session: SmartcarSession) async throws -> URL { throw CancellationError() }
    func cancel() {}
}

@MainActor
private final class DecliningSmartcarBrowser: SmartcarAuthenticating {
    func authenticate(_ session: SmartcarSession) async throws -> URL {
        URL(string: "\(session.callbackScheme)://callback?state=expected-state&error=access_denied")!
    }
    func cancel() {}
}

private final class HTTPFixture {
    enum Response { case hold, success, failure(URLError.Code), json(Data), routes([String: Data]), status(Int) }
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
    private var recordedHeaders: [[String: String]] = []
    private var held: [URLProtocol] = []
    var events: [[String: Any]] { lock.lock(); defer { lock.unlock() }; return recordedEvents }
    var requests: [URL] { lock.lock(); defer { lock.unlock() }; return recordedRequests }
    var requestHeaders: [[String: String]] { lock.lock(); defer { lock.unlock() }; return recordedHeaders }

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

    func replyHeld(route: String, status: Int = 200, data: Data = Data("[]".utf8)) {
        lock.lock()
        let matching = held.filter { $0.request.url?.path == route }
        held.removeAll { $0.request.url?.path == route }
        lock.unlock()
        for transport in matching { respond(transport, status: status, data: data) }
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
        defer { started?.fulfill() }
        if let url = transport.request.url {
            lock.lock(); recordedRequests.append(url); recordedHeaders.append(transport.request.allHTTPHeaderFields ?? [:]); lock.unlock()
        }
        switch response {
        case .hold: lock.lock(); held.append(transport); lock.unlock()
        case .success: respond(transport, status: 200)
        case .status(let code): respond(transport, status: code)
        case .json(let data): respond(transport, status: 200, data: data)
        case .routes(let data):
            if let body = data[transport.request.url!.path] { respond(transport, status: 200, data: body) }
            else { respond(transport, status: 404) }
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
