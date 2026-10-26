import type { PrintJobState, PrintJobStatus } from './events';

// The REST contract of the API gateway (backend/gateway):
//   POST /documents/text {text}             -> {documentId}
//   GET  /jobs/:jobId                       -> PrintJobStatus
//   GET  /jobs/:jobId/pages                 -> {jobId, pages}, the lines the agent sends to the printer
//   POST /jobs/:jobId/status {state, page}  -> 204; a job only moves forward, else 409
//   WS   /status?deviceId=...               -> stream of PrintJobStatus (JSON), starting with "queued"
// Every request carries the anonymous device id in X-Device-Id; there are no accounts.
// File uploads come with the OCR service. Cancelling is reportStatus(jobId, 'cancelled', page).

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

export interface ApiOptions {
  baseUrl: string; // no trailing slash
  deviceId: string;
  fetch?: typeof fetch; // injectable for tests
}

export function createApiClient({ baseUrl, deviceId, fetch: f }: ApiOptions) {
  const doFetch = f ?? ((input: RequestInfo | URL, init?: RequestInit) => fetch(input, init));

  async function call<T>(path: string, init: RequestInit = {}): Promise<T> {
    const res = await doFetch(baseUrl + path, {
      ...init,
      headers: { ...init.headers, 'X-Device-Id': deviceId },
    });
    if (!res.ok) throw new ApiError(res.status, await res.text());
    if (res.status === 204) return undefined as T;
    return res.json() as Promise<T>;
  }

  return {
    submitText: (text: string) =>
      call<{ documentId: string }>('/documents/text', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text }),
      }),

    getJob: (jobId: string) => call<PrintJobStatus>(`/jobs/${encodeURIComponent(jobId)}`),

    getPages: (jobId: string) =>
      call<{ jobId: string; pages: string[][] }>(`/jobs/${encodeURIComponent(jobId)}/pages`),

    // page is 1-based; a retry of the same report is fine, going back is a 409 ApiError.
    reportStatus: (jobId: string, state: Exclude<PrintJobState, 'queued'>, page: number, reason?: string) =>
      call<void>(`/jobs/${encodeURIComponent(jobId)}/status`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ state, page, reason }),
      }),

    // ponytail: no auto-reconnect; when the socket closes `onClose` fires and the app must call watchStatus again.
    watchStatus(onStatus: (s: PrintJobStatus) => void, onClose?: () => void): () => void {
      const ws = new WebSocket(`${baseUrl.replace(/^http/, 'ws')}/status?deviceId=${encodeURIComponent(deviceId)}`);
      ws.onmessage = (e) => {
        try { onStatus(JSON.parse(String(e.data)) as PrintJobStatus); } catch { /* ignore a malformed message */ }
      };
      ws.onclose = () => onClose?.();
      return () => ws.close();
    },
  };
}
