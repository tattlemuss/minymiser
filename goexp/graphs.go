package main

import (
	"os"
	"sort"

	"github.com/wcharczuk/go-chart/v2"
	//exposes "chart"
)

// Scatter plot for X, Y ints
func scatter_int_map(path string, results map[int]int) error {
	// Create sorted list
	keys := make([]int, 0)
	for i := range results {
		keys = append(keys, i)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})

	// Convert map to 2 arrays
	xvals := make([]float64, 0)
	yvals := make([]float64, 0)
	for i := range keys {
		xvals = append(xvals, float64(keys[i]))
		yvals = append(yvals, float64(results[keys[i]]))
	}
	graph := chart.Chart{
		Series: []chart.Series{
			chart.ContinuousSeries{
				Style: chart.Style{
					DotWidth: 3,
				},
				XValues: xvals,
				YValues: yvals,
			},
		},
	}

	fh, _ := os.Create(path)
	err := graph.Render(chart.SVG, fh)
	if err != nil {
		return err
	}
	fh.Close()
	return nil
}
