#!/usr/bin/env python3

def read_ym(strm, outstrm):
	head = strm.read(4)
	print("============== new file ================")

	reg_dict = {}

	CACHE_SIZE = 255
	recent_buff = [None] * CACHE_SIZE

	in_count = 0
	out_count = 0
	in_recent = 0
	out_recent = 0

	packed = []
	recent_pos = 0

	packed = bytearray()

	all_data = strm.read()
	#assert((len(all_data) % 14) == 0)

	num_vbls = int(len(all_data) / 14)

	for curr_vbl in range(0, num_vbls):
		regs = bytearray(14)
		for r in range(0, 14):
			regs[r] = all_data[r * num_vbls + curr_vbl]

		regs = bytes(regs)

		if regs in reg_dict:
			in_count += 1
		else:
			reg_dict[regs] = True
			out_count += 1

		# Encode using the "recent buffer"
		try:
			r_index = recent_buff.index(regs)
			in_recent += 1
			packed.append(r_index + 1)
		except ValueError:
			# Not in cache
			out_recent += 1
			recent_buff[recent_pos] = regs
			recent_pos = (recent_pos + 1) % CACHE_SIZE

			packed.append(0)
			packed += regs

	print("In", in_count)
	print("Out:", out_count, " = ", out_count * 14 / 1024, "KB")
	print("Percent", in_count * 100.0 / (in_count + out_count))

	print("======== Recent ")
	print("In {}".format(in_recent))
	print("Out: {} -> {}". format(out_recent, out_recent * 15))
	print("Hit Percent", in_recent * 100.0 / (in_recent + out_recent))
	print("Output data {} bytes".format(len(packed)))
	outstrm.write(packed)

#read_ym(open("led1.ym", "rb"), open("led1.ymp", "wb"))
read_ym(open("sanxion.ym", "rb"), open("sanxion.ymp", "wb"))
read_ym(open("motus.ym", "rb"), open("motus.ymp", "wb"))
#read_ym(open("test.ym", "rb"), open("test.ymp", "wb"))
