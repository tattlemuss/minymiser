package main

import (
	"errors"
	"fmt"
	"os"
)

const num_regs = 14
const buffer_size = 512

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func empty_arr() []byte {
	return make([]byte, 0)
}

func enc_byte(output []byte, value byte) []byte {
	return append(output, value)
}

func enc_word(output []byte, value uint16) []byte {
	output = append(output, byte(value>>8))
	return append(output, byte(value&255))
}

func enc_long(output []byte, value uint32) []byte {
	output = append(output, byte(value>>24)&255)
	output = append(output, byte(value>>16)&255)
	output = append(output, byte(value>>8)&255)
	return append(output, byte(value&255))
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
	// Encodes all the tokens into a binary stream.
	encode(tokens []token, input []byte) []byte
	// Calculate the cost for adding literals or matches
	cost(lit_count int, m match) int
	// Calculate the cost of just a match
	match_cost(m match) int
	// Apply N literals
	lit(lit_count int)
	// Apply a match
	match(m match)
}

type encoder_v1 struct {
	num_literals int
}

func encode_count(output []byte, count int, literal_flag byte) []byte {
	if count < 128 {
		output = append(output, byte(count)|literal_flag)
	} else {
		output = append(output, 0|literal_flag)
		output = enc_word(output, uint16(count))
	}
	return output
}

func encode_offset(output []byte, offset int) []byte {
	if offset < 256 {
		output = append(output, byte(offset))
	} else {
		output = append(output, byte(0))
		output = enc_word(output, uint16(offset))
	}
	return output
}

func (e encoder_v1) encode(tokens []token, input []byte) []byte {
	output := make([]byte, 0)
	for i := 0; i < len(tokens); i += 1 {
		var t token = tokens[i]
		if t.is_match {
			output = encode_count(output, t.len, 0)
			output = encode_offset(output, t.off)
		} else {
			// Encode the literal
			output = encode_count(output, t.len, 0x80)
			literals := input[t.off : t.off+t.len]
			// https://github.com/golang/go/issues/28292
			output = append(output, literals...)
		}
	}
	return output
}

// Return the additional cost (in bytes) of adding literal(s) and match to an output stream
func (e encoder_v1) cost(lit_count int, m match) int {
	cost := 0
	tmp_literals := e.num_literals
	cost += lit_count

	// Check if literal count will increase cost
	for i := 0; i < lit_count; i += 1 {
		if tmp_literals == 0 {
			cost += 1 //  // cost of switching match->list
		} else if tmp_literals == 127 {
			// cost of swtiching to extra-byte encoing
			cost += 2 // needs 2 extra bytes
		}
		tmp_literals += 1
	}

	cost += e.match_cost(m)
	return cost
}

func (e encoder_v1) match_cost(m match) int {
	cost := 0
	// Match
	// A match is always new, so apply full cost
	if m.len > 0 {
		// length encoding
		cost = 1
		if m.len >= 128 {
			cost += 2
		}
		// distance encoding
		cost += 1
		if m.off >= 256 {
			cost += 2
		}
	}
	return cost
}

func (e encoder_v1) lit(lit_count int) {
	e.num_literals += lit_count
}

func (e encoder_v1) match(m match) {
	e.num_literals = 0
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
		if length >= 2 && length > best_length {
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
			mc := float64(enc.match_cost(m)) / float64(length)
			if mc < best_cost {
				best_cost = mc
				best_match = m
			}
		}
	}
	return best_match
}

func pack_register_greedy(enc encoder, data []byte) []byte {
	var tokens []token

	head := 0
	lit_count := 0
	match_count := 0
	match_bytes := 0

	for head < len(data) {
		best := find_cheapest_match(enc, data, head, buffer_size)
		//match := find_longest_match(data, head, buffer_size)
		if best.len != 0 {
			head += best.len
			tokens = append(tokens, token{true, best.len, best.off})
			match_count += 1
			match_bytes += best.len
		} else {
			last_index := len(tokens) - 1
			// Literal
			if last_index >= 0 && tokens[last_index].is_match == false {
				tokens[last_index].len += 1
			} else {
				tokens = append(tokens, token{false, 1, head})
			}
			head += 1 // literal
			lit_count += 1
		}
	}
	fmt.Printf("Matches %v Literals %v (%f%%)\n", match_count, lit_count,
		float32(match_count)*100.0/(lit_count+match_count))

	return enc.encode(tokens, data)
}

func pack_register_lazy(enc encoder, data []byte) []byte {
	var tokens []token

	used_match := 0
	used_matchlit := 0
	used_second := 0
	head := 0

	for head < len(data) {
		//best0 := find_longest_match(data, head, buffer_size)
		best0 := find_cheapest_match(enc, data, head, buffer_size)
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
				//best1 := find_longest_match(data, head+1, buffer_size)
				best1 := find_cheapest_match(enc, data, head+1, buffer_size)
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
			if last_index >= 0 && tokens[last_index].is_match == false {
				tokens[last_index].len += 1
			} else {
				tokens = append(tokens, token{false, 1, head})
			}
			//fmt.Println(head, data[head], "Literal len", tokens[len(tokens)-1].length)
			head += 1 // literal
			enc.lit(1)
		} else {
			head += best0.len
			tokens = append(tokens, token{true, best0.len, best0.off})
			used_match += 1
			enc.match(best0)
		}
	}
	//fmt.Println("Used match:", used_match, "used matchlit:", used_matchlit,
	//	"used second", used_second)

	return enc.encode(tokens, data)
}

func pack(data []byte) ([]byte, error) {
	// check header
	if data[0] != 'Y' ||
		data[1] != 'M' ||
		data[2] != '3' ||
		data[3] != '!' {
		return empty_arr(), errors.New("Not a YM3 file")
	}

	data_size := len(data) - 4
	if data_size%num_regs != 0 {
		return empty_arr(), errors.New("Unexpected data size")
	}
	data_size_per_reg := data_size / num_regs
	all_data := make([]packedstream, num_regs)
	for reg := 0; reg < num_regs; reg += 1 {
		// Split register data
		reg_data := make([]byte, data_size_per_reg)
		for i := 0; i < data_size_per_reg; i += 1 {
			src_i := 4 + reg*data_size_per_reg + i
			reg_data[i] = data[src_i]
		}
		enc := encoder_v1{0}
		greedy := pack_register_greedy(enc, reg_data)
		all_data[reg].data = pack_register_lazy(enc, reg_data)
		fmt.Println("reg", reg, "Packed length", len(all_data[reg].data), "Greedy", len(greedy))
	}

	// Generate the final data
	output_data := make([]byte, 0)

	// First the header with the offsets...
	var offset int = 4 * num_regs
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
	fmt.Println("File size:", len(dat))

	packed_data, err := pack(dat)
	check(err)
	fmt.Println("Packed size:", len(packed_data))

	err = os.WriteFile(argsWithoutProg[1], packed_data, 0644)
	check(err)
}
