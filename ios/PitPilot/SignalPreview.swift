import SwiftUI
import Charts

actor SignalPreviewGate {
    static let shared = SignalPreviewGate()
    private var active = 0
    private var waiting: [CheckedContinuation<Void, Never>] = []
    func acquire() async {
        if active < 3 { active += 1; return }
        await withCheckedContinuation { waiting.append($0) }
    }
    func release() {
        if waiting.isEmpty { active -= 1 } else { waiting.removeFirst().resume() }
    }
}

struct SignalPreview: View {
    @EnvironmentObject private var store: GarageStore
    let vehicleID: String
    let metric: String
    let latest: LatestSignals
    @State private var history: SignalHistory?
    @State private var loading = false
    @State private var failed = false
    @State private var requestID = UUID()
    private var reading: LatestSignal? { latest.readings(metric).first }
    private var days: Int { latest.initialHistoryDays(metric, statistic: reading?.statistic ?? "sample") }
    private var requestKey: String { "\(vehicleID)/\(reading?.id ?? metric)/\(reading?.latest.key ?? "")/\(days)" }
    private var series: SignalHistorySeries? {
        history?.series.first { $0.source == reading?.source && $0.statistic == reading?.statistic && $0.quality == reading?.quality }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let series, let history {
                let data = SignalChartData(series: series, unit: history.unit, calendar: reading?.latest.calendarDate != nil, valueLabels: latest.definition(metric)?.valueLabels)
                if !data.buckets.isEmpty {
                    SignalMiniChart(data: data, unit: history.unit, estimated: series.quality == "estimated")
                        .accessibilityLabel("\(latest.label(metric)) history preview")
                        .accessibilityIdentifier("preview-\(metric)")
                    Text("\(days == 365 ? "1 year" : "\(days) days") · \(data.calendar ? "daily readings" : "recorded readings")")
                        .font(.caption2).foregroundStyle(.secondary)
                } else { placeholder("No history in this range") }
            } else { placeholder(loading ? "Loading trend…" : failed ? "History unavailable" : "No history yet") }
        }
        .task(id: requestKey) {
            guard let reading else { return }
            let currentRequest = UUID()
            requestID = currentRequest
            history = store.cachedSignalHistory(vehicleID: vehicleID, metric: metric, statistic: reading.statistic, days: days)
            guard !store.offline else { return }
            loading = true; failed = false
            await SignalPreviewGate.shared.acquire()
            do {
                try Task.checkCancellation()
                let loaded = try await store.signalHistory(vehicleID: vehicleID, metric: metric, statistic: reading.statistic, days: days)
                try Task.checkCancellation()
                if requestID == currentRequest { history = loaded }
            } catch is CancellationError { }
            catch { if requestID == currentRequest { failed = true } }
            await SignalPreviewGate.shared.release()
            if requestID == currentRequest { loading = false }
        }
    }

    private func placeholder(_ title: String) -> some View {
        Text(title).font(.caption).foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, minHeight: 66, alignment: .leading)
    }
}

struct SignalMiniChart: View {
    let data: SignalChartData
    let unit: String
    let estimated: Bool
    private var tint: Color { estimated ? .cyan : PitStyle.amber }

    var body: some View {
        Group {
            if data.kind == .state || data.kind == .category {
                Chart(data.stateMarks) { mark in
                    PointMark(x: .value("Time", mark.start), y: .value("State", mark.label))
                        .foregroundStyle(mark.label == "Mixed" || mark.label == "Unknown" ? .gray : tint)
                        .symbolSize(18)
                }
                .chartYAxis { AxisMarks(position: .leading) { AxisValueLabel() } }
            } else {
                Chart {
                    ForEach(data.vertices) { vertex in
                        AreaMark(x: .value("Time", vertex.x), yStart: .value("Baseline", data.yDomain(unit: unit).lowerBound), yEnd: .value("Value", vertex.value), series: .value("Segment", vertex.segment))
                            .foregroundStyle(LinearGradient(colors: [tint.opacity(0.3), tint.opacity(0.02)], startPoint: .top, endPoint: .bottom))
                        LineMark(x: .value("Time", vertex.x), y: .value("Value", vertex.value), series: .value("Segment", vertex.segment))
                            .foregroundStyle(tint).lineStyle(StrokeStyle(lineWidth: 2, lineCap: .round))
                        PointMark(x: .value("Time", vertex.x), y: .value("Value", vertex.value)).foregroundStyle(tint).symbolSize(data.vertices.count == 1 ? 24 : 4)
                    }
                    if data.kind == .bars {
                        ForEach(data.buckets) { bucket in
                            let bounds = data.barBounds(bucket)
                            RectangleMark(xStart: .value("Start", bounds.lowerBound), xEnd: .value("End", bounds.upperBound), yStart: .value("Baseline", 0.0), yEnd: .value("Value", bucket.point.mean))
                                .foregroundStyle(tint.gradient)
                        }
                    }
                }.chartYScale(domain: data.yDomain(unit: unit)).chartYAxis(.hidden)
            }
        }
        .chartXScale(domain: data.xDomain).chartXAxis(.hidden)
        .chartPlotStyle { $0.clipped() }.frame(height: 66)
        .allowsHitTesting(false).accessibilityElement(children: .ignore)
        .accessibilityValue("\(data.buckets.count) recorded periods. Open the reading for values and dates.")
    }
}
