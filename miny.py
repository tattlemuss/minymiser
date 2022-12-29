#!/usr/bin/env python3
import numpy as np
import matplotlib.pyplot as plt

def percent(inp, outp):
	return inp * 100.0 / (inp + outp)

class Settings:
	def __init__(self):
		self.search_dist = 4096

class Stats:
	def __init__(self):
		self.offsets = []
		self.counts = []

class MatchCache:
	""" Stores locations where a given byte has already been seen """
	def __init__(self, data):
		self.cache = {}

	def add(self, value, offset):
		if value in self.cache:
			# Newest values go at front
			self.cache[value].insert(0, offset)
		else:
			self.cache[value] = [offset]

	def get(self, value):
		if value in self.cache:
			return self.cache[value]
		else:
			return ()

	def cull(self, value, pos):
		if value in self.cache:
			while self.cache[value][-1] < pos:
				self.cache[value].pop()

class PackFormat1:
	def __init__(self):
		pass

	def calc_match_cost(self, count, offset, multiple):
		count = int(count / multiple)
		offset = int(offset / multiple)

		cost = 2
		if count >= 256:
			cost += 1
		if offset >= 256:
			cost += 1
		return cost

	def create_bytestream(self, packed, multiple):
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

	def unpack(self, input, multiple):
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

class PackFormat2:
	def __init__(self):
		pass

	def calc_match_cost(self, count, offset, multiple):
		count = int(count / multiple)
		offset = int(offset / multiple)

		cost = 2		# initial 4 bits, 1 byte for offset
		if count >= 16:
			cost += 1
		if count >= 256 + 16:
			cost += 1

		if offset >= 256:
			cost += 1
		return cost

	def create_bytestream(self, tokens, multiple):
		""" Format:
		
			7 6 5 4 3 2 1 0
			L L L L M M M M 	L = literals count, M = match count

			Match is always > 1

			byte		if 0, long_count

			if long_count:
				2 bytes - count 0-fffff
		"""

		output = bytearray()
		i = 0
		while i <  len(tokens):
			# Are there literals before the match?
			lit = ('L', bytearray())
			match = ('M', 0, 0)
			if tokens[i][0] == "L":
				# literals
				lit = tokens[i]
				i += 1

			# Literals must always be followed by a match, else the tokens are
			# wrong
			if i < len(tokens):
				match = tokens[i]
				assert(match[0] == "M")
				i += 1

			literals = lit[1]
			litcount = int(len(literals) / multiple)
			matchcount = int(match[1] / multiple)
			matchoffset = int(match[2] / multiple)

			# Create header byte
			l0 = litcount
			longoffset = 0
			if l0 > 7:
				l0 = 7
			m0 = matchcount
			if m0 > 15:
				m0 = 15
			if matchoffset >= 256:
				longoffset = 0x80

			output.append(longoffset | l0 << 4 | m0)

			"""
* 0-248: the value is added to the 7 stored in the token, to compose the final literals length. 
For instance a length of 206 will be stored as 7 in the token + a single byte with the value of 199, as 7 + 199 = 206.
* 250: a second byte follows. The final literals value is 256 + the second byte. 
For instance, a literals length of 499 is encoded as 7 in the token, a byte with the value of 250, and a final byte with the value of 243, as 256 + 243 = 499.
* 249: a second and third byte follow, forming a little-endian 16-bit value. 
The final literals value is that 16-bit value. For instance, a literals length of 1024 is stored as 7 in the token, then byte values of 249, 0 and 4, as (4 * 256) = 1024.
			"""
			# Remaining lit count
			def output_count(count, already):
				# Litcount is already 15
				if count <= 253 + already:
					# 0-253
					output.append(count - already)
				else:
					if count + already + 254 <= 253:
						output.append(254, count - already - 254)
					else:
						# More than 254+already
						# Special case for 255-512?
						output.append(255)
						# Original value
						output.append(count >> 8)
						output.append(count & 255)

			if l0 == 7:
				output_count(litcount, 7)
			output += literals
			if m0 == 15:
				output_count(matchcount, 15)

			# Match offset, already flagged in header byte
			if longoffset:
				output.append(matchoffset >> 8)
				output.append(matchoffset & 255)
			else:
				output.append(matchoffset)

		return output

	def unpack(self, input, multiple):
		return
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

def find_quick_match(data, pos, dist, multiple, pack_format, cache):
	best_cost = 100000.0
	match = (0,0)

	# Find the recent matches for this char
	curr_value = data[pos]
	cache_hits = cache.get(curr_value)

	for test_pos in cache_hits:
		offset = pos - test_pos
		if offset > dist:
			break
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
		#if count == 0:
		#	continue
		if count < 3:
			continue

		# Calc this before any reductions
		bytes = pack_format.calc_match_cost(count, offset, multiple)

		cost = bytes / count	# Number of bytes encoded

		# heuristic: choose lowest packed:unpacked ratio
		#value = count
		if cost < best_cost:
			match = (offset, count)
			best_cost = cost

	return match

def create_tokens(data, search_len, stats, multiple, pack_format):
	pos = 0
	data_len = len(data)
	lit_count = 0
	match_count = 0
	match_bytes = 0

	packing = []
	open_literal = bytearray()
	cache = MatchCache(data)

	while pos < data_len:
		(offset, count) = find_quick_match(data, pos, search_len, multiple, pack_format, cache)
		if count > multiple:
			# Good match, probably
			#print("Dist {} Len {}".format(offset, count))
			for n in range(0, count):
				cache.add(data[pos], pos)
				cache.cull(data[pos], pos - search_len)
				pos += 1
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
				cache.add(data[pos], pos)
				cache.cull(data[pos], pos - search_len)
				pos += 1

	if len(open_literal) != 0:
		packing.append(("L", open_literal))
		open_literal = bytearray()

	#print("Done")
	#print("Matches {} Literals {} ({:.2f})%".format(match_count, lit_count, percent(match_count, lit_count)))
	print("Match bytes {} of {} {:.1f}%".format(match_bytes, data_len, 100 * match_bytes / data_len))
	return packing

def all_in_one(unpacked_data, search_size, stats, multiple, pack_format):
	tokens = create_tokens(unpacked_data, search_size, stats, multiple, pack_format)
	packed_bytes = pack_format.create_bytestream(tokens, multiple)

	print("Packed size: {}".format(len(packed_bytes)))
	u = pack_format.unpack(packed_bytes, multiple)
	assert(bytes(u) == unpacked_data)
	#print("Unpack check OK")
	return packed_bytes

def get_channel(all_data, num_vbls, reg):
	base = reg * num_vbls
	return all_data[base:base+num_vbls]

def interleave(list1, list2):
	import itertools
	return bytearray(itertools.chain(*zip(list1, list2)))

def read_ym(fname, outfname, pack_format, settings):
	#pack_format = PackFormat2()
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

	for r in range(0, 14):
		print("==== reg {} ====".format(r))
		packed_bytes = all_in_one(raw[r], settings.search_dist, stats, 1, pack_format)
		packed[r] = packed_bytes

	# Output offsets
	outstrm  = open(outfname, "wb")
	import struct
	offset = 14*4
	for r in range(0, 14):
		outstrm.write(struct.pack(">I", offset))
		offset += len(packed[r])

	for r in range(0, 14):
		outstrm.write(packed[r])
	outstrm.close()

def read_ym2(fname, outfname, pack_format, settings):
	print("============== new file 2: {} ================".format(fname))
	strm = open(fname, "rb")
	head = strm.read(4)
	all_data = strm.read()
	strm.close()

	num_vbls = int(len(all_data) / 14)
	stats = Stats()

	raws = []
	packeds = []
	cache_sizes = [None]
	for r in range(0, 14):
		raws.append(get_channel(all_data, num_vbls, r))

	stream_count = 0
	for r0, r1 in ((0,1), (2,3), (4,5), (11, 12)):
		print("==== regs: {} {} ====".format(r0, r1))
		tmp = interleave(raws[r0], raws[r1])
		packeds.append(all_in_one(tmp, settings.search_dist * 2, stats, 2, pack_format))

	for r0 in (6, 7, 8, 9, 10, 13):
		print("==== reg: {} ====".format(r0, r1))
		packeds.append(all_in_one(raws[r0], settings.search_dist, stats, 1, pack_format))

	# Output offsets
	outstrm  = open(outfname, "wb")
	import struct
	stream_count = len(packeds)
	offset = stream_count*4
	for r in range(0, stream_count):
		outstrm.write(struct.pack(">I", offset))
		offset += len(packeds[r])

	for r in range(0, stream_count):
		outstrm.write(packeds[r])
	outstrm.close()

	#for l in pack[0]:
	#	print(l)

#read_ym(open("led1.ym", "rb"), open("led1.ymp", "wb"))	 WRONG FORMAT
pack_format = PackFormat1()
settings = Settings()
settings.search_dist = 512
read_ym("sanxion.ym", "sanxion.ymp", pack_format, settings)
#read_ym2("sanxion.ym", "sanxion.ymp2", pack_format, settings)

read_ym("motus.ym", "motus.ymp", pack_format, settings)
#read_ym2("motus.ym", "motus.ymp2", pack_format, settings)

read_ym("led2.ym", "led2.ymp", pack_format, settings)
#read_ym2("led2.ym", "led2.ymp2", pack_format, settings)
