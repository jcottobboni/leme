# Grafana e Prometheus

Painel **Integrações → Grafana e Prometheus**: abre a interface web do Grafana ou do Prometheus de um
cluster por um port-forward local, sem expor nada para fora.

## Detecção
- **Grafana:** Services com o label `app.kubernetes.io/name=grafana` (ou `app=grafana`) e porta 3000
  ou chamada `http`/`http-web`/`service`. Só o nome do Service não basta.
- **Prometheus/Thanos:** a mesma detecção das métricas (`prom.Detect`).
- **Credencial de admin:** só a *referência* ao Secret (env `GF_SECURITY_ADMIN_*` com `secretKeyRef`,
  `GF_SECURITY_ADMIN_PASSWORD__FILE` num volume de Secret, ou nomes convencionais conferidos só por
  metadados). O engine não lê os valores.

## API
- `GET /api/clusters/{id}/grafana/targets` → `{targets:[{kind, namespace, service, port, scheme, score, adminSecret?, forward?}]}`.
- `POST /api/clusters/{id}/grafana/open {namespace, service, port}` → `{url, forwardId, reused}`. O alvo
  é detectado de novo a cada chamada (um serviço qualquer é recusado com 404); reaproveita o
  port-forward ativo do mesmo alvo. Exige o cluster com escrita liberada, como todo port-forward.
  Serviços HTTPS não abrem no app.

## Janela
- **Desktop:** janela própria com `sandbox`, sem preload, sessão em memória nova, navegação presa à
  origem do forward, sem popups, permissões nem downloads (`embed:open` no processo principal confere
  que a porta é de um forward ativo do engine).
- **Navegador (`leme serve`):** nova aba com `noopener`.
- O Leme nunca preenche o login. **Credencial de admin** abre o Secret mascarado; o valor só aparece
  com *Revelar*, que fica registrado.
- **Parar** encerra o port-forward.

## Testes
- Go: `go test ./internal/grafana/ ./internal/api/`.
- e2e (só kind `kind-leme-dev`, fixture `app/e2e/fixtures/grafana/m5-grafana.yaml`):
  `npx playwright test e2e/grafana.spec.ts e2e/grafana-electron.spec.ts`.
