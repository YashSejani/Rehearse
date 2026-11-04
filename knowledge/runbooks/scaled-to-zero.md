# Runbook: deployment scaled to zero replicas

## Symptoms
- Deployment with spec.replicas 0 (ScaledToZero); no pods exist, so nothing crashes and nothing logs.
- Every endpoint that needs this service returns errors (for user: /user login and /reservation).
- Events: "Scaled down replica set <name> from 1 to 0".

## Cause
Someone or something set replicas to 0 (kubectl scale, an autoscaler, a bad deploy).

## Fix
- ScaleReplicas back to the normal count (1 for every HotelReservation service).
- Never propose replicas 0; the Safety Gate blocks it.
