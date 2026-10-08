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
                Text("Tap a metric to explore its history. Daily summaries describe a period, not the vehicle right now.")
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
    @State private var selectedDate: Date?

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
                Text("THE READING, IN CONTEXT").font(.caption.weight(.bold)).tracking(2).foregroundStyle(PitStyle.amber)
                Picker("Reading type", selection: $statistic) {
                    ForEach(statistics, id: \.self) { Text(SignalFormat.statistic($0)).tag($0) }
                }.pickerStyle(.menu).accessibilityIdentifier("signalStatistic")
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
                    if history.series.contains(where: { $0.points.contains(where: { $0.calendarDate == nil }) }) {
                        SignalHistoryChart(history: history, selectedDate: $selectedDate)
                    }
                    ForEach(history.series) { series in
                        let dated = series.points.filter { $0.calendarDate != nil }
                        if !dated.isEmpty {
                            Text("Calendar-date snapshots").font(.headline)
                            Text("Each point represents a calendar day. Time and timezone unknown; no intraday timing is implied.")
                                .font(.footnote).foregroundStyle(.secondary)
                            Text(series.label).font(.subheadline)
                            CalendarSignalChart(points: dated, unit: history.unit)
                        }
                    }
                    Text(statistic == "sample"
                         ? "Dots are timestamped readings. Shaded ranges preserve minimum and maximum values within each bucket. Gaps are not connected."
                         : "Each mark covers a supplied reporting period. These summaries do not reveal the time of individual measurements.")
                        .font(.footnote).foregroundStyle(.secondary)
                    Text("Displayed interval: \(SignalFormat.interval(history.from, history.to))")
                        .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    if let selectedDate {
                        selectedValues(history, at: selectedDate)
                    }
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

    @ViewBuilder private func selectedValues(_ history: SignalHistory, at date: Date) -> some View {
        ForEach(history.series) { series in
            ForEach(series.points.filter { point in
                guard let start = point.bucketStart.flatMap(SignalFormat.date), let end = point.bucketEnd.flatMap(SignalFormat.date) else { return false }
                return date >= start && date <= end
            }) { point in
                VStack(alignment: .leading, spacing: 6) {
                    Text(series.label).font(.headline)
                    SignalBucketDetails(point: point, unit: history.unit, statistic: series.statistic)
                }.padding().background(PitStyle.panel, in: RoundedRectangle(cornerRadius: 14))
            }
        }
    }

    @MainActor private func load() async {
        let id = UUID()
        requestID = id
        let requestedStatistic = statistic
        let requestedDays = days
        history = store.cachedSignalHistory(vehicleID: vehicleID, metric: metric, statistic: requestedStatistic, days: requestedDays)
        saved = history != nil
        selectedDate = nil
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

private struct CalendarSignalChart: View {
    let points: [SignalHistoryPoint]
    let unit: String
    var body: some View {
        Chart {
            ForEach(points.sorted { ($0.calendarDate ?? "") < ($1.calendarDate ?? "") }) { point in
                if let date = point.calendarDate {
                    RuleMark(x: .value("Calendar date", date), yStart: .value("Minimum", point.minimum), yEnd: .value("Maximum", point.maximum))
                        .foregroundStyle(PitStyle.amber.opacity(0.5)).lineStyle(StrokeStyle(lineWidth: 4))
                        .accessibilityLabel("\(date), timezone unknown")
                        .accessibilityValue("Minimum \(SignalFormat.value(point.minimum, unit: unit)), maximum \(SignalFormat.value(point.maximum, unit: unit))")
                    PointMark(x: .value("Calendar date", date), y: .value("Snapshot average", point.mean))
                        .foregroundStyle(PitStyle.amber).symbolSize(65)
                        .accessibilityLabel("\(date), \(point.count == 1 ? "snapshot" : "snapshot average"), timezone unknown")
                        .accessibilityValue(SignalFormat.value(point.mean, unit: unit))
                }
            }
        }.chartYAxisLabel(unit).frame(height: 240).accessibilityIdentifier("calendarSignalChart")
    }
}

private struct SignalHistoryChart: View {
    let history: SignalHistory
    @Binding var selectedDate: Date?
    private var timeRange: ClosedRange<Date> {
        let start = SignalFormat.date(history.from) ?? Date()
        let end = SignalFormat.date(history.to) ?? start.addingTimeInterval(1)
        return min(start, end)...max(start, end)
    }
    var body: some View {
        Chart {
            ForEach(history.series) { series in
                ForEach(series.points) { point in
                    if point.calendarDate == nil, let start = point.windowStart.flatMap(SignalFormat.date), let end = point.windowEnd.flatMap(SignalFormat.date) {
                        RectangleMark(xStart: .value("Period start", start), xEnd: .value("Period end", end),
                                      yStart: .value("Minimum", point.minimum), yEnd: .value("Maximum", point.maximum))
                            .foregroundStyle(by: .value("Source and quality", series.label)).opacity(0.25)
                            .accessibilityLabel("\(series.label), \(SignalFormat.interval(point.windowStart, point.windowEnd))")
                            .accessibilityValue("Minimum \(point.minimum.formatted()), maximum \(point.maximum.formatted()) \(history.unit)")
                        if series.statistic != "sample" {
                            RuleMark(xStart: .value("Period start", start), xEnd: .value("Period end", end), y: .value("Bucket average", point.mean))
                                .foregroundStyle(by: .value("Source and quality", series.label))
                                .accessibilityLabel("Period bucket average").accessibilityValue(SignalFormat.value(point.mean, unit: history.unit))
                        }
                    }
                    if series.statistic == "sample", let first = point.firstObservedAt.flatMap(SignalFormat.date) {
                        PointMark(x: .value("First observed", first), y: .value("First reading", point.first))
                            .foregroundStyle(by: .value("Source and quality", series.label))
                            .accessibilityLabel("\(series.label), \(first.formatted())").accessibilityValue(SignalFormat.value(point.first, unit: history.unit))
                        if point.lastObservedAt != point.firstObservedAt, let last = point.lastObservedAt.flatMap(SignalFormat.date) {
                            PointMark(x: .value("Last observed", last), y: .value("Last reading", point.last))
                                .foregroundStyle(by: .value("Source and quality", series.label))
                                .accessibilityLabel("\(series.label), \(last.formatted())").accessibilityValue(SignalFormat.value(point.last, unit: history.unit))
                        }
                    }
                }
            }
        }
        .chartXSelection(value: $selectedDate)
        .chartXScale(domain: timeRange, range: .plotDimension(padding: 8))
        .chartPlotStyle { $0.clipped() }
        .chartYAxisLabel(history.unit)
        .chartLegend(position: .bottom, alignment: .leading)
        .frame(height: 280)
        .accessibilityIdentifier("signalHistoryChart")
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
            Text("Min \(SignalFormat.value(point.minimum, unit: unit)) · Max \(SignalFormat.value(point.maximum, unit: unit))")
            Text("Bucket average \(SignalFormat.value(point.mean, unit: unit)) · \(point.count) \(statistic == "sample" ? "readings" : "summaries")")
                .font(.caption).foregroundStyle(.secondary)
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
                            else if diagnostic.codes.isEmpty { Text("No codes reported") }
                            else { Text(diagnostic.codes.joined(separator: ", ")).textSelection(.enabled) }
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
