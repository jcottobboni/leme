# Argo CD integration

Leme shows Argo CD Applications of a connected cluster in **Integrações → Argo CD**.

## What it needs
- The `applications.argoproj.io` and `appprojects.argoproj.io` CRDs in the cluster. Without them
  the panel explains what is missing and nothing else changes.
- Nothing else: Leme reads and writes Applications through the Kubernetes API with your
  kubeconfig. It never asks for Argo CD credentials and never talks to the Argo CD API server.

## What it shows
- Applications (all namespaces) with sync and health badges, search and filters.
- Detail: sources, destination, compared revision, last operation, managed resources grouped by
  kind (each one opens the object; objects missing from the connected cluster are marked), the
  conditions and the deploy history. **Ver na Topologia** opens the destination namespace.
- Resources are checked against the connected cluster only when the destination is that cluster
  (`https://kubernetes.default.svc` or `in-cluster`).

## What it can change
Only two requests, both on `argoproj.io/Application` and both built by the engine (the UI only
sends the action name):

| Action | Merge patch |
|---|---|
| Sync | `{"operation":{"initiatedBy":{"username":"leme"},"sync":{"revision":<status.sync.revision or spec.source.targetRevision>,"prune":<bool>}}}` (multi-source: `revisions`) |
| Refresh | `{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"normal"\|"hard"}}}` |

- Sync is refused while an operation is running or a requested one is still pending.
- Each request goes through the change proposal flow: before/after diff, server-side dry-run when
  the session is writable, an RBAC check, a 10-minute token bound to the object's
  resourceVersion and an explicit confirmation. Applying re-runs the dry-run.
- Read-only sessions (and production until unlocked) cannot apply: the buttons are disabled and
  the engine answers 403.
- Proposals and applies are recorded in the audit log with source `argocd`.
- The Argo CD controller performs the sync; Leme only records the request.
- The AI Copilot and MCP agents still cannot change Applications.

## API
- `GET /api/clusters/{id}/argocd?namespace=`
- `GET /api/clusters/{id}/argocd/apps/{ns}/{name}`
- `POST /api/clusters/{id}/argocd/apps/{ns}/{name}/propose` with `{action: "sync"|"refresh", prune?, hard?}`
- `POST /api/clusters/{id}/argocd/apply/{token}`
