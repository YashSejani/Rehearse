'use client';
import { use, useEffect, useState } from 'react';

export default function Incident({ params }) {
  const { id } = use(params);
  const [data, setData] = useState(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState('');
  const load = () => fetch(`/api/incidents/${id}`).then(r => r.json()).then(setData);
  useEffect(() => { load(); const t = setInterval(load, 4000); return () => clearInterval(t); }, [id]);

  if (!data) return <p className="muted">Loading…</p>;
  if (data.error) return <p className="status failed">{data.error}</p>;
  const inc = data.incident, p = data.proposal, a = inc.answer || {};
  const decide = async (what) => {
    setBusy(true);
    const r = await fetch(`/api/proposals/${p.id}/${what}`, { method: 'POST', body: JSON.stringify({ by: 'dashboard', reason: 'rejected in dashboard' }) });
    const d = await r.json();
    setMsg(d.error ? `Error: ${d.error}` : `${what === 'approve' ? 'Approved' : 'Rejected'}: ${d.status}`);
    setBusy(false);
    load();
  };
  return (
    <>
      <h1>Incident #{inc.id}: {inc.namespace}/{inc.target}</h1>
      <p className="muted">Alert {inc.alert || '-'} · {new Date(inc.created_at).toLocaleString()} · <span className={`status ${inc.status}`}>{inc.status}</span></p>
      <div className="card">
        <h2>Diagnosis</h2>
        <p><b>{a.root_cause || 'investigating…'}</b></p>
        {a.category && <p>Category <b>{a.category}</b> · confidence <b>{(a.confidence ?? 0).toFixed(2)}</b> · target <b>{a.target}</b></p>}
        {a.evidence?.length > 0 && <><h3>Evidence (quoted from logs, events and metrics)</h3><ul>{a.evidence.map((e, i) => <li key={i}><code>{e}</code></li>)}</ul></>}
        {a.action && <p>Proposed action: <code>{a.action.type} {JSON.stringify(a.action.params)}</code></p>}
      </div>
      {p && (
        <div className="card">
          <h2>Safety Gate proposal #{p.id} <span className={`status ${p.status}`}>{p.status}</span></h2>
          {p.plan && <p>{p.plan.description}{!p.plan.invertible && ' (cannot be fully undone)'}</p>}
          <p>Rehearsal: <b>{p.rehearsal || '-'}</b></p>
          <h3>Raw diff (dry-run result vs live object)</h3>
          <pre>{p.diff || '(no diff)'}</pre>
          {p.status === 'awaiting_approval' && (
            <p>
              <button className="approve" disabled={busy} onClick={() => decide('approve')}>Approve and apply</button>
              <button className="reject" disabled={busy} onClick={() => decide('reject')}>Reject</button>
            </p>
          )}
          {msg && <p>{msg}</p>}
          {p.reason && <p className="muted">Reason: {p.reason}</p>}
          <h3>Audit trail</h3>
          <table><tbody>{(p.events || []).map((e, i) => (
            <tr key={i}><td>{new Date(e.at).toLocaleTimeString()}</td><td>{e.step}</td>
              <td className={`status ${e.ok ? 'ok' : 'failed'}`}>{e.ok ? 'ok' : 'FAIL'}</td><td>{e.detail}</td></tr>))}</tbody></table>
        </div>
      )}
    </>
  );
}
