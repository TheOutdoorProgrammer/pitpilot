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
        XCTAssertNil(APIClient.operation(route: "client-events", method: "POST"))
        XCTAssertNil(APIClient.operation(route: "unknown/private-id", method: "GET"))
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
}
