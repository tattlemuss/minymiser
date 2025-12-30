package main

type PackStream struct {
	byteData []byte // Data added as bytes
	bitData  []uint16
	// Next Bitmask to write (or not) to bitData.
	// If 0, need to create a new byte
	bitMask  uint16
	bitCount int
}

func NewPackStream() *PackStream {
	p := PackStream{
		byteData: make([]byte, 0),
	}
	return &p
}

func (p *PackStream) AddBytes(input []byte) {
	p.byteData = append(p.byteData, input...)
}

func (p *PackStream) AddByte(input byte) {
	p.byteData = append(p.byteData, input)
}

func (p *PackStream) AddWord(input uint16) {
	p.byteData = append(p.byteData, byte(input>>8))
	p.byteData = append(p.byteData, byte(input&255))
}

func (p *PackStream) AddBit(input byte) {
	if p.bitMask == 0 {
		p.bitData = append(p.bitData, 0)
		p.bitMask = 0x8000
	}
	if input != 0 {
		p.bitData[len(p.bitData)-1] |= p.bitMask
	}
	p.bitMask >>= 1
	p.bitCount++
}

func (p *PackStream) BitCount() int {
	return len(p.byteData)*8 + p.bitCount
}
