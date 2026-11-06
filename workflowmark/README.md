[![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/workflowmark.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/workflowmark) [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/workflowmark)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/workflowmark)

# workflowmark

Reads an Argo Workflows workflow's supersede mark from the Kubernetes API.

When a newer run of the same concurrency key supersedes an older one, it annotates the older run:

| Annotation | Value |
|---|---|
| `ci.mycarrier.tech/superseded-by` | the newer run's name |
| `ci.mycarrier.tech/superseded-by-correlation-id` | the correlation id of the work the newer run carries, when it has one |
| `ci.mycarrier.tech/superseded-by-commit-sha` | the commit the newer run is for, when it has one |

A process inside the older run reads the mark immediately before a write that only the current run may make, and holds back once `By` is set. What "holds back" means (refuse, skip, or let a write for other work through) is the caller's decision; this module only reads.

## Installation

```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/workflowmark
```

## Usage

```go
r, err := workflowmark.NewInCluster(
    workflowmark.InClusterAPIURL, workflowmark.InClusterServiceAccountDir, namespace, workflowName)
if err != nil {
    return err // no CA: fail closed, never write unguarded
}
mark, err := r.SupersededBy(ctx)
if err != nil {
    return err // unreadable: treat as "not known to be unmarked"
}
if mark.By != "" {
    return nil // superseded: skip the write
}
```

## Behaviour

- Authenticates as the pod's service account. It needs `get` on `workflows.argoproj.io` in its namespace.
- Re-reads the token on every call, because projected tokens rotate.
- Each read has a 10 s timeout and reads at most 32 MiB of the body.
- A non-200 answer, or a body that is not a workflow, is an error. An unmarked run, and a workflow with no annotations, both return an empty `Mark`.
