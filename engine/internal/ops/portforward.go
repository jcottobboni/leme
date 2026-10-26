package ops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// ForwardRequest asks for a new port-forward.
type ForwardRequest struct {
	Namespace  string `json:"namespace"`
	Kind       string `json:"kind"` // pod | service
	Name       string `json:"name"`
	RemotePort int    `json:"remotePort"`
	LocalPort  int    `json:"localPort"` // 0 = random
}

// Forward is an active session.
type Forward struct {
	ID         string    `json:"id"`
	ClusterID  string    `json:"clusterId"`
	Context    string    `json:"context"`
	Namespace  string    `json:"namespace"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Pod        string    `json:"pod"`
	RemotePort int       `json:"remotePort"`
	LocalPort  int       `json:"localPort"`
	URL        string    `json:"url"`
	Started    time.Time `json:"started"`
	Error      string    `json:"error,omitempty"`

	stop chan struct{}
}

// Forwards tracks port-forward sessions across clusters.
type Forwards struct {
	mu   sync.Mutex
	byID map[string]*Forward
}

// NewForwards creates the registry.
func NewForwards() *Forwards { return &Forwards{byID: map[string]*Forward{}} }

// List returns active sessions.
func (f *Forwards) List() []Forward {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Forward, 0, len(f.byID))
	for _, v := range f.byID {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// Stop ends a session.
func (f *Forwards) Stop(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.byID[id]
	if ok {
		close(v.stop)
		delete(f.byID, id)
	}
	return ok
}

// StopCluster ends every session of a cluster.
func (f *Forwards) StopCluster(clusterID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, v := range f.byID {
		if v.ClusterID == clusterID {
			close(v.stop)
			delete(f.byID, id)
		}
	}
}

// Start resolves the target pod and opens the forward on 127.0.0.1.
// A tunnel bypasses the read-only guard (e.g. a database port), so it needs a writable session
// and is torn down when the session is locked again or disconnected.
func (f *Forwards) Start(ctx context.Context, s *cluster.Session, req ForwardRequest) (Forward, error) {
	if err := s.CheckWritable(); err != nil {
		return Forward{}, err
	}
	if req.RemotePort <= 0 || req.RemotePort > 65535 || req.LocalPort < 0 || req.LocalPort > 65535 {
		return Forward{}, errors.New("invalid port")
	}
	podName, port, err := resolveTarget(ctx, s, req)
	if err != nil {
		return Forward{}, err
	}
	transport, upgrader, err := spdy.RoundTripperFor(s.Config)
	if err != nil {
		return Forward{}, err
	}
	u := s.Typed.CoreV1().RESTClient().Post().Resource("pods").Namespace(req.Namespace).Name(podName).SubResource("portforward").URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, u)
	stop, ready := make(chan struct{}), make(chan struct{})
	fw, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{fmt.Sprintf("%d:%d", req.LocalPort, port)}, stop, ready, io.Discard, io.Discard)
	if err != nil {
		return Forward{}, err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- fw.ForwardPorts() }()
	select {
	case <-ready:
	case err := <-errCh:
		return Forward{}, fmt.Errorf("port-forward: %w", err)
	case <-time.After(15 * time.Second):
		close(stop)
		return Forward{}, errors.New("port-forward: timeout")
	}
	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		close(stop)
		return Forward{}, fmt.Errorf("port-forward: %v", err)
	}
	id := randomID()
	v := &Forward{
		ID: id, ClusterID: s.Context.ID, Context: s.Context.Name, Namespace: req.Namespace, Kind: req.Kind, Name: req.Name,
		Pod: podName, RemotePort: req.RemotePort, LocalPort: int(ports[0].Local),
		URL: fmt.Sprintf("http://127.0.0.1:%d", ports[0].Local), Started: time.Now(), stop: stop,
	}
	f.mu.Lock()
	f.byID[id] = v
	out := *v
	f.mu.Unlock()
	scope := s.WriteScope()
	go func() {
		select {
		case <-scope.Done():
			f.Stop(id)
		case <-stop:
		}
	}()
	go func() {
		if err := <-errCh; err != nil {
			f.mu.Lock()
			if cur, ok := f.byID[id]; ok {
				cur.Error = err.Error()
			}
			f.mu.Unlock()
		}
	}()
	return out, nil
}

func resolveTarget(ctx context.Context, s *cluster.Session, req ForwardRequest) (string, int, error) {
	if req.Kind != "service" {
		return req.Name, req.RemotePort, nil
	}
	svc, err := s.Typed.CoreV1().Services(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		return "", 0, err
	}
	if len(svc.Spec.Selector) == 0 {
		return "", 0, errors.New("service has no selector")
	}
	pods, err := s.Typed.CoreV1().Pods(req.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String()})
	if err != nil {
		return "", 0, err
	}
	var pod *corev1.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil {
			pod = p
			break
		}
	}
	if pod == nil {
		return "", 0, errors.New("no running pod behind the service")
	}
	target := intstr.FromInt32(int32(req.RemotePort))
	for _, sp := range svc.Spec.Ports {
		if int(sp.Port) == req.RemotePort {
			if sp.TargetPort.IntValue() != 0 || sp.TargetPort.StrVal != "" {
				target = sp.TargetPort
			}
			break
		}
	}
	if target.Type == intstr.Int {
		return pod.Name, target.IntValue(), nil
	}
	for _, c := range pod.Spec.Containers {
		for _, cp := range c.Ports {
			if cp.Name == target.StrVal {
				return pod.Name, int(cp.ContainerPort), nil
			}
		}
	}
	if n, err := strconv.Atoi(target.StrVal); err == nil {
		return pod.Name, n, nil
	}
	return "", 0, fmt.Errorf("named port %q not found in pod %s", target.StrVal, pod.Name)
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
