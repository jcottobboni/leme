# Contributing

Thanks for helping. Bug reports, ideas and pull requests are all welcome. Everyone taking part
follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## Ground rules

1. **Tests never touch real clusters.** Everything runs against the local kind cluster created by
   `make dev-cluster` (kubeconfig `~/.kube/leme-dev/kubeconfig`). Test code always pins that
   kubeconfig and the `kind-leme-dev` context explicitly. Fake contexts used to test the UI
   point to unroutable addresses (`192.0.2.0/24`) or closed local ports and are never connected.
2. **Safety lives in the engine.** Anything that can change a cluster must be checked in the Go
   engine (read-only mode, production unlock), not only hidden in the UI. Mutations go through
   dry-run, diff and confirmation.
3. **Write it yourself.** Do not copy code, assets, icons or text from proprietary tools. Code from
   open source projects is fine when its license is compatible with Apache-2.0; keep the license
   header and add it to `THIRD_PARTY_NOTICES.md`.
4. **No telemetry and no runtime calls to third-party services.**

## Development setup

See [docs/development.md](docs/development.md). In short:

```bash
make deps          # npm ci in app/
make dev-cluster   # kind cluster + fixtures (once)
make test          # go vet + Go tests + typecheck + Playwright e2e
```

## Code style

- **Go:** `gofmt`, `go vet` clean, table tests next to the code.
- **TypeScript/React:** `npx tsc --noEmit -p app` clean; follow the surrounding code. Comments say
  why, not what.
- **CSS:** each feature defines only classes with its own prefix (`topo-*`, `map-*`, `mc-*`…) or
  rules scoped under the feature's container. Global class names leak into other screens once the
  feature is opened.
- **UI tests** use `data-testid` selectors.
- **UI changes:** run `make sweep` and look at the screenshots and the overflow report before
  opening the pull request.

## Pull requests

- Keep them small and focused; describe what changed and how you verified it.
- Add or update tests for behavior changes; add screenshots for UI changes.
- Commit messages in the imperative mood (`Add namespace filter to the map`).
- By contributing you agree that your contribution is licensed under the Apache License 2.0.

## Security issues

Do not open public issues for vulnerabilities; see [SECURITY.md](SECURITY.md).
