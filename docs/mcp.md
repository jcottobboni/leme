# Leme MCP server

`leme mcp` is a [Model Context Protocol](https://modelcontextprotocol.io) server over stdio. It lets agents such as Claude Code read your clusters through Leme and, only where you explicitly allowed it, change them in a reviewed two-step flow.

It runs as a separate process started by the agent (no network port, no token). It reads the same kubeconfig files as the app (`KUBECONFIG`, or `~/.kube/config`) and the same settings the app writes.

## Setup

### Claude Code

The app shows the exact command for your installation: cluster menu > **Acesso de agentes (MCP)…**. It looks like:

```sh
claude mcp add leme -- "/opt/Leme/resources/engine/leme" mcp
```

On Windows the engine is `...\resources\engine\leme.exe`. To use extra kubeconfig files, append `--kubeconfig /path/to/file` (repeatable) after `mcp`.

### Other MCP clients

Configure a stdio server whose command is the Leme engine binary and whose arguments are `mcp`:

```json
{ "mcpServers": { "leme": { "command": "/opt/Leme/resources/engine/leme", "args": ["mcp"] } } }
```

Set `KUBECONFIG` in the server environment if the client does not inherit yours. `LEME_CONFIG_DIR` overrides where Leme keeps `settings.json` and `audit.jsonl` (it must match the app's).

## Tools

Every tool takes an optional `context` (kubeconfig context name; default: the current-context). Connections are opened lazily, per context.

| Tool | What it does |
|---|---|
| `list_contexts` | Contexts with API server host, production flag, current flag and whether agent writes are enabled |
| `list_resources` | Objects of a kind (`kind`, `namespace`, `labelSelector`): names and a short status, at most 200 |
| `get_resource` | One object as sanitized YAML |
| `get_events` | Events of a namespace, optionally of one object (at most 100) |
| `get_logs` | Last log lines of a container (`tailLines` up to 500, credentials masked) |
| `diagnose` | Leme's rule-based diagnosis of an object or namespace, with evidence and suggestions |
| `can_i` | `SelfSubjectAccessReview` for a verb and resource |
| `propose_change` | Step 1: validate a JSON merge patch against an existing object, return a diff, the dry-run result and a token |
| `apply_change` | Step 2: apply a proposal by its token |

Tool errors are returned as results with `isError: true`; the server never exits because of a failing call. Diagnostics go to stderr, stdout carries only the protocol.

## Safety model

- **Reads are always allowed**, but everything returned is sanitized: Secret and ConfigMap values, environment variable values, `managedFields`, long or credential-like annotations are removed or masked, and log/event text has credentials masked. There is no tool that reveals Secrets.
- **Writes are off by default**, per context. The user enables them in the app (**Permitir escrita por agentes neste contexto**). For production contexts the user must also type the exact context name, the same rule used to unlock production writes in the app. The setting is read from disk on every call, so switching it off in the app takes effect immediately, even for an agent that already has a token.
- **Two steps, always.** `propose_change` returns a redacted diff and, when writes are enabled, runs a server-side dry-run and returns a single-use token valid for 10 minutes, bound to the cluster and to the object's `resourceVersion`. `apply_change` checks the gate again, re-runs the dry-run, refuses if the object changed in the meantime, and only then writes. Tool descriptions instruct the agent to show the diff to the user and get approval before applying.
- **When writes are disabled**, `propose_change` still returns the diff (a preview for the user) but with no dry-run and no token, and says how to enable writes in the app. `apply_change` is refused.
- **Never available:** deleting, creating, `exec`, port-forward, and changes to Secrets, RBAC (Roles, Bindings, ServiceAccounts), Namespaces, CRDs, webhooks and ConfigMap contents. Changes cannot alter `apiVersion`, `kind`, name, namespace, owner references or finalizers.
- **Cluster content is untrusted.** Logs, events and annotations can contain text written to manipulate an agent; tool descriptions say so, and the server never executes anything based on them.
- **Audit.** Every propose/apply attempt (allowed or denied) is appended to `audit.jsonl` in the config directory with source `mcp`. It records the object and outcome, never contents or credentials.
- The agent uses your kubeconfig credentials: Kubernetes RBAC still applies on top of everything above.

## Implementation notes

The server is a small, dependency-free JSON-RPC 2.0 implementation (`engine/internal/mcp`) supporting `initialize` (protocol versions 2025-06-18, 2025-03-26 and 2024-11-05, negotiated), `ping`, `tools/list` and `tools/call`. It was preferred over the official Go SDK to avoid a new dependency in the engine's module; the tool surface is small and fully covered by in-process tests.
