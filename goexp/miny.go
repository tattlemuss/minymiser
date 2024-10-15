package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
)

const num_regs = 14

var register_names = [num_regs]string{
	"A period lo",
	"A period hi",
	"B period lo",
	"B period hi",
	"C period lo",
	"C period hi",
	"Noise period",
	"Mixer",
	"A volume",
	"B volume",
	"C volume",
	"Env period lo",
	"Env period hi",
	"Env shape"}

func empty_arr() []byte {
	return make([]byte, 0)
}

func ratio(num int, denom int) float32 {
	if denom == 0 {
		return 0.0
	}
	return float32(num) / float32(denom)
}

func percent(num int, denom int) float32 {
	return 100.0 * ratio(num, denom)
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

// Describes packing config for a whole file
type file_pack_cfg struct {
	cache_size int
	verbose    bool
}

// Describes packing config for a single register stream
type stream_pack_cfg struct {
	buffer_size int
	verbose     bool
}

func add_literals(tokens []token, count int, pos int) []token {
	last_index := len(tokens) - 1
	if last_index >= 0 && !tokens[last_index].is_match {
		tokens[last_index].len++
	} else {
		return append(tokens, token{false, count, pos})
	}
	return tokens
}

func find_longest_match(data []byte, head int, distance int) match {
	best_offset := -1
	best_length := 0
	max_dist := distance
	if head < distance {
		max_dist = head
	}

	for offset := 1; offset <= max_dist; offset++ {
		length := 0
		check_pos := head - offset
		for head+length < len(data) && data[check_pos+length] == data[head+length] {
			length++
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
	for offset := 1; offset <= max_dist; offset++ {
		length := 0
		check_pos := head - offset
		for head+length < len(data) && data[check_pos+length] == data[head+length] {
			length++
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

func pack_register_greedy(enc encoder, data []byte, cfg stream_pack_cfg) []byte {
	var tokens []token

	head := 0
	match_bytes := 0
	lit_bytes := 0

	for head < len(data) {
		//best := find_cheapest_match(enc, data, head, buffer_size)
		best := find_longest_match(data, head, cfg.buffer_size)
		if best.len != 0 {
			head += best.len
			tokens = append(tokens, token{true, best.len, best.off})
			match_bytes += best.len
		} else {
			// Literal
			tokens = add_literals(tokens, 1, head)
			head++ // literal
			lit_bytes++
		}
	}
	if cfg.verbose {
		fmt.Printf("\tGreedy: Matches %v Literals %v (%.2f%%)\n", match_bytes, lit_bytes,
			percent(match_bytes, lit_bytes+match_bytes))
	}
	return enc.encode(tokens, data)
}

func pack_register_lazy(enc encoder, data []byte, use_cheapest bool, cfg stream_pack_cfg) []byte {
	var tokens []token

	used_match := 0
	used_matchlit := 0
	used_second := 0
	match_bytes := 0
	lit_bytes := 0
	head := 0

	var best0 match
	var best1 match
	buffer_size := cfg.buffer_size
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
				used_matchlit++
			}
		}

		if !choose_lit {
			used_match++
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
					rate0 := ratio(cost0, best0.len)
					rate1 := ratio(cost1, 1+best1.len)
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
			// Literal
			tokens = add_literals(tokens, 1, head)
			head++
			enc.lit(1)
			lit_bytes++
		} else {
			head += best0.len
			tokens = append(tokens, token{true, best0.len, best0.off})
			used_match++
			enc.match(best0)
			match_bytes += best0.len
		}
	}

	if cfg.verbose {
		fmt.Printf("\tLazy: Matches %v Literals %v (%.2f%%)\n", match_bytes, lit_bytes,
			percent(match_bytes, lit_bytes+match_bytes))
		fmt.Println("\tLazy: Used match:", used_match, "used matchlit:", used_matchlit,
			"used second", used_second)
	}
	return enc.encode(tokens, data)
}

// Pack a YM3 data file and return an encoded array of bytes.
func pack(data []byte, file_cfg file_pack_cfg) ([]byte, error) {
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

	// Compression settings
	use_cheapest := false

	stream_cfg := stream_pack_cfg{}
	stream_cfg.buffer_size = file_cfg.cache_size / num_regs
	stream_cfg.verbose = file_cfg.verbose

	//scale_deltas := make([]int, num_regs)
	for reg := 0; reg < num_regs; reg++ {
		// Split register data
		start_pos := 4 + reg*data_size_per_reg
		reg_data := data[start_pos : start_pos+data_size_per_reg]

		if file_cfg.verbose {
			fmt.Println("Packing register", reg, register_names[reg])
		}
		// Pack
		enc := encoder_v1{0}
		greedy := pack_register_greedy(&enc, reg_data, stream_cfg)
		packed := &all_data[reg].data
		*packed = pack_register_lazy(&enc, reg_data, use_cheapest, stream_cfg)

		// Experiment to gauge how much a stream benefits from the larger buffer size
		//scale_test := pack_register_lazy(&enc, reg_data, use_cheapest, 128)
		//scale_deltas[reg] = len(scale_test) - len(*packed)
		//fmt.Printf("\t **** Scale delta %d\n", scale_deltas[reg])
		if file_cfg.verbose {
			fmt.Printf("\tLazy size %v Greedy size %v (%+d)\n",
				len(*packed), len(greedy), len(greedy)-len(*packed))
		}

		// Verify by unpacking
		unpacked := enc.unpack(*packed)
		if !reflect.DeepEqual(reg_data, unpacked) {
			return empty_arr(), errors.New("failed to verify pack<->unpack round trip, there is a bug")
		} else {
			if file_cfg.verbose {
				fmt.Println("\tVerify OK")
			}
		}
	}

	// Generate the final data
	output_data := make([]byte, 0)

	// First the header with the offsets...
	var offset int = 4*num_regs + 2

	// Output size in VBLs first
	output_data = enc_word(output_data, uint16(data_size_per_reg))
	for reg := 0; reg < num_regs; reg++ {
		output_data = enc_long(output_data, uint32(offset))
		offset += len(all_data[reg].data)
	}

	// ... then the data
	for reg := 0; reg < num_regs; reg++ {
		output_data = append(output_data, all_data[reg].data...)
	}

	return output_data, nil
}

func pack_file(input_path string, output_path string, file_cfg file_pack_cfg) error {
	dat, err := os.ReadFile(input_path)
	if err != nil {
		return err
	}

	packed_data, err := pack(dat, file_cfg)
	if err != nil {
		return err
	}

	fmt.Printf("Original size: %d Packed size %d (%.2f%%) Total RAM %d",
		len(dat), len(packed_data), percent(len(packed_data), len(dat)),
		file_cfg.cache_size+len(packed_data))

	err = os.WriteFile(output_path, packed_data, 0644)
	return err
}

func main() {
	packCmd := flag.NewFlagSet("pack", flag.ExitOnError)
	packOptSize := packCmd.Int("cachesize", num_regs*512, "overall cache size in bytes")
	packOptVerbose := packCmd.Bool("verbose", false, "verbose output")
	unpackCmd := flag.NewFlagSet("unpack", flag.ExitOnError)

	subcommands := []string{"pack", "unpack"}
	flagsets := []*flag.FlagSet{packCmd, unpackCmd}

	usage := func() {
		fmt.Println()
		fmt.Println("usage: miny <subcommand> [arguments]")
		fmt.Println("  where <subcommand> is one of", subcommands)
		fmt.Println()
		for _, fs := range flagsets {
			fs.Usage()
		}
	}

	if len(os.Args) < 2 {
		fmt.Println("error: expected a subcommand, one of", subcommands)
		usage()
		os.Exit(1)
	}

	pack := func(args []string) {
		packCmd.Parse(args)
		files := packCmd.Args()
		if len(files) != 2 {
			fmt.Println("pack: expected <input> <output> arguments")
			usage()
			os.Exit(1)
		}
		cfg := file_pack_cfg{}
		cfg.cache_size = *packOptSize
		cfg.verbose = *packOptVerbose
		err := pack_file(files[0], files[1], cfg)
		if err != nil {
			fmt.Println("Error in pack_file", err.Error())
			os.Exit(1)
		}
	}

	// This all feels rather clunky...
	switch os.Args[1] {
	case packCmd.Name():
		pack(os.Args[2:])
	case unpackCmd.Name():
		fmt.Println("unpack not currently supported")
		os.Exit(1)
	default:
		fmt.Println("error: unknown subcommand")
		usage()
		os.Exit(1)
	}
}
