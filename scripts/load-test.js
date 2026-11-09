// Load test of the whole path: open the status socket, send text, wait for the push that the job is queued.
// Run: docker compose up -d --build --scale notification-service=2
//      docker compose run --rm k6
import http from 'k6/http';
import ws from 'k6/ws';
import { check } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const BASE = __ENV.BASE || 'http://localhost:8080';
const pushMs = new Trend('push_ms', true);
const pushReceived = new Rate('push_received');

export const options = {
  stages: [
    { duration: '15s', target: 20 },
    { duration: '30s', target: 20 },
    { duration: '5s', target: 0 },
  ],
  thresholds: {
    push_received: ['rate>0.99'],
    push_ms: ['p(95)<2000'],
    http_req_failed: ['rate<0.01'],
  },
};

export default function () {
  const device = `k6-${__VU}-${__ITER}-${Date.now()}`;
  const params = { headers: { 'X-Device-Id': device, 'Content-Type': 'application/json' } };
  let jobId = null;

  ws.connect(`${BASE.replace('http', 'ws')}/status?deviceId=${device}`, {}, (socket) => {
    let sentAt;
    socket.on('open', () => {
      sentAt = Date.now();
      const res = http.post(`${BASE}/documents/text`, JSON.stringify({ text: 'load test' }), params);
      check(res, { 'text accepted': (r) => r.status === 201 });
    });
    socket.on('message', (raw) => {
      const status = JSON.parse(raw);
      if (status.state !== 'queued') return;
      pushMs.add(Date.now() - sentAt);
      jobId = status.jobId;
      socket.close();
    });
    socket.setTimeout(() => socket.close(), 10000);
  });

  pushReceived.add(jobId !== null);
  if (jobId) {
    // so test jobs don't stay open
    http.post(`${BASE}/jobs/${jobId}/status`, JSON.stringify({ state: 'cancelled', page: 0 }), params);
  }
}
