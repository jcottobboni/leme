package project

import (
	"testing"
)

const certCRD = `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: certificates.cert-manager.io}
spec:
  group: cert-manager.io
  versions:
  - name: v1alpha1
    served: false
    additionalPrinterColumns: [{name: Old, type: string, jsonPath: .spec.old}]
  - name: v1
    served: true
    additionalPrinterColumns:
    - {name: Ready, type: string, jsonPath: '.status.conditions[?(@.type=="Ready")].status'}
    - {name: Secret, type: string, jsonPath: .spec.secretName}
    - {name: Issuer, type: string, jsonPath: .spec.issuerRef.name, priority: 1}
    - {name: Replicas, type: integer, jsonPath: .spec.replicas}
    - {name: Hosts, type: string, jsonPath: '.spec.dnsNames[*]'}
    - {name: Expires, type: date, jsonPath: .status.notAfter}
    - {name: Broken, type: string, jsonPath: '.spec[unclosed'}
    - {name: Age, type: date, jsonPath: .metadata.creationTimestamp}
`

func TestPrinterColumns(t *testing.T) {
	crd := obj(t, certCRD)
	pcs := PrinterColumns(crd, "v1")
	if len(pcs) != 8 || pcs[0].Name != "Ready" || pcs[2].Priority != 1 {
		t.Fatalf("columns of the served version: %+v", pcs)
	}
	p := NewPrinter(pcs, true)
	if p == nil {
		t.Fatal("no printer")
	}
	var keys []string
	for _, c := range p.Columns() {
		keys = append(keys, c.Key+"|"+c.Type)
	}
	want := []string{"name|", "namespace|", "pc:Ready|status", "pc:Secret|", "pc:Replicas|number", "pc:Hosts|", "pc:Expires|age", "age|age"}
	if len(keys) != len(want) {
		t.Fatalf("columns %v, want %v (priority>0, broken path and Age skipped)", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("columns %v, want %v", keys, want)
		}
	}

	r := Project(obj(t, `
apiVersion: cert-manager.io/v1
kind: Certificate
metadata: {name: web, namespace: demo, uid: c1}
spec: {secretName: web-tls, replicas: 2, dnsNames: [a.example.com, b.example.com], issuerRef: {name: le}}
status:
  notAfter: "2027-01-01T00:00:00Z"
  conditions: [{type: Ready, status: "True"}]
`))
	p.Apply(obj(t, `
apiVersion: cert-manager.io/v1
kind: Certificate
metadata: {name: web, namespace: demo, uid: c1}
spec: {secretName: web-tls, replicas: 2, dnsNames: [a.example.com, b.example.com], issuerRef: {name: le}}
status:
  notAfter: "2027-01-01T00:00:00Z"
  conditions: [{type: Ready, status: "True"}]
`), &r)
	cells := map[string]any{"pc:Ready": "True", "pc:Secret": "web-tls", "pc:Replicas": int64(2), "pc:Hosts": "a.example.com,b.example.com", "pc:Expires": "2027-01-01T00:00:00Z"}
	for k, v := range cells {
		if r.Cells[k] != v {
			t.Errorf("%s = %#v, want %#v", k, r.Cells[k], v)
		}
	}
	if r.Health != "ok" {
		t.Errorf("generic Ready condition health: %s", r.Health)
	}

	// Missing fields stay empty.
	empty := Project(obj(t, `{apiVersion: cert-manager.io/v1, kind: Certificate, metadata: {name: x, namespace: demo}}`))
	p.Apply(obj(t, `{apiVersion: cert-manager.io/v1, kind: Certificate, metadata: {name: x, namespace: demo}}`), &empty)
	if len(empty.Cells) != 0 {
		t.Errorf("missing fields: %v", empty.Cells)
	}
	// Cluster-scoped: no namespace column; nothing to add: no printer.
	if c := NewPrinter(pcs, false).Columns(); c[1].Key == "namespace" {
		t.Errorf("cluster-scoped columns: %v", c)
	}
	if NewPrinter([]PrinterColumn{{Name: "Age", Type: "date", JSONPath: ".metadata.creationTimestamp"}}, true) != nil {
		t.Error("a printer with no extra column is useless")
	}
}
