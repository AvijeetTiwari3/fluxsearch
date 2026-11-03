import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  stages: [
    { duration: '15s', target: 50 },  // ramp-up
    { duration: '30s', target: 200 }, // stress load
    { duration: '15s', target: 0 },   // cooldown
  ],
  thresholds: {
    http_req_duration: ['p(95)<45', 'p(99)<95'], // sub-50ms p95 requirement
    http_req_failed: ['rate<0.01'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const QUERIES = [
  'distributed',
  'google database',
  'consensus raft',
  'vector similarity',
  'bm25 ranking',
  'spanner truetime',
  'mapreduce clusters',
];

export default function () {
  const query = QUERIES[Math.floor(Math.random() * QUERIES.length)];
  const res = http.get(`${BASE_URL}/api/v1/search?q=${encodeURIComponent(query)}&k=10`);
  
  check(res, {
    'status is 200': (r) => r.status === 200,
    'took_ms present': (r) => JSON.parse(r.body).took_ms !== undefined,
  });

  sleep(0.05); // 50ms think time
}
