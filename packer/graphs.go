package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/wcharczuk/go-chart/v2"
	//exposes "chart"
)

func histo(name string, vals []int) {
	for i := 10; i < 100; i += 5 {
		pos := i * len(vals) / 100
		fmt.Printf("%4v ", vals[pos])
	}
	fmt.Printf("%4d ", uint(vals[len(vals)-1]))
	fmt.Println("<--" + name)
}

func xy_plot(path string, x []float64, y []float64) error {
	graph := chart.Chart{
		Series: []chart.Series{
			chart.ContinuousSeries{
				Style: chart.Style{
					DotWidth:    3,
					StrokeWidth: chart.Disabled,
				},
				XValues: x,
				YValues: y,
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
	return xy_plot(path, xvals, yvals)
}

// Scatter plot for X, Y ints
func linegraph_int(path string, results []int) error {
	xvals := make([]float64, 0)
	yvals := make([]float64, 0)
	for i := range results {
		if results[i] != 0 {
			xvals = append(xvals, float64(i))
			yvals = append(yvals, float64(results[i]))
		}
	}
	graph := chart.Chart{
		Series: []chart.Series{
			&chart.ContinuousSeries{
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
