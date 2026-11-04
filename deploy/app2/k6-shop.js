// Constant traffic for the shop app (Phase 11): browse the catalog and place orders.
import http from 'k6/http';

const NS = __ENV.TARGET_NS || 'app2';
const RATE = parseInt(__ENV.RATE || '3', 10);
export const options = {
  scenarios: { constant: { executor: 'constant-arrival-rate', rate: RATE, timeUnit: '1s', duration: '8760h', preAllocatedVUs: 5, maxVUs: 30 } },
  systemTags: ['status', 'method', 'expected_response', 'error_code', 'scenario'],
  discardResponseBodies: true,
  noConnectionReuse: true,
};
const tag = (endpoint) => ({ tags: { endpoint, target_ns: NS } });

export default function () {
  if (Math.random() < 0.6) http.get(`http://catalog.${NS}.svc:8080/products`, tag('products'));
  else http.post(`http://orders.${NS}.svc:8080/orders`, null, tag('orders'));
}
