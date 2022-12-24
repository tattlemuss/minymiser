#!/usr/bin/env python3

def read_ym(strm):
	head = strm.read(4)
	print("============== new file ================")

	reg_dict = {}

	recent_buff = []

	in_count = 0
	out_count = 0
	in_recent = 0
	out_recent = 0

	packed = []
	
	while True:
		regs = strm.read(14)
		if len(regs) == 0:
			break
		if regs in reg_dict:
			in_count += 1
		else:
			reg_dict[regs] = True
			out_count += 1

		try:
			r_index = recent_buff.index(regs)
			in_recent += 1
			packed.append(r_index)
		except ValueError:
			out_recent += 1
			recent_buff.append(regs)
			# recycle
			if len(recent_buff) > 255:
				recent_buff = recent_buff[1:]	# kill oldest
			packed.append(0)
			packed.append((regs))

	print("In", in_count)
	print("Out:", out_count, " = ", out_count * 14 / 1024, "KB")
	print("Percent", in_count * 100.0 / (in_count + out_count))

	print("======== Recent ")
	print("In", in_recent)
	print("Out:", out_recent)
	print("Percent", in_recent * 100.0 / (in_recent + out_recent))
	print("Output data {} bytes".format(len(packed)))

read_ym(open("/home/steve/led1.ym", "rb"))
read_ym(open("/home/steve/sanxion.ym", "rb"))
