package analyze

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/project"
)

// workloadFindings covers Deployment, StatefulSet, DaemonSet and DeploymentConfig through the same
// WorkloadState the grid shows, so a "Travado" row always has a "Rollout travado" finding.
func workloadFindings(u *unstructured.Unstructured) []Finding {
	st, ok := project.StateOf(u)
	if !ok {
		return nil
	}
	ref := refOf(u)
	replicas := fmt.Sprintf("desejadas=%d prontas=%d disponíveis=%d", st.Desired, st.Ready, st.Available)
	switch st.State {
	case project.StateStuck:
		return []Finding{{ID: "rollout-stuck", Severity: High, Object: ref, Title: "Rollout travado",
			Explanation: "A atualização não progrediu dentro do prazo (progressDeadlineSeconds); as novas réplicas não ficaram disponíveis.",
			Evidence:    []string{st.Condition.String(), replicas},
			Suggestion:  "Veja os problemas dos pods deste workload (imagem, probes, recursos). Corrija a causa ou reverta com rollout undo."}}
	case project.StateDegraded:
		sev := Medium
		if st.Available == 0 || st.Condition != nil {
			sev = High
		}
		ev := []string{replicas}
		if st.Condition != nil {
			ev = append(ev, st.Condition.String())
		}
		return []Finding{{ID: "replicas-unavailable", Severity: sev, Object: ref, Title: "Réplicas indisponíveis",
			Explanation: "Há menos réplicas disponíveis do que o desejado e nenhum rollout em andamento.",
			Evidence:    ev,
			Suggestion:  "Veja os problemas dos pods deste workload: costumam indicar imagem inexistente, falha de probe, falta de recursos ou crash."}}
	}
	return nil
}

func jobFindings(u *unstructured.Unstructured) []Finding {
	c, ok := cond(u.Object, "Failed")
	if !ok || condStr(c, "status") != "True" {
		return nil
	}
	return []Finding{{ID: "job-failed", Severity: High, Object: refOf(u), Title: "Job falhou",
		Explanation: "O Job esgotou as tentativas ou o prazo e não vai mais executar.",
		Evidence:    []string{fmt.Sprintf("condição Failed=True reason=%s%s", condStr(c, "reason"), msgSuffix(condStr(c, "message"))), fmt.Sprintf("status.failed=%d", num(u.Object, "status", "failed"))},
		Suggestion:  "Veja os logs dos pods do Job; corrija a causa e recrie o Job (ou ajuste backoffLimit/activeDeadlineSeconds)."}}
}

const staleCronJob = 7 * 24 * time.Hour

func cronJobFindings(u *unstructured.Unstructured) []Finding {
	ref := refOf(u)
	if s, _, _ := unstructured.NestedBool(u.Object, "spec", "suspend"); s {
		return []Finding{{ID: "cronjob-suspended", Severity: Low, Object: ref, Title: "CronJob suspenso",
			Explanation: "Com spec.suspend=true nenhuma execução nova é agendada.",
			Evidence:    []string{"spec.suspend=true"}, Suggestion: "Se não for intencional, retome com suspend=false.", Fixable: true}}
	}
	last, err := time.Parse(time.RFC3339, str(u.Object, "status", "lastSuccessfulTime"))
	switch {
	case err == nil && now().Sub(last) > staleCronJob:
		return []Finding{{ID: "cronjob-stale", Severity: Medium, Object: ref, Title: "CronJob sem execução bem-sucedida há muito tempo",
			Explanation: "A última execução bem-sucedida é antiga; o job pode estar falhando ou deixando de ser agendado.",
			Evidence:    []string{"status.lastSuccessfulTime=" + str(u.Object, "status", "lastSuccessfulTime")},
			Suggestion:  "Veja os Jobs recentes do CronJob e os logs dos seus pods."}}
	case err != nil && str(u.Object, "status", "lastScheduleTime") != "" && now().Sub(u.GetCreationTimestamp().Time) > 24*time.Hour:
		return []Finding{{ID: "cronjob-never-succeeded", Severity: Medium, Object: ref, Title: "CronJob nunca teve execução bem-sucedida",
			Explanation: "O CronJob já foi agendado, mas nenhuma execução terminou com sucesso.",
			Evidence:    []string{"status.lastScheduleTime=" + str(u.Object, "status", "lastScheduleTime"), "status.lastSuccessfulTime ausente"},
			Suggestion:  "Veja os Jobs do CronJob e os logs dos seus pods."}}
	}
	return nil
}

func nodeFindings(u *unstructured.Unstructured) []Finding {
	ref := refOf(u)
	var out []Finding
	if c, ok := cond(u.Object, "Ready"); ok && condStr(c, "status") != "True" {
		out = append(out, Finding{ID: "node-not-ready", Severity: Critical, Object: ref, Title: "Nó NotReady",
			Explanation: "O nó não está saudável; os pods nele podem ficar inacessíveis e serão reagendados.",
			Evidence:    []string{fmt.Sprintf("condição Ready=%s reason=%s%s", condStr(c, "status"), condStr(c, "reason"), msgSuffix(condStr(c, "message")))},
			Suggestion:  "Verifique o kubelet, a rede e o estado da máquina do nó; considere drenar e substituir o nó."})
	}
	for _, t := range []struct{ typ, name, sug string }{
		{"MemoryPressure", "pressão de memória", "Libere memória, reduza a densidade de pods ou aumente o nó."},
		{"DiskPressure", "pressão de disco", "Libere disco (imagens e logs antigos) ou aumente o volume do nó."},
		{"PIDPressure", "pressão de PIDs", "Reduza processos/threads por pod ou aumente o limite de PIDs."},
		{"NetworkUnavailable", "rede indisponível", "Verifique o plugin CNI e a configuração de rede do nó."},
	} {
		if c, ok := cond(u.Object, t.typ); ok && condStr(c, "status") == "True" {
			out = append(out, Finding{ID: "node-" + strings.ToLower(t.typ), Severity: High, Object: ref, Title: "Nó com " + t.name,
				Explanation: "O nó reporta " + t.typ + "; o kubelet pode despejar pods.",
				Evidence:    []string{fmt.Sprintf("condição %s=True reason=%s%s", t.typ, condStr(c, "reason"), msgSuffix(condStr(c, "message")))},
				Suggestion:  t.sug})
		}
	}
	return out
}

func hpaFindings(u *unstructured.Unstructured) []Finding {
	ref := refOf(u)
	var out []Finding
	if c, ok := cond(u.Object, "ScalingActive"); ok && condStr(c, "status") == "False" && condStr(c, "reason") != "ScalingDisabled" {
		out = append(out, Finding{ID: "hpa-no-metrics", Severity: Medium, Object: ref, Title: "HPA não consegue obter métricas",
			Explanation: "Sem métricas o autoscaler não escala o workload.",
			Evidence:    []string{fmt.Sprintf("condição ScalingActive=False reason=%s%s", condStr(c, "reason"), msgSuffix(condStr(c, "message")))},
			Suggestion:  "Verifique se o metrics-server está instalado e saudável e se o alvo define resources.requests para a métrica usada."})
	}
	max := num(u.Object, "spec", "maxReplicas")
	if max > 0 && num(u.Object, "status", "currentReplicas") >= max && num(u.Object, "status", "desiredReplicas") >= max {
		out = append(out, Finding{ID: "hpa-at-max", Severity: Low, Object: ref, Title: "HPA no máximo de réplicas",
			Explanation: "O autoscaler atingiu maxReplicas e não pode escalar mais, mesmo se a carga continuar alta.",
			Evidence:    []string{fmt.Sprintf("currentReplicas=%d desiredReplicas=%d maxReplicas=%d", num(u.Object, "status", "currentReplicas"), num(u.Object, "status", "desiredReplicas"), max)},
			Suggestion:  "Aumente spec.maxReplicas se a carga justifica, considerando a capacidade do cluster.", Fixable: true})
	}
	return out
}
