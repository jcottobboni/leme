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
  tunnels and `kubectl proxy` expose remote clusters there. Every other cluster opens read-only, and the **engine** rejects edits, deletes,
  scale, restart, exec, port-forward and Helm dry-runs while read-only, not only the UI.
- **Production contexts** (markers such as `prd`, `prod`, `production`, `live` in the context,
  cluster, user, namespace or API host, also glued to a prefix as in `ocpprd` or `k8sprod01`) can only be unlocked by typing the exact context name.
  Switching back to read-only immediately closes that cluster's terminals and port-forwards.
- **Every change is previewed.** YAML edits go through a server-side dry-run, a diff and a
  confirmation. Updates carry the `resourceVersion` you opened, so they never overwrite someone
  else's newer change.
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

## External scanners

Polaris and Kubescape only see YAML exported to a private temporary directory (`0700`, removed
afterwards): no Secrets, no ConfigMaps, and `env[].value` masked. They run with an isolated `HOME`,
`KUBECONFIG=/dev/null` and update checks off, so they never reach your clusters. Trivy only scans
images and runs one scan at a time with a minimal environment.
