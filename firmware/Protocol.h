#pragma once
// BrailleBar BLE-protocol, geen Arduino-afhankelijkheden zodat het op de pc getest kan worden.
//
// Eén bericht per regel, afgesloten met '\n'.
//   app -> printer   CHUNK|<jobId>|<seq>|<tekst>|<checksum>   één braille-regel (leeg = lege regel)
//                    STATUS | PAUSE | RESUME | CANCEL|<jobId>
//   printer -> app   ACK|<seq>   chunk is GEPRINT (of was al geprint)
//                    NAK|<seq>   chunk beschadigd, opnieuw sturen
//                    STATE|ready|busy|paused|error
// <checksum> = XOR van alle bytes van <tekst>, als 2 hoofdletter-hexcijfers ("ab" -> "03").
// Ondersteunde tekens: a-z (hoofdletters tellen als kleine letters) en spatie. Elk ander teken wordt een lege cel.

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

const int MAX_CELLS = 30; // tekens per regel, pas aan op de echte breedte van de machine
const int MAX_JOBID = 36; // lengte van een uuid

struct Chunk {
  char jobId[MAX_JOBID + 1];
  long seq;
  char text[MAX_CELLS + 1];
};

enum ParseResult {
  PARSE_OK,
  PARSE_NO_SEQ, // geen bruikbaar seq-nummer: kan niet NAK'en, de app probeert opnieuw na de timeout
  PARSE_BAD     // seq is geldig, de rest niet: NAK|seq
};

inline uint8_t xorChecksum(const char* s, size_t n) {
  uint8_t x = 0;
  for (size_t i = 0; i < n; i++) x ^= (uint8_t)s[i];
  return x;
}

// line = "CHUNK|<jobId>|<seq>|<tekst>|<checksum>" zonder '\n'
inline ParseResult parseChunk(const char* line, Chunk& c) {
  if (strncmp(line, "CHUNK|", 6) != 0) return PARSE_NO_SEQ;
  const char* job = line + 6;
  const char* bar1 = strchr(job, '|');
  if (!bar1) return PARSE_NO_SEQ;
  const char* seq = bar1 + 1;
  const char* bar2 = strchr(seq, '|');
  if (!bar2 || bar2 == seq) return PARSE_NO_SEQ;
  char* end;
  c.seq = strtol(seq, &end, 10);
  if (end != bar2) return PARSE_NO_SEQ;

  const char* text = bar2 + 1;
  const char* bar3 = strchr(text, '|');
  if (!bar3) return PARSE_BAD;
  size_t jobLen = bar1 - job, textLen = bar3 - text;
  if (c.seq < 0 || jobLen == 0 || jobLen > MAX_JOBID || textLen > MAX_CELLS) return PARSE_BAD;

  const char* cs = bar3 + 1;
  if (strlen(cs) != 2) return PARSE_BAD;
  unsigned long want = strtoul(cs, &end, 16);
  if (*end != '\0' || want != xorChecksum(text, textLen)) return PARSE_BAD;

  memcpy(c.jobId, job, jobLen);
  c.jobId[jobLen] = '\0';
  memcpy(c.text, text, textLen);
  c.text[textLen] = '\0';
  return PARSE_OK;
}
