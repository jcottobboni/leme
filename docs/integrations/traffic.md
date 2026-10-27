# Tráfego observado

O Leme mostra conexões reais entre workloads quando o Prometheus do cluster já tem métricas de uma
das fontes abaixo. Nada é instalado no cluster: o engine só faz consultas PromQL pelo Prometheus
configurado (painel Métricas → Prometheus) ou detectado.

Onde aparece:

- **Integrações → Tráfego observado**: fonte detectada, tabela origem → destino, porta e volume, com
  ordenação, link para o objeto e para a Topologia.
- **Topologia → Tráfego**: arestas tracejadas e animadas com o volume. Pontas fora do grafo atual
  (outro namespace, IP externo) viram um nó "externo".
- **API**: `GET /api/clusters/{id}/traffic?namespace=&window=5m` devolve `traffic.Snapshot`
  (`engine/internal/traffic/edge.go`). Janelas: `1m`, `5m`, `15m`, `30m`, `1h`.

## Fontes

| Fonte | Métrica | Volume |
|---|---|---|
| Caretta (eBPF) | `caretta_links_observed` | bytes/s: `increase(...[janela]) / segundos` |
| Hubble (Cilium) | `hubble_flows_processed_total` com rótulos de workload | flows/s: `rate(...[janela])` |

Com as duas presentes, o Caretta é usado (mede bytes).

### Caretta

```sh
helm repo add groundcover https://helm.groundcover.com
helm install caretta groundcover/caretta --namespace caretta --create-namespace
```

O Prometheus precisa raspar o DaemonSet do Caretta (porta de métricas do chart).

### Hubble

Habilite a métrica `flow` com os rótulos de workload, por exemplo nos values do Cilium:

```yaml
hubble:
  metrics:
    enabled:
      - "flow:labelsContext=source_namespace,source_workload,destination_namespace,destination_workload"
```

Sem `labelsContext` a métrica existe mas não identifica os workloads; o Leme avisa nesse caso. Versões
antigas do Cilium não exportam `*_workload_kind`: o Leme assume `Deployment` e, na Topologia, casa a
ponta pelo namespace e nome.

## Limites

- Filtro de namespace: a conexão entra se qualquer uma das pontas estiver no namespace.
- No máximo 2.000 conexões, as de maior volume; o excedente é avisado em `errors.limit`.
- Falhas parciais (consulta de uma fonte, Prometheus fora do ar) vão em `errors`; o painel e a Topologia
  continuam funcionando.
- gRPC do Hubble Relay não é usado (exigiria port-forward contínuo e dependência do Cilium).
