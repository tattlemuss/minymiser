package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
)

const num_regs = 14

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func empty_arr() []byte {
	return make([]byte, 0)
}

func percent(num int, denom int) float32 {
	if denom == 0 {
		return 0.0
	}
	return 100.0 * float32(num) / float32(denom)
}

// Describes a match or a series of literals.
type token struct {
	is_match bool
	len      int // length in bytes
	off      int // reverse offset if is_match, abs position if literal
}

// Describes a match run.
type match struct {
	len int
	off int
}

// Contains a single stream of packed data.
type packedstream struct {
	data []byte
}

type encoder interface {
	// Calculate the cost for adding literals or matches, or both
	cost(lit_count int, m match) int

	// Apply N literals to the internal state
	lit(lit_count int)
	// Apply a match to the internal state
	match(m match)

	// Encodes all the given tokens into a binary stream.
	encode(tokens []token, input []byte) []byte

	// Unpacks the given packed binary stream.
	unpack(input []byte) []byte
}

func find_longest_match(data []byte, head int, distance int) match {
	best_offset := -1
	best_length := 0
	max_dist := distance
	if head < distance {
		max_dist = head
	}

	for offset := 1; offset <= max_dist; offset += 1 {
		length := 0
		check_pos := head - offset
		for head+length < len(data) && data[check_pos+length] == data[head+length] {
			length += 1
		}
		if length >= 3 && length > best_length {
			best_length = length
			best_offset = offset
		}
	}
	return match{len: best_length, off: best_offset}
}

func find_cheapest_match(enc encoder, data []byte, head int, distance int) match {
	best_match := match{0, 0}
	// Any pack rate of less than 1.0 is automatically useless!
	var best_cost float64 = 1.0
	max_dist := distance
	if head < distance {
		max_dist = head
	}
	for offset := 1; offset <= max_dist; offset += 1 {
		length := 0
		check_pos := head - offset
		for head+length < len(data) && data[check_pos+length] == data[head+length] {
			length += 1
		}
		if length >= 3 {
			m := match{len: length, off: offset}
			mc := float64(enc.cost(0, m)) / float64(length)
			if mc < best_cost {
				best_cost = mc
				best_match = m
			}
		}
	}
	return best_match
}

func pack_register_greedy(enc encoder, data []byte, buffer_size int) []byte {
	var tokens []token

	head := 0
	match_bytes := 0
	lit_bytes := 0

	for head < len(data) {
		//best := find_cheapest_match(enc, data, head, buffer_size)
		best := find_longest_match(data, head, buffer_size)
		if best.len != 0 {
			head += best.len
			tokens = append(tokens, token{true, best.len, best.off})
			match_bytes += best.len
		} else {
			last_index := len(tokens) - 1
			// Literal
			if last_index >= 0 && !tokens[last_index].is_match {
				tokens[last_index].len += 1
			} else {
				tokens = append(tokens, token{false, 1, head})
			}
			head += 1 // literal
			lit_bytes += 1
		}
	}
	fmt.Printf("\tGreedy: Matches %v Literals %v (%.2f%%)\n", match_bytes, lit_bytes,
		percent(match_bytes, lit_bytes+match_bytes))

	return enc.encode(tokens, data)
}

func pack_register_lazy(enc encoder, data []byte, use_cheapest bool, buffer_size int) []byte {
	var tokens []token

	used_match := 0
	used_matchlit := 0
	used_second := 0
	match_bytes := 0
	lit_bytes := 0
	head := 0

	var best0 match
	var best1 match
	for head < len(data) {
		if use_cheapest {
			best0 = find_cheapest_match(enc, data, head, buffer_size)
		} else {
			best0 = find_longest_match(data, head, buffer_size)
		}
		choose_lit := best0.len == 0

		// We have 2 choices really
		// Apply 0 (as a match or a literal)
		// Apply literal 0 (and check the next byte for a match)
		if !choose_lit {
			// See if doing N literals is smaller
			cost0 := enc.cost(0, best0)
			cost_lit := enc.cost(best0.len, match{})
			if cost_lit < cost0 {
				choose_lit = true
				used_matchlit += 1
			}
		}

		if !choose_lit {
			used_match += 1
			// We only need to decide to choose the second match, if both
			// 0 and 1 are matches rather than literals.
			if best0.len != 0 && head+1 < len(data) {
				if use_cheapest {
					best1 = find_cheapest_match(enc, data, head+1, buffer_size)
				} else {
					best1 = find_longest_match(data, head+1, buffer_size)
				}
				if best1.len != 0 {
					cost0 := enc.cost(0, best0)
					cost1 := enc.cost(1, best1)
					rate0 := float32(cost0) / float32(best0.len)
					rate1 := float32(cost1) / float32(1+best1.len)
					if rate1 < rate0 {
						choose_lit = true
						used_match--
						used_second++
					}
				}
			}
		}

		// Add the decision to the token stream,
		// and update the encoder's state so it can update future encoding costs.
		if choose_lit {
			last_index := len(tokens) - 1
			// Literal
			if last_index >= 0 && !tokens[last_index].is_match {
				tokens[last_index].len += 1
			} else {
				tokens = append(tokens, token{false, 1, head})
			}
			//fmt.Println(head, data[head], "Literal len", tokens[len(tokens)-1].length)
			head += 1 // literal
			enc.lit(1)
			lit_bytes++
		} else {
			head += best0.len
			tokens = append(tokens, token{true, best0.len, best0.off})
			used_match += 1
			enc.match(best0)
			match_bytes += best0.len
		}
	}
	fmt.Println("\tLazy: Used match:", used_match, "used matchlit:", used_matchlit,
		"used second", used_second)
	fmt.Printf("\tLazy: Matches %v Literals %v (%.2f%%)\n", match_bytes, lit_bytes,
		percent(match_bytes, lit_bytes+match_bytes))
	return enc.encode(tokens, data)
}

// Pack a YM3 data file and return an encoded array of bytes.
func pack(data []byte) ([]byte, error) {
	// check header
	if data[0] != 'Y' ||
		data[1] != 'M' ||
		data[2] != '3' ||
		data[3] != '!' {
		return empty_arr(), errors.New("not a YM3 file")
	}

	data_size := len(data) - 4
	if data_size%num_regs != 0 {
		return empty_arr(), errors.New("unexpected data size")
	}
	data_size_per_reg := data_size / num_regs
	all_data := make([]packedstream, num_regs)
	buffer_size := 512
	for reg := 0; reg < num_regs; reg += 1 {
		// Split register data
		start_pos := 4 + reg*data_size_per_reg
		reg_data := data[start_pos : start_pos+data_size_per_reg]

		fmt.Println("Packing register", reg)
		// Pack
		enc := encoder_v1{0}
		greedy := pack_register_greedy(&enc, reg_data, buffer_size)
		all_data[reg].data = pack_register_lazy(&enc, reg_data, true, buffer_size)

		packed := &all_data[reg].data
		fmt.Printf("\tLazy size %v Greedy size %v (%+d)\n",
			len(*packed), len(greedy), len(greedy)-len(*packed))

		// Verify by unpacking
		unpacked := enc.unpack(*packed)
		fmt.Println("\tUnpacked length", len(unpacked), " expected length", len(reg_data))
		if !reflect.DeepEqual(reg_data, unpacked) {
			return empty_arr(), errors.New("failed to verify pack<->unpack round trip, there is a bug")
		} else {
			fmt.Println("\tVerify OK")
		}
	}

	// Generate the final data
	output_data := make([]byte, 0)

	// First the header with the offsets...
	var offset int = 4*num_regs + 2

	// Output size in VBLs first
	output_data = enc_word(output_data, uint16(data_size_per_reg))
	for reg := 0; reg < num_regs; reg += 1 {
		output_data = enc_long(output_data, uint32(offset))
		offset += len(all_data[reg].data)
	}

	// ... then the data
	for reg := 0; reg < num_regs; reg += 1 {
		output_data = append(output_data, all_data[reg].data...)
	}

	return output_data, nil
}

func main() {
	argsWithoutProg := os.Args[1:]
	if len(argsWithoutProg) < 2 {
		panic("usage: <inputfile> <outputfile>")
	}

	dat, err := os.ReadFile(argsWithoutProg[0])
	check(err)

	packed_data, err := pack(dat)
	check(err)
	fmt.Printf("Original size: %d Packed size %d (%.2f%%)",
		len(dat), len(packed_data), percent(len(packed_data), len(dat)))

	err = os.WriteFile(argsWithoutProg[1], packed_data, 0644)
	check(err)
}
