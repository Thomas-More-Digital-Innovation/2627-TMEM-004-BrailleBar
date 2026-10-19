// BLE messages between the device agent and the ESP32. Mirror of firmware/Protocol.h: change both together.
// One message per line, '\n' terminated. Checksum = XOR of the text bytes as 2 uppercase hex digits.

export const MAX_CELLS = 30; // keep equal to MAX_CELLS in firmware/Protocol.h
export const MAX_JOBID = 36;

export type PrinterState = 'ready' | 'busy' | 'paused' | 'paper_out' | 'error';
const PRINTER_STATES: string[] = ['ready', 'busy', 'paused', 'paper_out', 'error'];

export type PrinterMessage =
  | { type: 'ACK'; seq: number }
  | { type: 'NAK'; seq: number }
  | { type: 'STATE'; state: PrinterState };

// The printer only knows a-z and space (punctuation, numbers, accents are out of scope): anything else becomes an empty cell.
export const toPrinterText = (s: string): string => s.replace(/[^a-zA-Z ]/g, ' ');

export function checksum(text: string): string {
  let x = 0;
  for (let i = 0; i < text.length; i++) x ^= text.charCodeAt(i) & 0xff;
  return x.toString(16).toUpperCase().padStart(2, '0');
}

// Strict on purpose: a bad line must fail here, not print garbage. Clean the text with toPrinterText first.
export function encodeChunk(jobId: string, seq: number, text: string): string {
  if (!/^[A-Za-z0-9_-]+$/.test(jobId) || jobId.length > MAX_JOBID) throw new Error(`bad jobId: ${jobId}`);
  if (!Number.isInteger(seq) || seq < 0) throw new Error(`bad seq: ${seq}`);
  if (text.length > MAX_CELLS) throw new Error(`line longer than ${MAX_CELLS} cells`);
  if (/[^\x20-\x7e]|\|/.test(text)) throw new Error('line has characters that cannot be sent');
  return `CHUNK|${jobId}|${seq}|${text}|${checksum(text)}\n`;
}

export const commands = {
  status: 'STATUS\n',
  pause: 'PAUSE\n',
  resume: 'RESUME\n',
  cancel: (jobId: string) => `CANCEL|${jobId}\n`,
};

// One complete line from the printer (with or without the trailing newline). null = not a message we know.
export function parsePrinterMessage(line: string): PrinterMessage | null {
  const [kind, arg] = line.trim().split('|');
  if ((kind === 'ACK' || kind === 'NAK') && /^\d+$/.test(arg ?? '')) return { type: kind, seq: Number(arg) };
  if (kind === 'STATE' && PRINTER_STATES.includes(arg)) return { type: 'STATE', state: arg as PrinterState };
  return null;
}
