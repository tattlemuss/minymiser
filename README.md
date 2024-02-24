MinYMiser
=========

Some experiments in compressing YM-2149 register data streams and depacking at runtime.

The idea is to create a generalised music player with low runtime overhead (CPU and memory),
in a tradeoff between normal music drivers (which tend to have higher CPU time) and "YM dump"
methods (which tend to require large memory footprints)

The core approach is to use [LZSA](https://github.com/emmanuel-marty/lzsa) to compress the stream
of YM registers, using a small rolling window of previous frames. This means that the memory 
footprint is limited to the packed file plus a small temporary buffer, rather than storing a larger
YM dump in memory.

C++ code for a compressor is in the "/packer" directory. It should produce .ymp compressed files.
The code is a single file and is quite naive, but is essentially a working lzsa compressor. The
command line is

  packer input.ym3 output.ymp

... where the input is a YM3-format file, as produced by Hatari amongst others.

"player.s" is example code for playback on a standard Atari ST. It should play back the compressed
streams in around 3-4 scanlines on an 8MHz machine.

The current file format used is to compress each register's data stream separately, so it stores
14 streams in the file. Each LZ stream has a 512-byte rolling window, so about 10 seconds of data,
to look back into and store matches. The player decompresses these in real time.

The "/python" directory contains my prototyping area. It tries to compress the files in various ways:

- using LZSA1 vs LZSA2 format
- compressing the streams individually, all as a large single group, or as selective groups
  of registers (e.g. storing registers 0 and 1, the square-wave period of channel A, together)
- using simple delta-packing with a bitmask to reduce size (the "traditional" way to pack)

The C++ packer and runtime only currently support the LZSA1 format, and register streams packed
individually. (It would be good to support LZSA2.) This matches the ".all.ymp" files produced
by the Python packer.

File Sizes / Memory
===================

The LZ methods are around 10%-40% of the size of the delta-packed bitmask, and generally 5-10%
of the size of the raw .ym file. The compromise to this is the added CPU time to depack, and
a 5K buffer for the LZ rolling window. The buffer size could easily be adjusted to improve pack
ratio vs runtime memory.

Test Data
=========

There are 3 test data streams in the repo. Please note that the "motus.ym" file seems to be
incorrect and some voices are garbled.

Omissions
=========
Only .ym3 input data format is supported.

Currently the player doesn't support looping the data stream.

The system doesn't support timer effects like SID. It might be possible to support these with
extensions to the system.

