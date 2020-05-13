# tryout-kube-strategic-merge-patch

A simple `main.go` to exercise:

- how the API Conflict error would happen
- if using client-go: `Update()` vs. `UpdateStatus()` vs. `Patch()`
- how to use `StrategicMergePatchType`
- customize `mergepatch.PreconditionFunc`