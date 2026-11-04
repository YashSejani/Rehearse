# Runbook: configuration, readiness and network problems

## Missing ConfigMap or Secret key (CreateContainerConfigError)
- Pod waiting with reason CreateContainerConfigError: "couldn't find key X in ConfigMap Y".
- Caused by a rollout that added an env var from a key that does not exist.
- Fix: RollbackDeploy to the previous revision (category config).

## Failing readiness probe (NotReady)
- Pod Running but not Ready for minutes; the Service has no endpoints, so requests through it fail.
- Caused by a rollout that added or changed a readinessProbe (wrong path or port).
- Fix: RollbackDeploy (category config).

## Wrong Service port (targetPort)
- All pods Running and Ready, but every request through the Service fails or is refused.
- Caused by a Service whose targetPort does not match the container port.
- Our actions cannot change a Service: propose None, category network or config, and describe the port mismatch.

## Network delay or packet loss
- p95 latency of the endpoints that use one service jumps (for example from 3 ms to more than 1000 ms), with few errors.
- Pods are healthy. Caused by network trouble on that pod (in our lab: a Chaos Mesh NetworkChaos).
- Propose None (category network); restarting does not fix the network.

## CPU starvation
- Latency rises and CPU use sits at the container's CPU limit; no crashes.
- Propose None or RestartDeployment if a runaway process is the cause (category resources).
