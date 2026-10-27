# Security

Leme holds the keys to your clusters, so its security model is part of the product.

## Reporting a vulnerability

Please **do not open a public issue**. Use GitHub's private vulnerability reporting
(**Security → Report a vulnerability** on the repository page). Include the version, the steps to
reproduce and the impact. You should get an answer within 7 days; fixes for confirmed issues are
released as soon as possible and credited in the release notes unless you prefer otherwise.

Only the latest release receives security fixes while the project is in 0.x.

## Threat model

Leme runs on your workstation with your kubeconfig credentials. It protects against:

- **Accidental changes to the wrong cluster**, especially production.
- **Other local processes and web pages** reaching the engine's local API.
- **Credential leakage** to logs, third-party hosts, the UI or external tools.
- **Hostile cluster content** (object names, annotations, logs) being treated as code or markup.

It does not protect against an attacker who already controls your user account or your machine.

## Production safety

- **Nothing connects on its own.** Restoring the window layout never reconnects a cluster.
- **Fail-safe read-only.** Only clearly local clusters open with write access: the context name
  must come from a local tool (kind, k3d, minikube, Docker Desktop, ...) and the API server must
  be on loopback or a local container network. A loopback address alone is not enough, since SSH
  tunnels and `kubectl proxy` expose remote clusters there. Every other cluster opens read-only,
  and the **engine** rejects edits, deletes, scale, restart, exec, port-forward and Helm dry-runs
  while read-only, not only the UI.
- **Production contexts** (markers such as `prd`, `prod`, `production`, `live` in the context,
  cluster, user, namespace or API host, also glued to a prefix as in `ocpprd` or `k8sprod01`) can
  only be unlocked by typing the exact context name. A context whose API server is also used by
  a production context counts as production too: staging and production namespaces often share
  one cluster, and the guard covers the whole session, not a namespace.
  Switching back to read-only immediately closes that cluster's terminals and port-forwards.
- **Pod shells can be allowed without unlocking writes** ("Liberar shell" in the cluster menu,
  with the typed context name on production). Revoking it closes the open shells.
- **Every change is previewed.** YAML edits go through a server-side dry-run, a diff and a
  confirmation. Updates carry the `resourceVersion` you opened, so they never overwrite someone
  else's newer change.
- **Kubeconfig files and folders added in the app** ("Arquivos kubeconfig") are stored as paths
  only (settings.json, 0600) and read by the engine like ~/.kube/config. A kubeconfig can run
  commands through `exec` credential plugins when you connect, so add only trusted files and
  folders; folder scans are bounded (depth 4, 5,000 entries, 200 kubeconfigs, backups skipped).
- **Cluster profiles are cosmetic.** Alias, color and environment set in the UI never relax the
  engine's read-only or production rules.

## Local API

- The engine listens on `127.0.0.1` only, on a random port, with a random per-session token.
  On WebSocket the token travels in the subprotocol, never in the URL.
- Requests with an unexpected `Origin` or `Host` are rejected.
- Port-forwards bind to `127.0.0.1` only.
- The renderer runs sandboxed, with context isolation, no Node integration and a strict
  Content-Security-Policy; there are no network requests to third parties at runtime.

## Credentials and secrets

- Kubeconfig content, tokens, Secret values and exec output are never written to logs.
- Secret values and Helm values/notes stay hidden until you click **Reveal**; revealing is logged
  without the value.
- **OpenShift login** keeps the token in engine memory only. It is never written to the kubeconfig,
  never sent to the UI and is dropped on the first 401. Passwords and authorization codes are only
  sent over `https` to hosts under the cluster's domain; the PKCE callback requires `state`, accepts
  only GET and expires after 5 minutes.
- **Prometheus:** the kubeconfig token is only sent to the API server or to hosts under the same
  cluster domain, and service-proxy paths are validated so they cannot escape `/proxy`.
- Settings are stored with `0600` permissions.

## AI Copilot and MCP agents

- **Off by default.** Nothing is sent to an AI provider until you enable the Copilot, and each
  cloud endpoint asks for consent once. Local endpoints (Ollama, localhost) never leave the machine.
- **What leaves:** sanitized YAML of the object in focus (no Secret values, no ConfigMap values,
  no env values, no managedFields; fields named like credentials, private keys such as a Route's
  TLS key, `--password=` arguments, tokens and annotations of Secrets are masked at any depth),
  recent events with credentials masked, and Leme's own diagnosis. Logs only when you tick "Permitir logs", with obvious credentials masked. The
  "see what will be sent" button shows the exact text.
- **Anonymization** (default on for cloud providers) replaces namespaces, object names (and names
  derived from them or referenced by them: nodes, service accounts, claims, config maps, secrets),
  the context, cluster and user names, private hosts, IPs and e-mails with aliases, restored only
  on your screen. Aliases are kept per conversation, so later turns stay anonymized.
- **API keys** are stored by the desktop shell with the OS keychain (session-only when no keychain
  exists), handed to the engine in memory, never returned to the UI, and bound to the endpoint
  they were entered for: changing the endpoint does not send an existing key elsewhere.
- **The model only reads and proposes.** Cluster content is treated as untrusted data. A proposed
  fix is a merge patch to one existing object of an allowed kind (no delete, Secrets, RBAC,
  ServiceAccounts, Namespaces, CRDs, webhooks or ConfigMap contents), validated by the engine,
  dry-run on the server and shown as a diff; applying needs your click, the cluster's write unlock
  and an unchanged resourceVersion. Patches that would write a masked placeholder back are refused,
  and changes that widen privileges or exposure (privileged, host namespaces or paths, another
  ServiceAccount, new Secret references, Service exposure) are listed and need an explicit ack.
- **MCP (`leme mcp`):** reads are sanitized the same way. Writes are disabled per context until
  you enable them in the app (production also needs the typed context name), always go through
  propose → apply with a 10-minute single-use token, and are re-checked against the settings on
  every call. The permission is bound to the server, cluster and user the context pointed to when
  granted. Agents can never apply changes that widen privileges or exposure, or that touch values
  hidden from them.
- **Audit:** every question, proposal and apply (Copilot and MCP) is recorded locally in
  `audit.jsonl` (0600) without prompts, answers or object contents.

## Integrations (Argo CD, Grafana, Istio, traffic)

- **Argo CD:** Leme reads `Application`/`AppProject` objects with your kubeconfig and never talks to
  the Argo CD API or asks for its credentials. Sync and Refresh are change proposals (diff, server
  dry-run, confirmation, resourceVersion check) under the same write lock; a sync whose destination
  is another cluster also asks for the Application name. MCP agents cannot apply them.
- **Grafana and Prometheus:** only services detected by the engine can be opened, through a local
  port-forward (which needs the write lock, like any port-forward). The window is sandboxed, without
  preload, with a fresh in-memory session and navigation pinned to the forwarded address. Leme
  never fills in a login; an admin Secret is only referenced and revealed on explicit request.
- **Istio:** `istioctl analyze` runs offline (`--use-kube=false`) on YAML exported to a private
  temporary directory, like the scanners below: no Secrets or ConfigMaps, env values masked,
  `KUBECONFIG=/dev/null`, isolated `HOME`, 2-minute timeout.
- **Observed traffic:** PromQL queries only, through the Prometheus you configured. Nothing is
  installed in the cluster.

## External scanners

Polaris and Kubescape only see YAML exported to a private temporary directory (`0700`, removed
afterwards): no Secrets, no ConfigMaps, and `env[].value` masked. They run with an isolated `HOME`,
`KUBECONFIG=/dev/null` and update checks off, so they never reach your clusters. Trivy only scans
images and runs one scan at a time with a minimal environment.
