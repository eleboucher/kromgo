package kromgo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kashalls/kromgo/cmd/kromgo/init/configuration"
	"github.com/kashalls/kromgo/cmd/kromgo/init/prometheus"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"go.uber.org/zap"
)

type HistoryDataPoint struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

type HistorySeries struct {
	Labels map[string]string  `json:"labels"`
	Data   []HistoryDataPoint `json:"data"`
}

type HistoryResponse struct {
	Metric string          `json:"metric"`
	Title  string          `json:"title"`
	Start  int64           `json:"start"`
	End    int64           `json:"end"`
	Step   int64           `json:"step"`
	Series []HistorySeries `json:"series"`
}

// durationUnitRe matches a numeric value followed by a custom unit (y or d).
var durationUnitRe = regexp.MustCompile(`(\d+(?:\.\d+)?)(y|d)`)

// parseDuration extends time.ParseDuration with support for days (d) and years (y).
// Units can be combined in any order: "1y30d", "7d12h", "3d5y", "2d".
// A value of "0" means unlimited (only meaningful for maxDuration config).
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	multipliers := map[string]time.Duration{
		"y": 365 * 24 * time.Hour,
		"d": 24 * time.Hour,
	}

	var total time.Duration
	remaining := s
	for _, m := range durationUnitRe.FindAllStringSubmatch(s, -1) {
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		total += time.Duration(float64(multipliers[m[2]]) * n)
		remaining = strings.Replace(remaining, m[0], "", 1)
	}

	if remaining != "" {
		d, err := time.ParseDuration(remaining)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		total += d
	}

	return total, nil
}

func parseTimeParam(s string) (time.Time, error) {
	// Try Unix timestamp (integer)
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(ts, 0), nil
	}
	// Try RFC3339
	return time.Parse(time.RFC3339, s)
}

func parseHistoryParams(r *http.Request) (start, end time.Time, step time.Duration, err error) {
	now := time.Now()

	// last=7d is shorthand for start=now-7d&end=now
	if s := r.URL.Query().Get("last"); s != "" {
		var d time.Duration
		d, err = parseDuration(s)
		if err != nil {
			return
		}
		end = now
		start = now.Add(-d)
	} else {
		// Parse end
		end = now
		if s := r.URL.Query().Get("end"); s != "" {
			end, err = parseTimeParam(s)
			if err != nil {
				return
			}
		}

		// Parse start
		start = end.Add(-1 * time.Hour)
		if s := r.URL.Query().Get("start"); s != "" {
			start, err = parseTimeParam(s)
			if err != nil {
				return
			}
		}

		if start.After(end) {
			err = errStartAfterEnd
			return
		}
	}

	// Parse step
	minStep := time.Minute
	step = max(end.Sub(start)/100, minStep)
	if s := r.URL.Query().Get("step"); s != "" {
		step, err = parseDuration(s)
		if err != nil {
			return
		}
		step = max(step, minStep)
	}

	return
}

var errStartAfterEnd = &historyParamError{"start must be before end"}

type historyParamError struct{ msg string }

func (e *historyParamError) Error() string { return e.msg }

func (h *KromgoHandler) historyEnabled(metric configuration.Metric) bool {
	if metric.History != nil && metric.History.Enabled != nil {
		return *metric.History.Enabled
	}
	return h.Config.History.Enabled
}

func (h *KromgoHandler) historyMaxDuration(metric configuration.Metric) time.Duration {
	if metric.History != nil && metric.History.MaxDuration != "" {
		if d, err := parseDuration(metric.History.MaxDuration); err == nil {
			return d
		}
	}
	if h.Config.History.MaxDuration != "" {
		if d, err := parseDuration(h.Config.History.MaxDuration); err == nil {
			return d
		}
	}
	return time.Hour
}

func (h *KromgoHandler) handleHistory(w http.ResponseWriter, r *http.Request, metric configuration.Metric) {
	if !h.historyEnabled(metric) {
		HandleError(w, r, metric.Name, "History not enabled for this metric", http.StatusForbidden)
		return
	}

	start, end, step, err := parseHistoryParams(r)
	if err != nil {
		HandleError(w, r, metric.Name, "Invalid parameter: "+err.Error(), http.StatusBadRequest)
		return
	}

	if maxDur := h.historyMaxDuration(metric); maxDur > 0 && end.Sub(start) > maxDur {
		HandleError(w, r, metric.Name, "Requested time window exceeds maximum allowed duration", http.StatusBadRequest)
		return
	}

	result, warnings, err := prometheus.Papi.QueryRange(r.Context(), metric.Query, v1.Range{
		Start: start,
		End:   end,
		Step:  step,
	})
	if err != nil {
		requestLog(r).With(zap.Error(err)).Error("error executing history query")
		HandleError(w, r, metric.Name, "Query Error", http.StatusInternalServerError)
		return
	}
	if len(warnings) > 0 {
		for _, warning := range warnings {
			requestLog(r).With(zap.String("warning", warning)).Warn("encountered warnings while executing history query")
		}
	}

	matrix, ok := result.(model.Matrix)
	if !ok {
		requestLog(r).Error("history query did not return a matrix")
		HandleError(w, r, metric.Name, "Unexpected result type", http.StatusInternalServerError)
		return
	}

	title := metric.Name
	if metric.Title != "" {
		title = metric.Title
	}

	series := make([]HistorySeries, 0, len(matrix))
	for _, stream := range matrix {
		labels := make(map[string]string, len(stream.Metric))
		for k, v := range stream.Metric {
			labels[string(k)] = string(v)
		}
		data := make([]HistoryDataPoint, 0, len(stream.Values))
		for _, point := range stream.Values {
			data = append(data, HistoryDataPoint{
				T: int64(point.Timestamp) / 1000,
				V: float64(point.Value),
			})
		}
		series = append(series, HistorySeries{Labels: labels, Data: data})
	}

	resp := HistoryResponse{
		Metric: metric.Name,
		Title:  title,
		Start:  start.Unix(),
		End:    end.Unix(),
		Step:   int64(step.Seconds()),
		Series: series,
	}

	jsonResponse, err := json.Marshal(resp)
	if err != nil {
		requestLog(r).With(zap.Error(err)).Error("error marshaling history response")
		HandleError(w, r, metric.Name, "Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonResponse)
}
