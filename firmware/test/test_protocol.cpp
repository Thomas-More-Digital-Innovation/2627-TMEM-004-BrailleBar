// Pc-test voor Protocol.h, vanuit firmware/test:  g++ -std=c++11 test_protocol.cpp -o test_protocol && ./test_protocol
#include <assert.h>
#include <stdio.h>
#include "../Protocol.h"

int main() {
  Chunk c;

  assert(xorChecksum("ab", 2) == 0x03);
  assert(xorChecksum("a b", 3) == 0x23);
  assert(xorChecksum("", 0) == 0);

  assert(parseChunk("CHUNK|j1|5|ab|03", c) == PARSE_OK);
  assert(!strcmp(c.jobId, "j1") && c.seq == 5 && !strcmp(c.text, "ab"));

  assert(parseChunk("CHUNK|j1|6|a b|23", c) == PARSE_OK); // spatie in de tekst
  assert(!strcmp(c.text, "a b"));

  assert(parseChunk("CHUNK|j1|0||00", c) == PARSE_OK); // lege regel
  assert(c.text[0] == '\0' && c.seq == 0);

  // seq bruikbaar, rest stuk -> NAK
  assert(parseChunk("CHUNK|j1|5|ab|04", c) == PARSE_BAD && c.seq == 5); // verkeerde checksum
  assert(parseChunk("CHUNK|j1|5|ab|3", c) == PARSE_BAD);                // checksum te kort
  assert(parseChunk("CHUNK|j1|5|ab|0x", c) == PARSE_BAD);               // geen hex
  assert(parseChunk("CHUNK|j1|5|ab", c) == PARSE_BAD);                  // checksum ontbreekt
  assert(parseChunk("CHUNK||5|ab|03", c) == PARSE_BAD);                 // lege jobId
  assert(parseChunk("CHUNK|j1|-1|ab|03", c) == PARSE_BAD);              // negatief seq
  assert(parseChunk("CHUNK|j1|5|abcdefghijklmnopqrstuvwxyzabcde|00", c) == PARSE_BAD); // 31 tekens > MAX_CELLS

  // geen seq -> stil negeren
  assert(parseChunk("CHUNK|j1|x|ab|03", c) == PARSE_NO_SEQ);
  assert(parseChunk("CHUNK|j1|", c) == PARSE_NO_SEQ);
  assert(parseChunk("CHUNK|j1||ab|03", c) == PARSE_NO_SEQ);
  assert(parseChunk("STATUS", c) == PARSE_NO_SEQ);

  puts("ok");
  return 0;
}
