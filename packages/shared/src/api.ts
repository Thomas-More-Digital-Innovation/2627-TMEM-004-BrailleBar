import type { PrintJobStatus } from './events';

// The REST contract the API gateway has to implement (not built yet, so this file is the draft of it):
//   POST /documents/text {text}        -> {documentId}
//   POST /documents (multipart "file") -> {documentId}
//   GET  /jobs/:jobId                  -> PrintJobStatus
//   POST /jobs/:jobId/cancel           -> PrintJobStatus
//   WS   /status?deviceId=...          -> stream of PrintJobStatus (JSON)
// Every request carries the anonymous device id in X-Device-Id; there are no accounts.

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
    return res.json() as Promise<T>;
  }

  return {
    submitText: (text: string) =>
      call<{ documentId: string }>('/documents/text', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text }),
      }),

    // React Native passes {uri, name, type} instead of a Blob; its FormData accepts that, hence the cast.
    uploadFile: (file: Blob, name: string) => {
      const form = new FormData();
      form.append('file', file as Blob, name);
      return call<{ documentId: string }>('/documents', { method: 'POST', body: form });
    },

    getJob: (jobId: string) => call<PrintJobStatus>(`/jobs/${encodeURIComponent(jobId)}`),

    cancelJob: (jobId: string) =>
      call<PrintJobStatus>(`/jobs/${encodeURIComponent(jobId)}/cancel`, { method: 'POST' }),

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
