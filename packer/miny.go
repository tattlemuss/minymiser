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

// Contains a single stream of packed or unpacked data.
type ByteSlice []byte

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

func EncByte(output []byte, value byte) []byte {
	return append(output, value)
}

func EncWord(output []byte, value uint16) []byte {
	output = append(output, byte(value>>8))
	return append(output, byte(value&255))
}

func EncLong(output []byte, value uint32) []byte {
	output = append(output, byte(value>>24)&255)
	output = append(output, byte(value>>16)&255)
	output = append(output, byte(value>>8)&255)
	return append(output, byte(value&255))
}

// Describes a match or a series of literals.
type Token struct {
	isMatch bool
	len     int // length in bytes
	off     int // reverse offset if isMatch, abs position if literal
}

// Describes a Match run.
type Match struct {
	len int
	off int
}

// Raw unpacked data for all the registers, plus tune length.
type YmStreams struct {
	// A binary array for each register to pack
	register [numRegs]ByteSlice
	numVbls  int // size of each packedstream
	dataSize int // sum of sizes of all register arrays
}

// Interface for being able to encode a stream into a packed format.
type Encoder interface {
	// Calculate the Cost for adding literals or matches, or both
	Cost(litCount int, m Match) int

	// Apply N literals to the internal state
	ApplyLit(litCount int)
	// Apply a ApplyMatch to the internal state
	ApplyMatch(m Match)

	// Encodes all the given tokens into a binary stream.
	Encode(tokens []Token, input []byte) []byte

	// Unpacks the given packed binary stream.
	Decode(input []byte) []byte
}

// Describes packing config for a whole file
type FilePackConfig struct {
	cacheSizes []int // cache size for each individual reg
	verbose    bool
	verify     bool
	report     bool // print output report to stdout
	encoder    int  // 1 or 2
}

// Describes packing config for a single register stream
type StreamPackCfg struct {
	bufferSize int // cache size for just this stream
	verbose    bool
}

func FindLongestMatch(data []byte, head int, distance int) Match {
	bestOffset := -1
	bestLength := 0
	maxDist := distance
	if head < distance {
		maxDist = head
	}

	for offset := 1; offset <= maxDist; offset++ {
		length := 0
		checkPos := head - offset
		for head+length < len(data) && data[checkPos+length] == data[head+length] {
			length++
		}
		if length >= 3 && length > bestLength {
			bestLength = length
			bestOffset = offset
		}
	}
	return Match{len: bestLength, off: bestOffset}
}

func FindCheapestMatch(enc Encoder, data []byte, head int, distance int) Match {
	bestMatch := Match{0, 0}
	// Any pack rate of less than 1.0 is automatically useless!
	var bestCost float64 = 1.0
	maxDist := distance
	if head < distance {
		maxDist = head
	}
	for offset := 1; offset <= maxDist; offset++ {
		length := 0
		checkPos := head - offset
		for head+length < len(data) && data[checkPos+length] == data[head+length] {
			length++
		}
		if length >= 3 {
			m := Match{len: length, off: offset}
			mc := float64(enc.Cost(0, m)) / float64(length)
			if mc < bestCost {
				bestCost = mc
				bestMatch = m
			}
		}
	}
	return bestMatch
}

// Add a number of literals to a set of tokens.
// If the last entry was a match, create a new token to
// represent them
func AddLiterals(tokens []Token, count int, pos int) []Token {
	lastIndex := len(tokens) - 1
	if lastIndex >= 0 && !tokens[lastIndex].isMatch {
		tokens[lastIndex].len++
	} else {
		return append(tokens, Token{false, count, pos})
	}
	return tokens
}

func TokenizeGreedy(enc Encoder, data []byte, cfg StreamPackCfg) []byte {
	var tokens []Token

	head := 0
	matchBytes := 0
	litBytes := 0

	for head < len(data) {
		best := FindLongestMatch(data, head, cfg.bufferSize)
		if best.len != 0 {
			head += best.len
			tokens = append(tokens, Token{true, best.len, best.off})
			matchBytes += best.len
		} else {
			// Literal
			tokens = AddLiterals(tokens, 1, head)
			head++ // literal
			litBytes++
		}
	}
	if cfg.verbose {
		fmt.Printf("\tGreedy: Matches %v Literals %v (%.2f%%)\n", matchBytes, litBytes,
			Percent(matchBytes, litBytes+matchBytes))
	}
	return enc.Encode(tokens, data)
}

func TokenizeLazy(enc Encoder, data []byte, useCheapest bool, cfg StreamPackCfg) []Token {
	var tokens []Token

	usedMatch := 0
	usedMatchlit := 0
	usedSecond := 0
	matchBytes := 0
	litBytes := 0
	head := 0

	var best0 Match
	var best1 Match
	bufferSize := cfg.bufferSize
	for head < len(data) {
		if useCheapest {
			best0 = FindCheapestMatch(enc, data, head, bufferSize)
		} else {
			best0 = FindLongestMatch(data, head, bufferSize)
		}
		chooseLit := best0.len == 0

		// We have 2 choices really
		// Apply 0 (as a match or a literal)
		// Apply literal 0 (and check the next byte for a match)
		if !chooseLit {
			// See if doing N literals is smaller
			cost0 := enc.Cost(0, best0)
			costLit := enc.Cost(best0.len, Match{})
			if costLit < cost0 {
				chooseLit = true
				usedMatchlit++
			}
		}

		if !chooseLit {
			usedMatch++
			// We only need to decide to choose the second match, if both
			// 0 and 1 are matches rather than literals.
			if best0.len != 0 && head+1 < len(data) {
				if useCheapest {
					best1 = FindCheapestMatch(enc, data, head+1, bufferSize)
				} else {
					best1 = FindLongestMatch(data, head+1, bufferSize)
				}
				if best1.len != 0 {
					cost0 := enc.Cost(0, best0)
					cost1 := enc.Cost(1, best1)
					rate0 := Ratio(cost0, best0.len)
					rate1 := Ratio(cost1, 1+best1.len)
					if rate1 < rate0 {
						chooseLit = true
						usedMatch--
						usedSecond++
					}
				}
			}
		}

		// Add the decision to the token stream,
		// and update the encoder's state so it can update future encoding costs.
		if chooseLit {
			// Literal
			tokens = AddLiterals(tokens, 1, head)
			head++
			enc.ApplyLit(1)
			litBytes++
		} else {
			head += best0.len
			tokens = append(tokens, Token{true, best0.len, best0.off})
			usedMatch++
			enc.ApplyMatch(best0)
			matchBytes += best0.len
		}
	}

	if cfg.verbose {
		fmt.Printf("\tLazy: Matches %v Literals %v (%.2f%%)\n", matchBytes, litBytes,
			Percent(matchBytes, litBytes+matchBytes))
		fmt.Println("\tLazy: Used match:", usedMatch, "used matchlit:", usedMatchlit,
			"used second", usedSecond)
	}

	return tokens
}

// Split the file data array and create individual streams for the registers.
func CreateYmStreams(data []byte) (YmStreams, error) {
	// check header
	if len(data) < 4 || !reflect.DeepEqual(data[:4], ym3Header) {
		return YmStreams{}, errors.New("not a YM3 file")
	}

	dataSize := len(data) - 4
	if dataSize%numRegs != 0 {
		return YmStreams{}, errors.New("unexpected data size")
	}
	// Convert to memory types
	numVbls := dataSize / numRegs
	ym3 := YmStreams{}
	ym3.numVbls = numVbls
	ym3.dataSize = dataSize
	for reg := 0; reg < numRegs; reg++ {
		// Split register data
		startPos := 4 + reg*numVbls
		ym3.register[reg] = data[startPos : startPos+numVbls]
	}
	return ym3, nil
}

// Load an input file and create the ym_streams data object.
func LoadStreamFile(inputPath string) (YmStreams, error) {
	dat, err := os.ReadFile(inputPath)
	if err != nil {
		return YmStreams{}, err
	}

	ymStreams, err := CreateYmStreams(dat)
	return ymStreams, err
}

// General packing statistics
type PackStats struct {
	lenMap     map[int]int // length -> count
	distMap    map[int]int // dist -> cound
	offs       []int
	lens       []int
	litlens    []int
	numNatches int
	numTokens  int
}

// Core function to ack a YM3 data file and return an encoded array of bytes.
func PackAll(ymData *YmStreams, fileCfg FilePackConfig) ([]byte, error) {
	// Compression settings
	useCheapest := false

	streamCfg := StreamPackCfg{}
	streamCfg.verbose = fileCfg.verbose
	packedStreams := make([]ByteSlice, numRegs)

	var stats PackStats
	stats.lenMap = make(map[int]int)
	stats.distMap = make(map[int]int)

	for reg := 0; reg < numRegs; reg++ {
		streamCfg.bufferSize = fileCfg.cacheSizes[reg]
		if fileCfg.verbose {
			fmt.Println("Packing register", reg, registerNames[reg])
		}
		// Pack
		var enc Encoder
		if fileCfg.encoder == 1 {
			enc = &Encoder_v1{0}
		} else if fileCfg.encoder == 2 {
			enc = &Encoder_v2{0}
		} else {
			return EmptySlice(), fmt.Errorf("unknown encoder ID: (%d)", fileCfg.encoder)
		}
		regData := ymData.register[reg]
		packed := &packedStreams[reg]

		tokens := TokenizeLazy(enc, regData, useCheapest, streamCfg)
		*packed = enc.Encode(tokens, regData)

		// Graph histogram
		for i := range tokens {
			t := &tokens[i]
			if t.isMatch {
				if t.len < 512 {
					stats.lenMap[t.len]++
				}
				stats.distMap[t.off]++
				stats.offs = append(stats.offs, t.off)
				stats.lens = append(stats.lens, t.len)
				stats.numNatches++
			} else {
				stats.litlens = append(stats.litlens, t.len)
			}
		}
		stats.numTokens += len(tokens)

		// Verify by unpacking
		if fileCfg.verify {
			unpacked := enc.Decode(*packed)
			if !reflect.DeepEqual(regData, unpacked) {
				return EmptySlice(), errors.New("failed to verify pack<->unpack round trip, there is a bug")
			} else {
				if fileCfg.verbose {
					fmt.Println("\tVerify OK")
				}
			}
		}
	}

	// Group the registers into sets with the same size
	sets := make(map[int][]int)
	for reg := 0; reg < numRegs; reg++ {
		sets[fileCfg.cacheSizes[reg]] = append(sets[fileCfg.cacheSizes[reg]], reg)
	}

	// Calc mapping of YM reg->stream in the file
	// and generate the header data for them.

	// We will output the registers to the file, ordered by set
	// and flattened.
	regOrder := make([]byte, numRegs)
	// The inverse order is used at runtime to map from YM reg
	// to depacked stream in the file.
	inverseRegOrder := make([]byte, numRegs)

	// Data repreesenting the set configuration
	setHeaderData := []byte{}
	var streamId byte = 0
	for cacheSize, set := range sets {
		// Loop
		setHeaderData = EncWord(setHeaderData, uint16(len(set)-1))
		setHeaderData = EncWord(setHeaderData, uint16(cacheSize))
		for _, reg := range set {
			inverseRegOrder[reg] = streamId
			regOrder[streamId] = byte(reg)
			streamId++
		}
	}
	setHeaderData = EncWord(setHeaderData, uint16(0xffff))

	// Number of bytes required by the set data
	// 4 bytes per set -- loop count, cache size
	// 2 bytes -- end sentinel
	if len(setHeaderData) != 2+(4*len(sets)) {
		panic("header size mismatch")
	}

	// Generate the final data
	outputData := make([]byte, 0)

	// Calc overall header size
	headerSize := 2 + // header
		2 + // cache size
		2 + // num vbls
		numRegs + // register order
		4*numRegs + // offsets to packed streams
		len(setHeaderData) // set information

	// Header: "Y" + 0x1 (version)
	outputData = EncByte(outputData, 'Y')
	outputData = EncByte(outputData, 0x1)

	// 0) Output required cache size (for user reference)
	outputData = EncWord(outputData, uint16(Sum(fileCfg.cacheSizes)))

	// 1) Output size in VBLs
	outputData = EncWord(outputData, uint16(ymData.numVbls))

	// 2) Order of registers
	outputData = append(outputData, inverseRegOrder...)

	dataPos := headerSize
	// Offsets to register data
	for _, reg := range regOrder {
		outputData = EncLong(outputData, uint32(dataPos))
		dataPos += len(packedStreams[reg])
	}
	// Set data
	outputData = append(outputData, setHeaderData...)

	if len(outputData) != headerSize {
		panic("header size mismatch 2")
	}

	// ... then the data
	for _, reg := range regOrder {
		outputData = append(outputData, packedStreams[reg]...)
	}

	if fileCfg.report {
		origSize := ymData.dataSize
		packedSize := len(outputData)
		cacheSize := Sum(fileCfg.cacheSizes)
		totalSize := cacheSize + packedSize
		fmt.Println("===== Complete =====")
		fmt.Printf("Original size:    %6d\n", origSize)
		fmt.Printf("Packed size:      %6d (%.1f%%)\n", packedSize, Percent(packedSize, origSize))
		fmt.Printf("Num cache sizes:  %6d (smaller=faster)\n", len(sets))
		fmt.Printf("Total cache size: %6d\n", cacheSize)
		fmt.Printf("Total RAM:        %6d (%.1f%%)\n", totalSize, Percent(totalSize, origSize))
	}

	return outputData, nil
}

// Pack a file with custom config like cache size.
func CommandCustom(inputPath string, outputPath string, fileCfg FilePackConfig) error {
	ymData, err := LoadStreamFile(inputPath)
	if err != nil {
		return err
	}

	fileCfg.report = true
	packedData, err := PackAll(&ymData, fileCfg)
	if err != nil {
		return err
	}

	err = os.WriteFile(outputPath, packedData, 0644)
	return err
}

type MinpackResult struct {
	regCacheSize int // size of cache for a single register
	cacheSize    int // total size of all caches added together
	packedSize   int
}

// Choose the cache size which gives minimal sum of
// [packed file size] + [cache size]
func MinpackFindCacheSize(ymData *YmStreams, minCacheSize int, maxCacheSize int,
	cacheSizeStep int, phase string) (int, error) {
	messages := make(chan MinpackResult, 5)

	// Async func to pack the file and return sizes
	FindPackedSizeFunc := func(regCacheSize int, ymData *YmStreams, cfg FilePackConfig) {
		packedData, err := PackAll(ymData, cfg)
		if err != nil {
			fmt.Println(err)
			messages <- MinpackResult{0, 0, 0}
		} else {
			messages <- MinpackResult{regCacheSize, Sum(cfg.cacheSizes), len(packedData)}
		}
	}

	// Launch the async packers
	for cacheSize := minCacheSize; cacheSize <= maxCacheSize; cacheSize += cacheSizeStep {
		cfg := FilePackConfig{}
		cfg.cacheSizes = FilledSlice(numRegs, cacheSize)
		cfg.verbose = false
		cfg.encoder = 1
		go FindPackedSizeFunc(cacheSize, ymData, cfg)
	}

	// Receive results and find the smallest
	sizeMap := make(map[int]int)

	smallestCacheSize := -1
	smallestTotalSize := 1 * 1024 * 1024
	fmt.Printf("Collecting stats (%s)", phase)
	for i := minCacheSize; i <= maxCacheSize; i += cacheSizeStep {
		msg := <-messages

		thisSize := msg.packedSize
		totalSize := msg.cacheSize + thisSize
		fmt.Print(".")
		if totalSize < smallestTotalSize {
			smallestTotalSize = totalSize
			smallestCacheSize = msg.regCacheSize
		}

		sizeMap[msg.cacheSize] = thisSize //totalSize
	}
	fmt.Println()
	return smallestCacheSize, nil
}

// Pack file to be played back with low CPU (single cache size for
// all registers)
func CommandQuick(inputPath string, outputPath string) error {
	ymData, err := LoadStreamFile(inputPath)
	if err != nil {
		return err
	}

	fmt.Println("---- Pass 1 ----")
	smallestCacheSize, err := MinpackFindCacheSize(&ymData, 64, 1024, 32, "broad")
	if err != nil {
		return err
	}

	fmt.Println("---- Pass 2 ----")
	smallestCacheSize, err = MinpackFindCacheSize(&ymData, smallestCacheSize-32,
		smallestCacheSize+32, 2, "narrow")
	if err != nil {
		return err
	}

	fileCfg := FilePackConfig{}
	fileCfg.verbose = false
	fileCfg.cacheSizes = FilledSlice(numRegs, smallestCacheSize)
	fileCfg.encoder = 1
	fileCfg.report = true
	packedData, err := PackAll(&ymData, fileCfg)
	if err != nil {
		return err
	}

	err = os.WriteFile(outputPath, packedData, 0644)
	return err
}

// Records resulting size for a pack of a single register stream, for
// a given cache size.
type RegPackSizes struct {
	reg       int
	cacheSize int
	totalSize int
}

// Records how big every register packs to, given a seris of cache sizes.
type PerRegStats struct {
	// key is the tried cache size, value is array of totalPackedSizes for each reg
	totalPackedSizes map[int]([]int)
}

func FindSmallestTotalSize(stats *PerRegStats, regs []RegPackSizes) (int, int) {
	smallestTotal := 99999999999
	smallestIndex := -1

	for key := range stats.totalPackedSizes {
		// Create total cost
		totalCost := 0
		for j := range regs {
			totalCost += stats.totalPackedSizes[key][regs[j].reg]
		}
		if totalCost < smallestTotal {
			smallestTotal = totalCost
			smallestIndex = key
		}
	}
	return smallestIndex, smallestTotal
}

// Result for a single attempt at packing a register stream with
// a given cache size.
// TODO share with RegPackSizes?
type SmallResult struct {
	reg        int
	cacheSize  int
	packedSize int
}

func CommandSmall(inputPath string, outputPath string) error {
	ymData, err := LoadStreamFile(inputPath)
	if err != nil {
		return err
	}

	stats := PerRegStats{}
	stats.totalPackedSizes = make(map[int][]int)
	minSize := 8
	maxSize := 1024
	step := 16
	for size := minSize; size < maxSize; size += step {
		stats.totalPackedSizes[size] = make([]int, numRegs)
	}

	messages := make(chan SmallResult, 15)

	// Async func to pack the file and return sizes
	FindPackedSizeFunc := func(reg int, regCacheSize int, ymData *YmStreams) {
		enc := Encoder_v1{0}
		var cfg StreamPackCfg
		cfg.bufferSize = regCacheSize
		cfg.verbose = false
		regData := ymData.register[reg]
		tokens := TokenizeLazy(&enc, regData, true, cfg)
		packedData := enc.Encode(tokens, regData)
		if err != nil {
			fmt.Println(err)
			messages <- SmallResult{reg, 0, 0}
		} else {
			messages <- SmallResult{reg, regCacheSize, len(packedData)}
		}
	}

	fmt.Print("Collecting stats")
	for reg := 0; reg < numRegs; reg++ {
		fmt.Print(".")

		// Launch...
		for size := minSize; size < maxSize; size += step {
			go FindPackedSizeFunc(reg, size, &ymData)
		}

		// ... Collect.
		for size := minSize; size < maxSize; size += step {
			msg := <-messages
			csize := msg.cacheSize
			total := msg.cacheSize + msg.packedSize
			stats.totalPackedSizes[csize][reg] = total
		}

	}
	fmt.Println()

	bestTotalSize := 0
	bestCacheSize := 0

	statsForRegs := make([]RegPackSizes, 0)
	var smallCfg FilePackConfig
	smallCfg.encoder = 1
	smallCfg.verbose = false
	smallCfg.verify = false
	smallCfg.report = true
	smallCfg.cacheSizes = make([]int, numRegs)

	for reg := 0; reg < numRegs; reg++ {
		minTotal := 9999999
		minCache := minTotal

		for csize := range stats.totalPackedSizes {
			total := stats.totalPackedSizes[csize][reg]
			if total < minTotal {
				minTotal = total
				minCache = csize
			}
		}
		bestTotalSize += minTotal
		bestCacheSize += minCache
		statsForRegs = append(statsForRegs, RegPackSizes{reg, minCache, minTotal})

		smallCfg.cacheSizes[reg] = minCache
	}

	sort.Slice(statsForRegs, func(i, j int) bool {
		if statsForRegs[i].cacheSize != statsForRegs[j].cacheSize {
			return statsForRegs[i].cacheSize < statsForRegs[j].cacheSize
		}
		return statsForRegs[i].totalSize < statsForRegs[j].totalSize
	})

	// Now we can grade the streams based on who needs the biggest cache
	for i := 0; i < numRegs; i++ {
		reg := statsForRegs[i].reg
		fmt.Printf("Reg %2d Needs cache %4d -> Total size %5d (%s)\n", reg, statsForRegs[i].cacheSize,
			statsForRegs[i].totalSize,
			registerNames[reg])
	}

	// Write out a final minimal file
	packedFile, _ := PackAll(&ymData, smallCfg)
	err = os.WriteFile(outputPath, packedFile, 0644)
	if err != nil {
		return err
	}

	if false {
		// This is experimental code to force a pack with
		// 2 cache sizes only.
		for split := 1; split < numRegs-1; split++ {
			// Make 2 lists, the "small" list and the big one
			smallList := statsForRegs[:split]
			bigList := statsForRegs[split:]

			smallCache, smallTotal := FindSmallestTotalSize(&stats, smallList)
			bigCache, bigTotal := FindSmallestTotalSize(&stats, bigList)

			finalSize := smallTotal + bigTotal
			fmt.Printf("total size with caches %d/%d -> %d (loss %d)\n",
				smallCache, bigCache, finalSize, bestTotalSize-finalSize)
		}
	}

	return nil
}

type CliCommand struct {
	fn       func(args []string) error
	flagSet  *flag.FlagSet
	argsDesc string // argument description
	desc     string
}

// Describes how to use a given command.
func PrintCmdUsage(name string, cmd CliCommand) {
	fmt.Printf("%s %s - %s\n", name, cmd.argsDesc, cmd.desc)
	fs := cmd.flagSet
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
	packDlags := flag.NewFlagSet("pack", flag.ExitOnError)
	//unpackCmd := flag.NewFlagSet("unpack", flag.ExitOnError)
	quickFlags := flag.NewFlagSet("minpack", flag.ExitOnError)
	smallFlags := flag.NewFlagSet("smallest", flag.ExitOnError)
	helpFlags := flag.NewFlagSet("help", flag.ExitOnError)

	packOptSize := packDlags.Int("cachesize", numRegs*512, "overall cache size in bytes")
	packOptVerbose := packDlags.Bool("verbose", false, "verbose output")
	packOptEncoder := packDlags.Int("encoder", 1, "encoder version (1|2)")
	var commands map[string]CliCommand

	cmdCustom := func(args []string) error {
		packDlags.Parse(args)
		files := packDlags.Args()
		if len(files) != 2 {
			fmt.Println("'pack' command: expected <input> <output> arguments")
			os.Exit(1)
		}
		cfg := FilePackConfig{}
		cfg.cacheSizes = FilledSlice(numRegs, *packOptSize/numRegs)
		cfg.verbose = *packOptVerbose
		cfg.encoder = *packOptEncoder
		return CommandCustom(files[0], files[1], cfg)
	}

	cmdQuick := func(args []string) error {
		quickFlags.Parse(args)
		files := quickFlags.Args()
		if len(files) != 2 {
			fmt.Println("'quick' command: expected <input> <output> arguments")
			os.Exit(1)
		}
		return CommandQuick(files[0], files[1])
	}

	cmdSmall := func(args []string) error {
		smallFlags.Parse(args)
		files := smallFlags.Args()
		if len(files) != 2 {
			fmt.Println("'small' command: expected <input> <output> arguments")
			os.Exit(1)
		}
		return CommandSmall(files[0], files[1])
	}

	cmdHelp := func(args []string) error {
		helpFlags.Parse(args)
		names := helpFlags.Args()
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
		"pack": {cmdCustom, packDlags, "<input> <output>", "pack with custom settings"},
		//"unpack":   {nil, unpackCmd, "<input> <output>", "unpack to YM3 format (TBD)"},
		"quick": {cmdQuick, quickFlags, "<input> <output>", "pack to small with quick runtime"},
		"small": {cmdSmall, smallFlags, "<input> <output>", "pack to smallest runtime memory (more CPU)"},
		"help":  {cmdHelp, helpFlags, "", "list commands or describe a single command"},
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
		fmt.Println("Error: ", err.Error())
		os.Exit(1)
	}
}
