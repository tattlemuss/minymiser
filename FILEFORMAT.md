YMP file format (v3)
====================

Concepts and Terms
------------------

The YMP file as a whole is encoded as 13 separate "streams". They map closely, but not exactly,
to the 14 registers of the YM/AY chip which control sound output as encoded in a normal YM
file.

The only difference is that the 6 bits of register 7 (the "mixer" register) are extracted and
inserted into the upper bits of registers 8/9/10 (the "volume" registers).

Each stream has its own "cache" of previously-decoded output, which it uses for its LZ-like
decompression. The runtime decompressor keeps a circular buffer of the last N bytes of that
stream, where N is the cache's size.

Each stream can have a different size of cache. In the worst case this could be 13 completely
different sizes. However, the packer attempts to balance runtime CPU cost against packing size,
and will group together all the streams with the same cache size in the file data. This allows
the runtime code to skip extra checks for looping the circular buffer. A set of streams all
sharing the same cache is named a "cache set".

So that the packer can group the streams sharing the same cache size efficiently, the order
of the encoded streams in the file is not the same as the listing below! There is a remapping
table in the file header, as described later.

The (Logical) Streams
---------------------

   	| Stream # | Data
	+----------+------
	| 0        | YM Register 0;  A period lo
	| 1        | YM Register 1;  A period hi
	| 2        | YM Register 2;  B period lo
	| 3        | YM Register 3;  B period hi
	| 4        | YM Register 4;  C period lo
	| 5        | YM Register 5;  C period hi
	| 6        | YM Register 6;  Noise period
	| 7        | YM Register 8;  Bit 7 = Mixer Bit 3 (noise); Bit 6 = Mixer Bit 0 (square)
	| 8        | YM Register 9;  Bit 7 = Mixer Bit 4 (noise); Bit 6 = Mixer Bit 1 (square)
	| 9        | YM Register 10; Bit 7 = Mixer Bit 5 (noise); Bit 6 = Mixer Bit 2 (square)
	| 10       | YM Register 11; Env period lo
	| 11       | YM Register 12; Env period hi
	| 12       | YM Register 13; Env shape (0xff if not to be written)

File Format
-----------

The order of the file data is:

* a fixed-size header
* the "cache set" information
* the 13 packed streams themselves.

All data is packed contiguously without padding unless specified.
All u16/u32 values are big-endian.
The [x] notation represents an array of x values.

Header format:

	| Format  | Data
	+---------+------
	| u8      | Format marker: 'Y'
	| u8	  | Format marker: 0x3 (encoding version)
	| u16     | Total size of required cache for all streams
	| u32     | Number of frames of music
	| u8[13]  | "remap table" Mapping from the 13 streams in the file to its logical meaning.
	| u8      | Empty padding for word alignment.
	| u32[13] | Offsets to the packed data for each stream, relative to the start of the entire file.

Cache set format:

The "cache set" information follows immediately. This is a series of 2 x u16 values.

	| Format | Data
	+--------+------
	| u16    | Number of streams sharing this cache size, minus 1
	| u16    | Size of the cache for these streams

The "minus 1" is to ease the use of the "dbf" loop command in m68k.

The series is terminated by a single u16 value of 0xffff.

For a concrete example, see "Cache Set Example" later on.

Packed stream data
------------------
Each stream is packed in a simple LZ format, made of a series of "tokens". Each token can
either represent a set of "literals" or a "match". The tokens are encoded with a variable
length.

Literal tokens contain only a length value. This is the number of bytes to copy from the
packed stream. The bytes to copy are then immediately after the token in the packed stream.

Match tokens contain a length, then an offset. The length is the same as for literals (number
of bytes to copy). The offset is the number of bytes back in the previously decoded data
to copy from. For example, the match token (length=3, offset=50) means "copy 3 bytes from
50 bytes earlier in the unpacked data".

Type/Length encoding:

For each token, the top bit of the first byte encodes the token type. A "0" bit represents
a match. A "1" bit represents a literal.

The rest of the byte (bottom 7 bits) represents the length, unless that value is 0.

If the value is 0, the full length is encoded as the next two bytes (big-endian format).
A length can never be more than 0xffff bytes. If that happens, multiple tokens are used.

Examples:

    | Type  | Length | Encoding
	+-------+--------+-----------
	| Match | 1      | 0x01
	| Match | 256	 | 0x00,0x01,0x00
	| Lit   | 1  	 | 0x81
	| Lit   | 767	 | 0x80,0x02,0xff

Match offset encoding:

For a Match token, the offset follows the length. To decode the offset, a simple prefix encoding is used.

Start with an "offset" variable of 0. Keep reading a byte at a time. While the byte == 0, add 255 to the offset.
When the byte is not 0, add that byte to the offset and stop.

Examples:

	| Offset    | Encoding
	+-----------+-----------
	| 1			| 0x01
	| 255       | 0xff
	| 256		| 0x00,0x01
	| 510		| 0x00,0xff
	| 511		| 0x00,0x00,0x01

There is no marker to flag "end of stream". The length of the stream is determined by the number of bytes decoded which matches the "number of frames of music" in the file header.

There is a reference implementation of the token decoder in the Decode() function of "encoder_v1.go".
Although it is written in Go language, it should serve as reasonable pseudocode. I even added some
comments!

Cache Set Example
-----------------

As an example, consider the case where there are 2 cache sizes.
The first cache set has a size of 512. This set has the registers 0,6,2,4 in it, in that order.
The second cache set has the size of 256. This set has all the remaining registers.

The "remap table" would have the following 13 values:

	0, 6, 2, 4, 1, 3, 5, 7, 8, 9, 10, 11, 12

The "cache set" information would have the following values:

	3,		| Cache set has 4 streams
	512,	| Stream size
	8,		| Cache set has 9 streams
	256,	| Stream size
	0xffff	| Terminator
