# Rehearse dashboard (Phase 10)

Incidents, incident detail (evidence, raw diff, rehearsal result, Approve / Reject) and the
audit history. It talks to the agent service (`rehearse serve`), which forwards approvals to
the Safety Gate. The shared token stays on the server side (`app/api/[...path]/route.js`).

```bash
cd dashboard && npm ci
REHEARSE_API_URL=http://172.20.0.1:8090 REHEARSE_DASHBOARD_TOKEN=<same as rehearse serve> npm run dev
# open http://localhost:3001
```
