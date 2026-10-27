const maxCells = 30; // keep equal to MAX_CELLS in firmware/Protocol.h
const maxJobId = 36;

const printerStates = {'ready', 'busy', 'paused', 'paper_out', 'error'};

String toPrinterText(String s) => s.replaceAll(RegExp('[^a-zA-Z ]'), ' ');

String checksum(String text) {
  var x = 0;
  for (final c in text.codeUnits) {
    x ^= c & 0xff;
  }
  return x.toRadixString(16).toUpperCase().padLeft(2, '0');
}

// Strict on purpose: a bad line must fail here, not print garbage. Clean the text with toPrinterText first.
String encodeChunk(String jobId, int seq, String text) {
  if (!RegExp(r'^[A-Za-z0-9_-]+$').hasMatch(jobId) || jobId.length > maxJobId) {
    throw ArgumentError('bad jobId: $jobId');
  }
  if (seq < 0) throw ArgumentError('bad seq: $seq');
  if (text.length > maxCells) throw ArgumentError('line longer than $maxCells cells');
  if (RegExp(r'[^\x20-\x7e]|\|').hasMatch(text)) throw ArgumentError('line has characters that cannot be sent');
  return 'CHUNK|$jobId|$seq|$text|${checksum(text)}\n';
}

const statusCommand = 'STATUS\n';
const pauseCommand = 'PAUSE\n';
const resumeCommand = 'RESUME\n';
String cancelCommand(String jobId) => 'CANCEL|$jobId\n';

sealed class PrinterMessage {}

class Ack extends PrinterMessage {
  Ack(this.seq);
  final int seq;
}

class Nak extends PrinterMessage {
  Nak(this.seq);
  final int seq;
}

class PrinterState extends PrinterMessage {
  PrinterState(this.state);
  final String state;
}

// One complete line from the printer (with or without the trailing newline). null = not a message we know.
PrinterMessage? parsePrinterMessage(String line) {
  final parts = line.trim().split('|');
  final arg = parts.length > 1 ? parts[1] : '';
  final seq = RegExp(r'^\d+$').hasMatch(arg) ? int.parse(arg) : null;
  return switch (parts[0]) {
    'ACK' when seq != null => Ack(seq),
    'NAK' when seq != null => Nak(seq),
    'STATE' when printerStates.contains(arg) => PrinterState(arg),
    _ => null,
  };
}
