package main

import (
	"fmt"
	"testing"
)

func TestEncV2(t *testing.T) {
	var costs = []struct {
		lit                int
		matchlen, matchoff int
		want               int
	}{
		{1, 0, 0, 2}, // 1 literal == 1 byte + 1 lit
		{2, 0, 0, 3},
		{15, 0, 0, 15 + 1},
		{16, 0, 0, 16 + 2},
		{255, 0, 0, 255 + 2},
		{256, 0, 0, 256 + 4}, // 00 00 00 256

		{0, 1, 1, 1}, // match of 1,1 -> 1 byte
	}
	var e encoder_v2
	for _, tt := range costs {
		e.num_literals = 0 // reset
		ans := e.cost(tt.lit, Match{tt.matchlen, tt.matchoff})
		if ans != tt.want {
			t.Errorf("cost failure: got %d, want %d", ans, tt.want)
		}

		if tt.lit != 0 {
			testname := fmt.Sprintf("lit_cost %d", tt.lit)
			t.Run(testname, func(t *testing.T) {
				ans := e.lit_cost(tt.lit)
				if ans != tt.want {
					t.Errorf("lit_cost failure: got %d, want %d", ans, tt.want)
				}
			})
		}
		if tt.matchlen != 0 {
			testname := fmt.Sprintf("match_cost %d,%d", tt.matchlen, tt.matchoff)
			t.Run(testname, func(t *testing.T) {
				m := Match{tt.matchlen, tt.matchoff}
				ans := e.match_cost(m)
				if ans != tt.want {
					t.Errorf("match_cost failure: got %d, want %d", ans, tt.want)
				}
			})
		}
	}
}
