#!/usr/bin/env python3

import numpy as np
import matplotlib.pyplot as plt


def percent(inp, outp):
	return inp * 100.0 / (inp + outp)

class Stats:
	def __init__(self):
		self.offsets = []
		self.counts = []

def find_quick_match(data, pos, dist):
	best_value = 0
	match = (0,0)

	for offset in range(1, dist):
		test_pos = pos - offset
		if test_pos < 0:
			break

		# Find match length
		count = 0
		while pos + count < len(data):
			if data[pos + count] != data[test_pos + count]:
				break
			count += 1

		# heuristic: choose longest match
		value = count
		if value > best_value:
			match = (offset, count)
			best_value = value
	return match

def encode(data, search_len, stats):
	pos = 0
	data_len = len(data)
	lit_count = 0
	match_count = 0
	match_bytes = 0

	while pos < data_len:
		(offset, count) = find_quick_match(data, pos, search_len)
		if count > 1:
			# Good match, probably
			#print("Dist {} Len {}".format(offset, count))
			pos += count
			match_bytes += count
			match_count += 1
			stats.offsets.append(offset)
			stats.counts.append(count)
		else:
			#print("Literal {}".format(data[pos]))
			pos += 1
			lit_count += 1
	#print("Done")
	print("Matches {} Literals {} ({:.2f})%".format(match_count, lit_count, percent(match_count, lit_count)))
	print("Match bytes {} of {} {:.1f}%".format(match_bytes, data_len, 100 * match_bytes / data_len))

def read_ym(strm, outstrm):
	head = strm.read(4)
	print("============== new file ================")

	reg_dict = {}

	packed = bytearray()

	all_data = strm.read()
	#assert((len(all_data) % 14) == 0)

	num_vbls = int(len(all_data) / 14)
	stats = Stats()

	for r in range(0, 14):
		base = r * num_vbls
		reg_0 = all_data[base:base+num_vbls]
		print("==== reg {} ====".format(r))
		encode(reg_0, 8192, stats)

	plt.scatter(stats.offsets, stats.counts, s=1, alpha=0.5)
	#print(offsets)
	#plt.hist(offsets, bins=128)
	#plt.hist(stats.counts, bins=128)


	plt.show()


read_ym(open("led1.ym", "rb"), open("led1.ymp", "wb"))
#read_ym(open("sanxion.ym", "rb"), open("sanxion.ymp", "wb"))
#read_ym(open("motus.ym", "rb"), open("motus.ymp", "wb"))
