# Kyverno policies for the Safety Gate

Applied by `make policies`. They match only requests made by the gate's ServiceAccount
(`system:serviceaccount:rehearse:rehearse-gate`), so the evaluation harness can still inject
faults (e.g. scale to 0) while every change the gate makes, including its dry-runs, is checked.
The gate re-checks the same rules itself (`internal/gate/policy.go`) in case Kyverno is down.
