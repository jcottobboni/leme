package security

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParsePolaris(t *testing.T) {
	b := []byte(`{"Results":[{"Name":"web","Namespace":"demo","Kind":"Deployment","Results":{},
	  "PodResult":{"Results":{"hostIPCSet":{"ID":"hostIPCSet","Message":"ok","Success":true,"Severity":"danger","Category":"Security"},
	    "automountServiceAccountToken":{"ID":"automountServiceAccountToken","Message":"The ServiceAccount will be automounted","Success":false,"Severity":"warning","Category":"Security"}},
	  "ContainerResults":[{"Name":"nginx","Results":{"runAsRootAllowed":{"ID":"runAsRootAllowed","Message":"Should not be allowed to run as root","Success":false,"Severity":"danger","Category":"Security"},
	    "cpuLimitsMissing":{"ID":"cpuLimitsMissing","Message":"CPU limits should be set","Success":false,"Severity":"warning","Category":"Efficiency"}}}]}}]}`)
	fs, err := ParsePolaris(b)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Finding{}
	for _, f := range fs {
		got[f.ID] = f
	}
	if len(fs) != 3 || got["polaris/runAsRootAllowed"].Severity != High || got["polaris/runAsRootAllowed"].Container != "nginx" || got["polaris/cpuLimitsMissing"].Severity != Low {
		t.Fatalf("%+v", fs)
	}
}

func TestParseKubescape(t *testing.T) {
	b := []byte(`{"summaryDetails":{"controls":{"C-0013":{"name":"Non-root containers","severity":"Medium","category":{"name":"Workload"}}}},
	 "results":[{"resourceID":"path=1/api=apps/v1/demo/Deployment/web","controls":[
	   {"controlID":"C-0013","name":"Non-root containers","status":{"status":"failed"},"rules":[{"paths":[{"fixPath":{"path":"spec.template.spec.containers[0].securityContext.runAsNonRoot","value":"true"}}]}]},
	   {"controlID":"C-0002","name":"x","status":{"status":"passed"}}]},
	  {"resourceID":"path=2/api=v1/demo/Service/web","controls":[{"controlID":"C-0013","name":"Non-root containers","status":{"status":"failed"}}]}]}`)
	fs, err := ParseKubescape(b, "nsa")
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 || fs[0].Group != "apps" || fs[0].Kind != "Deployment" || fs[0].Name != "web" || fs[0].Severity != Medium || !strings.Contains(fs[0].Remediation, "runAsNonRoot = true") {
		t.Fatalf("%+v", fs)
	}
	if fs[1].Group != "" || fs[1].Kind != "Service" || fs[1].Namespace != "demo" {
		t.Fatalf("core resource id: %+v", fs[1])
	}
}

func TestParseTrivy(t *testing.T) {
	b := []byte(`{"Metadata":{"OS":{"Family":"alpine","Name":"3.21.3"}},"Results":[{"Vulnerabilities":[
	  {"VulnerabilityID":"CVE-2","PkgName":"b","InstalledVersion":"1","FixedVersion":"2","Severity":"LOW"},
	  {"VulnerabilityID":"CVE-1","PkgName":"a","InstalledVersion":"1","FixedVersion":"","Severity":"CRITICAL","Title":"bad"}]}]}`)
	r, err := ParseTrivy(b)
	if err != nil || r.Counts[Critical] != 1 || r.Counts[Low] != 1 || r.Vulns[0].ID != "CVE-1" || r.OS != "alpine 3.21.3" {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestTrivyRejectsBadImage(t *testing.T) {
	tr := NewTrivy(t.TempDir())
	for _, img := range []string{"--config=/etc/passwd", "a b", ""} {
		if r := tr.Scan(t.Context(), img, false); r.Error == "" {
			t.Errorf("%q must be rejected", img)
		}
	}
}

func TestExportSkipsSecretsAndStatus(t *testing.T) {
	dep := obj(t, "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web, namespace: demo, annotations: {kubectl.kubernetes.io/last-applied-configuration: secret-ish}}\nspec: {replicas: 1, template: {spec: {containers: [{name: c, env: [{name: DB_PASSWORD, value: hunter2}]}]}}}\nstatus: {replicas: 1}\n")
	sec := obj(t, "apiVersion: v1\nkind: Secret\nmetadata: {name: s, namespace: demo}\ndata: {k: c2VjcmV0}\n")
	dir, err := Export(Input{Objects: map[string][]*unstructured.Unstructured{"apps/deployments": {dep}, "/secrets": {sec}}})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	files, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if len(files) != 1 {
		t.Fatalf("only the deployment is exported: %v", files)
	}
	b, _ := os.ReadFile(files[0])
	if strings.Contains(string(b), "status:") || strings.Contains(string(b), "secret-ish") || strings.Contains(string(b), "hunter2") || !strings.Contains(string(b), "DB_PASSWORD") {
		t.Fatalf("status/last-applied must be stripped:\n%s", b)
	}
	st, _ := os.Stat(dir)
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 { // Windows has no Unix mode bits
		t.Fatalf("export dir mode %v", st.Mode().Perm())
	}
}
