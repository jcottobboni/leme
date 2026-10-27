## What and why

<!-- What changes, and the problem it solves. Link the issue if there is one. -->

## How it was tested

- [ ] `make test` (Go tests, typecheck and e2e) against the local kind cluster only
- [ ] Screenshots for UI changes

## Checklist

- [ ] Anything that can change a cluster is checked in the engine (read-only mode, production unlock), not only in the UI
- [ ] No code, assets or text copied from proprietary tools; new open source code is listed in `THIRD_PARTY_NOTICES.md`
- [ ] No kubeconfigs, tokens or real hostnames in code, tests or screenshots
- [ ] `CHANGELOG.md` updated for user-visible changes
