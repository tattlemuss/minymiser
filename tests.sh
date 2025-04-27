#!/usr/bin/env sh
# This script runs all modes of the compressor on each .ym file
# in the test_data directory.
# The output files are named such that it should be easy to compare the
# output filesize.

mkdir test_output
rm -f test_output/*.ymp

for fname in `cd test_data && ls *.ym*`
do
	echo $fname
	packer/miny pack test_data/$fname test_output/$fname.pack.ymp > test_output/$fname.pack.txt
	packer/miny small test_data/$fname test_output/$fname.small.ymp > test_output/$fname.small.txt
	packer/miny quick test_data/$fname test_output/$fname.quick.ymp > test_output/$fname.quick.txt
	packer/miny simple test_data/$fname test_output/$fname.yu > test_output/$fname.simple.txt
	packer/miny delta test_data/$fname test_output/$fname.yd > test_output/$fname.delta.txt
done

