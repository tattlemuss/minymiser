package main

type Encoder_v1 struct {
	numLiterals int
}

func encodeCount(p *PackStream, count int, literalFlag byte) {
	if count < 128 {
		p.AddByte(byte(count) | literalFlag)
	} else {
		p.AddByte(0 | literalFlag)
		p.AddWord(uint16(count))
	}
}

func encodeOffset(p *PackStream, offset int) {
	for offset >= 256 {
		// 256 can be encoded as "255 + 1"
		p.AddByte(0)
		offset -= 255
	}
	if offset == 0 {
		panic("Problem when encoding offset")
	}
	p.AddByte(byte(offset))
}

func (e *Encoder_v1) litCost(count int) int {
	if count == 0 {
		return 0
	}
	cost := 0
	if count < 128 {
		cost = 1 * 8
	} else {
		cost = 3 * 8
	}
	cost += count * 8 // needs the actual literal data too
	return cost
}

// Calculate the byte cost of only a match
func (e *Encoder_v1) matchCost(m Match) int {
	cost := 0
	// Match
	// A match is always new, so apply full cost
	if m.len > 0 {
		// length encoding
		cost = 8
		if m.len >= 128 {
			cost += 2 * 8
		}
		// offset encoding
		cost += 8 // always 1 byte
		offset := m.off
		for offset >= 256 {
			cost += 8
			offset -= 255
		}
	}
	return cost
}

// Return the additional Cost (in bits) of adding literal(s) and match to an output stream
func (e *Encoder_v1) Cost(litCount int, m Match) int {
	cost := 0
	if litCount != 0 {
		// Check if literal count will increase cost
		currLitCost := e.litCost(e.numLiterals)
		nextLitCost := e.litCost(e.numLiterals + litCount)
		cost += (nextLitCost - currLitCost)
	}
	cost += e.matchCost(m)
	return cost
}

func (e *Encoder_v1) Encode(t *Token, p *PackStream, input []byte) {
	if t.isMatch {
		encodeCount(p, t.len, 0)
		encodeOffset(p, t.off)
	} else {
		// Encode the literal
		encodeCount(p, t.len, 0x80)
		literals := input[t.off : t.off+t.len]
		// https://github.com/golang/go/issues/28292
		p.AddBytes(literals)
	}
}

func (e *Encoder_v1) Decode(input []byte) []byte {
	output := make([]byte, 0)
	head := 0
	// Loop over all tokens
	for head < len(input) {
		// Choose either match or literal, depending on the top bit of the next byte
		top := input[head]
		head++
		if (top & 0x80) != 0 {
			// Literals
			// These are encoded as "Length only"
			var count int = int(top & 0x7f)
			if count == 0 {
				count = int(input[head]) << 8
				count |= int(input[head+1])
				head += 2
			}
			// Copy the next "count" bytes of the packed stream to the output
			output = append(output, input[head:head+count]...)
			head += count
		} else {
			// Match
			// Encoded as "Length, then Offset"
			var count int = int(top & 0x7f)
			if count == 0 {
				count = int(input[head]) << 8
				count |= int(input[head+1])
				head += 2
			}
			// Decode the offset
			var offset int = 0
			// The "for" below acts as a "while" loop.
			for input[head] == 0 {
				offset += 255
				head++
			}
			offset += int(input[head])
			head++

			// Copy bytes from the previously-decoded data, at a distance of "offset"
			matchPos := len(output) - offset
			for count > 0 {
				output = append(output, output[matchPos])
				matchPos++
				count--
			}
		}
	}
	return output
}

func (e *Encoder_v1) ApplyLit(litCount int) {
	e.numLiterals += litCount
}

func (e *Encoder_v1) ApplyMatch(m Match) {
	e.numLiterals = 0
}

func (e *Encoder_v1) Reset() {
	e.numLiterals = 0
}
