package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

// This is the number of registers - 1, since the mixer
// register data is mixed into the channel volume register streams.
const numStreams = 13

var streamNames = [numStreams]string{
	"A period lo", "A period hi",
	"B period lo", "B period hi",
	"C period lo", "C period hi",
	"Noise period",
	"A volume + mixer",
	"B volume + mixer",
	"C volume + mixer",
	"Env period lo", "Env period hi",
	"Env shape"}

// Contains a single stream of packed or unpacked data.
type ByteSlice []byte

// Create a byte slice of size 0
func EmptySlice() []byte {
	return make([]byte, 0)
}

func FilledSlice(size int, val int) []int {
	arr := make([]int, numStreams)
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

// Raw unpacked data for all the registers, plus tune length.
type YmStreams struct {
	// A binary array for each streamData stream to pack
	streamData [numStreams][]byte
	numVbls    int // size of each packedstream
	dataSize   int // sum of sizes of all register arrays
}

func GetEncoder(choice int) (Encoder, error) {
	switch choice {
	case 1:
		return &Encoder_v1{0}, nil
	case 2:
		return &Encoder_v2{0}, nil
	}

	return nil, fmt.Errorf("unknown encoder ID: (%d)", choice)
}

type UserConfig struct {
	verbose bool
	encoder int // 1 or 2
}

// Describes packing config for a whole file
type FilePackConfig struct {
	cacheSizes []int // cache size for each individual stream
	uc         UserConfig
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
		for head+length < len(data) &&
			data[checkPos+length] == data[head+length] &&
			length < 0xff00 {
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
		for head+length < len(data) &&
			data[checkPos+length] == data[head+length] &&
			length < 0xff00 {
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

	// We also split literals at just before 64K to avoid runtime
	// 16-bit overflow
	if lastIndex >= 0 &&
		!tokens[lastIndex].isMatch &&
		tokens[lastIndex].len < 0xfff0 {
		tokens[lastIndex].len++
	} else {
		return append(tokens, Token{false, count, pos})
	}
	return tokens
}

func TokenizeGreedy(enc Encoder, data []byte, cfg StreamPackCfg) []Token {
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
	return tokens
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

// Take the 14 raw register arrays and multiplex the mixer bits
// into the final YmStreams representation.
// NOTE: this overwrites contents of some of the original
// RawRegisters byte slices (for the volume channels)
func RemapFromRaw(rawRegs *RawRegisters) *YmStreams {
	// Pull out mixer bits and write into the volume streams
	for channel := 0; channel < 3; channel++ {
		target_channel := 8 + channel
		tone_bit := channel
		noise_bit := channel + 3

		for i, val := range rawRegs.data[7] {
			if (rawRegs.data[target_channel][i] & 0xc0) != 0 {
				panic("wrong data")
			}
			// Put the tone and noise mixer bits into bits
			// 6 and 7 of the volume
			var acc byte = 0
			if val&(1<<tone_bit) != 0 {
				acc |= 1 << 6
			}
			if val&(1<<noise_bit) != 0 {
				acc |= 1 << 7
			}
			rawRegs.data[target_channel][i] |= acc
		}
	}
	var ymStr YmStreams
	ymStr.numVbls = len(rawRegs.data[0])
	ymStr.dataSize = 0
	// Remap the final set
	for strm := 0; strm < numStreams; strm++ {
		if strm < 7 {
			ymStr.streamData[strm] = rawRegs.data[strm]
		} else {
			ymStr.streamData[strm] = rawRegs.data[strm+1]
		}
		// Accumulate data size
		ymStr.dataSize += len(ymStr.streamData[strm])
	}
	return &ymStr
}

// Load an input file and create the ym_streams data object.
func LoadStreamFile(inputPath string) (*YmStreams, error) {
	dat, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}

	rawRegisters, err := LoadRawRegisters(dat)
	if err != nil {
		return nil, err
	}
	ymStr := RemapFromRaw(rawRegisters)
	return ymStr, nil
}

// General packing statistics
type PackStats struct {
	lenMap     map[int]int // length -> count
	distMap    map[int]int // dist -> count
	litlenMap  map[int]int
	offs       []int
	lens       []int
	numMatches int
	numTokens  int
	matchSize  int
}

func PrintMap(m *map[int]int) {
	max := 0
	tot := 0
	keys := make([]int, 0, len(*m))
	for k := range *m {
		keys = append(keys, k)
		cnt := (*m)[k]
		tot += cnt
		if cnt > max {
			max = cnt
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] > keys[j]
	})
	for k := range keys {
		cnt := (*m)[k]
		dup := 80.0 * cnt / max

		if dup != 0 {
			fmt.Printf("[% 4d] %s %d (%v%%)\n", k, strings.Repeat("*", int(dup)), cnt, cnt*100.0/tot)
		} else {
			fmt.Printf("[% 4d] %d\n", k, cnt)
		}
	}
}

// Core function to ack a YM3 data file and return an encoded array of bytes.
func PackAll(ymStr *YmStreams, fileCfg FilePackConfig,
	report bool, verify bool) ([]byte, error) {
	// Compression settings
	useCheapest := false

	streamCfg := StreamPackCfg{}
	streamCfg.verbose = fileCfg.uc.verbose

	var stats PackStats
	stats.lenMap = make(map[int]int)
	stats.distMap = make(map[int]int)
	stats.litlenMap = make(map[int]int)

	// Records the tokens needed
	tokensPerStream := make([][]Token, numStreams)
	enc, err := GetEncoder(fileCfg.uc.encoder)
	if err != nil {
		return nil, err
	}

	for strmIdx := 0; strmIdx < numStreams; strmIdx++ {
		streamCfg.bufferSize = fileCfg.cacheSizes[strmIdx]
		if fileCfg.uc.verbose {
			fmt.Println("Packing register", strmIdx, streamNames[strmIdx])
		}
		// Pack
		regData := ymStr.streamData[strmIdx]
		tokens := TokenizeLazy(enc, regData, useCheapest, streamCfg)
		tokensPerStream[strmIdx] = tokens

		// Graph histogram
		for i := range tokens {
			t := &tokens[i]
			if t.isMatch {
				stats.lenMap[t.len]++
				stats.distMap[t.off]++
				stats.offs = append(stats.offs, t.off)
				stats.lens = append(stats.lens, t.len)
				stats.numMatches++
				stats.matchSize += t.len
			} else {
				stats.litlenMap[t.len]++
			}
		}
		stats.numTokens += len(tokens)
	}

	// Group the registers into sets with the same size
	sets := make(map[int][]int)
	for strmIdx := 0; strmIdx < numStreams; strmIdx++ {
		sets[fileCfg.cacheSizes[strmIdx]] = append(sets[fileCfg.cacheSizes[strmIdx]], strmIdx)
	}

	// Calc mapping of YM reg->stream in the file
	// and generate the header data for them.

	// We will output the registers to the file, ordered by set
	// and flattened.
	regOrder := make([]byte, numStreams)
	// The inverse order is used at runtime to map from YM reg
	// to depacked stream in the file.
	inverseRegOrder := make([]byte, numStreams)

	// Data repreesenting the set configuration
	setHeaderData := []byte{}
	var streamId byte = 0
	for cacheSize, set := range sets {
		if fileCfg.uc.verbose {
			fmt.Printf("Adding set with cache size %d\n", cacheSize)
		}
		setHeaderData = EncWord(setHeaderData, uint16(len(set)-1))
		setHeaderData = EncWord(setHeaderData, uint16(cacheSize))
		for _, reg := range set {
			if fileCfg.uc.verbose {
				fmt.Printf(" - reg stream %d (%s)\n", reg, streamNames[reg])
			}
			inverseRegOrder[reg] = streamId
			regOrder[streamId] = byte(reg)
			streamId++
		}
	}

	// Flag end of cache set
	setHeaderData = EncWord(setHeaderData, uint16(0xffff))

	// Number of bytes required by the set data
	// 4 bytes per set -- loop count, cache size
	// 2 bytes -- end sentinel
	if len(setHeaderData) != 2+(4*len(sets)) {
		panic("header size mismatch")
	}

	// Do the final interleaving of the encoded tokens into a single stream,
	// knowing the order-per-frame that they will be depacked in
	encodedTokens := make([]byte, 0)
	nextTokenFrame := make([]int, numStreams) // frame number when next token gets used
	nextTokenIndex := make([]int, numStreams) // index in tokensPerStream[x]

	// Use a dumb loop to check the next token.
	// We could use a constantly-sorted list (mapped by lower position+lower reg order),
	// but there seems little need for the complexity since matches tend to be
	// short.
	for frameIdx := 0; frameIdx < ymStr.numVbls; frameIdx++ {
		for r := 0; r < numStreams; r++ {
			strmIdx := regOrder[r]
			if nextTokenFrame[strmIdx] == frameIdx {
				// Read the next token from the packed data
				tIdx := nextTokenIndex[strmIdx]
				t := tokensPerStream[strmIdx][tIdx]
				encodedTokens = enc.Encode(&t, encodedTokens, ymStr.streamData[strmIdx])

				// Move on to the next tokem in this stream
				nextTokenIndex[strmIdx]++
				nextTokenFrame[strmIdx] += t.len
			}
		}
	}

	// Generate the final data
	outputData := make([]byte, 0)

	// Calc overall header size
	headerSize := 2 + // header
		2 + // cache size
		4 + // num vbls
		numStreams + // register order
		1 + // padding
		len(setHeaderData) // set information

	// Header: "Y" + 0x3 (version)
	outputData = EncByte(outputData, 'Y')
	outputData = EncByte(outputData, 0x3)

	// 0) Output required cache size (for user reference)
	outputData = EncWord(outputData, uint16(Sum(fileCfg.cacheSizes)))

	// 1) Output size in VBLs
	outputData = EncLong(outputData, uint32(ymStr.numVbls))

	// 2) Order of registers
	outputData = append(outputData, inverseRegOrder...)
	outputData = EncByte(outputData, 0x0) // padding

	// Set data
	outputData = append(outputData, setHeaderData...)

	if len(outputData) != headerSize {
		panic("header size mismatch 2")
	}

	// ... then the data
	outputData = append(outputData, encodedTokens...)

	if report {
		origSize := ymStr.dataSize
		packedSize := len(outputData)
		cacheSize := Sum(fileCfg.cacheSizes)
		totalSize := cacheSize + packedSize
		bpf := float32(packedSize) / float32(ymStr.numVbls)

		fmt.Println("===== Complete =====")
		fmt.Printf("Original size:    %6d\n", origSize)
		fmt.Printf("Packed size:      %6d (%.1f%%) (%.2f bytes/frame)\n", packedSize, Percent(packedSize, origSize), bpf)
		fmt.Printf("Num cache sizes:  %6d (smaller=faster)\n", len(sets))
		fmt.Printf("Total cache size: %6d\n", cacheSize)
		fmt.Printf("Total RAM:        %6d (%.1f%%)\n", totalSize, Percent(totalSize, origSize))

		if fileCfg.uc.verbose {
			fmt.Printf("Num matches       %6d (%.1f%%)\n", stats.numMatches, Percent(stats.numMatches, stats.numTokens))
			fmt.Printf("Num tokens        %6d (%.2f tokens/frame)\n", stats.numTokens, float32(stats.numTokens)/float32(ymStr.numVbls))

			fmt.Printf("Matched size      %6d (%.1f%%)\n", stats.matchSize, Percent(stats.matchSize, origSize))
			fmt.Println("\nMatch Distances:")
			PrintMap(&stats.distMap)
			fmt.Println("\nMatch Lengths:")
			PrintMap(&stats.lenMap)
			fmt.Println("\nLiteral Lengths:")
			PrintMap(&stats.litlenMap)
		}
	}

	return outputData, nil
}

// Pack a file with custom config like cache size.
func CommandCustom(inputPath string, outputPath string, fileCfg FilePackConfig) error {
	ymStr, err := LoadStreamFile(inputPath)
	if err != nil {
		return err
	}

	packedData, err := PackAll(ymStr, fileCfg, true, true)
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
func MinpackFindCacheSize(ymStr *YmStreams, minCacheSize int, maxCacheSize int,
	cacheSizeStep int, phase string, encoder int) (int, error) {
	messages := make(chan MinpackResult, 5)

	// Async func to pack the file and return sizes
	FindPackedSizeFunc := func(regCacheSize int, ymStr *YmStreams, cfg FilePackConfig) {
		packedData, err := PackAll(ymStr, cfg, false, false)
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
		cfg.cacheSizes = FilledSlice(numStreams, cacheSize)
		cfg.uc.verbose = false
		cfg.uc.encoder = encoder
		go FindPackedSizeFunc(cacheSize, ymStr, cfg)
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
func CommandQuick(inputPath string, outputPath string, uc UserConfig) error {
	ymStr, err := LoadStreamFile(inputPath)
	if err != nil {
		return err
	}

	fmt.Println("---- Pass 1 ----")
	smallestCacheSize, err := MinpackFindCacheSize(ymStr, 64, 1024, 32, "broad", uc.encoder)
	if err != nil {
		return err
	}

	fmt.Println("---- Pass 2 ----")
	smallestCacheSize, err = MinpackFindCacheSize(ymStr, smallestCacheSize-32,
		smallestCacheSize+32, 2, "narrow", uc.encoder)
	if err != nil {
		return err
	}

	fileCfg := FilePackConfig{}
	fileCfg.uc = uc
	fileCfg.cacheSizes = FilledSlice(numStreams, smallestCacheSize)
	packedData, err := PackAll(ymStr, fileCfg, true, true)
	if err != nil {
		return err
	}

	err = os.WriteFile(outputPath, packedData, 0644)
	return err
}

// Records resulting size for a pack of a single register stream, for
// a given cache size.
type RegPackSizes struct {
	strmIdx   int
	cacheSize int
	totalSize int
}

// Records how big every register packs to, given a seris of cache sizes.
type PerRegStats struct {
	// key is the tried cache size, value is array of totalPackedSizes for each reg
	totalPackedSizes map[int]([]int)
}

func FindSmallestTotalSize(stats *PerRegStats, regs []RegPackSizes) (int, int) {
	smallestTotal := math.MaxInt
	smallestIndex := -1

	for key := range stats.totalPackedSizes {
		// Create total cost
		totalCost := 0
		for j := range regs {
			totalCost += stats.totalPackedSizes[key][regs[j].strmIdx]
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
	strmIdx    int
	cacheSize  int
	packedSize int
}

func CommandSmall(inputPath string, outputPath string, uc UserConfig) error {
	ymStr, err := LoadStreamFile(inputPath)
	if err != nil {
		return err
	}

	stats := PerRegStats{}
	stats.totalPackedSizes = make(map[int][]int)
	minSize := 8
	maxSize := 1024
	step := 16
	for size := minSize; size < maxSize; size += step {
		stats.totalPackedSizes[size] = make([]int, numStreams)
	}

	messages := make(chan SmallResult, 15)

	// Async func to pack the file and return sizes
	FindPackedSizeFunc := func(strmIdx int, regCacheSize int, ymStr *YmStreams) {
		enc, _ := GetEncoder(1)
		var cfg StreamPackCfg
		cfg.bufferSize = regCacheSize
		cfg.verbose = false
		regData := ymStr.streamData[strmIdx]
		tokens := TokenizeLazy(enc, regData, true, cfg)
		output := make([]byte, 0)
		for i := 0; i < len(tokens); i++ {
			output = enc.Encode(&tokens[i], output, regData)
		}
		if err != nil {
			fmt.Println(err)
			messages <- SmallResult{strmIdx, 0, 0}
		} else {
			messages <- SmallResult{strmIdx, regCacheSize, len(output)}
		}
	}

	fmt.Print("Collecting stats")
	for strmIdx := 0; strmIdx < numStreams; strmIdx++ {
		fmt.Print(".")

		// Launch...
		for size := minSize; size < maxSize; size += step {
			go FindPackedSizeFunc(strmIdx, size, ymStr)
		}

		// ... Collect.
		for size := minSize; size < maxSize; size += step {
			msg := <-messages
			csize := msg.cacheSize
			total := msg.cacheSize + msg.packedSize
			stats.totalPackedSizes[csize][strmIdx] = total
		}

	}
	fmt.Println()

	bestTotalSize := 0
	bestCacheSize := 0

	statsForRegs := make([]RegPackSizes, 0)
	var smallCfg FilePackConfig
	smallCfg.uc = uc
	smallCfg.cacheSizes = make([]int, numStreams)

	for strmIdx := 0; strmIdx < numStreams; strmIdx++ {
		minTotal := 9999999
		minCache := minTotal

		for csize := range stats.totalPackedSizes {
			total := stats.totalPackedSizes[csize][strmIdx]
			if total < minTotal {
				minTotal = total
				minCache = csize
			}
		}
		bestTotalSize += minTotal
		bestCacheSize += minCache
		statsForRegs = append(statsForRegs, RegPackSizes{strmIdx, minCache, minTotal})

		smallCfg.cacheSizes[strmIdx] = minCache
	}

	sort.Slice(statsForRegs, func(i, j int) bool {
		if statsForRegs[i].cacheSize != statsForRegs[j].cacheSize {
			return statsForRegs[i].cacheSize < statsForRegs[j].cacheSize
		}
		return statsForRegs[i].totalSize < statsForRegs[j].totalSize
	})

	// Now we can grade the streams based on who needs the biggest cache
	for i := 0; i < numStreams; i++ {
		strmIdx := statsForRegs[i].strmIdx
		fmt.Printf("Stream %2d Needs cache %4d -> Total size %5d (%s)\n", strmIdx, statsForRegs[i].cacheSize,
			statsForRegs[i].totalSize,
			streamNames[strmIdx])
	}

	// Write out a final minimal file
	packedFile, err := PackAll(ymStr, smallCfg, true, true)
	if err != nil {
		return err
	}
	err = os.WriteFile(outputPath, packedFile, 0644)
	if err != nil {
		return err
	}

	if false {
		// This is experimental code to force a pack with
		// 2 cache sizes only.
		for split := 1; split < numStreams-1; split++ {
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

func CommandSimple(inputPath string, outputPath string) error {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return err
	}

	rawRegs, err := LoadRawRegisters(data)
	if err != nil {
		return err
	}
	numFrames := len(rawRegs.data[0])

	// The format of the output is
	// 2 bytes -- header "YU"
	// 4 bytes -- number of frames to play
	// Followed by blocks of 14 bytes with the full set of register data per frame.
	var outputData []byte
	outputData = EncByte(outputData, 'Y')
	outputData = EncByte(outputData, 'U')
	outputData = EncWord(outputData, uint16(numFrames))

	// Interleave the registers by frame
	for i := 0; i < numFrames; i++ {
		for reg := 0; reg < numYmRegs; reg++ {
			outputData = EncByte(outputData, data[4+(reg*numFrames)+i])
		}
	}

	err = os.WriteFile(outputPath, outputData, 0644)
	return err
}

func CommandDelta(inputPath string, outputPath string) error {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return err
	}

	rawRegs, err := LoadRawRegisters(data)
	if err != nil {
		return err
	}
	numFrames := len(rawRegs.data[0])

	// The format of the output is
	// 2 bytes -- header "YU"
	// 4 bytes -- number of frames to play
	// Followed by blocks of 14 bytes with the full set of register data per frame.
	var outputData []byte
	outputData = EncByte(outputData, 'Y')
	outputData = EncByte(outputData, 'D')
	outputData = EncLong(outputData, uint32(numFrames))

	// Interleave the registers by frame
	previous_val := make([]byte, numYmRegs)
	for i := 0; i < numYmRegs; i++ {
		previous_val[i] = 0xff
	}

	for frame := 0; frame < numFrames; frame++ {
		var mask byte = 0
		var vals []byte
		for reg := 0; reg < numYmRegs; reg++ {
			regVal := data[4+(reg*numFrames)+frame]
			do_out := false // enforce on first frame
			if reg == 13 {
				// Spacial case -- only write out any non-0xff value
				do_out = (regVal != 0xff)
			} else {
				do_out = (regVal != previous_val[reg]) ||
					(frame == 0)
			}

			mask <<= 1
			if do_out {
				mask |= 1
				vals = append(vals, regVal)
			}
			previous_val[reg] = regVal

			if reg == 6 || reg == 13 {
				outputData = EncByte(outputData, mask<<1)
				outputData = append(outputData, vals...)
				mask = 0
				vals = vals[:0] // resets slice without freeing memory
			}
		}
	}

	err = os.WriteFile(outputPath, outputData, 0644)
	return err
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
	uc := UserConfig{}
	customFlags := flag.NewFlagSet("pack", flag.ExitOnError)
	customFlags.BoolVar(&uc.verbose, "verbose", false, "verbose output")
	customFlags.IntVar(&uc.encoder, "encoder", 1, "encoder version (1|2)")
	//unpackCmd := flag.NewFlagSet("unpack", flag.ExitOnError)

	quickFlags := flag.NewFlagSet("minpack", flag.ExitOnError)
	quickFlags.BoolVar(&uc.verbose, "verbose", false, "verbose output")
	quickFlags.IntVar(&uc.encoder, "encoder", 1, "encoder version (1|2)")
	packOptSize := customFlags.Int("cachesize", numStreams*512, "overall cache size in bytes")

	smallFlags := flag.NewFlagSet("smallest", flag.ExitOnError)
	smallFlags.BoolVar(&uc.verbose, "verbose", false, "verbose output")
	smallFlags.IntVar(&uc.encoder, "encoder", 1, "encoder version (1|2)")

	simpleFlags := flag.NewFlagSet("simple", flag.ExitOnError)
	deltaFlags := flag.NewFlagSet("delta", flag.ExitOnError)
	helpFlags := flag.NewFlagSet("help", flag.ExitOnError)

	var commands map[string]CliCommand

	cmdCustom := func(args []string) error {
		customFlags.Parse(args)
		files := customFlags.Args()
		if len(files) != 2 {
			fmt.Println("'pack' command: expected <input> <output> arguments")
			os.Exit(1)
		}
		cfg := FilePackConfig{}
		cfg.cacheSizes = FilledSlice(numStreams, *packOptSize/numStreams)
		cfg.uc = uc
		return CommandCustom(files[0], files[1], cfg)
	}

	cmdQuick := func(args []string) error {
		quickFlags.Parse(args)
		files := quickFlags.Args()
		if len(files) != 2 {
			fmt.Println("'quick' command: expected <input> <output> arguments")
			os.Exit(1)
		}
		return CommandQuick(files[0], files[1], uc)
	}

	cmdSmall := func(args []string) error {
		smallFlags.Parse(args)
		files := smallFlags.Args()
		if len(files) != 2 {
			fmt.Println("'small' command: expected <input> <output> arguments")
			os.Exit(1)
		}
		return CommandSmall(files[0], files[1], uc)
	}

	cmdSimple := func(args []string) error {
		simpleFlags.Parse(args)
		files := simpleFlags.Args()
		if len(files) != 2 {
			fmt.Println("'simple' command: expected <input> <output> arguments")
			os.Exit(1)
		}
		return CommandSimple(files[0], files[1])
	}

	cmdDelta := func(args []string) error {
		deltaFlags.Parse(args)
		files := deltaFlags.Args()
		if len(files) != 2 {
			fmt.Println("'delta' command: expected <input> <output> arguments")
			os.Exit(1)
		}
		return CommandDelta(files[0], files[1])
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
		"pack": {cmdCustom, customFlags, "<input> <output>", "pack with custom settings"},
		//"unpack":   {nil, unpackCmd, "<input> <output>", "unpack to YM3 format (TBD)"},
		"quick":  {cmdQuick, quickFlags, "<input> <output>", "pack to small with quick runtime"},
		"small":  {cmdSmall, smallFlags, "<input> <output>", "pack to smallest runtime memory (more CPU)"},
		"simple": {cmdSimple, simpleFlags, "<input> <output>", "de-interleave to per-frame register values"},
		"delta":  {cmdDelta, deltaFlags, "<input> <output>", "delta-pack file"},
		"help":   {cmdHelp, helpFlags, "", "list commands or describe a single command"},
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
