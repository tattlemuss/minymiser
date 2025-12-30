package main

type PackStream struct {
	encodedTokens []byte
}

func NewPackStream() *PackStream {
	p := PackStream{
		encodedTokens: make([]byte, 0),
	}
	return &p
}

func (p *PackStream) AddBytes(input []byte) {
	p.encodedTokens = append(p.encodedTokens, input...)
}

func (p *PackStream) AddByte(input byte) {
	p.encodedTokens = append(p.encodedTokens, input)
}

func (p *PackStream) AddWord(input uint16) {
	p.encodedTokens = append(p.encodedTokens, byte(input>>8))
	p.encodedTokens = append(p.encodedTokens, byte(input&255))
}

func (p *PackStream) BitCount() int {
	return len(p.encodedTokens) * 8
}
