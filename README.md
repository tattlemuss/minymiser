MinYMiser
=========

Some experiments in compressing YM-2149 register data streams and depacking at runtime.

The core approach is to use [LZSA](https://github.com/emmanuel-marty/lzsa) to compress the stream
of YM registers.

C++ code for a compressor is in the "/packer" directory. It should produce .ymp compressed files.
The code is a single file and is quite naive, but is essentially a working lzsa compressor.

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

Currently the player doesn't support looping the data stream.

