package project

// Labels are shown in PT-BR; keys stay stable because column visibility and widths are persisted by key.
var base = []Column{{Key: "name", Label: "Nome", Width: 280}, {Key: "namespace", Label: "Namespace", Width: 160}}
var ageCol = Column{Key: "age", Label: "Idade", Type: "age", Width: 80}

var projectors = map[string]projector{
	"Pod": {cols(Column{Key: "containers", Label: "Contêineres", Type: "containers", Width: 96}, Column{Key: "ready", Label: "Prontos", Width: 70}, Column{Key: "status", Label: "Status", Type: "status", Width: 150},
		Column{Key: "restarts", Label: "Reinícios", Type: "number", Width: 80}, Column{Key: "cpu", Label: "CPU", Type: "number", Width: 80},
		Column{Key: "memory", Label: "Memória", Type: "number", Width: 90}, Column{Key: "node", Label: "Nó", Width: 140},
		Column{Key: "ip", Label: "IP", Width: 120}), pod},
	"Deployment":                     {cols(Column{Key: "ready", Label: "Prontos", Width: 80}, Column{Key: "upToDate", Label: "Atualizados", Type: "number", Width: 90}, Column{Key: "available", Label: "Disponíveis", Type: "number", Width: 90}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), workload},
	"StatefulSet":                    {cols(Column{Key: "ready", Label: "Prontos", Width: 80}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), workload},
	"ReplicaSet":                     {cols(Column{Key: "desired", Label: "Desejados", Type: "number", Width: 80}, Column{Key: "ready", Label: "Prontos", Width: 80}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), workload},
	"DaemonSet":                      {cols(Column{Key: "desired", Label: "Desejados", Type: "number", Width: 80}, Column{Key: "ready", Label: "Prontos", Width: 80}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), workload},
	"Job":                            {cols(Column{Key: "completions", Label: "Conclusões", Width: 100}, Column{Key: "status", Label: "Status", Type: "status", Width: 110}), job},
	"CronJob":                        {cols(Column{Key: "schedule", Label: "Agendamento", Width: 130}, Column{Key: "state", Label: "Status", Type: "status", Width: 100}, Column{Key: "active", Label: "Ativos", Type: "number", Width: 70}, Column{Key: "lastSchedule", Label: "Executado há", Type: "age", Width: 110}), cronJob},
	"Service":                        {cols(Column{Key: "type", Label: "Tipo", Width: 110}, Column{Key: "clusterIP", Label: "IP do cluster", Width: 130}, Column{Key: "ports", Label: "Portas", Width: 200}, Column{Key: "selector", Label: "Seletor", Width: 220}), service},
	"Ingress":                        {cols(Column{Key: "class", Label: "Classe", Width: 100}, Column{Key: "hosts", Label: "Hosts", Width: 260}, Column{Key: "address", Label: "Endereço", Width: 140}), ingress},
	"ConfigMap":                      {cols(Column{Key: "keys", Label: "Chaves", Type: "number", Width: 70}), configMap},
	"Secret":                         {cols(Column{Key: "type", Label: "Tipo", Width: 260}, Column{Key: "keys", Label: "Chaves", Type: "number", Width: 70}), secret},
	"Node":                           {[]Column{{Key: "name", Label: "Nome", Width: 260}, {Key: "status", Label: "Status", Type: "status", Width: 110}, {Key: "roles", Label: "Papéis", Width: 140}, {Key: "version", Label: "Versão", Width: 110}, {Key: "cpu", Label: "CPU", Type: "number", Width: 80}, {Key: "memory", Label: "Memória", Type: "number", Width: 90}, {Key: "internalIP", Label: "IP interno", Width: 130}, ageCol}, node},
	"Namespace":                      {[]Column{{Key: "name", Label: "Nome", Width: 300}, {Key: "status", Label: "Status", Type: "status", Width: 110}, ageCol}, namespace},
	"Event":                          {[]Column{{Key: "type", Label: "Tipo", Type: "status", Width: 100}, {Key: "reason", Label: "Motivo", Width: 150}, {Key: "object", Label: "Objeto", Width: 240}, {Key: "message", Label: "Mensagem", Width: 420}, {Key: "count", Label: "Contagem", Type: "number", Width: 70}, {Key: "namespace", Label: "Namespace", Width: 140}, {Key: "lastSeen", Label: "Visto há", Type: "age", Width: 90}}, event},
	"PersistentVolumeClaim":          {cols(Column{Key: "status", Label: "Status", Type: "status", Width: 100}, Column{Key: "volume", Label: "Volume", Width: 220}, Column{Key: "capacity", Label: "Capacidade", Width: 90}, Column{Key: "storageClass", Label: "Classe de storage", Width: 140}), pvc},
	"PersistentVolume":               {[]Column{{Key: "name", Label: "Nome", Width: 260}, {Key: "capacity", Label: "Capacidade", Width: 90}, {Key: "status", Label: "Status", Type: "status", Width: 100}, {Key: "claim", Label: "Claim", Width: 240}, {Key: "storageClass", Label: "Classe de storage", Width: 140}, ageCol}, pv},
	"HorizontalPodAutoscaler":        {cols(Column{Key: "target", Label: "Alvo", Width: 200}, Column{Key: "metrics", Label: "Métricas (atual/alvo)", Width: 220}, Column{Key: "min", Label: "Mín", Type: "number", Width: 60}, Column{Key: "max", Label: "Máx", Type: "number", Width: 60}, Column{Key: "replicas", Label: "Réplicas", Type: "number", Width: 80}), hpa},
	"PodDisruptionBudget":            {cols(Column{Key: "minAvailable", Label: "Mín disponível", Width: 120}, Column{Key: "maxUnavailable", Label: "Máx indisponível", Width: 130}, Column{Key: "allowed", Label: "Interrupções permitidas", Type: "number", Width: 120}), pdb},
	"ResourceQuota":                  {cols(Column{Key: "usage", Label: "Uso / limite", Width: 420}), resourceQuota},
	"LimitRange":                     {cols(Column{Key: "types", Label: "Tipos", Width: 200}), limitRange},
	"CustomResourceDefinition":       {clusterCols(Column{Key: "group", Label: "Grupo", Width: 220}, Column{Key: "versions", Label: "Versões", Width: 140}, Column{Key: "scope", Label: "Escopo", Width: 110}), crd},
	"RoleBinding":                    {cols(Column{Key: "role", Label: "Papel", Width: 220}, Column{Key: "subjects", Label: "Sujeitos", Width: 260}), binding},
	"ClusterRoleBinding":             {clusterCols(Column{Key: "role", Label: "Papel", Width: 220}, Column{Key: "subjects", Label: "Sujeitos", Width: 260}), binding},
	"Role":                           {cols(Column{Key: "rules", Label: "Regras", Type: "number", Width: 70}), rbacRole},
	"ClusterRole":                    {clusterCols(Column{Key: "rules", Label: "Regras", Type: "number", Width: 70}), rbacRole},
	"StorageClass":                   {clusterCols(Column{Key: "provisioner", Label: "Provisionador", Width: 220}, Column{Key: "reclaimPolicy", Label: "Política de reclaim", Width: 140}, Column{Key: "volumeBindingMode", Label: "Modo de binding", Width: 160}, Column{Key: "default", Label: "Padrão", Width: 70}), storageClass},
	"PriorityClass":                  {clusterCols(Column{Key: "value", Label: "Valor", Type: "number", Width: 110}, Column{Key: "globalDefault", Label: "Padrão global", Width: 120}), priorityClass},
	"RuntimeClass":                   {clusterCols(Column{Key: "handler", Label: "Handler", Width: 160}), runtimeClass},
	"Lease":                          {cols(Column{Key: "holder", Label: "Detentor", Width: 280}), lease},
	"IngressClass":                   {clusterCols(Column{Key: "controller", Label: "Controlador", Width: 260}), ingressClass},
	"MutatingWebhookConfiguration":   {clusterCols(Column{Key: "webhooks", Label: "Webhooks", Type: "number", Width: 90}), webhookConfig},
	"ValidatingWebhookConfiguration": {clusterCols(Column{Key: "webhooks", Label: "Webhooks", Type: "number", Width: 90}), webhookConfig},
	"EndpointSlice":                  {cols(Column{Key: "addressType", Label: "Tipo de endereço", Width: 130}, Column{Key: "ports", Label: "Portas", Width: 180}, Column{Key: "endpoints", Label: "Endpoints", Type: "number", Width: 90}), endpointSlice},
	"Endpoints":                      {cols(Column{Key: "addresses", Label: "Endereços", Width: 300}), endpoints},
	"NetworkPolicy":                  {cols(Column{Key: "podSelector", Label: "Seletor de pods", Width: 240}, Column{Key: "policyTypes", Label: "Tipos de política", Width: 140}), networkPolicy},
	"VolumeAttachment":               {clusterCols(Column{Key: "attacher", Label: "Attacher", Width: 200}, Column{Key: "pv", Label: "PV", Width: 220}, Column{Key: "node", Label: "Nó", Width: 160}, Column{Key: "attached", Label: "Anexado", Type: "status", Width: 90}), volumeAttachment},
}

// clusterCols is cols for cluster-scoped kinds (no Namespace column).
func clusterCols(extra ...Column) []Column {
	out := append([]Column{base[0]}, extra...)
	return append(out, ageCol)
}

func cols(extra ...Column) []Column {
	out := append([]Column{}, base...)
	out = append(out, extra...)
	return append(out, ageCol)
}
