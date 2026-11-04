# Scenarios

One file per fault the evaluation harness (`ml/eval/run.py`) injects into HotelReservation.
Each scenario says how to break the app, how to know the fault is visible, what the right
answer is, and how to clean up. Format:

```yaml
name: wrong-image                 # unique id
description: one line
source: AIOpsLab file it was ported from, or "ours"
ask: geo                          # deployment we ask the agent about (`rehearse why deploy/<ask>`)
inject:                           # steps, run in order
  - kubectl: [args...]            #   kubectl --context <ctx> <args>   ({ns} = app namespace)
  - exec: {selector: io.kompose.service=mongodb-geo, script: /scripts/x.sh}   # run a script in the pod
  - manifest: chaos/pod-failure.yaml   # kubectl apply -f (cleanup: delete)
  - make: break-image
  - sleep: 10
wait_for:                         # symptom must be visible before the agent is asked
  issue: ImagePullBackOff         #   a `rehearse status` reason (any of a list), or
  error_endpoint: hotels          #   error ratio on that endpoint >= min_error_ratio, or
  p95_ms_endpoint: hotels         #   p95 latency on that endpoint >= min_p95_ms, or
  seconds: 60                     #   just wait
  timeout: 240
expected:
  fault: true                     # false for no-fault scenarios
  targets: [geo]                  # any of these counts as correct localization
  categories: [image]             # any of these
  actions: [RollbackDeploy, FixImage]
cleanup:                          # always run, even after errors or Ctrl-C
  - make: fix-image
```

Every cleanup ends with the harness waiting until `rehearse status` is healthy again.
