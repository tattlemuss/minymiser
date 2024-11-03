package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
)

const num_regs = 14

var register_names = [num_regs]string{
	"A period lo", "A period hi",
	"B period lo", "B period hi",
	"C period lo", "C period hi",
	"Noise period",
	"Mixer",
	"A volume", "B volume", "C volume",
	"Env period lo", "Env period hi",
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
	num_vbls  int // size of each packedstream
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
	cache_sizes []int // cache size for each individual reg
	verbose     bool
	verify      bool
	report      bool // print output report to stdout
	encoder     int  // 1 or 2
}

// Describes packing config for a single register stream
type stream_pack_cfg struct {
	buffer_size int // cache size for just this stream
	verbose     bool
}

func sum(arr []int) int {
	sum := 0
	for _, num := range arr {
		sum += num
	}
	return sum
}

func make_filled(size int, val int) []int {
	arr := make([]int, num_regs)
	for i := 0; i < size; i++ {
		arr[i] = val
	}
	return arr
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

type usage_map struct {
	counts []int // number of backrefs
}

func analyse_usages(data []byte, um *usage_map) {
	last_usage := [256]int{}
	for i := range last_usage {
		last_usage[i] = -1
	}

	for i := range data {
		val := data[i]
		if last_usage[val] >= 0 {
			distance := i - last_usage[val]
			if distance >= 3 && distance < len(um.counts) {
				um.counts[distance]++
			}
		}
		last_usage[val] = i
	}
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

func tokenize_greedy(enc encoder, data []byte, cfg stream_pack_cfg) []byte {
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

func tokensize_lazy(enc encoder, data []byte, use_cheapest bool, cfg stream_pack_cfg) []token {
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

	return tokens
}

// Read the file array and create individual streams for the registers.
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

// Core function to ack a YM3 data file and return an encoded array of bytes.
func pack(ym_data *ym_streams, file_cfg file_pack_cfg) ([]byte, error) {
	// Compression settings
	use_cheapest := false

	stream_cfg := stream_pack_cfg{}
	stream_cfg.verbose = file_cfg.verbose
	packed_streams := make([]packedstream, num_regs)

	x := make([]float64, 0)
	y := make([]float64, 0)
	var stats pack_stats
	stats.len_map = make(map[int]int)
	stats.dist_map = make(map[int]int)

	for reg := 0; reg < num_regs; reg++ {
		stream_cfg.buffer_size = file_cfg.cache_sizes[reg]
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

		analyse_usages(reg_data, &stats.um)
		tokens := tokensize_lazy(enc, reg_data, use_cheapest, stream_cfg)
		*packed = enc.encode(tokens, reg_data)

		// Graph histogram
		for i := range tokens {
			t := &tokens[i]
			if t.is_match {
				if t.len < 512 {
					stats.len_map[t.len]++
				}
				stats.dist_map[t.off]++
				stats.offs = append(stats.offs, t.off)
				stats.lens = append(stats.lens, t.len)
				//litlens = append(litlens, 0)
				x = append(x, float64(t.off))
				y = append(y, float64(t.len))
				stats.num_matches++
			} else {
				stats.litlens = append(stats.litlens, t.len)
			}
		}
		stats.num_tokens += len(tokens)

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
	if false {
		scatter_int_map("len.svg", stats.len_map)
		scatter_int_map("off.svg", stats.dist_map)
		// offset vs length scatter
		xy_plot("scatter.svg", x, y)

		linegraph_int("usage.svg", stats.um.counts)
		sort.Ints(stats.lens)
		sort.Ints(stats.litlens)
		sort.Ints(stats.offs)
		histo("Lens", stats.lens)
		histo("Offs", stats.offs)
		histo("LitLens", stats.litlens)
		fmt.Printf("Lits: %d Matches: %d (%.2f%%)\n", stats.num_tokens-stats.num_matches,
			stats.num_matches, percent(stats.num_matches, stats.num_tokens))
	}

	// Group the registers into sets with the same size
	sets := make(map[int][]int)
	for reg := 0; reg < num_regs; reg++ {
		sets[file_cfg.cache_sizes[reg]] = append(sets[file_cfg.cache_sizes[reg]], reg)
	}

	// Calc mapping of YM reg->stream in the file
	// and generate the header data for them.

	// We will output the registers to the file, ordered by set
	// and flattened.
	reg_order := make([]byte, num_regs)
	// The inverse order is used at runtime to map from YM reg
	// to depacked stream in the file.
	inverse_reg_order := make([]byte, num_regs)

	set_header_data := []byte{}
	var stream_id byte = 0
	for cache_size, set := range sets {
		// Loop
		set_header_data = enc_word(set_header_data, uint16(len(set)-1))
		set_header_data = enc_word(set_header_data, uint16(cache_size))
		for _, reg := range set {
			inverse_reg_order[reg] = stream_id
			reg_order[stream_id] = byte(reg)
			stream_id++
		}
	}
	set_header_data = enc_word(set_header_data, uint16(0xffff))

	// Number of bytes required by the set data
	// 4 bytes per set -- loop count, cache size
	// 2 bytes -- end sentinel
	if len(set_header_data) != 2+(4*len(sets)) {
		panic("header size mismatch")
	}

	// Generate the final data
	output_data := make([]byte, 0)

	// Calc overall header size
	header_size := 2 + num_regs + 4*num_regs + len(set_header_data)

	// 1) Output size in VBLs first
	output_data = enc_word(output_data, uint16(ym_data.num_vbls))

	// 2) Order of registers
	output_data = append(output_data, inverse_reg_order...)

	data_pos := header_size
	// Offsets to register data
	for _, reg := range reg_order {
		output_data = enc_long(output_data, uint32(data_pos))
		data_pos += len(packed_streams[reg].data)
	}
	// Set data
	output_data = append(output_data, set_header_data...)

	if len(output_data) != header_size {
		panic("header size mismatch 2")
	}

	// ... then the data
	for _, reg := range reg_order {
		output_data = append(output_data, packed_streams[reg].data...)
	}

	if file_cfg.report {
		orig_size := ym_data.data_size
		packed_size := len(output_data)
		cache_size := sum(file_cfg.cache_sizes)
		total_size := cache_size + packed_size
		fmt.Println("===== Complete =====")
		fmt.Printf("Original size:    %6d\n", orig_size)
		fmt.Printf("Packed size:      %6d (%.1f%%)\n", packed_size, percent(packed_size, orig_size))
		fmt.Printf("Num cache sizes:  %6d (smaller=faster)\n", len(sets))
		fmt.Printf("Total cache size: %6d\n", cache_size)
		fmt.Printf("Total RAM:        %6d (%.1f%%)\n", total_size, percent(total_size, orig_size))
	}

	return output_data, nil
}

// Load an input file and create the ym_streams data object.
func load_ym_stream(input_path string) (ym_streams, error) {
	dat, err := os.ReadFile(input_path)
	if err != nil {
		return ym_streams{}, err
	}

	ym_data, err := create_ym_streams(dat)
	return ym_data, err
}

// Pack a file with custom config like cache size.
func command_custom(input_path string, output_path string, file_cfg file_pack_cfg) error {
	ym_data, err := load_ym_stream(input_path)
	if err != nil {
		return err
	}

	file_cfg.report = true
	packed_data, err := pack(&ym_data, file_cfg)
	if err != nil {
		return err
	}

	err = os.WriteFile(output_path, packed_data, 0644)
	return err
}

type minpack_result struct {
	regcachesize int
	cachesize    int
	packedsize   int
}

func minpack_find_size(ym_data *ym_streams, min_i int, max_i int, step int, phase string) (int, error) {
	messages := make(chan minpack_result, 5)

	// Async func to pack the file and return sizes
	find_packed_size_func := func(regcachesize int, ym_data *ym_streams, cfg file_pack_cfg) {
		packed_data, err := pack(ym_data, cfg)
		if err != nil {
			fmt.Println(err)
			messages <- minpack_result{0, 0, 0}
		} else {
			messages <- minpack_result{regcachesize, sum(cfg.cache_sizes), len(packed_data)}
		}
	}

	// Launch the async packers
	for i := min_i; i <= max_i; i += step {
		cfg := file_pack_cfg{}
		cfg.cache_sizes = make_filled(num_regs, i)
		cfg.verbose = false
		cfg.encoder = 1
		go find_packed_size_func(i, ym_data, cfg)
	}

	// Receive results and find the smallest
	size_map := make(map[int]int)

	min_cachesize := -1
	min_size := 1 * 1024 * 1024
	fmt.Print("Collecting stats")
	for i := min_i; i <= max_i; i += step {
		msg := <-messages

		this_size := msg.packedsize
		total_size := msg.cachesize + this_size
		fmt.Print(".")
		//fmt.Printf("Cache size: %d Packed size: %d Total size: %d\n",
		//	msg.cachesize, this_size, total_size)

		if total_size < min_size {
			min_size = total_size
			min_cachesize = msg.regcachesize
		}

		size_map[msg.cachesize] = this_size //total_size
	}
	fmt.Println()

	//scatter_int_map(phase+".svg", size_map)
	return min_cachesize, nil
}

func command_quick(input_path string, output_path string) error {
	ym_data, err := load_ym_stream(input_path)
	if err != nil {
		return err
	}

	fmt.Println("---- Pass 1 ----")
	min_cachesize, err := minpack_find_size(&ym_data, 64, 1024, 32, "broad")
	if err != nil {
		return err
	}

	fmt.Println("---- Pass 2 ----")
	min_cachesize, err = minpack_find_size(&ym_data, min_cachesize-32,
		min_cachesize+32, 2, "narrow")
	if err != nil {
		return err
	}

	file_cfg := file_pack_cfg{}
	file_cfg.verbose = false
	file_cfg.cache_sizes = make_filled(num_regs, min_cachesize)
	file_cfg.encoder = 1
	file_cfg.report = true
	packed_data, err := pack(&ym_data, file_cfg)
	if err != nil {
		return err
	}

	err = os.WriteFile(output_path, packed_data, 0644)
	return err
}

type reg_stats struct {
	reg        int
	cache_size int
	total_size int
}

// Records how big each registers packs to, given a cache size
type per_reg_stats struct {
	// key is the cache size, value is array of total_packed_sizes for each reg
	total_packed_sizes map[int]([]int)
}

type pack_stats struct {
	um          usage_map
	len_map     map[int]int
	dist_map    map[int]int
	offs        []int
	lens        []int
	litlens     []int
	num_matches int
	num_tokens  int
}

func find_smallest(stats *per_reg_stats, regs []reg_stats) (int, int) {
	min_total := 99999999999
	min_index := -1

	for key := range stats.total_packed_sizes {
		// Create total cost
		total_cost := 0
		for j := range regs {
			total_cost += stats.total_packed_sizes[key][regs[j].reg]
		}
		if total_cost < min_total {
			min_total = total_cost
			min_index = key
		}
	}
	return min_index, min_total
}

type small_result struct {
	reg        int
	cachesize  int
	packedsize int
}

func command_small(input_path string, output_path string) error {
	ym_data, err := load_ym_stream(input_path)
	if err != nil {
		return err
	}

	csv, err := os.Create("sizes.csv")
	if err != nil {
		return err
	}
	stats := per_reg_stats{}
	stats.total_packed_sizes = make(map[int][]int)
	min_size := 8
	max_size := 1024
	step := 16
	for size := min_size; size < max_size; size += step {
		stats.total_packed_sizes[size] = make([]int, num_regs)
	}

	messages := make(chan small_result, 1)

	// Async func to pack the file and return sizes
	find_packed_size_func := func(reg int, regcachesize int, ym_data *ym_streams) {
		enc := encoder_v1{0}
		var cfg stream_pack_cfg
		cfg.buffer_size = regcachesize
		cfg.verbose = false
		reg_data := ym_data.register[reg].data
		tokens := tokensize_lazy(&enc, reg_data, true, cfg)
		packed_data := enc.encode(tokens, reg_data)
		if err != nil {
			fmt.Println(err)
			messages <- small_result{reg, 0, 0}
		} else {
			messages <- small_result{reg, regcachesize, len(packed_data)}
		}
	}

	fmt.Print("Collecting stats")
	for reg := 0; reg < num_regs; reg++ {
		fmt.Print(".")

		// Launch...
		for size := min_size; size < max_size; size += step {
			go find_packed_size_func(reg, size, &ym_data)
		}

		// ... Collect.
		for size := min_size; size < max_size; size += step {
			msg := <-messages
			csize := msg.cachesize
			total := msg.cachesize + msg.packedsize
			stats.total_packed_sizes[csize][reg] = total
		}

	}
	fmt.Println()

	best_total_size := 0
	best_cache_size := 0

	stats_for_regs := make([]reg_stats, 0)
	fmt.Fprintf(csv, "\nBest sizes per register\n")
	var minimal_cfg file_pack_cfg
	minimal_cfg.encoder = 1
	minimal_cfg.verbose = false
	minimal_cfg.verify = false
	minimal_cfg.report = true
	minimal_cfg.cache_sizes = make([]int, num_regs)

	for reg := 0; reg < num_regs; reg++ {
		fmt.Fprintf(csv, "Reg %d %s,", reg, register_names[reg])
		min_total := 9999999
		min_cache := min_total

		for csize := range stats.total_packed_sizes {
			total := stats.total_packed_sizes[csize][reg]
			fmt.Fprintf(csv, "%d,", total)
			if total < min_total {
				min_total = total
				min_cache = csize
			}
			fmt.Fprintf(csv, "\n")
		}
		best_total_size += min_total
		best_cache_size += min_cache
		stats_for_regs = append(stats_for_regs, reg_stats{reg, min_cache, min_total})

		minimal_cfg.cache_sizes[reg] = min_cache
	}

	sort.Slice(stats_for_regs, func(i, j int) bool {
		if stats_for_regs[i].cache_size != stats_for_regs[j].cache_size {
			return stats_for_regs[i].cache_size < stats_for_regs[j].cache_size
		}
		return stats_for_regs[i].total_size < stats_for_regs[j].total_size
	})

	// Now we can grade the streams based on who needs the biggest cache
	for i := 0; i < num_regs; i++ {
		reg := stats_for_regs[i].reg
		fmt.Fprintf(csv, "Reg %d %s,", reg, register_names[reg])
		fmt.Fprintf(csv, "%d,%d\n", stats_for_regs[i].total_size, stats_for_regs[i].cache_size)

		fmt.Printf("Reg %2d Needs cache %4d -> Total size %5d (%s)\n", reg, stats_for_regs[i].cache_size,
			stats_for_regs[i].total_size,
			register_names[reg])
	}

	csv.Close()

	// Write out a final minimal file
	min_file, _ := pack(&ym_data, minimal_cfg)
	err = os.WriteFile(output_path, min_file, 0644)
	if err != nil {
		return err
	}

	if false {
		for split := 1; split < num_regs-1; split++ {
			// Make 2 lists, the "small" list and the big one
			small_list := stats_for_regs[:split]
			big_list := stats_for_regs[split:]

			small_cache, small_total := find_smallest(&stats, small_list)
			big_cache, big_total := find_smallest(&stats, big_list)

			final_size := small_total + big_total
			fmt.Printf("total size with caches %d/%d -> %d (loss %d)\n",
				small_cache, big_cache, final_size, best_total_size-final_size)
		}
	}

	return nil
}

type command struct {
	fn       func(args []string) error
	flagset  *flag.FlagSet
	argsdesc string // argument description
	desc     string
}

func cmd_usage(name string, cmd command) {
	fmt.Printf("%s %s - %s\n", name, cmd.argsdesc, cmd.desc)
	fs := cmd.flagset
	var count int = 0
	fs.VisitAll(func(_ *flag.Flag) {
		count++
	})
	if count != 0 {
		fs.Usage()
	}
}

func usage(commands map[string]command) {
	fmt.Println()
	fmt.Println("Usage: miny <command> [arguments]")
	fmt.Println("Commands available:")

	names := []string{}
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		cmd := commands[name]
		fmt.Printf("    %-10s %s\n", name, cmd.desc)
	}
}

func main() {
	pack_flags := flag.NewFlagSet("pack", flag.ExitOnError)
	//unpackCmd := flag.NewFlagSet("unpack", flag.ExitOnError)
	quick_flags := flag.NewFlagSet("minpack", flag.ExitOnError)
	small_flags := flag.NewFlagSet("smallest", flag.ExitOnError)
	help_flags := flag.NewFlagSet("help", flag.ExitOnError)

	packOptSize := pack_flags.Int("cachesize", num_regs*512, "overall cache size in bytes")
	packOptVerbose := pack_flags.Bool("verbose", false, "verbose output")
	packOptEncoder := pack_flags.Int("encoder", 1, "encoder version (1|2)")
	var commands map[string]command

	cmd_pack := func(args []string) error {
		pack_flags.Parse(args)
		files := pack_flags.Args()
		if len(files) != 2 {
			fmt.Println("pack: expected <input> <output> arguments")
			os.Exit(1)
		}
		cfg := file_pack_cfg{}
		cfg.cache_sizes = make_filled(num_regs, *packOptSize/num_regs)
		cfg.verbose = *packOptVerbose
		cfg.encoder = *packOptEncoder
		return command_custom(files[0], files[1], cfg)
	}

	cmd_quick := func(args []string) error {
		quick_flags.Parse(args)
		files := quick_flags.Args()
		if len(files) != 2 {
			fmt.Println("minpack: expected <input> <output> arguments")
			os.Exit(1)
		}
		return command_quick(files[0], files[1])
	}

	cmd_small := func(args []string) error {
		small_flags.Parse(args)
		files := small_flags.Args()
		if len(files) != 2 {
			fmt.Println("minpack: expected <input> <output> arguments")
			os.Exit(1)
		}
		return command_small(files[0], files[1])
	}

	cmd_help := func(args []string) error {
		help_flags.Parse(args)
		names := help_flags.Args()
		if len(names) > 0 {
			cmd, pres := commands[names[0]]
			if !pres {
				fmt.Println("error: unknown command for help")
				usage(commands)
				os.Exit(1)
			}
			cmd_usage(names[0], cmd)
		} else {
			usage(commands)
		}
		return nil
	}

	commands = map[string]command{
		"pack": {cmd_pack, pack_flags, "<input> <output>", "pack with custom settings"},
		//"unpack":   {nil, unpackCmd, "<input> <output>", "unpack to YM3 format (TBD)"},
		"quick": {cmd_quick, quick_flags, "<input> <output>", "pack to small with quick runtime"},
		"small": {cmd_small, small_flags, "<input> <output>", "pack to smallest runtime memory (more CPU)"},
		"help":  {cmd_help, help_flags, "", "list commands or describe a single command"},
	}

	if len(os.Args) < 2 {
		fmt.Println("error: expected a command")
		usage(commands)
		os.Exit(1)
	}

	// This all feels rather clunky...
	cmd, pres := commands[os.Args[1]]
	if !pres {
		fmt.Println("error: unknown command")
		usage(commands)
		os.Exit(1)
	}

	err := cmd.fn(os.Args[2:])
	if err != nil {
		fmt.Println("Error in pack_file", err.Error())
		os.Exit(1)
	}
}
