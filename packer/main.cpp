#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <memory.h>
#include <assert.h>
#include <vector>

#define		INPUT_SIZE		(640000)
#define		REG_COUNT		(14)
// ---------------------------------------------------------------------------
class OutputBuffer
{
public:
	OutputBuffer()
	{
		memset(m_data, 0, sizeof(m_data));
		m_data[0] = 0x1;
		m_writeShift = 0;
		m_pCurrBits = NULL;
		m_pNext = m_data + 1U;

	}

	~OutputBuffer()
	{
	}

	void WriteBit(uint8_t bitVal)
	{
		if (m_writeShift == 0)
		{
			// Grab a new byte
			m_pCurrBits = m_pNext;		// This is where bits will next go
			m_pNext++;					// Next free pos
		}
		
		// Set the bit in currBits
		*m_pCurrBits |= (bitVal << m_writeShift);

		if (m_writeShift == 7)
		{
			m_pCurrBits = NULL;
			m_writeShift = 0;
			return;
		}
		m_writeShift++;
	}

	void WriteBits(uint32_t bitVal, uint32_t numBits)
	{
		uint32_t shift = numBits;
		// e.g. "4, 3, 2, 1"
		while (shift != 0)
		{
			WriteBit((bitVal >> (shift - 1)) & 1);
			--shift;
		}
	}

	void WriteByte(uint8_t byte)
	{
		*m_pNext = byte;
		m_pNext++;
	}
	
	int WriteToFile(const char* pFilename)
	{
		FILE* pOutfile = fopen(pFilename, "wb");
		if (!pOutfile)
		{
			printf("Can't read file\n");
			return 1;
		}
		int wBytes = fwrite(m_data, 1, m_pNext - m_data, pOutfile);
		fclose(pOutfile);
		return 0;
	}	

private:
	
	uint8_t			m_writeShift;			// which bit to write into
	uint8_t*		m_pNext;				// pointer to next free empty byte
	uint8_t*		m_pCurrBits;			// where bits should be written out
		
	uint8_t			m_data[INPUT_SIZE];		// data buffer
};


void WriteLengthArray(OutputBuffer& ob, const uint8_t* pArray, size_t count)
{
	// Write sets of 4 bits
	for (uint32_t i = 0; i < count; ++i)
	{
		ob.WriteBits(pArray[i], 4);
	}
}

// ----------------------------------------------------------------------------
//	LZ STRUCTURES
// ----------------------------------------------------------------------------
// Describes a prior match in the input stream
struct MatchPair
{
	bool IsMatch() const { return length != 0; }
	uint32_t EncodedBytesCount() const { return (length == 0) ?  1 : length; }

	void SetLiteral(uint32_t literalPosition)
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
private:	
	uint32_t	length;			// 0 for literal
	uint32_t	offset;			// Offset to previous match if match, else location of literal in input buffer
};

// ----------------------------------------------------------------------------
// Stores the set of matches for any byte in the input stream.
struct MatchList
{
	//std::vector<MatchPair>	m_list;		// Set of multiple matches found
	MatchPair best;
};

// ---------------------------------------------------------------------------
struct MatchCache
{

};

// ---------------------------------------------------------------------------
MatchPair FindLongestMatch(
		const uint8_t* pData, uint32_t data_size, MatchCache& cache,
		uint32_t pos,
		uint32_t max_dist)
{
	// Scan back to find matches
	uint32_t best_length = 0;
	MatchPair best_pair;
	best_pair.SetLiteral(pos);

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
void ScanMatches(const uint8_t* pData, uint32_t data_size, uint32_t max_dist)
{
	/*
		NOTE: this is a very slow N^2 algorithm. We can cache results 
		based on the starting patterns to make it much quicker.
	*/
	MatchList* pMatchLists = new MatchList[data_size];
	MatchCache cache;

	uint32_t match_bytes = 0;
	uint32_t literal_bytes = 0;
	uint32_t head = 0;

	while (head < data_size)
	{
		MatchPair best = FindLongestMatch(pData, data_size, cache,
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
		head += best.EncodedBytesCount();
	}

	printf("Match size..%u, Literal size..%u\n", match_bytes, literal_bytes);
	delete [] pMatchLists;
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

	uint32_t num_frames = reg_data_size / REG_COUNT;
	for (int reg = 0; reg < REG_COUNT; ++reg)
	{
		printf("Reg: %d\n", reg);
		const uint8_t* reg_data = pBaseRegs + reg * num_frames;
		ScanMatches(reg_data, num_frames, 512u);
	}

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