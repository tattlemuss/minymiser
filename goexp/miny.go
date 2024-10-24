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

var ym3_header = []byte{'Y', 'M', '3', '!'}

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

// Rpre
type ym_streams struct {
	// A binary array for each register to pack
	register  [num_regs]packedstream
	num_vbls  int
	data_size int
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
	cache_size int // cache size for whole file
	verbose    bool
	verify     bool
	encoder    int // 1 or 2
}

// Describes packing config for a single register stream
type stream_pack_cfg struct {
	buffer_size int // cache size for just this stream
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

func create_ym_streams(data []byte) (ym_streams, error) {
	// check header
	if len(data) < 4 || !reflect.DeepEqual(data[:4], ym3_header) {
		return ym_streams{}, errors.New("not a YM3 file")
	}

	data_size := len(data) - 4
	if data_size%num_regs != 0 {
		return ym_streams{}, errors.New("unexpected data size")
	}
	// Convert to memory types
	data_size_per_reg := data_size / num_regs
	ym3 := ym_streams{}
	ym3.num_vbls = data_size_per_reg
	ym3.data_size = data_size
	for reg := 0; reg < num_regs; reg++ {
		// Split register data
		start_pos := 4 + reg*data_size_per_reg
		ym3.register[reg].data = data[start_pos : start_pos+data_size_per_reg]
	}
	return ym3, nil
}

// Pack a YM3 data file and return an encoded array of bytes.
func pack(ym_data *ym_streams, file_cfg file_pack_cfg) ([]byte, error) {
	// Compression settings
	use_cheapest := false

	stream_cfg := stream_pack_cfg{}
	stream_cfg.buffer_size = file_cfg.cache_size / num_regs
	stream_cfg.verbose = file_cfg.verbose
	packed_streams := make([]packedstream, num_regs)

	for reg := 0; reg < num_regs; reg++ {
		if file_cfg.verbose {
			fmt.Println("Packing register", reg, register_names[reg])
		}
		// Pack
		var enc encoder
		if file_cfg.encoder == 1 {
			enc = &encoder_v1{0}
		} else if file_cfg.encoder == 2 {
			enc = &encoder_v2{0}
		} else {
			return empty_arr(), fmt.Errorf("unknown encoder ID: (%d)", file_cfg.encoder)
		}
		reg_data := ym_data.register[reg].data
		packed := &packed_streams[reg].data
		*packed = pack_register_lazy(enc, reg_data, use_cheapest, stream_cfg)

		// Experiment to gauge how much a stream benefits from the larger buffer size
		//scale_test := pack_register_lazy(&enc, reg_data, use_cheapest, 128)
		//scale_deltas[reg] = len(scale_test) - len(*packed)
		//fmt.Printf("\t **** Scale delta %d\n", scale_deltas[reg])
		//greedy := pack_register_greedy(&enc, reg_data, stream_cfg)
		//if file_cfg.verbose {
		//	fmt.Printf("\tLazy size %v Greedy size %v (%+d)\n",
		//		len(*packed), len(greedy), len(greedy)-len(*packed))
		//}

		// Verify by unpacking
		if file_cfg.verify {
			unpacked := enc.unpack(*packed)
			if !reflect.DeepEqual(reg_data, unpacked) {
				return empty_arr(), errors.New("failed to verify pack<->unpack round trip, there is a bug")
			} else {
				if file_cfg.verbose {
					fmt.Println("\tVerify OK")
				}
			}
		}
	}

	// Generate the final data
	output_data := make([]byte, 0)

	// First the header with the offsets...
	var offset int = 4*num_regs + 2

	// Output size in VBLs first
	output_data = enc_word(output_data, uint16(ym_data.num_vbls))
	for reg := 0; reg < num_regs; reg++ {
		output_data = enc_long(output_data, uint32(offset))
		offset += len(packed_streams[reg].data)
	}

	// ... then the data
	for reg := 0; reg < num_regs; reg++ {
		output_data = append(output_data, packed_streams[reg].data...)
	}

	return output_data, nil
}

func load_ym_stream(input_path string) (ym_streams, error) {
	dat, err := os.ReadFile(input_path)
	if err != nil {
		return ym_streams{}, err
	}

	ym_data, err := create_ym_streams(dat)
	return ym_data, err
}

func pack_file(input_path string, output_path string, file_cfg file_pack_cfg) error {
	ym_data, err := load_ym_stream(input_path)
	if err != nil {
		return err
	}

	packed_data, err := pack(&ym_data, file_cfg)
	if err != nil {
		return err
	}

	fmt.Printf("Original size: %d Packed size %d (%.2f%%) Total RAM %d\n",
		ym_data.data_size, len(packed_data), percent(len(packed_data), ym_data.data_size),
		file_cfg.cache_size+len(packed_data))

	err = os.WriteFile(output_path, packed_data, 0644)
	return err
}

type minpack_result struct {
	cachesize  int
	packedsize int
}

func minpack_find_size(ym_data *ym_streams, min_i int, max_i int, step int) (int, error) {
	messages := make(chan minpack_result, 5)

	// Async func to pack the file and return sizes
	find_packed_size_func := func(ym_data *ym_streams, cfg file_pack_cfg) {
		packed_data, err := pack(ym_data, cfg)
		if err != nil {
			fmt.Println(err)
			messages <- minpack_result{0, 0}
		} else {
			messages <- minpack_result{cfg.cache_size, len(packed_data)}
		}
	}

	// Launch the async packers
	for i := min_i; i <= max_i; i += step {
		cfg := file_pack_cfg{}
		cfg.cache_size = i
		cfg.verbose = false
		cfg.encoder = 1
		go find_packed_size_func(ym_data, cfg)
	}

	// Receive results and find the smallest
	min_cachesize := -1
	min_size := 1 * 1024 * 1024
	for i := min_i; i <= max_i; i += step {
		msg := <-messages

		this_size := msg.packedsize
		total_size := msg.cachesize + this_size
		fmt.Printf("Cache size: %d Packed size: %d Total size: %d\n",
			msg.cachesize, this_size, total_size)

		if total_size < min_size {
			min_size = total_size
			min_cachesize = msg.cachesize
		}
	}
	return min_cachesize, nil
}

func minpack_file(input_path string, output_path string) error {
	ym_data, err := load_ym_stream(input_path)
	if err != nil {
		return err
	}

	fmt.Println("---- Pass 1 ----")
	min_cachesize, err := minpack_find_size(&ym_data, 1024, 16*1024, 1024)
	if err != nil {
		return err
	}

	fmt.Println("---- Pass 2 ----")
	min_cachesize, err = minpack_find_size(&ym_data, min_cachesize-1024,
		min_cachesize+1024, 128)
	if err != nil {
		return err
	}

	file_cfg := file_pack_cfg{}
	file_cfg.verbose = false
	file_cfg.cache_size = min_cachesize
	file_cfg.encoder = 2
	packed_data, err := pack(&ym_data, file_cfg)
	if err != nil {
		return err
	}

	fmt.Println("---- Final output ----")
	fmt.Printf("Original size: %d Packed size %d (%.2f%%) Total RAM %d\n",
		ym_data.data_size, len(packed_data), percent(len(packed_data), ym_data.data_size),
		file_cfg.cache_size+len(packed_data))

	fmt.Printf("Needs a cache size of %d\n", min_cachesize)
	err = os.WriteFile(output_path, packed_data, 0644)
	return err
}

func main() {
	packCmd := flag.NewFlagSet("pack", flag.ExitOnError)
	packOptSize := packCmd.Int("cachesize", num_regs*512, "overall cache size in bytes")
	packOptVerbose := packCmd.Bool("verbose", false, "verbose output")
	packOptEncoder := packCmd.Int("encoder", 1, "encoder version (1|2)")

	unpackCmd := flag.NewFlagSet("unpack", flag.ExitOnError)

	minpackCmd := flag.NewFlagSet("minpack", flag.ExitOnError)

	subcommands := []string{"pack", "unpack", "minpack"}
	flagsets := []*flag.FlagSet{packCmd, unpackCmd, minpackCmd}

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
		cfg.encoder = *packOptEncoder
		err := pack_file(files[0], files[1], cfg)
		if err != nil {
			fmt.Println("Error in pack_file", err.Error())
			os.Exit(1)
		}
	}

	minpack := func(args []string) {
		minpackCmd.Parse(args)
		files := minpackCmd.Args()
		if len(files) != 2 {
			fmt.Println("minpack: expected <input> <output> arguments")
			usage()
			os.Exit(1)
		}
		err := minpack_file(files[0], files[1])
		if err != nil {
			fmt.Println("Error in minpack_file", err.Error())
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
	case minpackCmd.Name():
		minpack(os.Args[2:])
	default:
		fmt.Println("error: unknown subcommand")
		usage()
		os.Exit(1)
	}
}
