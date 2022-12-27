#!/usr/bin/env python3
import numpy as np
import matplotlib.pyplot as plt

def percent(inp, outp):
	return inp * 100.0 / (inp + outp)

class Stats:
	def __init__(self):
		self.offsets = []
		self.counts = []

def find_quick_match(data, pos, dist, multiple):
	best_value = 0
	match = (0,0)

	for offset in range(multiple, dist, multiple):
		test_pos = pos - offset
		if test_pos < 0:
			break

		# Find match length
		count = 0
		while pos + count < len(data):
			if data[pos + count] != data[test_pos + count]:
				break
			count += 1

		# Reduce to set of N bytes
		count = int(count / multiple) * multiple

		# heuristic: choose longest match
		value = count
		if value > best_value:
			match = (offset, count)
			best_value = value
	return match

def create_tokens(data, search_len, stats, multiple):
	pos = 0
	data_len = len(data)
	lit_count = 0
	match_count = 0
	match_bytes = 0

	packing = []
	open_literal = bytearray()

	while pos < data_len:
		(offset, count) = find_quick_match(data, pos, search_len, multiple)
		if count > 1:
			# Good match, probably
			#print("Dist {} Len {}".format(offset, count))
			pos += count
			match_bytes += count
			match_count += 1
			stats.offsets.append(offset)
			stats.counts.append(count)
			if len(open_literal) != 0:
				packing.append(("L", open_literal))
				open_literal = bytearray()

			packing.append(("M", count, offset, pos - count))	# last is for debug
		else:
			#print("Literal {}".format(data[pos]))
			for n in range(0, multiple):
				open_literal.append(data[pos])
				lit_count += 1
				pos += 1

	if len(open_literal) != 0:
		packing.append(("L", open_literal))
		open_literal = bytearray()

	#print("Done")
	print("Matches {} Literals {} ({:.2f})%".format(match_count, lit_count, percent(match_count, lit_count)))
	print("Match bytes {} of {} {:.1f}%".format(match_bytes, data_len, 100 * match_bytes / data_len))
	return packing

def unpack(input, multiple):
	class Input:
		def __init__(self, input):
			self.input = input
			self.pos = 0

		def byte(self):
			a = self.input[self.pos]
			self.pos += 1
			return a

	i = Input(input)
	output = bytearray()
	while i.pos < len(input):
		cmd = i.byte()
		if cmd & 0x80:
			# Literals
			count = cmd & 0x7f
			if count == 0:
				count = i.byte() << 8 | i.byte()
			count *= multiple
			for x in range(0, count):
				output.append(i.byte())
		else:
			count = cmd & 0x7f
			if count == 0:
				count = i.byte() << 8 | i.byte()
			offset = i.byte()
			if offset == 0:
				offset = i.byte() << 8 | i.byte()
			count *= multiple
			offset *= multiple
			for x in range(0, count):
				v = output[len(output) - offset]
				output.append(v)
	return output

def create_bytestream(packed, multiple):
	""" Format:
	
		byte		bit 7: 1 == literals 0 == match
					bits 6-0: if 0, long count (2 bytes)
							  if != 0, count-1 (7 bits 0-128)
		if long_count:
			2 bytes - count 0-fffff

		--------
		if literals: stop
		--------
		if matches:
		byte		if 0, long_count

		if long_count:
			2 bytes - count 0-fffff
	"""

	def output_count(count, literal_flag):
		assert(count > 0)
		assert((count % multiple) == 0)
		count = int(count / multiple)
		if count < 128:
			output.append(count | literal_flag)
		else:
			output.append(0 | literal_flag)
			output.append(count >> 8)
			output.append(count & 255)

	def output_offset(offset):
		assert(offset > 0)
		assert((offset % multiple) == 0)
		offset = int(offset / multiple)
		if offset < 256:
			output.append(offset)
		else:
			output.append(0)
			output.append(offset >> 8)
			output.append(offset & 255)

	output = bytearray()
	for p in packed:
		#print(p)
		if p[0] == "L":
			# literals
			lits = p[1]
			count = len(lits)
			output_count(count, 0x80)
			output += lits
		else:
			count, offset = p[1:3]
			output_count(count, 0)
			output_offset(offset)
	return output

def all_in_one(unpacked_data, search_size, stats, multiple):
	tokens = create_tokens(unpacked_data, search_size, stats, multiple)
	packed_bytes = create_bytestream(tokens, multiple)

	u = unpack(packed_bytes, multiple)
	print(len(u))
	assert(bytes(u) == unpacked_data)
	return packed_bytes

def get_channel(all_data, num_vbls, reg):
	base = reg * num_vbls
	return all_data[base:base+num_vbls]

def interleave(list1, list2):
	import itertools
	return bytearray(itertools.chain(*zip(list1, list2)))


def read_ym(fname, outfname):
	print("============== new file: {} ================".format(fname))
	strm = open(fname, "rb")
	head = strm.read(4)
	all_data = strm.read()
	strm.close()

	num_vbls = int(len(all_data) / 14)
	stats = Stats()

	raw = [None] * 14
	packed = [None] * 14
	for r in range(0, 14):
		raw[r] = get_channel(all_data, num_vbls, r)

	for r0, r1 in ((0,1), (2,3), (4,5)):
		print("==== regs: {} {} ====".format(r0, r1))
		tmp = interleave(raw[r0], raw[r1])
		packed_bytes = all_in_one(tmp, 1023, stats, 2)
		print(len(packed_bytes))

		packed_bytes1 = all_in_one(raw[r0], 511, stats, 1)
		packed_bytes2 = all_in_one(raw[r1], 511, stats, 1)
		print(len(packed_bytes1) + len(packed_bytes2))

	"""
	for r in range(0, 14):
		print("==== reg {} ====".format(r))
		tokens = create_tokens(raw[r], 511, stats, 1)
		for t in tokens:
			print(t)

		packed_bytes = create_bytestream(tokens)
		print(len(packed_bytes))
		
		# Check unpack process
		u = unpack(packed_bytes)
		print(len(u))
		assert(bytes(u) == raw[r])

		packed[r] = packed_bytes

	# Output offsets
	import struct
	offset = 14*4
	for r in range(0, 14):
		print(offset)
		outstrm.write(struct.pack(">I", offset))
		offset += len(packed[r])

	outstrm  = open(outfname, "wb")
	for r in range(0, 14):
		outstrm.write(packed[r])
	outstrm.close()

	#plt.scatter(stats.offsets, stats.counts, s=1, alpha=0.5)
	#plt.show()
	#plt.hist(stats.offsets, bins=128)
	#plt.show()
	#plt.hist(stats.counts, bins=128)
	#plt.show()

	#for l in pack[0]:
	#	print(l)
	"""

#read_ym(open("led1.ym", "rb"), open("led1.ymp", "wb"))	 WRONG FORMAT
read_ym("sanxion.ym", "sanxion.ymp")
read_ym("motus.ym", "motus.ymp")
read_ym("led2.ym", "led2.ymp")
