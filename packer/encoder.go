package main

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

// Interface for being able to encode a stream into a packed format.
type Encoder interface {
	// Calculate the Cost for adding literals or matches, or both
	Cost(litCount int, m Match) int

	// Apply N literals to the internal state
	ApplyLit(litCount int)
	// Apply a ApplyMatch to the internal state
	ApplyMatch(m Match)

	// Encodes a single token a binary stream.
	Encode(t *Token, output []byte, input []byte) []byte

	// Unpacks the given packed binary stream.
	Decode(input []byte) []byte

	// Clear internal state to restart
	// (used for testing)
	Reset()
}
