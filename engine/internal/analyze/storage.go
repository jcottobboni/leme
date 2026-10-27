package analyze

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func (c *scope) pvcFindings(u *unstructured.Unstructured) []Finding {
	phase := str(u.Object, "status", "phase")
	ref := refOf(u)
	switch phase {
	case "Lost":
		return []Finding{{ID: "pvc-lost", Severity: Critical, Object: ref, Title: "PVC perdeu o volume (Lost)",
			Explanation: "O PersistentVolume ligado ao PVC não existe mais.",
			Evidence:    []string{"status.phase=Lost"}, Suggestion: "Restaure o volume a partir de backup ou recrie o PVC."}}
	case "Pending":
	default:
		return nil
	}
	evs := c.eventsFor("PersistentVolumeClaim", u.GetNamespace(), u.GetName())
	ev := []string{"status.phase=Pending"}
	sc := str(u.Object, "spec", "storageClassName")
	if sc != "" {
		ev = append(ev, "spec.storageClassName="+sc)
	}
	if e, ok := lastEvent(evs, "WaitForFirstConsumer"); ok {
		return []Finding{{ID: "pvc-waiting-consumer", Severity: Info, Object: ref, Title: "PVC aguardando o primeiro pod",
			Explanation: "A StorageClass usa WaitForFirstConsumer: o volume só é criado quando um pod usar o PVC.",
			Evidence:    append(ev, "evento: "+e.Message), Suggestion: "Normal se nenhum pod usa o PVC ainda; crie o workload que o consome."}}
	}
	if e, ok := lastEvent(evs, "ProvisioningFailed", "FailedBinding"); ok {
		ev = append(ev, fmt.Sprintf("evento %s: %s", e.Reason, e.Message))
	}
	return []Finding{{ID: "pvc-pending", Severity: High, Object: ref, Title: "PVC pendente (não vinculado)",
		Explanation: "O PVC não foi vinculado a um volume; pods que o usam não iniciam.",
		Evidence:    ev,
		Suggestion:  "Confirme que a StorageClass existe (ou há uma padrão), que o provisionador está saudável e que há PV compatível com tamanho, accessMode e zona."}}
}
