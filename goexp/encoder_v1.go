package main

func enc_byte(output []byte, value byte) []byte {
	return append(output, value)
}

func enc_word(output []byte, value uint16) []byte {
	output = append(output, byte(value>>8))
	return append(output, byte(value&255))
}

func enc_long(output []byte, value uint32) []byte {
	output = append(output, byte(value>>24)&255)
	output = append(output, byte(value>>16)&255)
	output = append(output, byte(value>>8)&255)
	return append(output, byte(value&255))
}

type encoder_v1 struct {
	num_literals int
}

func encode_count(output []byte, count int, literal_flag byte) []byte {
	if count < 128 {
		output = append(output, byte(count)|literal_flag)
	} else {
		output = append(output, 0|literal_flag)
		output = enc_word(output, uint16(count))
	}
	return output
}

func encode_offset(output []byte, offset int) []byte {
	for offset >= 256 {
		// 256 can be encoded as "255 + 1"
		output = append(output, 0)
		offset -= 255
	}
	if offset == 0 {
		panic("Problem when encoding offset")
	}
	output = enc_byte(output, byte(offset))
	return output
}

// Return the additional cost (in bytes) of adding literal(s) and match to an output stream
func (e *encoder_v1) cost(lit_count int, m match) int {
	cost := 0
	tmp_literals := e.num_literals
	cost += lit_count

	// Check if literal count will increase cost
	for i := 0; i < lit_count; i += 1 {
		if tmp_literals == 0 {
			cost += 1 //  // cost of switching match->list
		} else if tmp_literals == 127 {
			// cost of swtiching to extra-byte encoing
			cost += 2 // needs 2 extra bytes
		}
		tmp_literals += 1
	}

	cost += e.match_cost(m)
	return cost
}

// Calculate the byte cost of only a match
func (e *encoder_v1) match_cost(m match) int {
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

func (e *encoder_v1) encode(tokens []token, input []byte) []byte {
	output := make([]byte, 0)
	for i := 0; i < len(tokens); i += 1 {
		var t token = tokens[i]
		if t.is_match {
			output = encode_count(output, t.len, 0)
			output = encode_offset(output, t.off)
		} else {
			// Encode the literal
			output = encode_count(output, t.len, 0x80)
			literals := input[t.off : t.off+t.len]
			// https://github.com/golang/go/issues/28292
			output = append(output, literals...)
		}
	}
	return output
}

func (e *encoder_v1) unpack(input []byte) []byte {
	output := make([]byte, 0)
	head := 0
	for head < len(input) {
		top := input[head]
		head++
		if (top & 0x80) != 0 {
			// Literals
			// Length only
			var count int = int(top & 0x7f)
			if count == 0 {
				count = int(input[head]) << 8
				count |= int(input[head+1])
				head += 2
			}
			output = append(output, input[head:head+count]...)
			head += count
		} else {
			// Match
			// Length, then Offset
			var count int = int(top & 0x7f)
			if count == 0 {
				count = int(input[head]) << 8
				count |= int(input[head+1])
				head += 2
			}
			var offset int = 0
			for input[head] == 0 {
				offset += 255
				head++
			}
			offset += int(input[head])
			head++
			match_pos := len(output) - offset
			for count > 0 {
				output = append(output, output[match_pos])
				match_pos++
				count--
			}
		}
	}
	return output
}

func (e *encoder_v1) lit(lit_count int) {
	e.num_literals += lit_count
}

func (e *encoder_v1) match(m match) {
	e.num_literals = 0
}
