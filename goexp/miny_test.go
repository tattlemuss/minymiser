package main

import "testing"

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
		ans := e.cost(tt.lit, match{tt.matchlen, tt.matchoff})
		if ans != tt.want {
			t.Errorf("cost failure: got %d, want %d", ans, tt.want)
		}

		if tt.lit != 0 {
			ans := e.lit_cost(tt.lit)
			if ans != tt.want {
				t.Errorf("lit_cost failure: got %d, want %d", ans, tt.want)
			}
		}
		if tt.matchlen != 0 {
			m := match{tt.matchlen, tt.matchoff}
			ans := e.match_cost(m)
			if ans != tt.want {
				t.Errorf("lit_cost failure: got %d, want %d", ans, tt.want)
			}
		}

	}
}
