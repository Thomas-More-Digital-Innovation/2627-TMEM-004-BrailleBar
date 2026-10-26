import { describe, expect, it } from 'vitest';
import {
  ApiError, checksum, commands, createApiClient, describeStatus, encodeChunk,
  parsePrinterMessage, toPrinterText, type PrintJobStatus,
} from '../src';

// Same vectors as firmware/test/test_protocol.cpp
describe('protocol', () => {
  it('checksums like the firmware', () => {
    expect(checksum('ab')).toBe('03');
    expect(checksum('a b')).toBe('23');
    expect(checksum('')).toBe('00');
  });

  it('encodes chunks', () => {
    expect(encodeChunk('j1', 5, 'ab')).toBe('CHUNK|j1|5|ab|03\n');
    expect(encodeChunk('j1', 0, '')).toBe('CHUNK|j1|0||00\n'); // empty line = blank line
  });

  it('refuses lines the printer would reject', () => {
    expect(() => encodeChunk('j1', 0, 'a'.repeat(31))).toThrow();
    expect(() => encodeChunk('j1', 0, 'a|b')).toThrow();
    expect(() => encodeChunk('j1', 0, 'é')).toThrow();
    expect(() => encodeChunk('j|1', 0, 'ab')).toThrow();
    expect(() => encodeChunk('j1', -1, 'ab')).toThrow();
  });

  it('cleans text to a-z and space', () => {
    expect(toPrinterText('Hello, World 42!')).toBe('Hello  World    ');
    expect(() => encodeChunk('j1', 0, toPrinterText('café, ok'))).not.toThrow();
  });

  it('parses printer messages', () => {
    expect(parsePrinterMessage('ACK|12\n')).toEqual({ type: 'ACK', seq: 12 });
    expect(parsePrinterMessage('NAK|3')).toEqual({ type: 'NAK', seq: 3 });
    expect(parsePrinterMessage('STATE|paused')).toEqual({ type: 'STATE', state: 'paused' });
    expect(parsePrinterMessage('STATE|banana')).toBeNull();
    expect(parsePrinterMessage('ACK|x')).toBeNull();
    expect(commands.cancel('j1')).toBe('CANCEL|j1\n');
  });
});

describe('describeStatus', () => {
  const base: PrintJobStatus = { jobId: 'j', documentId: 'd', deviceId: 'x', state: 'queued', page: 0, pageCount: 3 };
  it('announces each state', () => {
    expect(describeStatus({ ...base, state: 'printing', page: 2 })).toBe('Printing page 2 of 3');
    expect(describeStatus({ ...base, state: 'failed', reason: 'out of paper' })).toBe('Print stopped: out of paper');
    expect(describeStatus({ ...base, state: 'failed' })).toBe('Print stopped: unknown reason');
    expect(describeStatus({ ...base, state: 'done' })).toBe('Print finished');
  });
});

describe('api client', () => {
  it('sends the device id and parses JSON', async () => {
    let seen: { url: string; init?: RequestInit } | undefined;
    const api = createApiClient({
      baseUrl: 'http://x', deviceId: 'dev1',
      fetch: (async (url: string, init?: RequestInit) => {
        seen = { url, init };
        return new Response(JSON.stringify({ documentId: 'd1' }));
      }) as unknown as typeof fetch,
    });
    expect(await api.submitText('hi')).toEqual({ documentId: 'd1' });
    expect(seen?.url).toBe('http://x/documents/text');
    expect((seen?.init?.headers as Record<string, string>)['X-Device-Id']).toBe('dev1');
  });

  it('reports status and accepts the empty 204', async () => {
    let seen: { url: string; init?: RequestInit } | undefined;
    const api = createApiClient({
      baseUrl: 'http://x', deviceId: 'dev1',
      fetch: (async (url: string, init?: RequestInit) => {
        seen = { url, init };
        return new Response(null, { status: 204 });
      }) as unknown as typeof fetch,
    });
    expect(await api.reportStatus('j1', 'printing', 1)).toBeUndefined();
    expect(seen?.url).toBe('http://x/jobs/j1/status');
    expect(JSON.parse(String(seen?.init?.body))).toEqual({ state: 'printing', page: 1 });
  });

  it('throws ApiError on a failed request', async () => {
    const api = createApiClient({
      baseUrl: 'http://x', deviceId: 'dev1',
      fetch: (async () => new Response('nope', { status: 404 })) as unknown as typeof fetch,
    });
    await expect(api.getJob('j')).rejects.toBeInstanceOf(ApiError);
  });
});
