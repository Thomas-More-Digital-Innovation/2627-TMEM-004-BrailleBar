// Events that move through RabbitMQ (see Readme, "Flow of a document").
// Must match contracts/events.schema.json (the Go services follow the same schema); the contract test checks the examples.
// Every event is wrapped in an envelope; `id` is what each consumer stores in ProcessedEvent to skip duplicates.

export type DocumentSource = 'text' | 'pdf' | 'word' | 'image' | 'scan';

export interface OcrBox { x: number; y: number; w: number; h: number }
export interface OcrWord { text: string; box: OcrBox }
export interface OcrLine { text: string; box: OcrBox; words: OcrWord[] }
export interface OcrPage { page: number; width: number; height: number; lines: OcrLine[] }

export interface DocumentUploaded {
  documentId: string;
  deviceId: string;
  source: DocumentSource;
  needsOcr: boolean; // false for typed text and files with a text layer
}

export interface DocumentOcrCompleted {
  documentId: string;
  deviceId: string;
  pages: OcrPage[];
}

export interface DocumentLayoutCompleted {
  documentId: string;
  deviceId: string;
  cellsPerLine: number;
  pages: string[][]; // pages -> lines, each line fits cellsPerLine and is already printer text (see toPrinterText)
}

export interface PrintJobCreated {
  jobId: string;
  documentId: string;
  deviceId: string;
  pages: string[][]; // same shape as DocumentLayoutCompleted.pages; one line = one BLE chunk
}

// queued -> sending -> printing -> done is the happy path. failed/cancelled end a job and `reason` says why.
export type PrintJobState = 'queued' | 'sending' | 'printing' | 'done' | 'failed' | 'cancelled';

export interface PrintJobStatus {
  jobId: string;
  documentId: string;
  deviceId: string;
  state: PrintJobState;
  page: number; // 1-based, 0 before printing starts
  pageCount: number;
  reason?: string;
}

export interface EventMap {
  'document.uploaded': DocumentUploaded;
  'document.ocr.completed': DocumentOcrCompleted;
  'document.layout.completed': DocumentLayoutCompleted;
  'print.job.created': PrintJobCreated;
  'print.job.status': PrintJobStatus;
}

export type EventType = keyof EventMap;

export interface EventEnvelope<T extends EventType = EventType> {
  id: string;
  type: T;
  occurredAt: string; // ISO 8601
  payload: EventMap[T];
}

export type AnyEvent = { [T in EventType]: EventEnvelope<T> }[EventType];
