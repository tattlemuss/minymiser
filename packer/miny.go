package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
)

// This is the number of registers - 1, since the mixer
// register data is mixed into the channel volume register streams.
const numStreams = 13
const numYmRegs = 14

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

var ym3Header = []byte{'Y', 'M', '3', '!'}

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
	// A binary array for each streamData stream to pack
	streamData [numStreams][]byte
	numVbls    int // size of each packedstream
	dataSize   int // sum of sizes of all register arrays
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
	cacheSizes []int // cache size for each individual stream
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
// The Mixer register is extracted and its bits are distributed into other
// streams (the "volume" register streams)
func CreateYmStreams(data []byte) (YmStreams, error) {
	// check header
	if len(data) < 4 || !reflect.DeepEqual(data[:4], ym3Header) {
		return YmStreams{}, errors.New("not a YM3 file")
	}

	// There are 14 regs in the original file
	dataSize := len(data) - 4
	if dataSize%numYmRegs != 0 {
		return YmStreams{}, errors.New("unexpected data size")
	}
	// Convert to memory types
	numVbls := dataSize / numYmRegs
	ym3 := YmStreams{}
	ym3.numVbls = numVbls
	ym3.dataSize = dataSize

	var raw_registers [numYmRegs]ByteSlice

	for reg := 0; reg < numYmRegs; reg++ {
		// Split register data
		startPos := 4 + reg*numVbls
		raw_registers[reg] = data[startPos : startPos+numVbls]
	}

	// Pull out mixer bits
	for channel := 0; channel < 3; channel++ {
		target_channel := 8 + channel
		tone_bit := channel
		noise_bit := channel + 3

		for i, val := range raw_registers[7] {
			if (raw_registers[target_channel][i] & 0xc0) != 0 {
				panic("wrong data")
			}
			var acc byte = 0
			if val&(1<<tone_bit) != 0 {
				acc |= 1 << 6
			}
			if val&(1<<noise_bit) != 0 {
				acc |= 1 << 7
			}
			raw_registers[target_channel][i] |= acc
		}
	}

	// Remap the final set
	for reg := 0; reg < numStreams; reg++ {
		if reg < 7 {
			ym3.streamData[reg] = raw_registers[reg]
		} else {
			ym3.streamData[reg] = raw_registers[reg+1]
		}
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
	packedStreams := make([]ByteSlice, numStreams)

	var stats PackStats
	stats.lenMap = make(map[int]int)
	stats.distMap = make(map[int]int)

	for strmIdx := 0; strmIdx < numStreams; strmIdx++ {
		streamCfg.bufferSize = fileCfg.cacheSizes[strmIdx]
		if fileCfg.verbose {
			fmt.Println("Packing register", strmIdx, streamNames[strmIdx])
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
		regData := ymData.streamData[strmIdx]
		packed := &packedStreams[strmIdx]

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
		numStreams + // register order
		1 + // padding
		4*numStreams + // offsets to packed streams
		len(setHeaderData) // set information

	// Header: "Y" + 0x2 (version)
	outputData = EncByte(outputData, 'Y')
	outputData = EncByte(outputData, 0x2)

	// 0) Output required cache size (for user reference)
	outputData = EncWord(outputData, uint16(Sum(fileCfg.cacheSizes)))

	// 1) Output size in VBLs
	outputData = EncWord(outputData, uint16(ymData.numVbls))

	// 2) Order of registers
	outputData = append(outputData, inverseRegOrder...)
	outputData = EncByte(outputData, 0x0) // padding

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
		cfg.cacheSizes = FilledSlice(numStreams, cacheSize)
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
	fileCfg.cacheSizes = FilledSlice(numStreams, smallestCacheSize)
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
	smallestTotal := 99999999999
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
		stats.totalPackedSizes[size] = make([]int, numStreams)
	}

	messages := make(chan SmallResult, 15)

	// Async func to pack the file and return sizes
	FindPackedSizeFunc := func(strmIdx int, regCacheSize int, ymData *YmStreams) {
		enc := Encoder_v1{0}
		var cfg StreamPackCfg
		cfg.bufferSize = regCacheSize
		cfg.verbose = false
		regData := ymData.streamData[strmIdx]
		tokens := TokenizeLazy(&enc, regData, true, cfg)
		packedData := enc.Encode(tokens, regData)
		if err != nil {
			fmt.Println(err)
			messages <- SmallResult{strmIdx, 0, 0}
		} else {
			messages <- SmallResult{strmIdx, regCacheSize, len(packedData)}
		}
	}

	fmt.Print("Collecting stats")
	for strmIdx := 0; strmIdx < numStreams; strmIdx++ {
		fmt.Print(".")

		// Launch...
		for size := minSize; size < maxSize; size += step {
			go FindPackedSizeFunc(strmIdx, size, &ymData)
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
	smallCfg.encoder = 1
	smallCfg.verbose = false
	smallCfg.verify = false
	smallCfg.report = true
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
	packedFile, err := PackAll(&ymData, smallCfg)
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

	if len(data) < 4 || !reflect.DeepEqual(data[:4], ym3Header) {
		return errors.New("not a YM3 file")
	}

	// There are 14 regs in the original file
	dataSize := len(data) - 4
	if dataSize%numYmRegs != 0 {
		return errors.New("unexpected data size")
	}

	numFrames := dataSize / numYmRegs

	// The format of the output is
	// 2 bytes -- header "YU"
	// 2 bytes -- number of frames to play
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

	if len(data) < 4 || !reflect.DeepEqual(data[:4], ym3Header) {
		return errors.New("not a YM3 file")
	}

	// There are 14 regs in the original file
	dataSize := len(data) - 4
	if dataSize%numYmRegs != 0 {
		return errors.New("unexpected data size")
	}

	numFrames := dataSize / numYmRegs

	// The format of the output is
	// 2 bytes -- header "YU"
	// 2 bytes -- number of frames to play
	// Followed by blocks of 14 bytes with the full set of register data per frame.
	var outputData []byte
	outputData = EncByte(outputData, 'Y')
	outputData = EncByte(outputData, 'D')
	outputData = EncWord(outputData, uint16(numFrames))

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
	packDlags := flag.NewFlagSet("pack", flag.ExitOnError)
	//unpackCmd := flag.NewFlagSet("unpack", flag.ExitOnError)
	quickFlags := flag.NewFlagSet("minpack", flag.ExitOnError)
	smallFlags := flag.NewFlagSet("smallest", flag.ExitOnError)
	simpleFlags := flag.NewFlagSet("simple", flag.ExitOnError)
	deltaFlags := flag.NewFlagSet("delta", flag.ExitOnError)
	helpFlags := flag.NewFlagSet("help", flag.ExitOnError)

	packOptSize := packDlags.Int("cachesize", numStreams*512, "overall cache size in bytes")
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
		cfg.cacheSizes = FilledSlice(numStreams, *packOptSize/numStreams)
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
		"pack": {cmdCustom, packDlags, "<input> <output>", "pack with custom settings"},
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
