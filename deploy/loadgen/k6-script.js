// Constant traffic for HotelReservation, so a broken service shows up as errors.
// Same requests as AIOpsLab's wrk2 script (hotelReservation/wrk2/scripts/hotel-reservation/
// mixed-workload_type_1.lua), but with a more even mix so every endpoint has traffic.
// Each request carries an `endpoint` tag; k6 sends per-endpoint metrics to Prometheus.
import http from 'k6/http';

const BASE = __ENV.TARGET || 'http://frontend.hotel.svc:5000';
const RATE = parseInt(__ENV.RATE || '5', 10);
// Which copy of the app this traffic is for (hotel, or a rehearse-sbx-* sandbox). Every
// metric carries it, so the collector never mixes a sandbox's numbers with the real app's.
const NS = __ENV.TARGET_NS || 'hotel';
const tag = (endpoint) => ({ tags: { endpoint, target_ns: NS } });

export const options = {
  scenarios: {
    constant: {
      executor: 'constant-arrival-rate',
      rate: RATE,               // requests per second
      timeUnit: '1s',
      duration: '8760h',        // "forever"; the Deployment restarts k6 if it ever exits
      preAllocatedVUs: 10,
      maxVUs: 50,
    },
  },
  // Keep label count small in Prometheus: no full URL per request.
  systemTags: ['status', 'method', 'expected_response', 'error_code', 'scenario'],
  discardResponseBodies: true,
  // A new TCP connection per request, like many real users. With keep-alive, a broken
  // Service (wrong targetPort) went unnoticed: old connections kept the old route.
  noConnectionReuse: true,
};

const rand = (min, max) => Math.floor(Math.random() * (max - min + 1)) + min;
const day = (d) => `2015-04-${String(d).padStart(2, '0')}`;
const lat = () => (38.0235 + (rand(0, 481) - 240.5) / 1000.0).toFixed(4);
const lon = () => (-122.095 + (rand(0, 325) - 157.0) / 1000.0).toFixed(4);

// The app's test users are Cornell_<id> with password = <id> written 10 times.
function user() {
  const id = rand(0, 500);
  return { name: `Cornell_${id}`, pass: String(id).repeat(10) };
}

function hotels() {
  const inD = rand(9, 23);
  const outD = rand(inD + 1, 24);
  http.get(`${BASE}/hotels?inDate=${day(inD)}&outDate=${day(outD)}&lat=${lat()}&lon=${lon()}`,
    tag('hotels'));
}

function recommendations() {
  const req = ['dis', 'rate', 'price'][rand(0, 2)];
  http.get(`${BASE}/recommendations?require=${req}&lat=${lat()}&lon=${lon()}`,
    tag('recommendations'));
}

function login() {
  const u = user();
  http.post(`${BASE}/user?username=${u.name}&password=${u.pass}`, null,
    tag('user'));
}

function reservation() {
  const inD = rand(9, 23);
  const outD = inD + rand(1, 5);
  const u = user();
  http.post(`${BASE}/reservation?inDate=${day(inD)}&outDate=${day(outD)}` +
    `&hotelId=${rand(1, 80)}&customerName=${u.name}&username=${u.name}` +
    `&password=${u.pass}&number=1`, null,
    tag('reservation'));
}

// Mix: 40% search, 30% recommendations, 15% login, 15% reservation.
export default function () {
  const coin = Math.random();
  if (coin < 0.40) hotels();
  else if (coin < 0.70) recommendations();
  else if (coin < 0.85) login();
  else reservation();
}
