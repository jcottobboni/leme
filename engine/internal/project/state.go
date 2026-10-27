package project

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/kinds"
)

// Workload states, in PT-BR as shown in the grid "Status" cell, the detail, Problems, the map and the diagnosis.
const (
	StateAvailable   = "Disponível"   // every desired replica is updated, ready and available
	StateProgressing = "Progredindo"  // a rollout or scale is under way
	StateStuck       = "Travado"      // the rollout gave up (Progressing=False, ProgressDeadlineExceeded)
	StateDegraded    = "Degradado"    // fewer ready replicas than desired and nothing progressing
	StatePaused      = "Pausado"      // spec.paused
	StateNoReplicas  = "Sem réplicas" // desired 0
)

// Condition is the status condition that decided a state, kept as evidence.
type Condition struct {
	Type    string
	Status  string
	Reason  string
	Message string
}

func (c *Condition) String() string {
	if c == nil {
		return ""
	}
	s := fmt.Sprintf("condição %s=%s reason=%s", c.Type, c.Status, c.Reason)
	if c.Message != "" {
		s += ": " + c.Message
	}
	return s
}

// WorkloadState is the single semantic status of a replica-based workload (Deployment, StatefulSet,
// DaemonSet, ReplicaSet, ReplicationController, DeploymentConfig), derived from its status counters
// and conditions. The grid projection and the analyzer both use it, so they always agree.
type WorkloadState struct {
	State     string
	Health    string // ok | warn | error | neutral
	Desired   int64
	Ready     int64
	Updated   int64
	Available int64
	// Condition is set when a status condition decided the state (stuck rollout, replica failure).
	Condition *Condition
}

func condition(u *unstructured.Unstructured, typ string) (*Condition, bool) {
	for _, c := range slice(u, "status", "conditions") {
		cm, _ := c.(map[string]any)
		if strOr(cm["type"], "") == typ {
			return &Condition{Type: typ, Status: strOr(cm["status"], ""), Reason: strOr(cm["reason"], ""), Message: strOr(cm["message"], "")}, true
		}
	}
	return nil, false
}

func has(u *unstructured.Unstructured, path ...string) bool {
	_, found, _ := unstructured.NestedFieldNoCopy(u.Object, path...)
	return found
}

// StateOf derives the workload state; ok is false for kinds that are not replica-based built-in workloads.
func StateOf(u *unstructured.Unstructured) (WorkloadState, bool) {
	kind := u.GetKind()
	if !kinds.Builtin(kinds.GroupOf(u.GetAPIVersion())) {
		return WorkloadState{}, false
	}
	var s WorkloadState
	created, hasCreated := int64(0), false
	// The API server omits zero counters. Once the controller has written the status
	// (observedGeneration is set) a missing counter is 0; before that it is simply unknown.
	reported := has(u, "status", "observedGeneration")
	counter := func(dflt int64, path ...string) int64 {
		if reported || has(u, path...) {
			return num(u, path...)
		}
		return dflt
	}
	switch kind {
	case "DaemonSet":
		s.Desired, s.Ready = num(u, "status", "desiredNumberScheduled"), num(u, "status", "numberReady")
		s.Updated = counter(s.Desired, "status", "updatedNumberScheduled")
		s.Available = counter(s.Ready, "status", "numberAvailable")
	case "Deployment", "StatefulSet", "ReplicaSet", "ReplicationController", "DeploymentConfig":
		s.Desired = 1
		if has(u, "spec", "replicas") {
			s.Desired = num(u, "spec", "replicas")
		}
		s.Ready = num(u, "status", "readyReplicas")
		s.Updated = s.Desired
		if kind != "ReplicaSet" && kind != "ReplicationController" {
			s.Updated = counter(s.Desired, "status", "updatedReplicas")
		}
		s.Available = counter(s.Ready, "status", "availableReplicas")
		if kind != "Deployment" && (reported || has(u, "status", "replicas")) {
			created, hasCreated = num(u, "status", "replicas"), true
		}
	default:
		return WorkloadState{}, false
	}

	prog, hasProg := condition(u, "Progressing")
	if hasProg && prog.Status == "False" && (prog.Reason == "ProgressDeadlineExceeded" || kind == "DeploymentConfig") {
		s.State, s.Health, s.Condition = StateStuck, "error", prog
		return s, true
	}
	if rf, ok := condition(u, "ReplicaFailure"); ok && rf.Status == "True" {
		s.State, s.Health, s.Condition = StateDegraded, "error", rf
		return s, true
	}
	if s.Desired == 0 {
		s.State, s.Health = StateNoReplicas, "neutral"
		return s, true
	}
	complete := s.Ready >= s.Desired && s.Available >= s.Desired && s.Updated >= s.Desired
	if paused, _, _ := unstructured.NestedBool(u.Object, "spec", "paused"); paused {
		s.State, s.Health = StatePaused, "neutral"
		if !complete {
			s.Health = "warn"
		}
		return s, true
	}
	// Only reported facts count as progress: a missing counter is not evidence of a rollout.
	gen, observed := num(u, "metadata", "generation"), num(u, "status", "observedGeneration")
	progressing := gen > 0 && observed < gen ||
		s.Updated < s.Desired ||
		hasCreated && created < s.Desired ||
		hasProg && prog.Status == "True" && prog.Reason != "NewReplicaSetAvailable" ||
		kind == "StatefulSet" && str(u, "status", "updateRevision") != "" && str(u, "status", "currentRevision") != str(u, "status", "updateRevision")
	switch {
	case complete && !(gen > 0 && observed < gen):
		s.State, s.Health = StateAvailable, "ok"
	case progressing:
		s.State, s.Health = StateProgressing, "warn"
	default:
		s.State, s.Health = StateDegraded, "warn"
		if s.Available == 0 {
			s.Health = "error"
		}
	}
	return s, true
}

func workload(u *unstructured.Unstructured, r *Row) {
	s, _ := StateOf(u)
	r.Cells["ready"] = fmt.Sprintf("%d/%d", s.Ready, s.Desired)
	r.Cells["desired"] = s.Desired
	r.Cells["upToDate"] = s.Updated
	r.Cells["available"] = s.Available
	r.Cells["status"], r.Health = s.State, s.Health
}
