import SwiftUI
import Charts

struct SignalPreview: View {
    @EnvironmentObject private var store: GarageStore
    @AppStorage(DisplayUnits.preferenceKey) private var displayUnits: DisplayUnits = .metric
    let vehicleID: String
    let metric: String
    let latest: LatestSignals

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let preview = store.signalPreview(vehicleID: vehicleID, metric: metric) {
                let displayed = preview.history.displayed(in: displayUnits)
                let statistic = displayed.series.first?.statistic ?? "sample"
                let data = SignalChartData(history: displayed, statistic: statistic, valueLabels: latest.definition(metric)?.valueLabels, presentation: .averaged)
                if !data.buckets.isEmpty {
                    SignalMiniChart(data: data, unit: displayed.displayedUnit(statistic), estimated: displayed.series.first?.quality == "estimated", useLastValue: preview.aggregation == "last")
                        .accessibilityLabel("\(latest.label(metric)) history preview")
                        .accessibilityIdentifier("preview-\(metric)")
                    Text("\(preview.scope == "latest_session" ? "Latest session" : "Recent trend")\(preview.truncated ? " · latest portion" : "") · \(preview.aggregation == "sample_mean" ? "averages" : "states / values")\(store.offline || store.previewErrors[vehicleID] != nil ? " · saved" : "")")
                        .font(.caption2).foregroundStyle(.secondary)
                    if let source = displayed.series.first?.source {
                        Text(SignalFormat.source(source)).font(.caption2).foregroundStyle(.secondary)
                    }
                } else { placeholder("No history in this range") }
            } else { placeholder(store.previewsRefreshing.contains(vehicleID) ? "Loading trend…" : store.previewErrors[vehicleID] != nil ? "History unavailable" : "No history yet") }
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
    var useLastValue = false
    private var tint: Color { estimated ? .cyan : PitStyle.amber }

    var body: some View {
        let isolated = data.isolatedVertexIDs
        Group {
            if data.kind == .state || data.kind == .category {
                Chart(data.buckets) { bucket in
                    let rawLabel = data.kind == .state ? bucket.state : bucket.category
                    let label = rawLabel == "Mixed" || rawLabel == "Unknown" ? rawLabel : SignalFormat.value(bucket.point.last, unit: unit, labels: data.valueLabels)
                    PointMark(x: .value("Time", bucket.center), y: .value("State", label))
                        .foregroundStyle(label == "Mixed" || label == "Unknown" ? .gray : tint)
                        .symbolSize(18)
                }
                .chartYAxis { AxisMarks(position: .leading) { AxisValueLabel() } }
            } else {
                Chart {
                    ForEach(data.sparseVertices) { vertex in
                        LineMark(x: .value("Time", vertex.x), y: .value("Value", vertex.value), series: .value("Segment", vertex.segment))
                            .foregroundStyle(tint.opacity(0.5)).lineStyle(StrokeStyle(lineWidth: 1, dash: [2, 3]))
                    }
                    ForEach(data.vertices) { vertex in
                        AreaMark(x: .value("Time", vertex.x), yStart: .value("Baseline", data.yDomain(unit: unit).lowerBound), yEnd: .value("Value", vertex.value), series: .value("Segment", vertex.segment))
                            .foregroundStyle(LinearGradient(colors: [(vertex.estimated ? Color.cyan : PitStyle.amber).opacity(0.3), tint.opacity(0.02)], startPoint: .top, endPoint: .bottom))
                        LineMark(x: .value("Time", vertex.x), y: .value("Value", vertex.value), series: .value("Segment", vertex.segment))
                            .foregroundStyle(vertex.estimated ? .cyan : PitStyle.amber).lineStyle(StrokeStyle(lineWidth: 2, lineCap: .round))
                        if data.vertices.count <= 30 || vertex.calendarDay || isolated.contains(vertex.id) {
                            PointMark(x: .value("Time", vertex.x), y: .value("Value", vertex.value))
                                .foregroundStyle(vertex.estimated ? .cyan : PitStyle.amber).symbolSize(isolated.contains(vertex.id) ? 16 : 4)
                                .symbol(vertex.calendarDay ? BasicChartSymbolShape.square : BasicChartSymbolShape.circle)
                        }
                    }
                    if data.kind == .bars {
                        ForEach(data.buckets) { bucket in
                            let bounds = data.barBounds(bucket)
                            RectangleMark(xStart: .value("Start", bounds.lowerBound), xEnd: .value("End", bounds.upperBound), yStart: .value("Baseline", 0.0), yEnd: .value("Value", useLastValue ? bucket.point.last : bucket.point.mean))
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
