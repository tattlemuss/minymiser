package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const numYmRegs = 14

// Raw data type loaded from a file.
// Contains 14 arrays of raw register data.
type RawRegisters struct {
	data [numYmRegs]ByteSlice
}

func readFromYM3(data []byte) (*RawRegisters, error) {
	// There are 14 regs in the original file
	dataSize := len(data) - 4
	if dataSize%numYmRegs != 0 {
		return &RawRegisters{}, errors.New("unexpected data size")
	}
	// Convert to memory types
	numVbls := dataSize / numYmRegs
	var rawRegs RawRegisters

	for reg := 0; reg < numYmRegs; reg++ {
		// Split register data
		startPos := 4 + reg*numVbls
		rawRegs.data[reg] = data[startPos : startPos+numVbls]
	}
	return &rawRegs, nil
}

func ym5SkipStrings(r io.ByteReader) error {
	// Skip 3 strings: tune, author, notes
	var err error
	var v byte
	for i := 0; i < 3; i++ {
		for v = byte(1); v != 0; {
			v, err = r.ReadByte()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func ym5SkipDigidrums(r *bytes.Reader, digiCount uint16) error {
	for dd := uint16(0); dd < digiCount; dd++ {
		// Read
		var ddSize uint32
		err := binary.Read(r, binary.BigEndian, &ddSize)
		if err != nil {
			return err
		}
		_, err = r.Seek(int64(ddSize), io.SeekCurrent)
		if err != nil {
			return err
		}
	}
	return nil
}

type YM56Header struct {
	Header     uint32  // File ID "YM5!" "YM6!"
	Leonard    [8]byte // Check string "LeOnArD!"
	FrameCount uint32  // Nb of frame in the file
	Attr       uint32  // Song attributes
	DigiCount  uint16  // Nb of digidrum samples in file (can be 0)
	ClockHz    uint32  // YM master clock implementation in Hz .(ex:2000000 for ATARI-ST version, 1773400 for ZX-SPECTRUM)
	PlayHertz  uint16  // Original player frame in Hz (traditionnaly 50)
	LoopFrame  uint32  // Loop frame (traditionnaly 0 to loop at the beginning)
	SkipBytes  uint16  // Number of bytes to skip next
}

func readFromYM56(data []byte) (*RawRegisters, error) {
	r := bytes.NewReader(data)
	var info YM56Header
	err := binary.Read(r, binary.BigEndian, &info)
	if err != nil {
		return &RawRegisters{}, err
	}
	if info.DigiCount != 0 {
		fmt.Println("WARNING: can't correctly encode tunes with digidrum data")
	}

	// Skip the optional data
	r.Seek(int64(info.SkipBytes), io.SeekCurrent)

	// Skip the digidrums
	err = ym5SkipDigidrums(r, info.DigiCount)
	if err != nil {
		return nil, err
	}

	// Skip the name/author/converter
	err = ym5SkipStrings(r)
	if err != nil {
		return nil, err
	}

	// Fill out the actual YM data we want
	var rawRegs RawRegisters
	for reg := 0; reg < numYmRegs; reg++ {
		rawRegs.data[reg] = make([]byte, info.FrameCount)
		_, err := r.Read(rawRegs.data[reg])
		if err != nil {
			return &RawRegisters{}, err
		}
	}
	return &rawRegs, nil
}

// Split the file data array and create simple individual streams for the registers.
func LoadRawRegisters(data []byte) (*RawRegisters, error) {
	if len(data) < 4 {
		return &RawRegisters{}, errors.New("not a YM-stream file, too small for header")
	}
	r := bytes.NewReader(data)
	var fileHeader uint32
	err := binary.Read(r, binary.BigEndian, &fileHeader)
	if err == nil {
		switch fileHeader {
		case 0x594d3321:
			return readFromYM3(data)
		case 0x594d3521, 0x594d3621:
			return readFromYM56(data)
		}
	}
	return &RawRegisters{}, errors.New("not a supported YM-stream file")
}
