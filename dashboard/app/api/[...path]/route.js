// Server-side proxy to the agent service: the browser never sees the token.
const API = process.env.REHEARSE_API_URL || 'http://172.20.0.1:8090';
const TOKEN = process.env.REHEARSE_DASHBOARD_TOKEN || '';

async function forward(req, { params }) {
  const { path } = await params;
  const allowed = /^(incidents(\/\d+)?|audit|proposals\/\d+\/(approve|reject))$/;
  const p = path.join('/');
  if (!allowed.test(p)) return Response.json({ error: 'not found' }, { status: 404 });
  const res = await fetch(`${API}/api/${p}`, {
    method: req.method,
    headers: { 'X-Rehearse-Token': TOKEN, 'Content-Type': 'application/json' },
    body: req.method === 'POST' ? await req.text() : undefined,
    cache: 'no-store',
  });
  return new Response(await res.text(), { status: res.status, headers: { 'Content-Type': 'application/json' } });
}

export const GET = forward;
export const POST = forward;
