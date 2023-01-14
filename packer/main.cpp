#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <memory.h>
#include <assert.h>
#include <vector>

// ---------------------------------------------------------------------------
#define		REG_COUNT		(14)

// ---------------------------------------------------------------------------
class OutputBuffer : public std::vector<uint8_t>
{
public:
};

// ----------------------------------------------------------------------------
//	LZ STRUCTURES
// ----------------------------------------------------------------------------
// Describes a prior match in the input stream, or a single literal
struct Match
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
// Future accelerator for finding matches
struct MatchCache
{
};

// ---------------------------------------------------------------------------
struct LazyState
{
	// Current size of open literal run
	uint32_t m_numLiterals = 0;

	uint32_t CalcCost(uint32_t numLiterals, const Match* match) const
	{

		uint32_t cost = 0;
		uint32_t tmpLiterals = m_numLiterals;
		// Literal cost depends on number of literals already accumulated.
		cost += numLiterals;							// cost of literal itself
		for (uint32_t i = 0; i < numLiterals; ++i)
		{
			if (tmpLiterals == 0)
				cost++;			// switch cost
			if (tmpLiterals == 127)
				cost++;			// needs 2 bytes now
			++tmpLiterals;
		}

		// Match
		// A match is always new, so apply full cost
		if (match)
		{
			assert(match->IsMatch());
			cost = 1;
			if (match->GetLength() >= 128)
				cost++;

			cost += 1;
			if (match->GetOffset() >= 256)
				cost++;
		}
		return cost;
	}

	void ApplyCost(const Match& match)
	{
		if (match.IsLiteral())
			++m_numLiterals;
		else
			m_numLiterals = 0;
	}
};

// ---------------------------------------------------------------------------
Match FindLongestMatch(
		const uint8_t* pData, uint32_t data_size, MatchCache& cache,
		uint32_t pos,
		uint32_t max_dist)
{
	// Scan back to find matches
	uint32_t best_length = 0;
	Match best_pair;
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
Match FindCheapestMatch(
		const uint8_t* pData, uint32_t data_size, LazyState& state,
		uint32_t pos,
		uint32_t max_dist)
{
	// Scan back to find matches
	float best_ratio = 1.0f;		// enc bytes vs cost

	Match best_pair;
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
		if (match_length >= 3)
		{
			Match pair;
			pair.SetMatch(match_length, offset);
			float ratio = ((float) state.CalcCost(0, &pair)) / match_length;
			if (ratio < best_ratio)
			{
				best_pair.SetMatch(match_length, offset);
				best_ratio = ratio;
			}
		}
	}
	return best_pair;
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
void EncodeV1(OutputBuffer& output, const std::vector<Match>& matches)
{
	size_t index = 0;
	while (index < matches.size())
	{
		// Read all contiguous literals
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

// ---------------------------------------------------------------------------
void MatchGreedy(const uint8_t* pData, uint32_t data_size, uint32_t max_dist,
	std::vector<Match>& matches)
{
	matches.clear();
	MatchCache cache;

	uint32_t match_bytes = 0;
	uint32_t literal_bytes = 0;
	uint32_t head = 0;

	while (head < data_size)
	{
		Match best = FindLongestMatch(pData, data_size, cache,
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

// ---------------------------------------------------------------------------
void MatchLazy(const uint8_t* pData, uint32_t data_size, uint32_t max_dist,
	std::vector<Match>& matches)
{
	matches.clear();
	MatchCache cache;

	uint32_t used_match = 0;
	uint32_t used_matchlit = 0;
	uint32_t used_second = 0;
	
	uint32_t head = 0;

	LazyState state;
	while (head < data_size)
	{
		Match best0 = FindLongestMatch(pData, data_size, cache, head, max_dist);
		bool choose_lit = best0.IsLiteral();

		// We have 2 choices really
		// Apply 0 (as a match or a literal)
		// Apply literal 0 (and check the next byte for a match)
		if (!choose_lit)
		{
			// See if doing N literals is smaller
			uint32_t cost0 = state.CalcCost(0, &best0);
			uint32_t cost_lit = state.CalcCost(best0.EncodedBytesCount(), nullptr);
			if (cost_lit < cost0)
			{
				choose_lit = true;
				used_matchlit++;
			}
		}

		if (!choose_lit)
		{
			used_match++;
			if (best0.IsMatch() && head + 1 < data_size)
			{
				Match best1;
				best1 = FindLongestMatch(pData, data_size, cache, head + 1, max_dist);
				if (best1.IsMatch())
				{
					uint32_t cost0 = state.CalcCost(0, &best0);
					uint32_t cost1 = state.CalcCost(1, &best1);
					float rate0 = ((float)cost0) / best0.EncodedBytesCount();
					float rate1 = ((float)cost1) / (1 + best1.EncodedBytesCount());
					if (rate1 < rate0)
					{
						choose_lit = true;
						used_match--;
						used_second++;
					}
				}
			}
		}

		// Add N literals, plus the match
		if (choose_lit)
		{
			Match lit;
			lit.SetLiteral(pData[head]);
			
			matches.push_back(lit);
			state.ApplyCost(lit);
			++head;
		}
		else
		{
			used_match++;
			matches.push_back(best0);
			state.ApplyCost(best0);
			head += best0.EncodedBytesCount();
		}
	}
	printf("Used match: %u, used matchlit: %u, used second %u\n",
			used_match, used_matchlit, used_second);
}

// ----------------------------------------------------------------------------
int ProcessFile(const uint8_t* data, uint32_t data_size, const char* filename_out)
{
	const uint32_t search_size = 512U;

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

	OutputBuffer buffersG[REG_COUNT];	// Greedy results
	OutputBuffer buffersL[REG_COUNT];	// Lazy results
	
	uint32_t num_frames = reg_data_size / REG_COUNT;
	for (int reg = 0; reg < REG_COUNT; ++reg)
	{
		printf("Reg: %d\n", reg);
		const uint8_t* reg_data = pBaseRegs + reg * num_frames;

		std::vector<Match> matches;
		MatchGreedy(reg_data, num_frames, search_size, matches);
		EncodeV1(buffersG[reg], matches);
		printf("Greedy packed size: %u\n", buffersG[reg].size());

		matches.clear();
		MatchLazy(reg_data, num_frames, search_size, matches);
		EncodeV1(buffersL[reg], matches);
		printf("Lazy packed size: %u\n", buffersL[reg].size());
	}

	FILE* pOutfile = fopen(filename_out, "wb");
	if (!pOutfile)
	{
		fprintf(stderr, "ERROR: can't open %s\n", filename_out);
		return 1;
	}

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
		offset += buffersL[reg].size();
	}

	for (int reg = 0; reg < REG_COUNT; ++reg)
		fwrite(buffersL[reg].data(), 1, buffersL[reg].size(), pOutfile);

	fclose(pOutfile);
	return 0;
}

// ----------------------------------------------------------------------------
int main(int argc, char** argv)
{
	if (argc != 3)
	{
		fprintf(stderr, "Usage: <infile> <outfile>\n");
		return 1;
	}
	const char* filename_in = argv[1];
	const char* filename_out = argv[2];

	FILE* pInfile = fopen(filename_in, "rb");
	if (!pInfile)
	{
		fprintf(stderr, "Can't read file\n");
		return 1;
	}

	fseek(pInfile, 0, SEEK_END);
	long data_size = ftell(pInfile);
	fseek(pInfile, 0, SEEK_SET);

	printf("File size %ld bytes\n", data_size);

	uint8_t* pData = (uint8_t*) malloc(data_size);
	int readBytes = fread(pData, 1, data_size, pInfile);
	fclose(pInfile);

	int ret = ProcessFile(pData, readBytes, filename_out);

	free(pData);
	return ret;
}
