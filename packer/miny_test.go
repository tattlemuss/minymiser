package main

import (
	"fmt"
	"testing"
)

func costsForMatches(enc Encoder, test *testing.T) {
	var t Token
	var m Match

	input := make([]byte, 512)
	t.isMatch = true
	for tlen := 1; tlen < 512; tlen++ {
		m.len = tlen
		t.len = tlen
		for toff := 1; toff < 512; toff++ {
			m.off = toff
			t.off = toff
			cost := enc.Cost(0, m)
			encoded := NewPackStream()
			enc.Encode(&t, encoded, input)
			fs := encoded.FinalStream()
			if len(fs) != cost {
				test.Errorf("match_cost failure, len %d off %d: got %d, want %d",
					tlen, toff, len(fs), cost)
			}
		}
	}
}

func costsForLits(enc Encoder, test *testing.T) {
	var t Token
	var m Match

	input := make([]byte, 512)

	t.isMatch = false
	for tlen := 1; tlen < 512; tlen++ {
		cost := enc.Cost(tlen, m)
		t.len = tlen
		encoded := NewPackStream()
		enc.Encode(&t, encoded, input)
		fs := encoded.FinalStream()
		if len(fs) != cost {
			test.Errorf("lit_cost failure: len %d: result %d, expected %d", tlen, len(fs), cost)
		}
		enc.Reset()
	}
}

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
	var e Encoder_v2
	for _, tt := range costs {
		e.numLiterals = 0 // reset
		ans := e.Cost(tt.lit, Match{tt.matchlen, tt.matchoff})
		if ans != tt.want {
			t.Errorf("cost failure: got %d, want %d", ans, tt.want)
		}

		if tt.lit != 0 {
			testname := fmt.Sprintf("lit_cost %d", tt.lit)
			t.Run(testname, func(t *testing.T) {
				ans := e.litCost(tt.lit)
				if ans != tt.want {
					t.Errorf("lit_cost failure: got %d, want %d", ans, tt.want)
				}
			})
		}
		if tt.matchlen != 0 {
			testname := fmt.Sprintf("match_cost %d,%d", tt.matchlen, tt.matchoff)
			t.Run(testname, func(t *testing.T) {
				m := Match{tt.matchlen, tt.matchoff}
				ans := e.matchCost(m)
				if ans != tt.want {
					t.Errorf("match_cost failure: got %d, want %d", ans, tt.want)
				}
			})
		}
	}
}

func TestMatchCosts_V1(t *testing.T) {
	t.Log("Testing match length for Encoder_v1")
	var e1 Encoder_v1
	costsForMatches(&e1, t)
}

func TestLitCosts_V1(t *testing.T) {
	t.Log("Testing lit length for Encoder_v1")
	var e1 Encoder_v1
	costsForLits(&e1, t)
}

func TestMatchCosts_V2(t *testing.T) {
	t.Log("Testing match length for Encoder_v2")
	var e2 Encoder_v2
	costsForMatches(&e2, t)
}

func TestLitCosts_V2(t *testing.T) {
	t.Log("Testing lit length for Encoder_v2")
	var e1 Encoder_v2
	costsForLits(&e1, t)
}
