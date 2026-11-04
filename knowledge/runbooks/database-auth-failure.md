# Runbook: database login or permission failure (MongoDB auth)

## Symptoms
- The service that owns the database (geo for mongodb-geo, rate for mongodb-rate) crashes or returns errors
  after a restart; logs mention "not authorized", "Unauthorized", "auth failed" or "requires authentication".
- The database pod itself is Running and healthy.

## Cause
The database user lost its roles (for example readWrite on geo-db was revoked) or the user was removed,
so the service cannot read its data. In HotelReservation geo and rate connect as admin/admin.

## Fix
- None of the automatic actions can restore database permissions: propose action None, category dependency,
  target the database deployment, and say a human must restore the user's roles (grantRolesToUser).
- After the roles are back, RestartDeployment of the service (geo) makes it reconnect.
