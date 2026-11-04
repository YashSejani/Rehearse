# Runbook: image pull failure (ImagePullBackOff, ErrImagePull)

## Symptoms
- Pod waiting with reason ImagePullBackOff or ErrImagePull; event "Failed to pull image ...: not found".
- The deployment has 0 available replicas after a rollout (with strategy Recreate the old pod is already gone).
- Requests that need this service fail (HTTP 500 at the frontend).

## Cause
The deployment was changed to an image tag or digest that does not exist, or the registry cannot be reached.
Check `rehearse changes <service>`: the newest revision shows "image A -> B".

## Fix
- RollbackDeploy to the previous revision (safest: it restores the last image that worked).
- Or FixImage with the image of the previous working revision, exactly as shown in the changes (never :latest).
- Do not restart: a restart pulls the same missing image again.
