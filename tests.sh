#!/usr/bin/env sh

mkdir test_output
rm -f test_output/*.ymp

for fname in `cd test_data && ls *.ym*`
do
	echo $fname
	python3 python/miny.py test_data/$fname test_output/$fname.py.ymp > test_output/$fname.py.txt
	cpp/bin/test test_data/$fname test_output/$fname.cpp.ymp > test_output/$fname.cpp.txt
	goexp/miny test_data/$fname test_output/$fname.go.ymp > test_output/$fname.go.txt
done

