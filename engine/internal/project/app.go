package project

// AppOf returns the application an object belongs to, from the usual labels and the Helm
// release annotation (most specific grouping first). Empty when nothing identifies it.
func AppOf(labels, annotations map[string]string) string {
	if v := labels["app.kubernetes.io/part-of"]; v != "" {
		return v
	}
	if v := annotations["meta.helm.sh/release-name"]; v != "" {
		return v
	}
	for _, k := range []string{"app.kubernetes.io/instance", "app.kubernetes.io/name", "app", "k8s-app", "name"} {
		if v := labels[k]; v != "" {
			return v
		}
	}
	return ""
}
