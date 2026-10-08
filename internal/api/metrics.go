package api

import (
	"bytes"
	"errors"
	"net/http"
	"sort"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"google.golang.org/protobuf/proto"
)

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	vehicles, err := s.store.Vehicles(r.Context())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	families := map[string]*dto.MetricFamily{}
	appendGauge := func(name, help string, value float64, labels []*dto.LabelPair) {
		f := families[name]
		if f == nil {
			f = &dto.MetricFamily{Name: proto.String(name), Help: proto.String(help), Type: dto.MetricType_GAUGE.Enum()}
			families[name] = f
		}
		f.Metric = append(f.Metric, &dto.Metric{Label: labels, Gauge: &dto.Gauge{Value: proto.Float64(value)}})
	}
	for _, vehicle := range vehicles {
		latest, err := s.store.LatestSignals(r.Context(), vehicle.ID)
		if errors.Is(err, garage.ErrNotFound) {
			continue
		}
		if err != nil {
			s.failure(w, r, err)
			return
		}
		for _, series := range latest.Series {
			labels := []*dto.LabelPair{}
			for _, kv := range [][2]string{{"vehicle_id", vehicle.ID}, {"source", series.Source}, {"statistic", series.Statistic}, {"quality", series.Quality}, {"unit", series.Unit}} {
				labels = append(labels, &dto.LabelPair{Name: proto.String(kv[0]), Value: proto.String(kv[1])})
			}
			name := "pitpilot_vehicle_" + series.Metric
			if series.Statistic == "count" {
				name += "_observation_count"
			}
			appendGauge(name, "Latest stored vehicle value; statistic and quality labels describe its meaning.", series.Latest.Value, labels)
			stale := 0.0
			if series.Stale {
				stale = 1
			}
			appendGauge(name+"_stale", "One when the value is historical, future-dated, or outside its live freshness window.", stale, labels)
			if observed := series.Latest.ObservedAt; observed != nil {
				appendGauge(name+"_observed_timestamp_seconds", "Authentic source observation time as Unix seconds.", float64(observed.UnixMilli())/1000, labels)
			}
			if end := series.Latest.PeriodEnd; end != nil {
				appendGauge(name+"_period_end_timestamp_seconds", "Source aggregate period end as Unix seconds, not a measurement time.", float64(end.UnixMilli())/1000, labels)
			}
		}
	}
	var body bytes.Buffer
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err = expfmt.MetricFamilyToOpenMetrics(&body, families[name]); err != nil {
			s.failure(w, r, errors.New("metrics encoding failed"))
			return
		}
	}
	if _, err = expfmt.FinalizeOpenMetrics(&body); err != nil {
		s.failure(w, r, errors.New("metrics encoding failed"))
		return
	}
	w.Header().Set("Content-Type", string(expfmt.FmtOpenMetrics_1_0_0))
	w.WriteHeader(200)
	_, _ = w.Write(body.Bytes())
}
