#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <memory.h>
#include <assert.h>
#include <vector>

#define		INPUT_SIZE		(640000)
#define		REG_COUNT		(14)
// ---------------------------------------------------------------------------
class OutputBuffer : public std::vector<uint8_t>
{
public:
};

// ----------------------------------------------------------------------------
//	LZ STRUCTURES
// ----------------------------------------------------------------------------
// Describes a prior match in the input stream
struct Token
{
	bool IsMatch() const { return length != 0; }
	bool IsLiteral() const { return length == 0; }
	uint32_t EncodedBytesCount() const { return (length == 0) ?  1 : length; }

	void SetLiteral(uint8_t literalPosition)
	{
		this->length = 0;
		this->offset = literalPosition;
	}
	
	void SetMatch(uint32_t length, uint32_t offset)
	{
		this->length = length;
		this->offset = offset;
	}
	
	uint32_t GetLength() const { return length; }
	uint32_t GetOffset() const { return offset; }
	uint8_t GetLiteral() const { return offset; }
private:	
	uint32_t	length;			// 0 for literal
	uint32_t	offset;			// Offset to previous match if match, else value of literal in input buffer
};

// ---------------------------------------------------------------------------
struct MatchCache
{

};

// ---------------------------------------------------------------------------
Token FindLongestMatch(
		const uint8_t* pData, uint32_t data_size, MatchCache& cache,
		uint32_t pos,
		uint32_t max_dist)
{
	// Scan back to find matches
	uint32_t best_length = 0;
	Token best_pair;
	best_pair.SetLiteral(pData[pos]);

	// Scan backwards
	uint32_t offset = 1;
	for (;
		offset <= pos && offset <= max_dist;
		++offset)
	{
		uint32_t back = pos - offset;

		// Count how many matches we would get here
		uint32_t match_length = 0;
		while (pos + match_length < data_size)
		{
			if (pData[back + match_length] != pData[pos + match_length])
				break;
			++match_length;
		}

		// Did we find a match?
		bool add_match = false;

		if (match_length >= 3 && match_length > best_length)
			add_match = true;

		if (add_match)
		{
			assert(offset != 0);

			// Always add matches, since big offsets can actually
			// be encoded to shorter bit patterns than large offsets.
			//printf("%u match: L: %u off: %u\n", head, match_length, offset);
			best_pair.SetMatch(match_length, offset);
			best_length = match_length;
		}
	}
	return best_pair;
}

// ---------------------------------------------------------------------------
void MatchGreedy(const uint8_t* pData, uint32_t data_size, uint32_t max_dist,
	std::vector<Token>& matches)
{
	matches.clear();
	MatchCache cache;

	uint32_t match_bytes = 0;
	uint32_t literal_bytes = 0;
	uint32_t head = 0;

	while (head < data_size)
	{
		Token best = FindLongestMatch(pData, data_size, cache,
				head, max_dist);

		if (best.IsMatch())
		{
			//printf("Match Length %u Dist %u\n", best.GetLength(), best.GetOffset());
			match_bytes += best.GetLength();
		}
		else
		{
			//printf("Literal\n");
			literal_bytes++;
		}
		matches.push_back(best);
		head += best.EncodedBytesCount();
	}

	printf("Match size..%u, Literal size..%u\n", match_bytes, literal_bytes);
}

void EncodeCountV1(OutputBuffer& output, uint32_t count, uint8_t literal_flag)
{
	if (count < 128)
		output.push_back(count | literal_flag);
	else
	{
		output.push_back(0 | literal_flag);
		output.push_back(count >> 8);
		output.push_back(count & 255);
	}
}

void EncodeOffsetV1(OutputBuffer& output, uint32_t offset)
{
	assert(offset > 0);
	if (offset < 256)
		output.push_back(offset);
	else
	{
		output.push_back(0);
		output.push_back(offset >> 8);
		output.push_back(offset & 255);
	}
}

// ----------------------------------------------------------------------------
void EncodeV1(OutputBuffer& output, const std::vector<Token>& matches)
{
	size_t index = 0;
	while (index < matches.size())
	{
		// Read all literals
		size_t numLits = 0;
		size_t litIndex = index;
		while (litIndex < matches.size() && matches[litIndex].IsLiteral())
			++litIndex;

		size_t litCount = litIndex - index;
		if (litCount)
		{
			// Encode the literal
			EncodeCountV1(output, litCount, 0x80);
			for (size_t i = index; i < litIndex; ++i)
				output.push_back(matches[i].GetLiteral());
		}

		index = litIndex;
		if (index < matches.size())
		{
			// Match
			assert(matches[index].IsMatch());
			EncodeCountV1(output, matches[index].GetLength(), 0x0);
			EncodeOffsetV1(output, matches[index].GetOffset());
			index ++;
		}
	}
}
// ----------------------------------------------------------------------------
int ProcessFile(const uint8_t* data, uint32_t data_size)
{
	if (data[0] != 'Y' ||
		data[1] != 'M' ||
		data[2] != '3' ||
		data[3] != '!')
	{
		fprintf(stderr, "ERROR: not a YM3 file\n");
		return 1;
	}

	const uint8_t* pBaseRegs = data + 4;
	uint32_t reg_data_size = data_size - 4;
	if ((reg_data_size % REG_COUNT) != 0)
	{
		fprintf(stderr, "ERROR: bad YM3 size\n");
		return 1;
	}

	OutputBuffer buffers[REG_COUNT];
	uint32_t num_frames = reg_data_size / REG_COUNT;
	for (int reg = 0; reg < REG_COUNT; ++reg)
	{
		printf("Reg: %d\n", reg);
		const uint8_t* reg_data = pBaseRegs + reg * num_frames;

		std::vector<Token> matches;
		MatchGreedy(reg_data, num_frames, 512u, matches);
		EncodeV1(buffers[reg], matches);

		printf("Packed size: %u\n", buffers[reg].size());
	}

	FILE* pOutfile = fopen("test.out", "wb");
	uint32_t offset = 4 * REG_COUNT;
	for (int reg = 0; reg < REG_COUNT; ++reg)
	{
		uint32_t size = offset;
		uint8_t bigEnd[4];
		bigEnd[0] = (size >> 24) & 0xff;
		bigEnd[1] = (size >> 16) & 0xff;
		bigEnd[2] = (size >> 8) & 0xff;
		bigEnd[3] = (size >> 0) & 0xff;
		fwrite(bigEnd, 1, 4, pOutfile);
		offset += buffers[reg].size();
	}

	for (int reg = 0; reg < REG_COUNT; ++reg)
		fwrite(buffers[reg].data(), 1, buffers[reg].size(), pOutfile);

	return 0;
}

// ----------------------------------------------------------------------------
int main(int argc, char** argv)
{
	const char* filename_in = argv[1];
	FILE* pInfile = fopen(filename_in, "rb");
	if (!pInfile)
	{
		printf("Can't read file\n");
		return 1;
	}

	uint8_t* pData = (uint8_t*) malloc(INPUT_SIZE);
	int readBytes = fread(pData, 1, INPUT_SIZE, pInfile);
	printf("Read %d bytes\n", readBytes);
	fclose(pInfile);

	int ret = ProcessFile(pData, readBytes);

	free(pData);
	return ret;
}