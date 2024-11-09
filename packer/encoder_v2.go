package main

type Encoder_v2 struct {
	numLiterals int
}

/*
	Encoding scheme

	- First byte: match or literal count

	0x00-0xef  match count encoding
	0xf0-0xff  literal count

	Match count encoding:
	| llll | oooo |
	top nybble   0-0xe 	-- start length. If 0x0, fetch byte, If 0x0,0x0, fetch word
	lower nybble 0-0xf	-- start offset. If 0x0, use 0-prefix

	Literal count encoding:
	| 1111 | llll |
	lower nybble 0-0xf	-- start length. If 0x0, fetch byte. If 0x0 0x0, fetch word

	TODO match length is still a little wasteful.
*/

func encodeCountV2(output []byte, count int) []byte {
	if count < 256 {
		output = append(output, byte(count))
	} else {
		output = append(output, 0)
		output = EncWord(output, uint16(count))
	}
	return output
}

func encodeOffsetV2(output []byte, offset int) []byte {
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
func (e *Encoder_v2) Cost(litCount int, m Match) int {
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

// This cost includes the literals themselves...
func (e *Encoder_v2) litCost(litCount int) int {
	if litCount == 0 {
		return 0
	}
	cost := 1 // header byte	// encoding fx
	if litCount > 0xf {
		cost++ // encoding f0 xx
		if litCount > 0xff {
			cost += 2 // count as f0 00 xx xx
		}
	}
	cost += litCount
	return cost
}

// Calculate the byte cost of only a match
func (e *Encoder_v2) matchCost(m Match) int {
	if m.len == 0 {
		return 0
	}
	cost := 1 // header byte

	// Length first
	len := m.len
	off := m.off
	if len > 0xe {
		cost++ // another byte
		if len >= 0xff {
			cost += 2 // 0, then 0xffff offset
		}
	}

	// offset uses 0-prefix
	if off > 0xf {
		cost++
		for off >= 256 {
			cost++
			off -= 255
		}
	}
	return cost
}

func (e *Encoder_v2) Encode(tokens []Token, input []byte) []byte {
	output := make([]byte, 0)
	for i := 0; i < len(tokens); i++ {
		var t Token = tokens[i]
		if t.isMatch {
			var startLen byte = 0 // "more" marker
			var startOff byte = 0 // "more" marker
			if t.len <= 0xe {
				startLen = byte(t.len)
			}
			if t.off <= 0xf {
				startOff = byte(t.off)
			}
			output = append(output, startLen<<4|startOff)
			// Now rest of length
			if t.len > 0xe {
				output = encodeCountV2(output, t.len)
			}
			// and rest of offset
			if t.off > 0xf {
				output = encodeOffsetV2(output, t.off)
			}
		} else {
			// Encode the literal
			if t.len <= 0xf {
				output = append(output, 0xf0+byte(t.len))
			} else {
				output = append(output, 0xf0)
				output = encodeCountV2(output, t.len)
			}
			// Then copy literals
			literals := input[t.off : t.off+t.len]
			output = append(output, literals...)
		}
	}
	return output
}

func (e *Encoder_v2) Decode(input []byte) []byte {
	output := make([]byte, 0)
	head := 0
	for head < len(input) {
		top := input[head]
		if (top & 0xf0) == 0xf0 {
			// Literals
			// Length only
			var count int = int(top & 0xf)
			head++
			if count == 0 {
				count = int(input[head])
				head++
				if count == 0 {
					count = int(input[head]) << 8
					count |= int(input[head+1])
					head += 2
				}
			}
			output = append(output, input[head:head+count]...)
			head += count
		} else {
			// Match
			// Length + Offset encoded in one
			var count int = int(top >> 4)
			var off int = int(top & 0xf)
			head++
			if count == 0 {
				count = int(input[head])
				head++
				if count == 0 {
					count = int(input[head]) << 8
					count |= int(input[head+1])
					head += 2
				}
			}
			if off == 0 {
				// Longer offset, use prefix code
				for input[head] == 0 {
					off += 255
					head++
				}
				off += int(input[head])
				head++
			}
			matchPos := len(output) - off
			for count > 0 {
				output = append(output, output[matchPos])
				matchPos++
				count--
			}
		}
	}
	return output
}

func (e *Encoder_v2) ApplyLit(litCount int) {
	e.numLiterals += litCount
}

func (e *Encoder_v2) ApplyMatch(m Match) {
	e.numLiterals = 0
}
