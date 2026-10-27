package analyze

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func refOf(u *unstructured.Unstructured) Ref {
	return Ref{Group: u.GroupVersionKind().Group, Kind: u.GetKind(), Namespace: u.GetNamespace(), Name: u.GetName()}
}

// diagnose dispatches to the rules of the object's kind. With deep, the pods the object controls are included.
func (c *scope) diagnose(u *unstructured.Unstructured, deep bool) []Finding {
	var out []Finding
	g := u.GroupVersionKind().Group
	switch u.GetKind() {
	case "Pod":
		out = c.podFindings(u)
	case "Deployment", "StatefulSet", "DaemonSet", "DeploymentConfig", "ReplicaSet", "ReplicationController", "Job", "CronJob":
		switch u.GetKind() {
		case "Job":
			out = jobFindings(u)
		case "CronJob":
			out = cronJobFindings(u)
		case "ReplicaSet", "ReplicationController":
		default:
			out = workloadFindings(u)
		}
		if deep {
			for _, p := range c.podsOf(u) {
				out = append(out, c.podFindings(p)...)
			}
			if u.GetKind() == "CronJob" {
				for _, j := range c.mustList("batch", "jobs") {
					for _, o := range j.GetOwnerReferences() {
						if o.UID == u.GetUID() {
							out = append(out, jobFindings(j)...)
						}
					}
				}
			}
		}
	case "Service":
		if g == "" {
			out = c.serviceFindings(u)
		}
	case "Ingress":
		out = c.ingressFindings(u)
	case "Route":
		out = c.routeFindings(u)
	case "PersistentVolumeClaim":
		out = c.pvcFindings(u)
	case "Node":
		out = nodeFindings(u)
	case "HorizontalPodAutoscaler":
		out = hpaFindings(u)
	}
	return out
}

func isPullReason(r string) bool {
	switch r {
	case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "ErrImageNeverPull", "RegistryUnavailable":
		return true
	}
	return false
}

// healthyPod is a cheap pre-filter on the unstructured object so large namespaces skip the typed conversion.
func healthyPod(u *unstructured.Unstructured) bool {
	phase := str(u.Object, "status", "phase")
	if phase == "Succeeded" {
		return true
	}
	if phase == "Running" {
		if c, ok := cond(u.Object, "Ready"); ok && condStr(c, "status") == "True" {
			return true
		}
	}
	return false
}

func (c *scope) podFindings(u *unstructured.Unstructured) []Finding {
	if healthyPod(u) {
		return nil
	}
	p, err := convert[corev1.Pod](u)
	if err != nil {
		return nil
	}
	ref := refOf(u)
	evs := c.eventsFor("Pod", p.Namespace, p.Name)
	var out []Finding
	add := func(f Finding) {
		f.Object = ref
		if f.Evidence == nil {
			f.Evidence = []string{}
		}
		out = append(out, f)
	}

	var pullEv, crashEv, oomEv, cfgEv []string
	crashOOM, crash := false, false
	restarts := int32(0)
	statuses := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	for _, cs := range statuses {
		w, t, lt := cs.State.Waiting, cs.State.Terminated, cs.LastTerminationState.Terminated
		switch {
		case w != nil && isPullReason(w.Reason):
			pullEv = append(pullEv, fmt.Sprintf("container %q (imagem %s): waiting reason=%s", cs.Name, imageOf(p, cs), w.Reason))
			if w.Message != "" {
				pullEv = append(pullEv, w.Message)
			}
		case w != nil && w.Reason == "CrashLoopBackOff":
			crash = true
			restarts += cs.RestartCount
			crashEv = append(crashEv, fmt.Sprintf("container %q: waiting reason=CrashLoopBackOff, restartCount=%d", cs.Name, cs.RestartCount))
			if lt != nil {
				crashEv = append(crashEv, fmt.Sprintf("container %q: última terminação reason=%s exitCode=%d%s", cs.Name, lt.Reason, lt.ExitCode, msgSuffix(lt.Message)))
				if lt.Reason == "OOMKilled" {
					crashOOM = true
				}
			}
		case w != nil && (w.Reason == "CreateContainerConfigError" || w.Reason == "CreateContainerError"):
			cfgEv = append(cfgEv, fmt.Sprintf("container %q: waiting reason=%s%s", cs.Name, w.Reason, msgSuffix(w.Message)))
		case t != nil && t.Reason == "OOMKilled", lt != nil && lt.Reason == "OOMKilled" && cs.RestartCount > 0:
			oomEv = append(oomEv, fmt.Sprintf("container %q: OOMKilled, restartCount=%d", cs.Name, cs.RestartCount))
		}
	}
	if len(pullEv) > 0 {
		for _, e := range evs {
			if e.Reason == "Failed" && strings.Contains(e.Message, "pull") {
				pullEv = append(pullEv, "evento: "+e.Message)
				break
			}
		}
		add(Finding{ID: "image-pull", Severity: High, Title: "Falha ao baixar a imagem (ImagePullBackOff)",
			Explanation: "O kubelet não conseguiu baixar a imagem do container, então o pod não inicia.",
			Evidence:    pullEv, Fixable: true,
			Suggestion: "Confira o nome e a tag da imagem, se ela existe no registry e se as credenciais (imagePullSecrets) estão corretas e acessíveis ao namespace."})
	}
	if crash {
		f := Finding{ID: "crash-loop", Severity: High, Title: "Container reiniciando em loop (CrashLoopBackOff)",
			Explanation: "O container inicia e termina repetidamente; o Kubernetes aumenta o intervalo entre as tentativas.",
			Evidence:    crashEv, Suggestion: "Veja os logs da execução anterior do container (logs --previous), valide variáveis de ambiente, ConfigMaps/Secrets montados e o comando de entrada."}
		if crashOOM {
			f.Title = "Container reiniciando em loop por falta de memória (OOMKilled)"
			f.Explanation = "O container é encerrado pelo kernel por exceder o limite de memória e reinicia em loop."
			f.Suggestion = "Aumente resources.limits.memory (e requests) do container ou investigue vazamento de memória na aplicação."
			f.Fixable = true
		}
		add(f)
	}
	if len(oomEv) > 0 && !crash {
		add(Finding{ID: "oom-killed", Severity: Medium, Title: "Container encerrado por falta de memória (OOMKilled)",
			Explanation: "O container excedeu o limite de memória e foi morto pelo kernel.",
			Evidence:    oomEv, Fixable: true, Suggestion: "Aumente resources.limits.memory do container ou investigue o consumo de memória da aplicação."})
	}
	if len(cfgEv) > 0 {
		add(Finding{ID: "container-config", Severity: High, Title: "Erro ao criar o container",
			Explanation: "O container não pôde ser criado, em geral por referência a ConfigMap/Secret/chave inexistente ou comando inválido.",
			Evidence:    cfgEv, Suggestion: "Verifique se os ConfigMaps e Secrets referenciados (e suas chaves) existem no namespace e se o comando do container é válido."})
	}

	if p.Status.Phase == corev1.PodPending {
		if f, ok := schedulingFinding(p, evs); ok {
			add(f)
		} else if e, ok := lastEvent(evs, "FailedMount", "FailedAttachVolume"); ok {
			add(volumeFinding(e))
		}
	} else if e, ok := lastEvent(evs, "FailedMount", "FailedAttachVolume"); ok && !podReady(p) {
		add(volumeFinding(e))
	}

	if p.Status.Phase == corev1.PodFailed {
		id, title := "pod-failed", "Pod terminou com falha"
		if p.Status.Reason == "Evicted" {
			id, title = "pod-evicted", "Pod despejado (Evicted)"
		}
		add(Finding{ID: id, Severity: Medium, Title: title,
			Explanation: "O pod terminou em fase Failed e não será reiniciado pelo kubelet.",
			Evidence:    []string{"status.phase=Failed", "status.reason=" + p.Status.Reason + msgSuffix(p.Status.Message)},
			Suggestion:  "Para despejo, verifique pressão de recursos no nó e requests/limits; o controlador recriará o pod. Remova pods Failed antigos."})
	}

	if !podReady(p) && p.Status.Phase == corev1.PodRunning {
		var ev []string
		probe := false
		liveness := false
		for _, e := range evs {
			if e.Reason == "Unhealthy" {
				probe = true
				if strings.Contains(e.Message, "Liveness") {
					liveness = true
				}
				ev = append(ev, fmt.Sprintf("evento Unhealthy (x%d): %s", max(e.Count, 1), e.Message))
			}
		}
		if probe {
			sev := Medium
			if liveness {
				sev = High
			}
			add(Finding{ID: "probe-failed", Severity: sev, Title: "Probe de saúde falhando",
				Explanation: "O kubelet marca o pod como não pronto (readiness) ou reinicia o container (liveness) quando o probe falha.",
				Evidence:    lastN(ev, 3), Fixable: true,
				Suggestion: "Confirme que a aplicação responde no caminho/porta do probe, ajuste initialDelaySeconds/timeoutSeconds/failureThreshold e verifique se a app demora para iniciar (use startupProbe)."})
		}
	}

	if len(out) == 0 && p.Status.Phase == corev1.PodRunning && !podReady(p) {
		var ev []string
		for _, cs := range p.Status.ContainerStatuses {
			if !cs.Ready {
				ev = append(ev, fmt.Sprintf("container %q: ready=false", cs.Name))
			}
		}
		if c, ok := cond(u.Object, "Ready"); ok {
			ev = append(ev, "condição Ready="+condStr(c, "status")+" reason="+condStr(c, "reason")+msgSuffix(condStr(c, "message")))
		}
		add(Finding{ID: "not-ready", Severity: Medium, Title: "Pod em execução, mas não pronto",
			Explanation: "Nem todos os containers estão prontos, então o pod não recebe tráfego de Services.",
			Evidence:    ev, Suggestion: "Verifique os logs dos containers e a configuração do readinessProbe."})
	}
	return out
}

func schedulingFinding(p *corev1.Pod, evs []event) (Finding, bool) {
	var ev []string
	unsched := false
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
			unsched = true
			ev = append(ev, fmt.Sprintf("condição PodScheduled=False reason=%s%s", c.Reason, msgSuffix(c.Message)))
		}
	}
	e, hasEv := lastEvent(evs, "FailedScheduling")
	if hasEv {
		ev = append(ev, "evento FailedScheduling: "+e.Message)
	}
	if !unsched && !hasEv {
		return Finding{}, false
	}
	text := strings.ToLower(strings.Join(ev, " "))
	var sug []string
	fixable := false
	if strings.Contains(text, "insufficient cpu") || strings.Contains(text, "insufficient memory") {
		sug = append(sug, "Faltam recursos nos nós: reduza resources.requests do pod ou adicione/escale nós.")
		fixable = true
	}
	if strings.Contains(text, "taint") {
		sug = append(sug, "Os nós têm taints que o pod não tolera: adicione tolerations ou use nós sem o taint.")
		fixable = true
	}
	if strings.Contains(text, "affinity") || strings.Contains(text, "node selector") || strings.Contains(text, "nodeselector") || strings.Contains(text, "topology spread") {
		sug = append(sug, "Nenhum nó satisfaz nodeSelector/affinity/topologySpread do pod: revise essas regras e os labels dos nós.")
		fixable = true
	}
	if strings.Contains(text, "persistentvolumeclaim") || strings.Contains(text, "volume node affinity") {
		sug = append(sug, "Há PVC não vinculado ou volume em zona incompatível: verifique o PVC, a StorageClass e a zona do volume.")
	}
	if len(sug) == 0 {
		sug = append(sug, "Leia a mensagem do evento FailedScheduling para identificar a restrição que impede o agendamento.")
	}
	return Finding{ID: "unschedulable", Severity: High, Title: "Pod pendente: não foi possível agendar em um nó",
		Explanation: "O scheduler não encontrou nó que atenda às restrições do pod, que permanece em Pending.",
		Evidence:    ev, Suggestion: strings.Join(sug, " "), Fixable: fixable}, true
}

func volumeFinding(e event) Finding {
	return Finding{ID: "volume-mount", Severity: Medium, Title: "Falha ao montar ou anexar volume",
		Explanation: "O kubelet não conseguiu montar um volume do pod, que fica preso em ContainerCreating.",
		Evidence:    []string{fmt.Sprintf("evento %s: %s", e.Reason, e.Message)},
		Suggestion:  "Verifique se o PVC/ConfigMap/Secret referenciado existe e está vinculado, e o estado do driver de armazenamento."}
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func imageOf(p *corev1.Pod, cs corev1.ContainerStatus) string {
	for _, list := range [][]corev1.Container{p.Spec.InitContainers, p.Spec.Containers} {
		for _, c := range list {
			if c.Name == cs.Name {
				return c.Image
			}
		}
	}
	return cs.Image
}

func msgSuffix(m string) string {
	if m == "" {
		return ""
	}
	return ", message: " + m
}

func lastN(s []string, n int) []string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
