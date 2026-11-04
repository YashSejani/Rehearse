'use client';
import { useEffect, useState } from 'react';

export default function Incidents() {
  const [rows, setRows] = useState(null);
  const [err, setErr] = useState('');
  useEffect(() => {
    const load = () => fetch('/api/incidents').then(r => r.json()).then(d => d.error ? setErr(d.error) : setRows(d || [])).catch(e => setErr(String(e)));
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, []);
  if (err) return <p className="status failed">Cannot reach the agent service: {err}</p>;
  if (!rows) return <p className="muted">Loading…</p>;
  return (
    <>
      <h1>Incidents</h1>
      {rows.length === 0 && <p className="muted">No incidents yet. Alerts from Alertmanager start investigations automatically.</p>}
      <table>
        <thead><tr><th>#</th><th>When</th><th>Deployment</th><th>Alert</th><th>Diagnosis</th><th>Status</th></tr></thead>
        <tbody>
          {rows.map(i => (
            <tr key={i.id}>
              <td><a href={`/incidents/${i.id}`}>{i.id}</a></td>
              <td>{new Date(i.created_at).toLocaleString()}</td>
              <td>{i.namespace}/{i.target}</td>
              <td>{i.alert || '-'}</td>
              <td>{i.summary || <span className="muted">investigating…</span>}</td>
              <td className={`status ${i.status}`}>{i.status}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
