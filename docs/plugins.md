# Plugins

Leme plugins are YAML files. They add four things:

- **actions**: a local command run on the selected object (grid context menu, object detail, Ctrl+K);
- **links**: a URL built from the object, opened in the browser;
- **views**: extra grid columns, detail fields and a health rule for a kind (handy for CRDs);
- **rules**: diagnosis rules in [CEL](https://cel.dev) that show up in Problemas, in the object's
  Diagnóstico tab and in the Copilot.

No plugin code runs inside the app. An action runs a program you installed, and only after you
approve its exact command line.

**k9s plugins work as they are.** Leme reads k9s' `plugins.yaml` and k9s' plugin folder. You can also
drop any file from the [k9s community plugins](https://github.com/derailed/k9s/tree/master/plugins)
into Leme's plugin folder.

## Where plugins live

| Source | Path |
| --- | --- |
| Leme plugin folder | `<config>/plugins/*.yaml` |
| k9s `plugins.yaml` | `$K9S_CONFIG_DIR/plugins.yaml`, else the k9s config dir |
| k9s plugin folder | `$XDG_DATA_HOME/k9s/plugins/*.yaml`, else the k9s data dir |

`<config>` is `$LEME_CONFIG_DIR`, or by default:

| OS | Leme config dir |
| --- | --- |
| Linux | `~/.config/leme` |
| macOS | `~/Library/Application Support/leme` |
| Windows | `%APPDATA%\leme` |

The k9s directories follow k9s:

| OS | k9s config dir | k9s data dir |
| --- | --- | --- |
| Linux | `~/.config/k9s` | `~/.local/share/k9s` |
| macOS | `~/Library/Application Support/k9s` | `~/Library/Application Support/k9s` |
| Windows | `%LOCALAPPDATA%\k9s` | `%LOCALAPPDATA%\k9s` |

Open **Integrações → Plugins** to see:

- every file, with its errors and warnings;
- the actions and whether you trusted them;
- the k9s switch;
- an "Abrir pasta" button.

Files are reloaded automatically when they change.

## Leme format

```yaml
apiVersion: leme.dev/v1
name: rails               # plugin name: letters, digits, . _ -
description: Rails tools
actions:
- id: console
  title: Rails console
  scopes: [containers]    # plural, singular, short name, plural.group, "all"; containers = pods + container choice
  command: kubectl
  args: [exec, -it, $NAME, -n, $NAMESPACE, -c, $CONTAINER, --, bin/rails, console]
  mode: terminal          # terminal | output (default) | background
  confirm: false          # ask before every run
  mutates: false          # true: only on unlocked clusters, always confirmed
links:
- id: grafana
  title: Abrir no Grafana
  scopes: [deployments]
  url: https://grafana.example.com/d/app?var-ns=$NAMESPACE&var-app=$NAME
views:
- group: longhorn.io
  kind: Volume
  columns:                # grid columns: jsonPath or expr (CEL), one of them
  - {name: Estado, jsonPath: .status.state}
  - {name: Réplicas, expr: object.spec.numberOfReplicas}
  fields:                 # detail fields (Visão geral tab)
  - {name: Robustez, jsonPath: .status.robustness}
  health:                 # first match wins; no match keeps the default health
  - {when: 'object.status.robustness == "faulted"', status: error}
  - {when: 'object.status.robustness == "degraded"', status: warn}
  - {when: 'object.status.robustness == "healthy"', status: ok}
rules:
- id: no-limits
  title: Contêiner sem limits
  severity: medium        # critical | high | medium | low | info
  kinds: [apps/Deployment, apps/StatefulSet]   # "Kind" for the core group
  match: 'object.spec.template.spec.containers.exists(c, !has(c.resources) || !has(c.resources.limits))'
  explanation: Sem limits, um contêiner pode consumir a memória do node inteiro.
  suggestion: Defina resources.limits de CPU e memória.
  evidence: 'object.spec.template.spec.containers.filter(c, !has(c.resources) || !has(c.resources.limits)).map(c, "sem limits: " + c.name)'
```

Quote CEL expressions that contain `: ` (YAML would read them as a map).

More examples are in [`examples/plugins/`](../examples/plugins).

## Variables

Variables are written `$NAME` or `${NAME}`, like in k9s. They are replaced in `args` and in link
URLs, where values are URL-encoded. They are also set as environment variables of the command; for
the `COL-` variables the `-` becomes `_` (for example `COL_READY`).

| Variable | Value |
| --- | --- |
| `$NAME` | object name |
| `$NAMESPACE` | object namespace (empty for cluster-scoped kinds) |
| `$CONTEXT` | kubeconfig context name |
| `$CLUSTER` | cluster name of the context |
| `$USER` | user name of the context |
| `$KUBECONFIG` | the temporary kubeconfig of this run (see below) |
| `$POD` | the pod name, on pods |
| `$CONTAINER` | the container; for k9s `containers` scopes, Leme asks which one |
| `$FILTER` | the grid search text |
| `$RESOURCE_GROUP` | API group of the object |
| `$RESOURCE_VERSION` | API version of the object |
| `$RESOURCE_NAME` | plural resource name of the object |
| `$COL-<COLUMN>` | a grid column's value; the column name is in upper case with spaces as `_` (e.g. `$COL-READY`) |

Unknown variables are left alone, so the shell or the program can expand them.

## Modes

| Mode | Behavior |
| --- | --- |
| `terminal` | Runs in a terminal panel with a real PTY on Linux and macOS. On Windows it uses plain pipes: line output works, full-screen programs do not. k9s plugins without `background: true` use this mode. |
| `output` | Runs to completion and shows the output. The output is capped at 1 MiB and the run at 10 minutes. |
| `background` | Like `output`, but the output is shown only when the command fails (k9s `background: true`). |

## k9s compatibility

| k9s field | In Leme |
| --- | --- |
| `scopes` | Same names (plural, short names, `all`). `containers` is offered on pods. |
| `command`, `args` | Same |
| `background` | `true` runs in `background` mode, `false` in `terminal` mode |
| `confirm` | Same |
| `dangerous` | `mutates` |
| `description` | Title of the menu item |
| `shortCut` | Shown in the menu; it does not bind a key |
| `pipes` | Not supported: the plugin is skipped, with a warning |
| scopes with no Leme counterpart (`helm`, `contexts`, `xray`, `pulses`…) | Dropped, with a warning; the plugin is skipped when none is left |

A k9s plugin gets the id `k9s/<name>`. As in k9s, a later file overrides an earlier plugin with the
same name.

## Safety

- **Trust.** The first run of an action shows its exact command line and the file it comes from.
  Approving it covers that definition only. If the command, the arguments, the mode, `mutates`,
  `confirm`, the scopes, the title or the file change, Leme asks again. You can revoke trust in the Plugins panel.
- **Pinned cluster, no credentials on disk.** The command gets a `KUBECONFIG` of its own, created
  for this run and deleted after it:
  - it holds a single context, named like the real one;
  - it points to a proxy on `127.0.0.1` (HTTPS with a one-time certificate);
  - it carries a one-time token;
  - the proxy forwards requests with your real credentials, which never reach the plugin.

  `kubectl`, `helm`, `oc`, `flux`, `stern` and other kubeconfig-based tools work unchanged, and
  `--context $CONTEXT` resolves.
- **Production guard on the wire.** While the cluster is locked (read-only), the proxy:
  - lets only reads through (get, list, watch, logs, `kubectl auth can-i`);
  - refuses changes with a normal Kubernetes 403 error;
  - refuses exec and attach unless "Liberar shell" is on;
  - refuses the pod, service and node proxy.

  Other interactive streams are refused too. Locking the cluster, or revoking the shell, closes the
  exec and attach sessions a plugin already opened. A `mutates` action does not start on a locked
  cluster, and locking the cluster again kills it. Programs a run leaves in the background end with
  it; on Windows only the program itself is ended.
- **No injection from the cluster.** Values come from object names, columns and the search box.
  - When the command or any argument is a shell, an interpreter or a wrapper (`sh -c`, `bash`,
    `python -c`, `env`, `watch`, `kubectl exec … -- sh -c`, a `.bat`/`.cmd` on Windows), a value with
    characters a shell would interpret stops the run.
  - A value that would turn an argument into an option (`--server=…`) also stops the run.
- **Audit.** Trusting, revoking and running are recorded in the audit log with source `plugin`. The
  command output is not recorded.
- **CEL** expressions are sandboxed and cost-limited. Rules see a redacted copy of the object:
  Secret values and credentials in `env` are masked. Views on Secrets never see their values.

A trusted program can still read your files, including `~/.kube/config`, on its own. The proxy is a
guard against mistakes, not against a malicious program. Trust only plugins whose source you know.
