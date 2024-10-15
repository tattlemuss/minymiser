This is an experimental [go](https://go.dev/) version of the compressor executable.

Building
--------

* Download and run the Go installer
* `go build`
* This should produce a `miny` executable.
* Command-line is of the form `miny pack <infile> <outfile>`
* There are options to change the verbosity and cache size
* There is also the `miny minpack` command to find a minimum runtime RAM size by adjusting the cache size.
