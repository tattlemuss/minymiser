#!/usr/bin/env sh
# This script runs all 3 versions of the compressor on each .ym file
# in the test_data directory.
# The output files are named such that it should be easy to compare the
# output filesize.

mkdir test_output
rm -f test_output/*.ymp

for fname in `cd test_data && ls *.ym*`
do
	echo $fname
	python3 python/miny.py test_data/$fname test_output/$fname.py.ymp > test_output/$fname.py.txt
	cpp/bin/test test_data/$fname test_output/$fname.cpp.ymp > test_output/$fname.cpp.txt
	goexp/miny pack test_data/$fname test_output/$fname.go.ymp > test_output/$fname.go.txt
done

