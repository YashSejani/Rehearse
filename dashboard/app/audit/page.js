'use client';
import { useEffect, useState } from 'react';

export default function Audit() {
  const [rows, setRows] = useState(null);
  useEffect(() => { fetch('/api/audit').then(r => r.json()).then(setRows); }, []);
  if (!rows) return <p className="muted">Loading…</p>;
  if (rows.error) return <p className="status failed">{rows.error}</p>;
  return (
    <>
      <h1>Audit history</h1>
      <p className="muted">Every action that reached the Safety Gate, including blocked and undone ones.</p>
      <table>
        <thead><tr><th>#</th><th>When</th><th>Deployment</th><th>Action</th><th>Status</th><th>Approved by</th><th>Reason</th></tr></thead>
        <tbody>{rows.map(p => (
          <tr key={p.id}><td>{p.id}</td><td>{new Date(p.created_at).toLocaleString()}</td><td>{p.namespace}/{p.deployment}</td>
            <td><code>{p.action?.type}</code></td><td className={`status ${p.status}`}>{p.status}</td><td>{p.approved_by || '-'}</td><td>{p.reason}</td></tr>))}
        </tbody>
      </table>
    </>
  );
}
