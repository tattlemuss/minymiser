#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <memory.h>
#include <assert.h>
#include <vector>

// ----------------------------------------------------------------------------
#define		REG_COUNT		(14)

// ----------------------------------------------------------------------------
typedef std::vector<uint8_t>  OutputBuffer;

// ----------------------------------------------------------------------------
//	LZ STRUCTURES
// ----------------------------------------------------------------------------
// Describes a prior match in the input stream.
struct Match
{
	uint32_t	length = 0;		// 0 for literal
	uint32_t	offset = 0;		// Offset to previous match if match, else value of literal in input buffer
};

// ----------------------------------------------------------------------------
// Describes a number of literals followed by a match.
struct Token
{
	uint32_t	literal_len = 0;		// Number of literals before the match. Can be 0.
	Match		match;					// LZ-style match
};

// ----------------------------------------------------------------------------
class TokenStream : public std::vector<Token>
{
public:

	void AddMatch(const Match& m)
	{
		if (size() == 0 ||
			back().match.length != 0)
		{
			push_back(Token());
			back().literal_len = 0;
			back().match.length = 0;
			back().match.offset = 0;
		}
		back().match = m;
	}

	void AddLiterals(uint32_t num_literals)
	{
		if (size() == 0 ||
			back().match.length != 0)
		{
			push_back(Token());
			back().literal_len = 0;
			back().match.length = 0;
			back().match.offset = 0;
		}
		back().literal_len += num_literals;
	}
};

// ----------------------------------------------------------------------------
// Future accelerator for finding matches
struct MatchCache
{
};

// ----------------------------------------------------------------------------
struct EncoderV1
{
	// Current size of open literal run
	uint32_t m_numLiterals = 0;

	void Reset()
	{
		m_numLiterals = 0;
	}

	// Return the additional cost (in bytes) of adding literal(s) and match to an output stream
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
			cost = 1;
			if (match->length >= 128)
				cost++;

			cost += 1;
			if (match->offset >= 256)
				cost++;
		}
		return cost;
	}

	void ApplyLiterals(uint32_t numLiterals)
	{
		m_numLiterals += numLiterals;
	}

	void ApplyMatch(const Match& match)
	{
		m_numLiterals = 0;
	}

	void Encode(OutputBuffer& output, const uint8_t* data, const TokenStream& tokens)
	{
		size_t index = 0;
		while (index < tokens.size())
		{
			// Encode literals
			size_t litCount = tokens[index].literal_len;
			if (litCount)
			{
				// Encode the literal
				EncodeCount(output, litCount, 0x80);
				for (uint32_t i = 0; i < litCount; ++i)
					output.push_back(*data++);
			}

			// Match
			const Match& m = tokens[index].match;
			if (m.length)
			{
				EncodeCount(output, m.length, 0x0);
				EncodeOffset(output, m.offset);
				// Skip input data so literals stay aligned
				data += m.length;
			}
			index++;
		}
	}
private:
	// Write match or literal count
	static void EncodeCount(OutputBuffer& output, uint32_t count, uint8_t literal_flag)
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

	static void EncodeOffset(OutputBuffer& output, uint32_t offset)
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
};

// ---------------------------------------------------------------------------
Match FindLongestMatch(
		const uint8_t* data, uint32_t data_size, MatchCache& cache,
		uint32_t pos,
		uint32_t max_dist)
{
	// Scan back to find matches
	uint32_t best_length = 0;
	Match best_pair;

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
			if (data[back + match_length] != data[pos + match_length])
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
			best_pair.length = match_length;
			best_pair.offset = offset;
			best_length = match_length;
		}
	}
	return best_pair;
}

// ---------------------------------------------------------------------------
template <class ENCODER>
Match FindCheapestMatch(
		const uint8_t* data, uint32_t data_size, ENCODER& encoder,
		uint32_t pos,
		uint32_t max_dist)
{
	// Scan back to find matches
	float best_ratio = 1.0f;		// enc bytes vs cost
	Match best_pair;

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
			if (data[back + match_length] != data[pos + match_length])
				break;
			++match_length;
		}

		// Did we find a match?
		if (match_length >= 3)
		{
			Match pair;
			pair.length = match_length;
			pair.offset = offset;
			float ratio = ((float) encoder.CalcCost(0, &pair)) / match_length;
			if (ratio < best_ratio)
			{
				best_pair = pair;
				best_ratio = ratio;
			}
		}
	}
	return best_pair;
}

// ---------------------------------------------------------------------------
void MatchGreedy(TokenStream& tokens, const uint8_t* data, uint32_t data_size, uint32_t max_dist)
{
	tokens.clear();
	MatchCache cache;

	uint32_t match_bytes = 0;
	uint32_t literal_bytes = 0;
	uint32_t head = 0;

	while (head < data_size)
	{
		Match best = FindLongestMatch(data, data_size, cache,
				head, max_dist);

		if (best.length)
		{
			tokens.AddMatch(best);
			match_bytes += best.length;
			head += best.length;
		}
		else
		{
			tokens.AddLiterals(1);
			literal_bytes++;
			head++;
		}
	}

	printf("Match size..%u, Literal size..%u\n", match_bytes, literal_bytes);
}

// ---------------------------------------------------------------------------
template <class ENCODER>
void MatchLazy(TokenStream& tokens, const uint8_t* data, uint32_t data_size, uint32_t max_dist, ENCODER& encoder)
{
	tokens.clear();
	MatchCache cache;

	uint32_t used_match = 0;
	uint32_t used_matchlit = 0;
	uint32_t used_second = 0;
	
	uint32_t head = 0;
	encoder.Reset();

	while (head < data_size)
	{
		Match best0 = FindLongestMatch(data, data_size, cache, head, max_dist);
		bool choose_lit = best0.length == 0;

		// We have 2 choices really
		// Apply 0 (as a match or a literal)
		// Apply literal 0 (and check the next byte for a match)
		if (!choose_lit)
		{
			// See if doing N literals is smaller
			uint32_t cost0 = encoder.CalcCost(0, &best0);
			uint32_t cost_lit = encoder.CalcCost(best0.length, nullptr);
			if (cost_lit < cost0)
			{
				choose_lit = true;
				used_matchlit++;
			}
		}

		if (!choose_lit)
		{
			used_match++;
			// We only need to decide to choose the second match, if both
			// 0 and 1 are matches rather than literals.
			if (best0.length && head + 1 < data_size)
			{
				Match best1;
				best1 = FindLongestMatch(data, data_size, cache, head + 1, max_dist);
				if (best1.length)
				{
					uint32_t cost0 = encoder.CalcCost(0, &best0);
					uint32_t cost1 = encoder.CalcCost(1, &best1);
					float rate0 = ((float)cost0) / best0.length;
					float rate1 = ((float)cost1) / (1 + best1.length);
					if (rate1 < rate0)
					{
						choose_lit = true;
						used_match--;
						used_second++;
					}
				}
			}
		}

		// Add the decision to the token stream,
		// and update the encoder's state so it can update future encoding costs.
		if (choose_lit)
		{
			tokens.AddLiterals(1);
			encoder.ApplyLiterals(1);
			++head;
		}
		else
		{
			used_match++;
			tokens.AddMatch(best0);
			encoder.ApplyMatch(best0);
			head += best0.length;
		}
	}
	printf("Used match: %u, used matchlit: %u, used second %u\n",
			used_match, used_matchlit, used_second);
}

// ----------------------------------------------------------------------------
struct PackedFile
{
	OutputBuffer buffers[REG_COUNT];	// Packed data for each YM reg
};

// ----------------------------------------------------------------------------
int WriteFile(const char* filename_out, const PackedFile& file)
{
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
		offset += file.buffers[reg].size();
	}

	for (int reg = 0; reg < REG_COUNT; ++reg)
		fwrite(file.buffers[reg].data(), 1, file.buffers[reg].size(), pOutfile);

	fclose(pOutfile);
	return 0;
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

	PackedFile packedG;
	PackedFile packedL;
	uint32_t num_frames = reg_data_size / REG_COUNT;
	for (int reg = 0; reg < REG_COUNT; ++reg)
	{
		printf("Reg: %d\n", reg);
		const uint8_t* reg_data = pBaseRegs + reg * num_frames;

		EncoderV1 encoder;
		TokenStream tokens;
		/*
		MatchGreedy(tokens, reg_data, num_frames, search_size);
		encoder.Encode(packedG.buffers[reg], reg_data, tokens);
		printf("Greedy packed size: %u\n", packedG.buffers[reg].size());
		*/
		tokens.clear();
		MatchLazy(tokens, reg_data, num_frames, search_size, encoder);
		encoder.Encode(packedL.buffers[reg], reg_data, tokens);
		printf("Lazy packed size: %u\n", packedL.buffers[reg].size());
	}

	return WriteFile(filename_out, packedL);
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

	uint8_t* data = (uint8_t*) malloc(data_size);
	int readBytes = fread(data, 1, data_size, pInfile);
	fclose(pInfile);

	int ret = ProcessFile(data, readBytes, filename_out);

	free(data);
	return ret;
}
