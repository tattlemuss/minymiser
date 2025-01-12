MinYMiser
=========

Some experiments in compressing YM-2149 register data streams and depacking at runtime.

The idea is to create a generalised music player with low runtime overhead (CPU and memory),
in a tradeoff between normal music drivers (which tend to have higher CPU time) and "YM dump"
methods (which tend to require large memory footprints)

The core approach is to use [LZSA](https://github.com/emmanuel-marty/lzsa)-like methods to compress 
the stream of YM registers, using a small rolling window of previous frames. This means that the memory 
footprint is limited to the packed file plus a small temporary buffer, rather than storing a larger
YM dump in memory.

The packer aims to reduce the runtime memory used, so files won't appear as small as if you
used a "normal" packer. But those packers require the entire unpacked file in memory, whereas
Minymiser only uses a very small cache at runtime (usually less than 5K in size.)

Packing files
-------------

To build the packer, see [packer/README.md](packer/README.md)

The packer command-line is of the form `miny <command> <infile> <outfile>`

... where the input is a YM3-format file, as produced by Hatari amongst others. The output is a .ymp
file that can be used with the playback code in the `player` directory.

The `command` controls how the file is packed.

* `small` generates a file with the smallest combined runtime file + memory cache footprint, but might take more CPU at runtime.
* `quick` generates a file with higher memory footprint, but will take the least CPU at runtime.
* `pack` allows you to pack with a custom cache (not recommended)
* `simple` converts a YM3 file to the fastest format: a 4-byte header, then N frames of 14 bytes containing each register value in order.

Playback
--------

The code in `/player` is example code for playback on a standard Atari ST. It should play back the compressed
streams in a few scanlines on an 8MHz machine.

The current file format used is to compress each register's data stream separately, so it stores
14 streams in the file. Each register stream has an individually-sized window to look back into and 
store matches. The player decompresses these in real time.

File Sizes / Memory
-------------------

The LZ methods are around 10%-40% of the size of the delta-packed bitmask, and generally 5-10% of the size of the raw .ym file. When the runtime buffer is included, this can be up to around 15% sometimes.

Test Data
---------

There are 3 test data streams in the repo. Please note that the "motus.ym" file seems to be bad dump,
and some voices are garbled.

Omissions
---------

Only .ym3 input data format is supported.

The system doesn't support timer effects like SID. It might be possible to support these with
extensions to the system.

