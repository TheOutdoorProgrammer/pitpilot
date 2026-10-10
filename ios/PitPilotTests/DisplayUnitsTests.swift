import XCTest
@testable import PitPilot

final class DisplayUnitsTests: XCTestCase {
    func testSmallSignedPressureReadingsDoNotRoundToZero() {
        for pascals in [-1.0, 1.0] {
            let expected = (pascals * 0.000145).formatted(.number.precision(.fractionLength(0...6))) + " psi"
            let display = DisplayUnits.imperial.formatted(pascals, unit: "Pa")
            XCTAssertEqual(display, expected)
            XCTAssertNotEqual(display, DisplayUnits.imperial.formatted(0, unit: "Pa"))
            let projected = SignalHistoryPoint(minimum: pascals, maximum: pascals, mean: pascals,
                first: pascals, last: pascals, count: 1).displayed(in: .imperial, unit: "Pa")
            XCTAssertEqual(SignalFormat.value(projected.mean, unit: "psi"), expected)
            XCTAssertEqual(projected.count, 1)
        }
        XCTAssertEqual(DisplayUnits.imperial.formatted(39, unit: "kPa"),
                       5.66.formatted(.number.precision(.fractionLength(0...2))) + " psi")
        XCTAssertEqual(SignalFormat.value(0, unit: "psi"), "0 psi")
        XCTAssertEqual(SignalFormat.value(1, unit: "count"), "1")
        XCTAssertEqual(SignalFormat.value(0.001, unit: "boolean"), "Unknown")
        XCTAssertEqual(SignalFormat.value(0.001, unit: "code"), "Unknown code")
    }

    func testImperialConversionsUseUSVolumeAndPreserveTemperatureOffsets() {
        let cases: [(Double, String, Double, String)] = [
            (0, "°C", 32, "°F"), (100, "°C", 212, "°F"), (-40, "celsius", -40, "°F"),
            (160.9344, "km", 100, "mi"), (96.56064, "km/h", 60, "mph"),
            (30.48, "m", 100, "ft"), (206.8427187950508, "kPa", 30, "psi"),
            (-6894.757293168361, "Pa", -1, "psi"),
            (37.85411784, "L", 10, "US gal"), (7.570823568, "L/h", 2, "US gal/h"),
            (7.559872833333333, "g/s", 1, "lb/min")
        ]
        for (value, unit, expected, expectedUnit) in cases {
            XCTAssertEqual(DisplayUnits.imperial.value(value, unit: unit), expected, accuracy: 0.0000001, unit)
            XCTAssertEqual(DisplayUnits.imperial.unit(unit), expectedUnit)
        }
    }

    func testMetricNormalizesImperialSourcesAndBothModesRoundTrip() {
        let cases: [(Double, String, Double, String)] = [
            (212, "fahrenheit", 100, "°C"), (32, "°F", 0, "°C"),
            (1, "miles", 1.609344, "km"), (60, "mph", 96.56064, "km/h"),
            (10, "ft", 3.048, "m"), (1, "psi", 6.894757293168361, "kPa"),
            (1, "gal", 3.785411784, "L"), (2, "US gal/h", 7.570823568, "L/h"),
            (1, "lb/min", 7.559872833333333, "g/s")
        ]
        for (value, unit, expected, expectedUnit) in cases {
            let metricValue = DisplayUnits.metric.value(value, unit: unit)
            XCTAssertEqual(metricValue, expected, accuracy: 0.0000001, unit)
            XCTAssertEqual(DisplayUnits.metric.unit(unit), expectedUnit)
            XCTAssertEqual(DisplayUnits.imperial.value(metricValue, unit: expectedUnit), value, accuracy: 0.0000001, unit)
        }
    }

    func testStatesCountsUnknownAndDimensionlessValuesStayUnchanged() {
        for mode in DisplayUnits.allCases {
            for unit in ["boolean", "code", "count", "%", "percent", "rpm", "V", "s", "min", "deg", "ratio", "custom-unit", ""] {
                XCTAssertEqual(mode.unit(unit), unit)
                XCTAssertEqual(mode.value(1, unit: unit), 1)
                XCTAssertEqual(mode.value(-2.5, unit: unit), -2.5)
            }
            XCTAssertEqual(mode.formatted(1, unit: "boolean", labels: ["1": "Ready"]), "Ready")
            XCTAssertEqual(mode.formatted(0, unit: "boolean"), "Off")
            XCTAssertEqual(mode.formatted(0.5, unit: "boolean"), "Unknown")
            XCTAssertEqual(mode.formatted(2, unit: "code", labels: ["2": "Closed loop"]), "Closed loop")
        }
    }

    func testProjectionConvertsEveryBucketValueWithoutMutatingWireData() throws {
        let original = history(unit: "°C", series: [
            SignalHistorySeries(source: "pi", quality: "measured", statistic: "sample", points: [point()]),
            SignalHistorySeries(source: "smartcar", quality: "estimated", statistic: "mean", points: [point()], unit: "°C")
        ])
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        let before = try encoder.encode(original)
        let display = original.displayed(in: .imperial)
        XCTAssertEqual(display.unit, "°F")
        XCTAssertEqual(display.metric, original.metric)
        XCTAssertEqual(display.from, original.from)
        XCTAssertEqual(display.to, original.to)
        XCTAssertEqual(display.maxPoints, original.maxPoints)
        for series in display.series {
            let value = try XCTUnwrap(series.points.first)
            XCTAssertEqual(series.unit, "°F")
            XCTAssertEqual(value.minimum, 32)
            XCTAssertEqual(value.maximum, 212)
            XCTAssertEqual(value.mean, 122)
            XCTAssertEqual(value.first, 68)
            XCTAssertEqual(value.last, 176)
            XCTAssertEqual(value.count, 7)
            XCTAssertEqual(value.bucketStart, point().bucketStart)
            XCTAssertEqual(value.bucketEnd, point().bucketEnd)
            XCTAssertEqual(value.firstObservedAt, point().firstObservedAt)
            XCTAssertEqual(value.lastObservedAt, point().lastObservedAt)
        }
        XCTAssertEqual(display.series.map(\.id), original.series.map(\.id))
        XCTAssertEqual(try encoder.encode(original), before)
        XCTAssertEqual(try encoder.encode(display.displayed(in: .imperial)), try encoder.encode(display))
        let restored = display.displayed(in: .metric)
        XCTAssertEqual(restored.series[0].points[0].mean, original.series[0].points[0].mean, accuracy: 0.0000001)
        XCTAssertEqual(restored.series[0].points[0].first, original.series[0].points[0].first, accuracy: 0.0000001)
    }

    func testChartsUseConvertedExtremaVerticesDomainsAndKeepCounts() throws {
        let raw = history(unit: "°C", series: [
            SignalHistorySeries(source: "pi", quality: "measured", statistic: "sample", points: [point()]),
            SignalHistorySeries(source: "pi", quality: "derived", statistic: "count", points: [point()])
        ])
        let display = raw.displayed(in: .imperial)
        let chart = SignalChartData(history: display, statistic: "trend")
        XCTAssertEqual(chart.kind, .trend)
        XCTAssertEqual(chart.vertices.map(\.value), [68, 176])
        XCTAssertEqual(chart.buckets.first?.point.minimum, 32)
        XCTAssertEqual(chart.buckets.first?.point.maximum, 212)
        XCTAssertTrue(chart.yDomain(unit: display.unit).contains(32))
        XCTAssertTrue(chart.yDomain(unit: display.unit).contains(212))
        XCTAssertGreaterThan(chart.yDomain(unit: display.unit).lowerBound, 0)
        let count = SignalChartData(history: display, statistic: "count")
        XCTAssertEqual(count.kind, .bars)
        XCTAssertEqual(display.displayedUnit("count"), "count")
        XCTAssertEqual(display.series[1].unit, "count")
        XCTAssertEqual(count.buckets.first?.point.maximum, 100)
        XCTAssertEqual(count.buckets.first?.point.mean, 50)
        XCTAssertEqual(count.buckets.first?.point.count, 7)
    }

    func testExplicitSeriesUnitsAndCalendarMetadataSurviveProjection() throws {
        var day = point()
        day.calendarDate = "2026-01-01"
        day.timezone = "unknown"
        let raw = history(unit: "km", series: [
            SignalHistorySeries(source: "smartcar", quality: "measured", statistic: "snapshot", points: [day], unit: "mi")
        ])
        let display = raw.displayed(in: .imperial)
        XCTAssertEqual(display.series[0].points[0].mean, 50)
        XCTAssertEqual(display.series[0].points[0].calendarDate, day.calendarDate)
        XCTAssertEqual(display.series[0].points[0].timezone, "unknown")
        let chart = SignalChartData(history: display, statistic: "trend")
        XCTAssertTrue(chart.hasCalendarDays)
        XCTAssertEqual(chart.buckets.first?.point.mean, 50)
    }

    func testMissingReadingsStayMissingAndStateChartSemanticsSurvive() throws {
        XCTAssertTrue(history(unit: "km", series: []).displayed(in: .imperial).series.isEmpty)
        let statePoint = SignalHistoryPoint(windowStart: "2026-01-01T00:00:00Z", windowEnd: "2026-01-01T00:01:00Z",
            minimum: 0, maximum: 1, mean: 0.5, first: 0, last: 1, count: 2)
        let states = history(unit: "boolean", series: [SignalHistorySeries(source: "pi", quality: "measured", statistic: "sample", points: [statePoint])])
        let chart = SignalChartData(history: states.displayed(in: .imperial), statistic: "trend", valueLabels: ["0": "Not ready", "1": "Ready"])
        XCTAssertEqual(chart.kind, .state)
        XCTAssertEqual(chart.buckets.first?.state, "Mixed")
        XCTAssertTrue(chart.stateMarks.contains { $0.label == "Mixed" })
    }

    func testLatestCountsNeverAcquirePhysicalUnitsOrStateLabels() {
        let reading = LatestSignal(metric: "coolant_c", unit: "°C", source: "pi", statistic: "count", quality: "derived",
            latest: SignalObservation(key: "count", metric: "coolant_c", unit: "count", statistic: "count", quality: "derived", value: 1), stale: false)
        XCTAssertEqual(reading.formattedValue(in: .imperial, labels: ["1": "Ready"]), "1")
        XCTAssertEqual(DisplayUnits.imperial.formatted(39, unit: "kPa"), SignalFormat.value(5.656471771478159, unit: "psi"))
        XCTAssertEqual(DisplayUnits.imperial.explanation("Reported in liters per hour.", unit: "L/h"), "Reported in US gallons per hour.")
        XCTAssertEqual(DisplayUnits.imperial.explanation("The unit is grams per second, not GPS location.", unit: "g/s"), "The unit is pounds per minute, not GPS location.")
    }

    private func point() -> SignalHistoryPoint {
        SignalHistoryPoint(bucketStart: "2026-01-01T00:00:00Z", bucketEnd: "2026-01-01T00:01:00Z",
            windowStart: "2026-01-01T00:00:00Z", windowEnd: "2026-01-01T00:01:00Z",
            minimum: 0, maximum: 100, mean: 50, first: 20, last: 80, count: 7,
            firstObservedAt: "2026-01-01T00:00:00Z", lastObservedAt: "2026-01-01T00:01:00Z")
    }

    private func history(unit: String, series: [SignalHistorySeries]) -> SignalHistory {
        SignalHistory(metric: "synthetic", unit: unit, from: "2026-01-01T00:00:00Z", to: "2026-01-02T00:00:00Z", maxPoints: 120, series: series)
    }
}
