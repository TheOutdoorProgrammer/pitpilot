import XCTest
@testable import PitPilot

final class SignalAggregationTests: XCTestCase {
    private let base = Date(timeIntervalSince1970: 1_800_000_000)
    private func stamp(_ seconds: Double) -> String { ISO8601DateFormatter().string(from: base.addingTimeInterval(seconds)) }

    private func point(_ offset: Double, index: Int, gap: Double = 5) -> SignalHistoryPoint {
        SignalHistoryPoint(bucketStart: stamp(offset), bucketEnd: stamp(offset + 1800),
            windowStart: stamp(offset), windowEnd: stamp(offset + 1795),
            minimum: 0, maximum: 110, mean: 58, first: 0, last: 12, count: 360,
            firstObservedAt: stamp(offset), lastObservedAt: stamp(offset + 1795),
            minimumObservedAt: stamp(offset), maximumObservedAt: stamp(offset + 900),
            maxGapSeconds: gap, bucketIndex: index)
    }

    private func history(_ points: [SignalHistoryPoint], unit: String = "km/h") -> SignalHistory {
        SignalHistory(metric: "speed_kph", unit: unit, from: stamp(0), to: stamp(30 * 86400), maxPoints: 48,
            series: [SignalHistorySeries(source: "pi", quality: "measured", statistic: "sample", points: points)])
    }

    func testAveragesUseOneValuePerTimeBucketAndKeepRecordedPeaksAvailable() throws {
        let source = history([point(0, index: 0), point(1800, index: 1), point(86400, index: 2)])
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        let before = try encoder.encode(source)
        let average = SignalChartData(history: source, statistic: "trend", presentation: .averaged)
        let readings = SignalChartData(history: source, statistic: "trend", presentation: .readings)
        XCTAssertEqual(average.vertices.map(\.value), [58, 58, 58])
        XCTAssertEqual(average.vertices[0].segment, average.vertices[1].segment)
        XCTAssertNotEqual(average.vertices[1].segment, average.vertices[2].segment)
        XCTAssertTrue(average.sparseVertices.isEmpty)
        XCTAssertEqual(average.averagingIntervalLabel, "30-minute")
        XCTAssertEqual(readings.vertices.filter { $0.value == 110 }.count, 3)
        XCTAssertTrue(readings.vertices.contains { $0.value == 0 })
        XCTAssertLessThan(average.yDomain(unit: "km/h").upperBound, 110)
        XCTAssertGreaterThan(readings.yDomain(unit: "km/h").upperBound, 110)
        XCTAssertEqual(average.buckets.reduce(0) { $0 + $1.point.count }, 1080)
        XCTAssertEqual(average.buckets.first?.point.maximum, 110)
        XCTAssertEqual(try encoder.encode(source), before)
    }

    func testCoarseAverageAndUnknownOfflineCoverageDoNotShadeDriveGaps() {
        let points = [point(0, index: 0, gap: 1200), point(1800, index: 1)]
        let data = SignalChartData(history: history(points), statistic: "trend", presentation: .averaged)
        XCTAssertEqual(data.isolatedVertexIDs.count, 2)
        var legacy = point(0, index: 0)
        legacy.maxGapSeconds = nil
        let oldCache = SignalChartData(history: history([legacy, points[1]]), statistic: "trend", presentation: .averaged)
        XCTAssertEqual(oldCache.isolatedVertexIDs.count, 2)
    }

    func testPreviewBudgetStaysTenAndImperialAveragesUseConvertedValues() {
        let points = (0..<10).map { point(Double($0) * 1800, index: $0) }
        let source = history(points)
        let data = SignalChartData(history: source.displayed(in: .imperial), statistic: "sample", presentation: .averaged)
        XCTAssertEqual(data.vertices.count, 10)
        XCTAssertEqual(data.vertices[0].value, 58 / 1.609344, accuracy: 0.00001)
        XCTAssertEqual(data.buckets[0].point.maximum, 110 / 1.609344, accuracy: 0.00001)
        XCTAssertEqual(source.series[0].points[0].mean, 58)
    }

    func testBooleanAndCategoricalMeansNeverBecomeFractionalStates() {
        let mixed = SignalHistoryPoint(bucketStart: stamp(0), bucketEnd: stamp(60), windowStart: stamp(0), windowEnd: stamp(55),
            minimum: 0, maximum: 1, mean: 0.4, first: 0, last: 1, count: 12,
            firstObservedAt: stamp(0), lastObservedAt: stamp(55), maxGapSeconds: 5)
        for unit in ["boolean", "code"] {
            let data = SignalChartData(history: history([mixed], unit: unit), statistic: "trend", presentation: .averaged)
            XCTAssertTrue(data.vertices.isEmpty)
            XCTAssertEqual(data.stateMarks.map(\.label), ["Mixed"])
            XCTAssertFalse(data.stateMarks.contains { $0.label.contains("0.4") })
        }
    }
}
