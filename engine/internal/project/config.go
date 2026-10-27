package project

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func configMap(u *unstructured.Unstructured, r *Row) {
	d, _, _ := unstructured.NestedMap(u.Object, "data")
	b, _, _ := unstructured.NestedMap(u.Object, "binaryData")
	r.Cells["keys"] = int64(len(d) + len(b))
}

func secret(u *unstructured.Unstructured, r *Row) {
	r.Cells["type"] = str(u, "type")
	d, _, _ := unstructured.NestedMap(u.Object, "data")
	r.Cells["keys"] = int64(len(d))
}

func resourceQuota(u *unstructured.Unstructured, r *Row) {
	hard, _, _ := unstructured.NestedStringMap(u.Object, "status", "hard")
	if len(hard) == 0 {
		hard, _, _ = unstructured.NestedStringMap(u.Object, "spec", "hard")
	}
	used, _, _ := unstructured.NestedStringMap(u.Object, "status", "used")
	keys := make([]string, 0, len(hard))
	for k := range hard {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := used[k]
		if v == "" {
			v = "0"
		}
		parts = append(parts, fmt.Sprintf("%s: %s/%s", k, v, hard[k]))
	}
	r.Cells["usage"] = strings.Join(parts, ", ")
}

func limitRange(u *unstructured.Unstructured, r *Row) {
	var types []string
	for _, l := range slice(u, "spec", "limits") {
		lm, _ := l.(map[string]any)
		types = append(types, strOr(lm["type"], "?"))
	}
	r.Cells["types"] = strings.Join(types, ", ")
}

func webhookConfig(u *unstructured.Unstructured, r *Row) {
	r.Cells["webhooks"] = int64(len(slice(u, "webhooks")))
}
