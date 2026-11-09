#include <Arduino.h>
#include "BLESerial.h"
#include "Protocol.h"

// Code for the printer. Protocol: see Protocol.h
//
// ===== FIRST RUN ON THE REAL HARDWARE (this version was never run on the machine) =====
// 1. Keep Protocol.h next to this file (Arduino IDE: same sketch folder).
// 2. Open the serial monitor at 115200 with line ending "Newline". You can type protocol messages
//    by hand, no phone needed. Compute a checksum with the example in Protocol.h, e.g. "ab" -> 03:
//       CHUNK|test|0|ab|03        -> prints "ab", then replies ACK|0
//       CHUNK|test|0|ab|03        -> sent again: replies ACK|0 without printing (duplicate)
//       STATUS                    -> STATE|ready
// 3. Check the feed direction (stepperBewegingDoorvoer). The old code never set dirPinDoorvoer, so it
//    ran with the pin LOW. This version sets it LOW explicitly to keep the same behaviour. If paper
//    feeds the wrong way, change that one value to HIGH.
// 4. Set MAX_CELLS in Protocol.h to what fits on your paper (one cell = stappenNorm + stappenLetter
//    = 15 steps, starting at beginstappen). It is a guess (30) right now.
// 5. Check HOMING_MAX_STEPS below: it must be more than the real travel to the endstop, or homing
//    fails on a healthy machine. Unplug the endstop to test: you should get STATE|error, not a hang.
// 6. Keep ALL the tuned numbers (stappen*, delays, punch timing) as they are. Only the structure changed.
// 7. The agent (phone/PC) must wait for ACK after each chunk for as long as one full line takes
//    to print (tens of seconds), not a few seconds. ACK is sent after the line is PRINTED.
// 8. Not in this version: paper_out detection (no sensor), punctuation, numbers, capitals, accents.
//    Those characters print as an empty cell.
// 9. After STATE|error (homing failed) the printer must be reset. A half-printed line is not retried.

enum EmbossResult { EMBOSS_DONE, EMBOSS_CANCELLED, EMBOSS_FAILED }; // up here: Arduino puts its generated prototypes above the first function

QueueHandle_t bleQueue; // bytes van de telefoon, verwerkt in readInput()

// Declaraties: functies die gebruikt worden voor ze verderop gedefinieerd zijn
bool stepperInitialisatie();
void punch();
void stepperBewegingHor(int aantalStappenHor);
void stepperBewegingDoorvoer(int aantalStappenDoorvoer);

// alfabet : index 0 = spatie, 1..26 = a..z
const int alfabet[27][3][2] = {
  { {0,0}, {0,0}, {0,0} }, // spatie
  { {1,0}, {0,0}, {0,0} }, // a
  { {1,0}, {1,0}, {0,0} }, // b
  { {1,1}, {0,0}, {0,0} }, // c
  { {1,1}, {0,1}, {0,0} }, // d
  { {1,0}, {0,1}, {0,0} }, // e
  { {1,1}, {1,0}, {0,0} }, // f
  { {1,1}, {1,1}, {0,0} }, // g
  { {1,0}, {1,1}, {0,0} }, // h
  { {0,1}, {1,0}, {0,0} }, // i
  { {0,1}, {1,1}, {0,0} }, // j
  { {1,0}, {0,0}, {1,0} }, // k
  { {1,0}, {1,0}, {1,0} }, // l
  { {1,1}, {0,0}, {1,0} }, // m
  { {1,1}, {0,1}, {1,0} }, // n
  { {1,0}, {0,1}, {1,0} }, // o
  { {1,1}, {1,0}, {1,0} }, // p
  { {1,1}, {1,1}, {1,0} }, // q
  { {1,0}, {1,1}, {1,0} }, // r
  { {0,1}, {1,0}, {1,0} }, // s
  { {0,1}, {1,1}, {1,0} }, // t
  { {1,0}, {0,0}, {1,1} }, // u
  { {1,0}, {1,0}, {1,1} }, // v
  { {0,1}, {1,1}, {0,1} }, // w  speciaal, wijkt af
  { {1,1}, {0,0}, {1,1} }, // x
  { {1,1}, {0,1}, {1,1} }, // y
  { {1,0}, {0,1}, {1,1} }, // z
};

// Verwijzing naar juiste braillematrix, onbekend teken = lege cel (layout blijft kloppen)
const int (*getMatrix(char braille))[2] {
  braille = tolower(braille);
  if (braille >= 'a' && braille <= 'z') return alfabet[braille - 'a' + 1];
  return alfabet[0];
}

/* Stappenmotor waarden : */

int stappenNorm = 6; // stappen IN letter // GOED!! 6 stappen
int stappenLetter = 9; // stappen tussen letters // GOED!! 9 stappen
int beginstappen = 75; // beginpunt op papier
int stappenDoorvoerNorm = 38; // stappen IN letter (100 = 8mm)
int stappenDoorvoerLetter = 80; // stappen tussen letters
const int HOMING_MAX_STEPS = 1500; // meer stappen dan dit richting endstop = endstop of motor stuk

const int solen = 25;
const int dirPinHor = 26;
const int stepPinHor = 27; // needs to be on a separate pin that is not on RX
const int dirPinDoorvoer = 16;
const int stepPinDoorvoer = 17;
const int endStop = 32; // pin 8 will crash the ESP
const int doorvoer = 33; // pin 9 will crash the ESP

/* Protocol-status */

bool homingFailed = false;
bool paused = false;          // PAUSE: begint geen nieuwe regel, huidige regel wordt afgemaakt
bool cancelRequested = false; // CANCEL tijdens het printen: stopt na de huidige cel
char currentJob[MAX_JOBID + 1] = "";
long lastSeq = -1;     // laatste geprinte chunk van currentJob
long printingSeq = -1; // chunk die nu geprint wordt, -1 = geen
Chunk pending;         // maximaal 1 chunk wacht (de app stuurt pas door na ACK)
bool hasPending = false;

char line[128];
size_t lineLen = 0;
bool lineOverflow = false;

void reply(const String& msg) {
  Serial.println(msg);
  BLESerial.send(msg + "\n");
}

void sendState() {
  const char* s = homingFailed ? "error"
                : paused ? "paused"
                : (printingSeq != -1 || hasPending) ? "busy"
                : "ready";
  reply(String("STATE|") + s);
}

void resetJob() {
  currentJob[0] = '\0';
  lastSeq = -1;
  hasPending = false;
}

void handleChunk(const char* l) {
  Chunk c;
  ParseResult r = parseChunk(l, c);
  if (r == PARSE_NO_SEQ) return; // niets om te NAK'en, de app probeert opnieuw na de timeout
  if (r == PARSE_BAD) { reply(String("NAK|") + c.seq); return; }
  if (homingFailed) { sendState(); return; }

  if (strcmp(c.jobId, currentJob) != 0) {
    if (printingSeq != -1 || hasPending) { sendState(); return; } // ander job terwijl bezig
    strcpy(currentJob, c.jobId); // nieuw job
    lastSeq = -1;
  }
  if (c.seq == printingSeq) return;                       // wordt al geprint, ACK komt straks
  if (c.seq <= lastSeq) { reply(String("ACK|") + c.seq); return; } // al geprint, ACK was kwijt: nooit dubbel printen
  if (printingSeq != -1 || hasPending) { sendState(); return; }    // maar 1 chunk tegelijk
  pending = c;
  hasPending = true;
}

void handleLine(const char* l) {
  if (!strncmp(l, "CHUNK|", 6)) handleChunk(l);
  else if (!strcmp(l, "STATUS")) sendState();
  else if (!strcmp(l, "PAUSE")) { paused = true; sendState(); }
  else if (!strcmp(l, "RESUME")) { paused = false; sendState(); }
  else if (!strncmp(l, "CANCEL|", 7) && !strcmp(l + 7, currentJob)) {
    if (printingSeq != -1) cancelRequested = true; // printChunk() ruimt op
    else resetJob();
    sendState();
  }
}

// Leest alles wat binnen is (USB of Bluetooth) en verwerkt complete regels.
// Wordt ook tijdens het printen aangeroepen, zodat STATUS/PAUSE/CANCEL niet wachten.
void readInput() {
  for (;;) {
    char c;
    if (Serial.available()) c = Serial.read();                  // USB
    else if (xQueueReceive(bleQueue, &c, 0) != pdTRUE) return;  // telefoon, niets binnen
    if (c == '\r') continue;
    if (c == '\n') {
      line[lineLen] = '\0';
      if (lineLen > 0 && !lineOverflow) handleLine(line);
      lineLen = 0;
      lineOverflow = false;
    }
    else if (lineLen < sizeof(line) - 1) line[lineLen++] = c;
    else lineOverflow = true; // te lang = rommel, de hele regel wordt genegeerd
  }
}

void setup() {
  Serial.begin(115200);
  bleQueue = xQueueCreate(256, sizeof(char));
  BLESerial.begin("BrailleMachine"); // Change the name to whatever you want
  // onData draait in de Bluetooth-taak: alleen in de wachtrij zetten, niet daar embossen
  BLESerial.onData([](uint8_t* data, size_t len) {
    for (size_t i = 0; i < len; i++) xQueueSend(bleQueue, &data[i], 0); // vol = byte weg, checksum vangt dat op
  });
  BLESerial.onState([](bool connected) {
    Serial.println(connected ? "[BLE verbonden]" : "[BLE verbroken]"); // debug
  });
  pinMode(solen, OUTPUT);
  pinMode(stepPinHor, OUTPUT);
  pinMode(dirPinHor, OUTPUT);
  pinMode(stepPinDoorvoer, OUTPUT);
  pinMode(dirPinDoorvoer, OUTPUT);
  pinMode(endStop, INPUT_PULLUP);
  pinMode(doorvoer, INPUT_PULLUP);
  homingFailed = !stepperInitialisatie(); // roep initialisatie voor steppers op
  Serial.println(homingFailed ? "[Homing mislukt]" : "[Klaar]");
}

bool stepperInitialisatie() // gaat naar home-positie van x-as, false als de endstop nooit geraakt wordt
{
  digitalWrite(dirPinHor, LOW); //richting van eindstop
  int stappen = 0;
  while (digitalRead(endStop) == HIGH) //endstop pullUP, dus standaard hoog. Bewegen tot endstop
  {
    if (++stappen > HOMING_MAX_STEPS) return false;
    digitalWrite(stepPinHor, HIGH);
    delayMicroseconds(5);
    digitalWrite(stepPinHor, LOW);
    delayMicroseconds(2000);
  }
  delay(1000);
  stepperBewegingHor(beginstappen);
  Serial.println("[Homing klaar]");
  return true;
}


// Print 1 regel: 3 puntenrijen, na elke rij doorvoer en opnieuw homen
EmbossResult embossLine(const char* text)
{
  int n = strlen(text);
  if (n == 0) { // lege regel = papier doorvoeren, zelfde afstand als een volle regel
    stepperBewegingDoorvoer(3 * stappenDoorvoerNorm + stappenDoorvoerLetter);
    return EMBOSS_DONE;
  }
  for (int row = 0; row < 3; row++)  // door 3 rijen gaan
  {
    for (int k = 0; k < n; k++) // k = index van bepaald letter in de regel
    {
      readInput();
      if (cancelRequested) return EMBOSS_CANCELLED;
      const int (*M)[2] = getMatrix(text[k]); // pointer/verwijzing naar letter in array van 2 ints
      if (M[row][0] == 1) punch();
      delay(250);
      stepperBewegingHor(stappenNorm); //ruimte IN letter
      if (M[row][1] == 1) punch();
      delay(100); // evt verwaarloosbaar
      stepperBewegingHor(stappenLetter); // ruimte tussen letters
    }
    stepperBewegingDoorvoer(stappenDoorvoerNorm);
    delay(1000);
    if (!stepperInitialisatie()) return EMBOSS_FAILED;
  }
  stepperBewegingDoorvoer(stappenDoorvoerLetter);
  return EMBOSS_DONE;
}

void printChunk()
{
  Chunk c = pending;
  hasPending = false;
  printingSeq = c.seq;
  cancelRequested = false;
  EmbossResult res = embossLine(c.text);
  if (res == EMBOSS_DONE) {
    lastSeq = c.seq; // pas na het printen, zodat een crash nooit een regel overslaat
    reply(String("ACK|") + c.seq);
  }
  else if (res == EMBOSS_CANCELLED) {
    resetJob();
    homingFailed = !stepperInitialisatie(); // wagen terug naar home
    sendState();
  }
  else { // EMBOSS_FAILED: geen ACK, de app ziet STATE|error
    homingFailed = true;
    resetJob();
    sendState();
  }
  printingSeq = -1;
}

void loop() // loop staat standaard in main functie c++ dus eerst loop uitvoeren na setup
{
  while (digitalRead(doorvoer) == LOW) // handmatige doorvoer
  {
    digitalWrite(dirPinDoorvoer, LOW); // zelfde richting als stepperBewegingDoorvoer
    digitalWrite(stepPinDoorvoer, HIGH);
    delayMicroseconds(5);
    digitalWrite(stepPinDoorvoer, LOW);
    delayMicroseconds(2000);
  }
  readInput();
  if (hasPending && !paused && !homingFailed) printChunk();
}

void punch() // 1 puntje slaan (2x solenoide)
{
  delay(100);
  digitalWrite(solen, HIGH);
  delay(100);              // solenoïde actief
  digitalWrite(solen, LOW);
  delay(200);
  digitalWrite(solen, HIGH);
  delay(100);              // solenoïde actief
  digitalWrite(solen, LOW);
  delay(100);
}

void stepperBewegingHor(int aantalStappenHor) // beweging x-as
{
  digitalWrite(dirPinHor, HIGH); // richting weg van endstop
  for (int i = 0; i < aantalStappenHor; i++)
  {
    digitalWrite(stepPinHor, HIGH);
    delayMicroseconds(50);
    digitalWrite(stepPinHor, LOW);
    delayMicroseconds(2500); // hoger = trager (meer invloed dan on tijd)
  }
}

void stepperBewegingDoorvoer(int aantalStappenDoorvoer) //beweging doorvoer
{
  digitalWrite(dirPinDoorvoer, LOW); // was dirPinHor (typfout), doorvoer liep altijd met deze pin LOW
  for (int i = 0; i < aantalStappenDoorvoer; i++)
  {
    digitalWrite(stepPinDoorvoer, HIGH);
    delayMicroseconds(5); //hoger = trager
    digitalWrite(stepPinDoorvoer, LOW);
    delayMicroseconds(2000);
  }
}
