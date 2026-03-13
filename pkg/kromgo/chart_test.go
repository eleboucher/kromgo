package kromgo

import (
	"fmt"
	"os"
	"testing"

	"github.com/prometheus/common/model"
)

func makeMatrix(series [][]float64) model.Matrix {
	matrix := make(model.Matrix, len(series))
	for i, values := range series {
		stream := &model.SampleStream{
			Metric: model.Metric{"series": model.LabelValue(fmt.Sprintf("s%d", i))},
			Values: make([]model.SamplePair, len(values)),
		}
		for j, v := range values {
			stream.Values[j] = model.SamplePair{
				Timestamp: model.Time(j * 60 * 1000),
				Value:     model.SampleValue(v),
			}
		}
		matrix[i] = stream
	}
	return matrix
}

func makeMatrixLabeled(series map[string][]float64) model.Matrix {
	matrix := make(model.Matrix, 0, len(series))
	for name, values := range series {
		stream := &model.SampleStream{
			Metric: model.Metric{"instance": model.LabelValue(name)},
			Values: make([]model.SamplePair, len(values)),
		}
		for j, v := range values {
			stream.Values[j] = model.SamplePair{
				Timestamp: model.Time(j * 60 * 1000),
				Value:     model.SampleValue(v),
			}
		}
		matrix = append(matrix, stream)
	}
	return matrix
}

// TestRenderSparkline_Visual writes SVG files to /tmp so you can open them in a browser.
func TestRenderSparkline_Visual(t *testing.T) {
	cases := []struct {
		name   string
		matrix model.Matrix
		params chartParams
	}{
		{
			name:   "single_series",
			matrix: makeMatrix([][]float64{{10, 25, 15, 40, 30, 55, 45, 60, 50, 70}}),
			params: chartParams{width: 300, height: 80, strokeWidth: 2, legend: true},
		},
		{
			name:   "flat_line",
			matrix: makeMatrix([][]float64{{42, 42, 42, 42, 42}}),
			params: chartParams{width: 300, height: 80, strokeWidth: 2, legend: true},
		},
		{
			name:   "spike",
			matrix: makeMatrix([][]float64{{5, 5, 5, 100, 5, 5, 5}}),
			params: chartParams{width: 300, height: 80, strokeWidth: 2, legend: true},
		},
		{
			name: "multi_series_legend",
			matrix: makeMatrixLabeled(map[string][]float64{
				"server1": {10, 20, 15, 30, 25, 35, 28},
				"server2": {40, 35, 45, 30, 50, 42, 48},
			}),
			params: chartParams{width: 300, height: 80, strokeWidth: 2, legend: true},
		},
		{
			name: "multi_series_no_legend",
			matrix: makeMatrixLabeled(map[string][]float64{
				"server1": {10, 20, 15, 30, 25, 35, 28},
				"server2": {40, 35, 45, 30, 50, 42, 48},
			}),
			params: chartParams{width: 300, height: 80, strokeWidth: 2, legend: false},
		},
		{
			name: "many_series_legend",
			matrix: makeMatrixLabeled(map[string][]float64{
				"web-01":  {10, 20, 15, 30, 25},
				"web-02":  {40, 35, 45, 30, 50},
				"db-01":   {5, 8, 6, 12, 9},
				"cache-01": {60, 55, 65, 50, 70},
			}),
			params: chartParams{width: 400, height: 100, strokeWidth: 2, legend: true},
		},
		{
			name:   "custom_color",
			matrix: makeMatrix([][]float64{{10, 25, 15, 40, 30, 55, 45, 60}}),
			params: chartParams{width: 400, height: 100, strokeWidth: 2, color: "brightgreen", legend: true},
		},
		{
			name:   "wide",
			matrix: makeMatrix([][]float64{{5, 10, 8, 15, 12, 20, 18, 25, 22, 30, 28, 35}}),
			params: chartParams{width: 600, height: 120, strokeWidth: 3, legend: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svg := renderSparkline(tc.matrix, tc.params, nil)
			path := "/tmp/sparkline_" + tc.name + ".svg"
			if err := os.WriteFile(path, []byte(svg), 0644); err != nil {
				t.Fatalf("failed to write %s: %v", path, err)
			}
			t.Logf("wrote %s", path)
		})
	}
}
