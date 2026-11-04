# Runbook: container OOMKilled (out of memory)

## Symptoms
- Last terminated reason OOMKilled, exit code 137; pod in CrashLoopBackOff with restarts climbing.
- Memory use close to the memory limit just before each restart; a recent rollout lowered the limit.
- For a database (MongoDB needs about 60 MiB), a limit like 24Mi kills it seconds after start.

## Cause
The container needs more memory than its limit allows, so the kernel kills it. Usually a too-low
memory limit in the latest revision ("memory limit none -> 24Mi"), sometimes a memory leak.

## Fix
- PatchMemoryLimit on that deployment, to well above normal use (normal memory is in the baseline), e.g. 256Mi for MongoDB.
- Or RollbackDeploy if the limit was lowered by the last rollout.
- A cache in front of the database can hide the outage from users; check pod restarts, not only errors.
