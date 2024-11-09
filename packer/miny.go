package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
)

const numRegs = 14

var registerNames = [numRegs]string{
	"A period lo", "A period hi",
	"B period lo", "B period hi",
	"C period lo", "C period hi",
	"Noise period",
	"Mixer",
	"A volume", "B volume", "C volume",
	"Env period lo", "Env period hi",
	"Env shape"}

var ym3Header = []byte{'Y', 'M', '3', '!'}

// Create a byte slice of size 0
func EmptySlice() []byte {
	return make([]byte, 0)
}

func FilledSlice(size int, val int) []int {
	arr := make([]int, numRegs)
	for i := 0; i < size; i++ {
		arr[i] = val
	}
	return arr
}

func Ratio(num int, denom int) float32 {
	if denom == 0 {
		return 0.0
	}
	return float32(num) / float32(denom)
}

func Percent(num int, denom int) float32 {
	return 100.0 * Ratio(num, denom)
}

func Sum(arr []int) int {
	sum := 0
	for _, num := range arr {
		sum += num
	}
	return sum
}

// Describes a match or a series of literals.
type Token struct {
	is_match bool
	len      int // length in bytes
	off      int // reverse offset if is_match, abs position if literal
}

// Describes a Match run.
type Match struct {
	len int
	off int
}

// Contains a single stream of packed data.
type PackedData struct {
	data []byte
}

// Raw unpacked data for all the registers, plus tune length.
type YmStreams struct {
	// A binary array for each register to pack
	register  [numRegs]PackedData
	num_vbls  int // size of each packedstream
	data_size int // sum of sizes of all register arrays
}

// Interface for being able to encode a stream into a packed format.
type Encoder interface {
	// Calculate the cost for adding literals or matches, or both
	cost(lit_count int, m Match) int

	// Apply N literals to the internal state
	lit(lit_count int)
	// Apply a match to the internal state
	match(m Match)

	// Encodes all the given tokens into a binary stream.
	encode(tokens []Token, input []byte) []byte

	// Unpacks the given packed binary stream.
	unpack(input []byte) []byte
}

// Describes packing config for a whole file
type FilePackConfig struct {
	cache_sizes []int // cache size for each individual reg
	verbose     bool
	verify      bool
	report      bool // print output report to stdout
	encoder     int  // 1 or 2
}

// Describes packing config for a single register stream
type StreamPackCfg struct {
	buffer_size int // cache size for just this stream
	verbose     bool
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

func FindLongestMatch(data []byte, head int, distance int) Match {
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
	return Match{len: best_length, off: best_offset}
}

func FindCheapestMatch(enc Encoder, data []byte, head int, distance int) Match {
	best_match := Match{0, 0}
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
			m := Match{len: length, off: offset}
			mc := float64(enc.cost(0, m)) / float64(length)
			if mc < best_cost {
				best_cost = mc
				best_match = m
			}
		}
	}
	return best_match
}

// Add a number of literals to a set of tokens.
// If the last entry was a match, create a new token to
// represent them
func AddLiterals(tokens []Token, count int, pos int) []Token {
	last_index := len(tokens) - 1
	if last_index >= 0 && !tokens[last_index].is_match {
		tokens[last_index].len++
	} else {
		return append(tokens, Token{false, count, pos})
	}
	return tokens
}

func TokenizeGreedy(enc Encoder, data []byte, cfg StreamPackCfg) []byte {
	var tokens []Token

	head := 0
	match_bytes := 0
	lit_bytes := 0

	for head < len(data) {
		//best := find_cheapest_match(enc, data, head, buffer_size)
		best := FindLongestMatch(data, head, cfg.buffer_size)
		if best.len != 0 {
			head += best.len
			tokens = append(tokens, Token{true, best.len, best.off})
			match_bytes += best.len
		} else {
			// Literal
			tokens = AddLiterals(tokens, 1, head)
			head++ // literal
			lit_bytes++
		}
	}
	if cfg.verbose {
		fmt.Printf("\tGreedy: Matches %v Literals %v (%.2f%%)\n", match_bytes, lit_bytes,
			Percent(match_bytes, lit_bytes+match_bytes))
	}
	return enc.encode(tokens, data)
}

func TokenizeLazy(enc Encoder, data []byte, use_cheapest bool, cfg StreamPackCfg) []Token {
	var tokens []Token

	used_match := 0
	used_matchlit := 0
	used_second := 0
	match_bytes := 0
	lit_bytes := 0
	head := 0

	var best0 Match
	var best1 Match
	buffer_size := cfg.buffer_size
	for head < len(data) {
		if use_cheapest {
			best0 = FindCheapestMatch(enc, data, head, buffer_size)
		} else {
			best0 = FindLongestMatch(data, head, buffer_size)
		}
		choose_lit := best0.len == 0

		// We have 2 choices really
		// Apply 0 (as a match or a literal)
		// Apply literal 0 (and check the next byte for a match)
		if !choose_lit {
			// See if doing N literals is smaller
			cost0 := enc.cost(0, best0)
			cost_lit := enc.cost(best0.len, Match{})
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
					best1 = FindCheapestMatch(enc, data, head+1, buffer_size)
				} else {
					best1 = FindLongestMatch(data, head+1, buffer_size)
				}
				if best1.len != 0 {
					cost0 := enc.cost(0, best0)
					cost1 := enc.cost(1, best1)
					rate0 := Ratio(cost0, best0.len)
					rate1 := Ratio(cost1, 1+best1.len)
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
			tokens = AddLiterals(tokens, 1, head)
			head++
			enc.lit(1)
			lit_bytes++
		} else {
			head += best0.len
			tokens = append(tokens, Token{true, best0.len, best0.off})
			used_match++
			enc.match(best0)
			match_bytes += best0.len
		}
	}

	if cfg.verbose {
		fmt.Printf("\tLazy: Matches %v Literals %v (%.2f%%)\n", match_bytes, lit_bytes,
			Percent(match_bytes, lit_bytes+match_bytes))
		fmt.Println("\tLazy: Used match:", used_match, "used matchlit:", used_matchlit,
			"used second", used_second)
	}

	return tokens
}

// Split the file data array and create individual streams for the registers.
func CreateYmStreams(data []byte) (YmStreams, error) {
	// check header
	if len(data) < 4 || !reflect.DeepEqual(data[:4], ym3Header) {
		return YmStreams{}, errors.New("not a YM3 file")
	}

	data_size := len(data) - 4
	if data_size%numRegs != 0 {
		return YmStreams{}, errors.New("unexpected data size")
	}
	// Convert to memory types
	data_size_per_reg := data_size / numRegs
	ym3 := YmStreams{}
	ym3.num_vbls = data_size_per_reg
	ym3.data_size = data_size
	for reg := 0; reg < numRegs; reg++ {
		// Split register data
		start_pos := 4 + reg*data_size_per_reg
		ym3.register[reg].data = data[start_pos : start_pos+data_size_per_reg]
	}
	return ym3, nil
}

// Load an input file and create the ym_streams data object.
func LoadStreamFile(input_path string) (YmStreams, error) {
	dat, err := os.ReadFile(input_path)
	if err != nil {
		return YmStreams{}, err
	}

	ym_data, err := CreateYmStreams(dat)
	return ym_data, err
}

// General packing statistics
type PackStats struct {
	um          usage_map
	len_map     map[int]int
	dist_map    map[int]int
	offs        []int
	lens        []int
	litlens     []int
	num_matches int
	num_tokens  int
}

// Core function to ack a YM3 data file and return an encoded array of bytes.
func PackAll(ym_data *YmStreams, file_cfg FilePackConfig) ([]byte, error) {
	// Compression settings
	use_cheapest := false

	stream_cfg := StreamPackCfg{}
	stream_cfg.verbose = file_cfg.verbose
	packed_streams := make([]PackedData, numRegs)

	var stats PackStats
	stats.len_map = make(map[int]int)
	stats.dist_map = make(map[int]int)

	for reg := 0; reg < numRegs; reg++ {
		stream_cfg.buffer_size = file_cfg.cache_sizes[reg]
		if file_cfg.verbose {
			fmt.Println("Packing register", reg, registerNames[reg])
		}
		// Pack
		var enc Encoder
		if file_cfg.encoder == 1 {
			enc = &encoder_v1{0}
		} else if file_cfg.encoder == 2 {
			enc = &encoder_v2{0}
		} else {
			return EmptySlice(), fmt.Errorf("unknown encoder ID: (%d)", file_cfg.encoder)
		}
		reg_data := ym_data.register[reg].data
		packed := &packed_streams[reg].data

		analyse_usages(reg_data, &stats.um)
		tokens := TokenizeLazy(enc, reg_data, use_cheapest, stream_cfg)
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
				return EmptySlice(), errors.New("failed to verify pack<->unpack round trip, there is a bug")
			} else {
				if file_cfg.verbose {
					fmt.Println("\tVerify OK")
				}
			}
		}
	}

	// Group the registers into sets with the same size
	sets := make(map[int][]int)
	for reg := 0; reg < numRegs; reg++ {
		sets[file_cfg.cache_sizes[reg]] = append(sets[file_cfg.cache_sizes[reg]], reg)
	}

	// Calc mapping of YM reg->stream in the file
	// and generate the header data for them.

	// We will output the registers to the file, ordered by set
	// and flattened.
	reg_order := make([]byte, numRegs)
	// The inverse order is used at runtime to map from YM reg
	// to depacked stream in the file.
	inverse_reg_order := make([]byte, numRegs)

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
	header_size := 2 + // header
		2 + // cache size
		2 + // num vbls
		numRegs + // register order
		4*numRegs + // offsets to packed streams
		len(set_header_data) // set information

	// Header: "Y" + 0x1 (version)
	output_data = enc_byte(output_data, 'Y')
	output_data = enc_byte(output_data, 0x1)

	// 0) Output required cache size (for user reference)
	output_data = enc_word(output_data, uint16(Sum(file_cfg.cache_sizes)))

	// 1) Output size in VBLs
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
		cache_size := Sum(file_cfg.cache_sizes)
		total_size := cache_size + packed_size
		fmt.Println("===== Complete =====")
		fmt.Printf("Original size:    %6d\n", orig_size)
		fmt.Printf("Packed size:      %6d (%.1f%%)\n", packed_size, Percent(packed_size, orig_size))
		fmt.Printf("Num cache sizes:  %6d (smaller=faster)\n", len(sets))
		fmt.Printf("Total cache size: %6d\n", cache_size)
		fmt.Printf("Total RAM:        %6d (%.1f%%)\n", total_size, Percent(total_size, orig_size))
	}

	return output_data, nil
}

// Pack a file with custom config like cache size.
func CommandCustom(input_path string, output_path string, file_cfg FilePackConfig) error {
	ym_data, err := LoadStreamFile(input_path)
	if err != nil {
		return err
	}

	file_cfg.report = true
	packed_data, err := PackAll(&ym_data, file_cfg)
	if err != nil {
		return err
	}

	err = os.WriteFile(output_path, packed_data, 0644)
	return err
}

type MinpackResult struct {
	regcachesize int
	cachesize    int
	packedsize   int
}

// Choose the cache size which gives minimal sum of
// [packed file size] + [cache size]
func MinpackFindCacheSize(ym_data *YmStreams, min_i int, max_i int, step int, phase string) (int, error) {
	messages := make(chan MinpackResult, 5)

	// Async func to pack the file and return sizes
	find_packed_size_func := func(regcachesize int, ym_data *YmStreams, cfg FilePackConfig) {
		packed_data, err := PackAll(ym_data, cfg)
		if err != nil {
			fmt.Println(err)
			messages <- MinpackResult{0, 0, 0}
		} else {
			messages <- MinpackResult{regcachesize, Sum(cfg.cache_sizes), len(packed_data)}
		}
	}

	// Launch the async packers
	for i := min_i; i <= max_i; i += step {
		cfg := FilePackConfig{}
		cfg.cache_sizes = FilledSlice(numRegs, i)
		cfg.verbose = false
		cfg.encoder = 1
		go find_packed_size_func(i, ym_data, cfg)
	}

	// Receive results and find the smallest
	size_map := make(map[int]int)

	min_cachesize := -1
	min_size := 1 * 1024 * 1024
	fmt.Printf("Collecting stats (%s)", phase)
	for i := min_i; i <= max_i; i += step {
		msg := <-messages

		this_size := msg.packedsize
		total_size := msg.cachesize + this_size
		fmt.Print(".")
		if total_size < min_size {
			min_size = total_size
			min_cachesize = msg.regcachesize
		}

		size_map[msg.cachesize] = this_size //total_size
	}
	fmt.Println()
	return min_cachesize, nil
}

// Pack file to be played back with low CPU (single cache size for
// all registers)
func CommandQuick(input_path string, output_path string) error {
	ym_data, err := LoadStreamFile(input_path)
	if err != nil {
		return err
	}

	fmt.Println("---- Pass 1 ----")
	min_cachesize, err := MinpackFindCacheSize(&ym_data, 64, 1024, 32, "broad")
	if err != nil {
		return err
	}

	fmt.Println("---- Pass 2 ----")
	min_cachesize, err = MinpackFindCacheSize(&ym_data, min_cachesize-32,
		min_cachesize+32, 2, "narrow")
	if err != nil {
		return err
	}

	file_cfg := FilePackConfig{}
	file_cfg.verbose = false
	file_cfg.cache_sizes = FilledSlice(numRegs, min_cachesize)
	file_cfg.encoder = 1
	file_cfg.report = true
	packed_data, err := PackAll(&ym_data, file_cfg)
	if err != nil {
		return err
	}

	err = os.WriteFile(output_path, packed_data, 0644)
	return err
}

// Records resulting size for a pack of a single register stream, for
// a given cache size.
type RegPackSizes struct {
	reg        int
	cache_size int
	total_size int
}

// Records how big every register packs to, given a seris of cache sizes.
type PerRegStats struct {
	// key is the cache size, value is array of total_packed_sizes for each reg
	total_packed_sizes map[int]([]int)
}

func FindSmallestTotalSize(stats *PerRegStats, regs []RegPackSizes) (int, int) {
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

// Result for a single attempt at packing a register stream with
// a given cache size.
// TODO share with RegPackSizes?
type SmallResult struct {
	reg        int
	cachesize  int
	packedsize int
}

func CommandSmall(input_path string, output_path string) error {
	ym_data, err := LoadStreamFile(input_path)
	if err != nil {
		return err
	}

	stats := PerRegStats{}
	stats.total_packed_sizes = make(map[int][]int)
	min_size := 8
	max_size := 1024
	step := 16
	for size := min_size; size < max_size; size += step {
		stats.total_packed_sizes[size] = make([]int, numRegs)
	}

	messages := make(chan SmallResult, 15)

	// Async func to pack the file and return sizes
	find_packed_size_func := func(reg int, regcachesize int, ym_data *YmStreams) {
		enc := encoder_v1{0}
		var cfg StreamPackCfg
		cfg.buffer_size = regcachesize
		cfg.verbose = false
		reg_data := ym_data.register[reg].data
		tokens := TokenizeLazy(&enc, reg_data, true, cfg)
		packed_data := enc.encode(tokens, reg_data)
		if err != nil {
			fmt.Println(err)
			messages <- SmallResult{reg, 0, 0}
		} else {
			messages <- SmallResult{reg, regcachesize, len(packed_data)}
		}
	}

	fmt.Print("Collecting stats")
	for reg := 0; reg < numRegs; reg++ {
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

	stats_for_regs := make([]RegPackSizes, 0)
	var minimal_cfg FilePackConfig
	minimal_cfg.encoder = 1
	minimal_cfg.verbose = false
	minimal_cfg.verify = false
	minimal_cfg.report = true
	minimal_cfg.cache_sizes = make([]int, numRegs)

	for reg := 0; reg < numRegs; reg++ {
		min_total := 9999999
		min_cache := min_total

		for csize := range stats.total_packed_sizes {
			total := stats.total_packed_sizes[csize][reg]
			if total < min_total {
				min_total = total
				min_cache = csize
			}
		}
		best_total_size += min_total
		best_cache_size += min_cache
		stats_for_regs = append(stats_for_regs, RegPackSizes{reg, min_cache, min_total})

		minimal_cfg.cache_sizes[reg] = min_cache
	}

	sort.Slice(stats_for_regs, func(i, j int) bool {
		if stats_for_regs[i].cache_size != stats_for_regs[j].cache_size {
			return stats_for_regs[i].cache_size < stats_for_regs[j].cache_size
		}
		return stats_for_regs[i].total_size < stats_for_regs[j].total_size
	})

	// Now we can grade the streams based on who needs the biggest cache
	for i := 0; i < numRegs; i++ {
		reg := stats_for_regs[i].reg
		fmt.Printf("Reg %2d Needs cache %4d -> Total size %5d (%s)\n", reg, stats_for_regs[i].cache_size,
			stats_for_regs[i].total_size,
			registerNames[reg])
	}

	// Write out a final minimal file
	min_file, _ := PackAll(&ym_data, minimal_cfg)
	err = os.WriteFile(output_path, min_file, 0644)
	if err != nil {
		return err
	}

	if false {
		// This is experimental code to force a pack with
		// 2 cache sizes only.
		for split := 1; split < numRegs-1; split++ {
			// Make 2 lists, the "small" list and the big one
			small_list := stats_for_regs[:split]
			big_list := stats_for_regs[split:]

			small_cache, small_total := FindSmallestTotalSize(&stats, small_list)
			big_cache, big_total := FindSmallestTotalSize(&stats, big_list)

			final_size := small_total + big_total
			fmt.Printf("total size with caches %d/%d -> %d (loss %d)\n",
				small_cache, big_cache, final_size, best_total_size-final_size)
		}
	}

	return nil
}

type CliCommand struct {
	fn       func(args []string) error
	flagset  *flag.FlagSet
	argsdesc string // argument description
	desc     string
}

// Describes how to use a given command.
func PrintCmdUsage(name string, cmd CliCommand) {
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

func PrintUsage(commands map[string]CliCommand) {
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

	packOptSize := pack_flags.Int("cachesize", numRegs*512, "overall cache size in bytes")
	packOptVerbose := pack_flags.Bool("verbose", false, "verbose output")
	packOptEncoder := pack_flags.Int("encoder", 1, "encoder version (1|2)")
	var commands map[string]CliCommand

	cmd_pack := func(args []string) error {
		pack_flags.Parse(args)
		files := pack_flags.Args()
		if len(files) != 2 {
			fmt.Println("'pack' command: expected <input> <output> arguments")
			os.Exit(1)
		}
		cfg := FilePackConfig{}
		cfg.cache_sizes = FilledSlice(numRegs, *packOptSize/numRegs)
		cfg.verbose = *packOptVerbose
		cfg.encoder = *packOptEncoder
		return CommandCustom(files[0], files[1], cfg)
	}

	cmd_quick := func(args []string) error {
		quick_flags.Parse(args)
		files := quick_flags.Args()
		if len(files) != 2 {
			fmt.Println("'quick' command: expected <input> <output> arguments")
			os.Exit(1)
		}
		return CommandQuick(files[0], files[1])
	}

	cmd_small := func(args []string) error {
		small_flags.Parse(args)
		files := small_flags.Args()
		if len(files) != 2 {
			fmt.Println("'small' command: expected <input> <output> arguments")
			os.Exit(1)
		}
		return CommandSmall(files[0], files[1])
	}

	cmd_help := func(args []string) error {
		help_flags.Parse(args)
		names := help_flags.Args()
		if len(names) > 0 {
			cmd, pres := commands[names[0]]
			if !pres {
				fmt.Println("error: unknown command for help")
				PrintUsage(commands)
				os.Exit(1)
			}
			PrintCmdUsage(names[0], cmd)
		} else {
			PrintUsage(commands)
		}
		return nil
	}

	commands = map[string]CliCommand{
		"pack": {cmd_pack, pack_flags, "<input> <output>", "pack with custom settings"},
		//"unpack":   {nil, unpackCmd, "<input> <output>", "unpack to YM3 format (TBD)"},
		"quick": {cmd_quick, quick_flags, "<input> <output>", "pack to small with quick runtime"},
		"small": {cmd_small, small_flags, "<input> <output>", "pack to smallest runtime memory (more CPU)"},
		"help":  {cmd_help, help_flags, "", "list commands or describe a single command"},
	}

	if len(os.Args) < 2 {
		fmt.Println("error: expected a command")
		PrintUsage(commands)
		os.Exit(1)
	}

	// This all feels rather clunky...
	cmd, pres := commands[os.Args[1]]
	if !pres {
		fmt.Println("error: unknown command")
		PrintUsage(commands)
		os.Exit(1)
	}

	err := cmd.fn(os.Args[2:])
	if err != nil {
		fmt.Println("Error in pack_file", err.Error())
		os.Exit(1)
	}
}
