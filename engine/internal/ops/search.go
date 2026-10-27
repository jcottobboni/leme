package ops

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kinds"
)

// Hit is one global-search result.
type Hit struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Match     string `json:"match"` // what matched: name, label k=v or image
	score     int
}

var searchKinds = []string{"apps/deployments", "apps/statefulsets", "apps/daemonsets", "batch/cronjobs", "batch/jobs", "/pods", "/services",
	"networking.k8s.io/ingresses", "route.openshift.io/routes", "/configmaps", "/secrets", "/persistentvolumeclaims", "/nodes", "/namespaces"}

type metaEntry struct {
	group, kind, name, namespace string
	labels                       map[string]string
}

type searchIndex struct {
	at      time.Time
	entries []metaEntry
	images  map[string][]metaEntry // image -> workloads using it
	errs    map[string]string
}

// Searcher keeps a short-lived metadata index per session.
type Searcher struct {
	mu  sync.Mutex
	idx map[*cluster.Session]*searchIndex
}

// NewSearcher creates a searcher.
func NewSearcher() *Searcher { return &Searcher{idx: map[*cluster.Session]*searchIndex{}} }

func (s *Searcher) index(ctx context.Context, sess *cluster.Session) *searchIndex {
	s.mu.Lock()
	for k := range s.idx {
		if k.Life().Err() != nil {
			delete(s.idx, k)
		}
	}
	if i, ok := s.idx[sess]; ok && time.Since(i.at) < 30*time.Second {
		s.mu.Unlock()
		return i
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	idx := &searchIndex{at: time.Now(), images: map[string][]metaEntry{}, errs: map[string]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, key := range searchKinds {
		group, resource, _ := strings.Cut(key, "/")
		res, ok := sess.Lookup(group, resource)
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Metadata only: Secret and ConfigMap contents are never fetched.
			l, err := sess.Meta.Resource(res.GVR()).List(ctx, metav1.ListOptions{})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				idx.errs[key] = err.Error()
				return
			}
			for _, it := range l.Items {
				idx.entries = append(idx.entries, metaEntry{group: res.Group, kind: res.Kind, name: it.Name, namespace: it.Namespace, labels: it.Labels})
			}
		}()
	}
	for _, key := range []string{"apps/deployments", "apps/statefulsets", "apps/daemonsets", "batch/cronjobs"} {
		group, resource, _ := strings.Cut(key, "/")
		res, ok := sess.Lookup(group, resource)
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			objs, err := sess.Informers.List(ctx, cluster.Key{GVR: res.GVR()})
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, u := range objs {
				path := kinds.PodTemplatePath(res.Group, res.Kind)
				for _, field := range []string{"containers", "initContainers"} {
					cs, _, _ := unstructured.NestedSlice(u.Object, append(path, field)...)
					for _, c := range cs {
						if m, ok := c.(map[string]any); ok {
							if img, _ := m["image"].(string); img != "" {
								idx.images[img] = append(idx.images[img], metaEntry{group: res.Group, kind: res.Kind, name: u.GetName(), namespace: u.GetNamespace()})
							}
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	s.mu.Lock()
	s.idx[sess] = idx
	s.mu.Unlock()
	return idx
}

// Search finds objects by name, label (k=v or value) or container image.
func (s *Searcher) Search(ctx context.Context, sess *cluster.Session, q string, limit int) ([]Hit, map[string]string) {
	q = strings.ToLower(strings.TrimSpace(q))
	if len(q) < 2 {
		return nil, nil
	}
	idx := s.index(ctx, sess)
	hits := []Hit{}
	for _, e := range idx.entries {
		n := strings.ToLower(e.name)
		switch {
		case n == q:
			hits = append(hits, Hit{Group: e.group, Kind: e.kind, Name: e.name, Namespace: e.namespace, Match: "name", score: 100})
			continue
		case strings.HasPrefix(n, q):
			hits = append(hits, Hit{Group: e.group, Kind: e.kind, Name: e.name, Namespace: e.namespace, Match: "name", score: 80})
			continue
		case strings.Contains(n, q):
			hits = append(hits, Hit{Group: e.group, Kind: e.kind, Name: e.name, Namespace: e.namespace, Match: "name", score: 60})
			continue
		}
		for k, v := range e.labels {
			kv := strings.ToLower(k + "=" + v)
			if kv == q || strings.ToLower(v) == q || (strings.Contains(q, "=") && strings.Contains(kv, q)) {
				hits = append(hits, Hit{Group: e.group, Kind: e.kind, Name: e.name, Namespace: e.namespace, Match: k + "=" + v, score: 40})
				break
			}
		}
	}
	for img, users := range idx.images {
		if !strings.Contains(strings.ToLower(img), q) {
			continue
		}
		for _, e := range users {
			hits = append(hits, Hit{Group: e.group, Kind: e.kind, Name: e.name, Namespace: e.namespace, Match: "image " + img, score: 50})
		}
	}
	// Workloads before their pods, then shorter names.
	rank := map[string]int{"Deployment": 6, "StatefulSet": 6, "DaemonSet": 6, "CronJob": 5, "Service": 5, "Ingress": 4, "Route": 4, "Namespace": 4, "Node": 4}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i].score+rank[hits[i].Kind], hits[j].score+rank[hits[j].Kind]
		if a != b {
			return a > b
		}
		if len(hits[i].Name) != len(hits[j].Name) {
			return len(hits[i].Name) < len(hits[j].Name)
		}
		return hits[i].Namespace+hits[i].Name < hits[j].Namespace+hits[j].Name
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	if len(idx.errs) == 0 {
		return hits, nil
	}
	return hits, idx.errs
}
