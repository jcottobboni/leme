package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// Vuln is one CVE in an image.
type Vuln struct {
	ID        string `json:"id"`
	Package   string `json:"package"`
	Installed string `json:"installed"`
	Fixed     string `json:"fixed,omitempty"`
	Severity  string `json:"severity"`
	Title     string `json:"title,omitempty"`
}

// ImageReport is the vulnerability summary of one image.
type ImageReport struct {
	Image     string         `json:"image"`
	Container string         `json:"container,omitempty"`
	Source    string         `json:"source"` // trivy-cli or trivy-operator
	Scanned   time.Time      `json:"scanned"`
	OS        string         `json:"os,omitempty"`
	Counts    map[string]int `json:"counts"`
	Vulns     []Vuln         `json:"vulns"`
	Error     string         `json:"error,omitempty"`
}

var labelValue = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$`)

var imageRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)

// Trivy runs the Trivy CLI with a result cache and a concurrency limit.
type Trivy struct {
	CacheDir string
	mu       sync.Mutex
	cache    map[string]ImageReport
	inflight map[string]chan struct{} // image -> closed when its scan finishes
	// One scan at a time: Trivy keeps its DB in a bbolt file with an exclusive lock.
	slots chan struct{}
}

// NewTrivy creates a runner.
func NewTrivy(cacheDir string) *Trivy {
	return &Trivy{CacheDir: cacheDir, cache: map[string]ImageReport{}, inflight: map[string]chan struct{}{}, slots: make(chan struct{}, 1)}
}

// Scan returns a cached report (6 h) or scans the image.
func (t *Trivy) Scan(ctx context.Context, image string, refresh bool) ImageReport {
	if !imageRef.MatchString(image) || strings.HasPrefix(image, "-") {
		return ImageReport{Image: image, Source: "trivy-cli", Error: "referência de imagem inválida", Counts: map[string]int{}}
	}
	t.mu.Lock()
	if r, ok := t.cache[image]; ok && !refresh && time.Since(r.Scanned) < 6*time.Hour && r.Error == "" {
		t.mu.Unlock()
		return r
	}
	// Another request is already scanning this image: wait for it and share the result.
	if wait, ok := t.inflight[image]; ok {
		t.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return ImageReport{Image: image, Source: "trivy-cli", Error: ctx.Err().Error(), Counts: map[string]int{}}
		}
		t.mu.Lock()
		r := t.cache[image]
		t.mu.Unlock()
		return r
	}
	done := make(chan struct{})
	t.inflight[image] = done
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.inflight, image)
		t.mu.Unlock()
		close(done)
	}()
	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	case <-ctx.Done():
		return ImageReport{Image: image, Source: "trivy-cli", Error: ctx.Err().Error(), Counts: map[string]int{}}
	}
	r := t.run(ctx, image)
	t.mu.Lock()
	t.cache[image] = r
	t.mu.Unlock()
	return r
}

func (t *Trivy) run(ctx context.Context, image string) ImageReport {
	r := ImageReport{Image: image, Source: "trivy-cli", Scanned: time.Now(), Counts: map[string]int{}, Vulns: []Vuln{}}
	bin, err := lookPath("trivy")
	if err != nil {
		r.Error = err.Error()
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	// "--" ends option parsing: the image can never be read as a flag.
	cmd := exec.CommandContext(ctx, bin, "image", "--format", "json", "--scanners", "vuln", "--quiet", "--cache-dir", t.CacheDir, "--", image)
	cmd.Env = trivyEnv()
	out, err := cmd.Output()
	if err != nil {
		r.Error = fmt.Sprintf("trivy: %v%s", err, stderrOf(err))
		return r
	}
	rep, err := ParseTrivy(out)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	rep.Image, rep.Source, rep.Scanned = image, "trivy-cli", r.Scanned
	return rep
}

// trivyEnv passes only what Trivy needs: PATH, HOME (registry logins in ~/.docker) and proxies. Cloud
// credentials, KUBECONFIG and TRIVY_* settings of the engine's environment are not inherited.
func trivyEnv() []string {
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	for _, k := range []string{"HOME", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "DOCKER_CONFIG", "XDG_RUNTIME_DIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// ParseTrivy converts `trivy image --format json`.
func ParseTrivy(b []byte) (ImageReport, error) {
	var doc struct {
		Metadata struct {
			OS *struct {
				Family string `json:"Family"`
				Name   string `json:"Name"`
			} `json:"OS"`
		} `json:"Metadata"`
		Results []struct {
			Vulnerabilities []struct {
				VulnerabilityID  string `json:"VulnerabilityID"`
				PkgName          string `json:"PkgName"`
				InstalledVersion string `json:"InstalledVersion"`
				FixedVersion     string `json:"FixedVersion"`
				Severity         string `json:"Severity"`
				Title            string `json:"Title"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	r := ImageReport{Counts: map[string]int{}, Vulns: []Vuln{}}
	if err := json.Unmarshal(b, &doc); err != nil {
		return r, fmt.Errorf("saída inesperada do trivy: %w", err)
	}
	if doc.Metadata.OS != nil {
		r.OS = doc.Metadata.OS.Family + " " + doc.Metadata.OS.Name
	}
	for _, res := range doc.Results {
		for _, v := range res.Vulnerabilities {
			sev := normalizeSeverity(v.Severity)
			r.Counts[sev]++
			r.Vulns = append(r.Vulns, Vuln{ID: v.VulnerabilityID, Package: v.PkgName, Installed: v.InstalledVersion, Fixed: v.FixedVersion, Severity: sev, Title: v.Title})
		}
	}
	sortVulns(r.Vulns)
	return r, nil
}

func sortVulns(v []Vuln) {
	rank := map[string]int{Critical: 0, High: 1, Medium: 2, Low: 3, Info: 4}
	sort.SliceStable(v, func(i, j int) bool {
		if rank[v[i].Severity] != rank[v[j].Severity] {
			return rank[v[i].Severity] < rank[v[j].Severity]
		}
		return v[i].ID < v[j].ID
	})
}

var vulnReports = schema.GroupVersionResource{Group: "aquasecurity.github.io", Version: "v1alpha1", Resource: "vulnerabilityreports"}

// OperatorReports reads trivy-operator VulnerabilityReports for a workload, if the CRD exists.
func OperatorReports(ctx context.Context, sess *cluster.Session, kind, ns, name string) ([]ImageReport, error) {
	if _, ok := sess.Lookup(vulnReports.Group, vulnReports.Resource); !ok {
		return nil, errNoOperator
	}
	if !labelValue.MatchString(kind) || !labelValue.MatchString(name) || ns == "" {
		return nil, errors.New("workload inválido")
	}
	sel := fmt.Sprintf("trivy-operator.resource.kind=%s,trivy-operator.resource.name=%s", kind, name)
	l, err := sess.Dynamic.Resource(vulnReports).Namespace(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return nil, err
	}
	var out []ImageReport
	for _, it := range l.Items {
		out = append(out, operatorReport(it.Object))
	}
	return out, nil
}

var errNoOperator = errors.New("trivy-operator não instalado")

// IsNoOperator reports that the cluster has no trivy-operator CRD.
func IsNoOperator(err error) bool { return errors.Is(err, errNoOperator) }

func operatorReport(o map[string]any) ImageReport {
	r := ImageReport{Source: "trivy-operator", Counts: map[string]int{}, Vulns: []Vuln{}}
	labels, _, _ := unstructured.NestedStringMap(o, "metadata", "labels")
	r.Container = labels["trivy-operator.container.name"]
	reg, _, _ := unstructured.NestedString(o, "report", "registry", "server")
	repo, _, _ := unstructured.NestedString(o, "report", "artifact", "repository")
	tag, _, _ := unstructured.NestedString(o, "report", "artifact", "tag")
	r.Image = strings.TrimPrefix(reg+"/"+repo, "/")
	if tag != "" {
		r.Image += ":" + tag
	}
	if ts, _, _ := unstructured.NestedString(o, "report", "updateTimestamp"); ts != "" {
		r.Scanned, _ = time.Parse(time.RFC3339, ts)
	}
	vs, _, _ := unstructured.NestedSlice(o, "report", "vulnerabilities")
	for _, raw := range vs {
		m, _ := raw.(map[string]any)
		str := func(k string) string { s, _ := m[k].(string); return s }
		v := Vuln{ID: str("vulnerabilityID"), Package: str("resource"), Installed: str("installedVersion"), Fixed: str("fixedVersion"), Severity: normalizeSeverity(str("severity")), Title: str("title")}
		r.Counts[v.Severity]++
		r.Vulns = append(r.Vulns, v)
	}
	sortVulns(r.Vulns)
	return r
}
