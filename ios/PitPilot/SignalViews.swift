import SwiftUI
import Charts

struct VehicleSignalsView: View {
    @EnvironmentObject private var store: GarageStore
    let vehicleID: String
    private var latest: LatestSignals? { store.signals(vehicleID) }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack {
                Text("Under the hood").font(.title2.weight(.bold))
                Spacer()
                if store.signalsRefreshing.contains(vehicleID) { ProgressView().accessibilityLabel("Refreshing readings") }
            }
            if let error = store.signalErrors[vehicleID] {
                Label("Readings could not refresh. \(error)", systemImage: "wifi.exclamationmark")
                    .font(.callout).foregroundStyle(.orange)
                Button("Retry readings") { Task { await store.refreshSignals(vehicleID) } }
                    .disabled(store.signalsRefreshing.contains(vehicleID))
            }
            if let latest, !latest.metrics.isEmpty {
                Text("Tap a reading to explore its history.")
                    .font(.subheadline).foregroundStyle(.secondary)
                TimelineView(.periodic(from: .now, by: 60)) { context in
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 280), alignment: .top)], alignment: .leading, spacing: 14) {
                        ForEach(latest.metrics, id: \.self) { metric in
                            NavigationLink {
                                SignalHistoryView(vehicleID: vehicleID, metric: metric, latest: latest)
                            } label: {
                                SignalCard(metric: metric, latest: latest, now: context.date)
                            }.buttonStyle(.plain).accessibilityIdentifier("signal-\(metric)")
                        }
                    }
                }
                if let date = SignalFormat.date(latest.asOf) {
                    Text("Fetched \(date.formatted(date: .abbreviated, time: .shortened))")
                        .font(.caption).foregroundStyle(.secondary)
                }
            } else if store.signalsRefreshing.contains(vehicleID), latest == nil {
                ProgressView("Loading vehicle readings…").frame(maxWidth: .infinity).padding(30)
            } else if store.signalErrors[vehicleID] == nil {
                ContentUnavailableView("No vehicle readings yet", systemImage: "gauge.with.dots.needle.33percent",
                    description: Text("Available Pi, Smartcar, and imported readings appear here. Missing readings stay unknown."))
            }
            if let contexts = latest?.contexts, !contexts.isEmpty {
                SignalContextsView(contexts: contexts)
            }
        }
        .task { await store.refreshSignals(vehicleID) }
    }
}

private struct SignalCard: View {
    let metric: String
    let latest: LatestSignals
    let now: Date
    private var readings: [LatestSignal] { latest.readings(metric) }
    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                Text(latest.label(metric)).font(.headline)
                Spacer()
                Image(systemName: "chart.xyaxis.line").foregroundStyle(PitStyle.amber).accessibilityHidden(true)
                Image(systemName: "chevron.right").font(.caption).foregroundStyle(.secondary).accessibilityHidden(true)
            }
            if let reading = readings.first {
                VStack(alignment: .leading, spacing: 5) {
                    Text(reading.latest.calendarDate == nil ? SignalFormat.statistic(reading.statistic) : "Daily \(SignalFormat.statistic(reading.statistic).lowercased())")
                        .font(.caption.weight(.semibold)).foregroundStyle(PitStyle.amber)
                    Text(SignalFormat.value(reading.latest.value, unit: reading.unit))
                        .font(.system(.title, design: .rounded, weight: .bold)).monospacedDigit().foregroundStyle(.primary)
                    Text("\(SignalFormat.source(reading.source)) · \(reading.quality.capitalized)")
                        .font(.caption).foregroundStyle(.secondary)
                    Text(reading.latest.timeLabel).font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    if reading.statistic != "sample" {
                        Text("Historical summary").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    } else if reading.isStale(at: now, definition: latest.definition(metric)) {
                        Label("Stale reading", systemImage: "clock").font(.caption.weight(.semibold)).foregroundStyle(.orange)
                    }
                }.accessibilityElement(children: .combine)
            }
            let summaries = Array(readings.dropFirst()).filter { ["min", "max"].contains($0.statistic) }
            if !summaries.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(summaries) { reading in
                        Text("\(SignalFormat.statistic(reading.statistic)): \(SignalFormat.value(reading.latest.value, unit: reading.unit))")
                            .font(.subheadline.weight(.semibold))
                    }
                    Text("Historical periods; tap for times and sources").font(.caption).foregroundStyle(.secondary)
                }
            }
            if readings.count > 1 {
                Text("More readings and summaries").font(.caption).foregroundStyle(PitStyle.amber)
            }
        }.frame(maxWidth: .infinity, alignment: .leading).padding(20)
            .background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 20))
            .accessibilityElement(children: .combine)
            .accessibilityHint("Opens history chart")
    }
}

struct SignalHistoryView: View {
    @EnvironmentObject private var store: GarageStore
    let vehicleID: String
    let metric: String
    let latest: LatestSignals
    @State private var statistic: String
    @State private var days = 30
    @State private var history: SignalHistory?
    @State private var loading = false
    @State private var error: String?
    @State private var saved = false
    @State private var requestID: UUID?

    init(vehicleID: String, metric: String, latest: LatestSignals) {
        self.vehicleID = vehicleID
        self.metric = metric
        self.latest = latest
        let initialStatistic = latest.readings(metric).first?.statistic ?? "sample"
        _statistic = State(initialValue: initialStatistic)
        _days = State(initialValue: latest.initialHistoryDays(metric, statistic: initialStatistic))
    }
    private var statistics: [String] {
        Array(Set(latest.readings(metric).map(\.statistic))).sorted { SignalFormat.rank($0) < SignalFormat.rank($1) }
    }
    private var selectionKey: String { "\(statistic)/\(days)" }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                if let reading = latest.readings(metric).first(where: { $0.statistic == statistic }) {
                    VStack(alignment: .leading, spacing: 5) {
                        Text(SignalFormat.value(reading.latest.value, unit: reading.unit))
                            .font(.system(size: 38, weight: .bold, design: .rounded)).monospacedDigit()
                        Text("Last reported · \(reading.latest.calendarDate ?? reading.latest.timeLabel)")
                            .font(.subheadline).foregroundStyle(.secondary)
                    }.accessibilityElement(children: .combine)
                }
                if statistics.count > 1 {
                    Picker("Reading type", selection: $statistic) {
                        ForEach(statistics, id: \.self) { Text(SignalFormat.statistic($0)).tag($0) }
                    }.pickerStyle(.menu).accessibilityIdentifier("signalStatistic")
                }
                Picker("History range", selection: $days) {
                    Text("7 days").tag(7)
                    Text("30 days").tag(30)
                    Text("1 year").tag(365)
                }.pickerStyle(.segmented).accessibilityIdentifier("signalRange")
                if saved { Label("Saved history on this phone", systemImage: "wifi.slash").foregroundStyle(.orange).font(.callout) }
                if let error {
                    Text(error).foregroundStyle(.orange).font(.callout)
                    Button("Retry history") { Task { await load() } }.disabled(loading)
                }
                if loading { ProgressView("Loading history…").frame(maxWidth: .infinity) }
                if let history, !history.series.allSatisfy({ $0.points.isEmpty }) {
                    ForEach(history.series) { series in
                        ForEach([false, true], id: \.self) { calendar in
                            let data = SignalChartData(series: series, unit: history.unit, calendar: calendar)
                            if !data.buckets.isEmpty {
                                SignalSeriesChart(data: data, series: series, unit: history.unit)
                            }
                        }
                    }
                    DisclosureGroup("About these readings") {
                        VStack(alignment: .leading, spacing: 10) {
                            Text("Requested range: \(SignalFormat.interval(history.from, history.to))")
                            Text("Charts fit the available readings. Blank gaps are not zero.")
                            if history.series.contains(where: { $0.points.contains(where: { $0.calendarDate != nil }) }) {
                                Text("Calendar-date snapshots have day precision only. Time and timezone are unknown. Lines join adjacent reported days; missing days break the trend.")
                            }
                            if history.unit == "boolean" || history.unit == "code" {
                                Text("Lanes show observed states. Mixed means different states were recorded within a bucket; transition times and state durations are unavailable. Sample bands stop at 15 minutes; longer windows show recorded endpoints. No average is treated as a state.")
                            } else if statistic == "sample" {
                                Text("Lines connect recorded endpoints up to 15 minutes apart, breaking at empty buckets. They show a trend, not continuous coverage. Shading preserves each bucket's minimum and maximum; gaps inside a reduced bucket may be unavailable.")
                            } else {
                                Text("Each value describes its reporting period. When several summaries share a bucket, the chart shows their average and range, not a combined total or an exact measurement time.")
                            }
                        }.font(.footnote).foregroundStyle(.secondary).padding(.top, 8)
                    }.accessibilityIdentifier("signalProvenance")
                    DisclosureGroup("Read chart values") {
                        LazyVStack(alignment: .leading, spacing: 14) {
                            ForEach(history.series) { series in
                                Text(series.label).font(.headline).padding(.top, 12)
                                ForEach(series.points) { point in
                                    SignalBucketDetails(point: point, unit: history.unit, statistic: series.statistic)
                                }
                            }
                        }
                    }.accessibilityIdentifier("signalValues")
                } else if !loading, error == nil {
                    ContentUnavailableView("No readings in this range", systemImage: "chart.xyaxis.line",
                        description: Text("Choose a wider range or a different reading type. No values have been filled in."))
                }
            }.padding(20).frame(maxWidth: 760)
        }.frame(maxWidth: .infinity).background(PitStyle.background)
            .navigationTitle(latest.label(metric)).navigationBarTitleDisplayMode(.inline)
            .task(id: selectionKey) { await load() }
            .refreshable { await load() }
    }

    @MainActor private func load() async {
        let id = UUID()
        requestID = id
        let requestedStatistic = statistic
        let requestedDays = days
        history = store.cachedSignalHistory(vehicleID: vehicleID, metric: metric, statistic: requestedStatistic, days: requestedDays)
        saved = history != nil
        error = nil
        loading = true
        defer { if requestID == id { loading = false } }
        do {
            let result = try await store.signalHistory(vehicleID: vehicleID, metric: metric, statistic: requestedStatistic, days: requestedDays)
            guard requestID == id else { return }
            history = result
            saved = false
        } catch is CancellationError {
        } catch {
            if requestID == id { self.error = error.localizedDescription }
        }
    }
}

private struct SignalSeriesChart: View {
    let data: SignalChartData
    let series: SignalHistorySeries
    let unit: String
    @State private var selection: Double?
    private var tint: Color { series.quality == "estimated" ? .cyan : PitStyle.amber }
    private var yDomain: ClosedRange<Double> { data.yDomain(unit: unit) }
    private var identifier: String {
        if data.kind == .state { return "signalStateChart" }
        if data.kind == .category { return "signalCodeChart" }
        if data.kind == .bars { return "signalBarChart" }
        return data.calendar ? "calendarSignalChart" : "signalHistoryChart"
    }
    private var caption: String {
        if data.kind == .state || data.kind == .category {
            let kind = series.statistic == "sample" ? "Observed states" : SignalFormat.statistic(series.statistic)
            return "\(kind) · Mixed = multiple states"
        }
        if data.kind == .bars {
            return data.buckets.contains { $0.point.count > 1 } ? "Average per bucket · whiskers show range" : SignalFormat.statistic(series.statistic)
        }
        if data.calendar {
            let kind = series.statistic == "snapshot" ? "Daily snapshots" : "Daily \(SignalFormat.statistic(series.statistic).lowercased())"
            return "\(kind) · gaps show missing days"
        }
        if series.statistic == "sample" { return "Recorded trend · shaded range" }
        let statistic = SignalFormat.statistic(series.statistic)
        return data.buckets.contains { $0.point.count > 1 } ? "\(statistic) · bucket averages and range" : "\(statistic) · reported periods"
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                Text(series.label).font(.subheadline.weight(.semibold))
                Spacer()
                if data.kind == .trend || data.kind == .bars { Text(unit == "count" ? "Count" : unit).font(.caption).foregroundStyle(.secondary) }
            }
            if data.kind == .state || data.kind == .category { stateChart }
            else { numericChart }
            Text(caption).font(.caption).foregroundStyle(.secondary)
            if let first = data.buckets.first, let last = data.buckets.last {
                Text("\(data.xLabel(first.start)) to \(data.xLabel(last.end))\(data.calendar ? " · Day precision" : "")")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let selection, let bucket = data.buckets.min(by: { abs($0.center - selection) < abs($1.center - selection) }) {
                SignalBucketDetails(point: bucket.point, unit: unit, statistic: series.statistic)
                    .padding(12).frame(maxWidth: .infinity, alignment: .leading)
                    .background(PitStyle.background, in: RoundedRectangle(cornerRadius: 12))
            }
        }.padding(16).background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 20))
    }

    private var numericChart: some View {
        Chart {
            if data.kind == .trend {
                ForEach(data.vertices) { vertex in
                    AreaMark(x: .value("Position", vertex.x), yStart: .value("Baseline", yDomain.lowerBound), yEnd: .value("Value", vertex.value), series: .value("Segment", vertex.segment))
                        .foregroundStyle(LinearGradient(colors: [tint.opacity(0.24), tint.opacity(0.015)], startPoint: .top, endPoint: .bottom))
                        .interpolationMethod(.linear).accessibilityHidden(true)
                    LineMark(x: .value("Position", vertex.x), y: .value("Value", vertex.value), series: .value("Segment", vertex.segment))
                        .foregroundStyle(tint).lineStyle(StrokeStyle(lineWidth: 2.5, lineCap: .round, lineJoin: .round)).interpolationMethod(.linear)
                        .accessibilityHidden(true)
                    PointMark(x: .value("Position", vertex.x), y: .value("Value", vertex.value))
                        .foregroundStyle(tint).symbolSize(data.vertices.count > 30 ? 9 : 30)
                        .accessibilityLabel(data.xLabel(vertex.x)).accessibilityValue(SignalFormat.value(vertex.value, unit: unit))
                }
            }
            ForEach(data.buckets) { bucket in
                if data.kind == .bars {
                    let bounds = data.barBounds(bucket)
                    BarMark(xStart: .value("Bar start", bounds.lowerBound), xEnd: .value("Bar end", bounds.upperBound),
                            yStart: .value("Baseline", 0), yEnd: .value("Value", bucket.point.mean))
                        .foregroundStyle(tint.gradient)
                        .accessibilityLabel("\(data.xLabel(bucket.center)), \(bucket.point.count > 1 ? "average" : "value")")
                        .accessibilityValue(SignalFormat.value(bucket.point.mean, unit: unit))
                    if bucket.point.mean == 0 {
                        PointMark(x: .value("Position", bucket.center), y: .value("Value", 0))
                            .foregroundStyle(tint).symbolSize(30)
                            .accessibilityLabel("\(data.xLabel(bucket.center)), recorded zero")
                            .accessibilityValue(SignalFormat.value(0, unit: unit))
                    }
                }
                if bucket.point.minimum != bucket.point.maximum {
                    if !data.calendar, data.kind == .trend, bucket.start != bucket.end {
                        RectangleMark(xStart: .value("Period start", bucket.start), xEnd: .value("Period end", bucket.end), yStart: .value("Minimum", bucket.point.minimum), yEnd: .value("Maximum", bucket.point.maximum))
                            .foregroundStyle(tint.opacity(0.14)).accessibilityHidden(true)
                    }
                    RuleMark(x: .value("Position", bucket.center), yStart: .value("Minimum", bucket.point.minimum), yEnd: .value("Maximum", bucket.point.maximum))
                        .foregroundStyle(tint.opacity(0.7)).lineStyle(StrokeStyle(lineWidth: 2))
                        .accessibilityLabel("\(data.xLabel(bucket.center)), range")
                        .accessibilityValue("\(SignalFormat.value(bucket.point.minimum, unit: unit)) to \(SignalFormat.value(bucket.point.maximum, unit: unit))")
                }
            }
        }
        .chartXScale(domain: data.xDomain, range: .plotDimension(padding: 8))
        .chartYScale(domain: yDomain, range: .plotDimension(padding: 6))
        .chartXAxis { timeAxis }
        .chartYAxis {
            AxisMarks(position: .leading, values: .automatic(desiredCount: 4)) {
                AxisGridLine(stroke: StrokeStyle(lineWidth: 0.5)).foregroundStyle(.gray.opacity(0.25))
                AxisValueLabel()
            }
        }
        .chartXSelection(value: $selection)
        .chartPlotStyle { $0.clipped() }
        .frame(height: 260).accessibilityIdentifier(identifier)
    }

    private var stateChart: some View {
        Chart {
            ForEach(data.stateMarks) { mark in
                let state = mark.label
                if mark.start != mark.end {
                    RuleMark(xStart: .value("Start", mark.start), xEnd: .value("End", mark.end), y: .value("State", state))
                        .foregroundStyle(state == "Mixed" || state == "Unknown" || state == "Off" ? .gray : tint)
                        .lineStyle(StrokeStyle(lineWidth: 9, lineCap: .round, dash: state == "Mixed" ? [3, 3] : []))
                        .accessibilityLabel("\(data.xLabel(mark.start)) to \(data.xLabel(mark.end)), \(state), observed within this bucket")
                } else {
                    PointMark(x: .value("Position", mark.start), y: .value("State", state))
                        .foregroundStyle(state == "Mixed" || state == "Unknown" || state == "Off" ? .gray : tint).symbolSize(60)
                        .accessibilityLabel("\(data.xLabel(mark.start)), \(mark.summary ? SignalFormat.statistic(series.statistic) + " bucket" : "observed"), \(state)")
                }
            }
        }
        .chartXScale(domain: data.xDomain, range: .plotDimension(padding: 8))
        .chartYScale(domain: stateLanes)
        .chartXAxis { timeAxis }
        .chartYAxis {
            AxisMarks(position: .leading) {
                AxisGridLine(stroke: StrokeStyle(lineWidth: 0.5)).foregroundStyle(.gray.opacity(0.25))
                AxisValueLabel()
            }
        }
        .chartXSelection(value: $selection)
        .frame(height: max(160, CGFloat(stateLanes.count) * 46)).accessibilityIdentifier(identifier)
    }
    private var stateLanes: [String] {
        if data.kind == .state { return ["Off", "On", "Mixed", "Unknown"].filter { lane in lane == "Off" || lane == "On" || data.stateMarks.contains { $0.label == lane } } }
        return Array(Set(data.stateMarks.map(\.label))).sorted { $0.localizedStandardCompare($1) == .orderedAscending }
    }
    @AxisContentBuilder private var timeAxis: some AxisContent {
        AxisMarks(values: data.axisValues) { value in
            AxisValueLabel(anchor: value.index == 0 ? .topLeading : value.index == data.axisValues.count - 1 ? .topTrailing : .top) {
                if let number = value.as(Double.self) { Text(data.xLabel(number)).font(.caption2).fixedSize() }
            }
        }
    }
}

private struct SignalBucketDetails: View {
    let point: SignalHistoryPoint
    let unit: String
    let statistic: String
    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(point.calendarDate.map { "\($0) · Time and timezone unknown" } ?? SignalFormat.interval(point.windowStart, point.windowEnd))
                .font(.caption).foregroundStyle(.secondary)
            if unit == "boolean" || unit == "code" {
                let bucket = SignalChartBucket(point: point, start: 0, end: 0, calendar: point.calendarDate != nil)
                Text(unit == "boolean" ? bucket.state : bucket.category).font(.headline)
                if point.minimum != point.maximum { Text("Multiple states recorded; transition times unavailable.").font(.caption).foregroundStyle(.secondary) }
                Text("First \(SignalFormat.value(point.first, unit: unit)) · Last \(SignalFormat.value(point.last, unit: unit))").font(.caption)
                Text("\(point.count) \(statistic == "sample" ? "readings" : "summaries")").font(.caption).foregroundStyle(.secondary)
            } else {
                Text("Min \(SignalFormat.value(point.minimum, unit: unit)) · Max \(SignalFormat.value(point.maximum, unit: unit))")
                Text("\(point.count > 1 ? "Bucket average" : "Value") \(SignalFormat.value(point.mean, unit: unit)) · \(point.count) \(statistic == "sample" ? "readings" : "summaries")")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if statistic == "sample", let date = point.firstObservedAt {
                Text("First: \(SignalFormat.value(point.first, unit: unit)) at \(SignalFormat.date(date)?.formatted() ?? date)").font(.caption)
            }
            if statistic == "sample", let date = point.lastObservedAt, date != point.firstObservedAt {
                Text("Last: \(SignalFormat.value(point.last, unit: unit)) at \(SignalFormat.date(date)?.formatted() ?? date)").font(.caption)
            }
        }.accessibilityElement(children: .combine)
    }
}

private struct SignalContextsView: View {
    let contexts: [SourcedSignalContext]
    var body: some View {
        DisclosureGroup("Collection details and diagnostics") {
            LazyVStack(alignment: .leading, spacing: 18) {
                ForEach(contexts) { item in
                    VStack(alignment: .leading, spacing: 8) {
                        Text(SignalFormat.source(item.source)).font(.headline)
                        Text(item.context.timeLabel).font(.caption).foregroundStyle(.secondary)
                        if let diagnostic = item.context.diagnostic {
                            Text("\(diagnostic.class.capitalized) diagnostics").font(.subheadline.weight(.semibold))
                            if diagnostic.unknown { Text("Diagnostic result unknown").foregroundStyle(.orange) }
                            else if diagnostic.codes?.isEmpty != false { Text("No codes reported") }
                            else { Text((diagnostic.codes ?? []).joined(separator: ", ")).textSelection(.enabled) }
                            Text("\(diagnostic.successfulReads) successful reads").font(.caption).foregroundStyle(.secondary)
                        }
                        if let coverage = item.context.coverage {
                            Text("\(coverage.retainedSamples) retained samples · \(coverage.skippedIntervals) skipped intervals")
                            if coverage.firstObservedAt != nil {
                                Text("Observed \(SignalFormat.interval(coverage.firstObservedAt, coverage.lastObservedAt))").font(.caption)
                            }
                        }
                        if let segment = item.context.segment {
                            Text("Recording segment · \(segment.state.capitalized)").font(.subheadline.weight(.semibold))
                            Text(SignalFormat.interval(segment.startedAt, segment.endedAt)).font(.caption)
                            Text("\(segment.samples) samples · \(segment.runningSeconds.formatted()) s running · \(segment.idleSeconds.formatted()) s idle").font(.caption)
                            if let distance = segment.distanceKm { Text("Estimated distance \(SignalFormat.value(distance, unit: "km"))").font(.caption) }
                            if let speed = segment.topSpeedKph { Text("Top speed \(SignalFormat.value(speed, unit: "km/h"))").font(.caption) }
                            Text("Speed coverage \(segment.speedCoverageSeconds.formatted()) s").font(.caption)
                        }
                        if let location = item.context.location {
                            Text("Reported location · \(location.type)").font(.subheadline.weight(.semibold))
                            Text("\(location.latitude.formatted(.number.precision(.fractionLength(5)))), \(location.longitude.formatted(.number.precision(.fractionLength(5))))").textSelection(.enabled)
                            if let accuracy = location.accuracyMeters { Text("Reported accuracy \(accuracy.formatted()) m").font(.caption) }
                        }
                        if item.context.kind == "snapshot" { Text("Calendar-day snapshot; no exact observation time supplied.").font(.caption) }
                    }.accessibilityElement(children: .combine)
                    Divider()
                }
            }.padding(.top, 14)
        }.font(.subheadline).fixedSize(horizontal: false, vertical: true)
    }
}
