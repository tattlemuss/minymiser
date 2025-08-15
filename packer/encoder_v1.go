package main

type Encoder_v1 struct {
	numLiterals int
}

func encodeCount(output []byte, count int, literalFlag byte) []byte {
	if count < 128 {
		output = append(output, byte(count)|literalFlag)
	} else {
		output = append(output, 0|literalFlag)
		output = EncWord(output, uint16(count))
	}
	return output
}

func encodeOffset(output []byte, offset int) []byte {
	for offset >= 256 {
		// 256 can be encoded as "255 + 1"
		output = append(output, 0)
		offset -= 255
	}
	if offset == 0 {
		panic("Problem when encoding offset")
	}
	output = EncByte(output, byte(offset))
	return output
}

// Return the additional Cost (in bytes) of adding literal(s) and match to an output stream
func (e *Encoder_v1) Cost(litCount int, m Match) int {
	cost := 0
	tmpLiterals := e.numLiterals
	cost += litCount

	// Check if literal count will increase cost
	for i := 0; i < litCount; i += 1 {
		if tmpLiterals == 0 {
			cost += 1 //  // cost of switching match->list
		} else if tmpLiterals == 127 {
			// cost of swtiching to extra-byte encoing
			cost += 2 // needs 2 extra bytes
		}
		tmpLiterals += 1
	}

	cost += e.MatchCost(m)
	return cost
}

// Calculate the byte cost of only a match
func (e *Encoder_v1) MatchCost(m Match) int {
	cost := 0
	// Match
	// A match is always new, so apply full cost
	if m.len > 0 {
		// length encoding
		cost = 1
		if m.len >= 128 {
			cost += 2
		}
		// pffset encoding
		cost += 1
		offset := m.off
		for offset >= 256 {
			cost++
			offset -= 255
		}
	}
	return cost
}

func (e *Encoder_v1) Encode(tokens []Token, input []byte) []byte {
	output := make([]byte, 0)
	for i := 0; i < len(tokens); i += 1 {
		var t Token = tokens[i]
		if t.isMatch {
			output = encodeCount(output, t.len, 0)
			output = encodeOffset(output, t.off)
		} else {
			// Encode the literal
			output = encodeCount(output, t.len, 0x80)
			literals := input[t.off : t.off+t.len]
			// https://github.com/golang/go/issues/28292
			output = append(output, literals...)
		}
	}
	return output
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
