/*
Package packet defines the canonical SCQOSPacket — the single normalized
state object created from a Kubernetes AdmissionReview request.

Every gate receives exactly one SCQOSPacket. No gate reads from the
AdmissionReview directly. This is the contract boundary.

Flow:
  Kubernetes → POST /validate → AdmissionReview → Extract() → SCQOSPacket → gates
*/
package packet

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
)

// Annotation keys. All Supreme Computation required annotations live under this prefix.
const (
	AnnotationObserver = "scqos.io/observer" // Accountable human or system actor
	AnnotationLineage  = "scqos.io/lineage"  // Ticket, CI run ID, PR ref, or pipeline URL
	AnnotationPurpose  = "scqos.io/purpose"  // Declared intent of this resource
)

// SCQOSPacket is the normalized admission state passed to every gate.
// Constructed once per request by Extract(). Read-only after construction.
// Gates must not modify it.
type SCQOSPacket struct {
	// Identity
	UID       string                // AdmissionReview UID; echoed back in response
	Operation admissionv1.Operation // CREATE | UPDATE | DELETE | CONNECT
	Resource  string                // e.g. "pods", "deployments"
	Namespace string                // Target namespace
	Name      string                // Resource name (may be empty on CREATE)

	// Actor
	UserInfo authv1.UserInfo // Who or what is making this request

	// Raw objects (JSON bytes, gate-parseable)
	ObjectRaw    []byte // Incoming object spec (nil on DELETE)
	OldObjectRaw []byte // Prior object spec (nil on CREATE)

	// Derived metadata (never nil after Extract)
	Labels      map[string]string
	Annotations map[string]string

	// Supreme Computation annotation values — extracted for gate convenience.
	// Empty string means the annotation was absent or blank.
	Observer string // scqos.io/observer
	Lineage  string // scqos.io/lineage
	Purpose  string // scqos.io/purpose

	// Image references (Pods and Pod-template-bearing resources).
	// Each entry is the full image string as declared in the spec.
	// Empty for ConfigMaps, Secrets, ServiceAccounts.
	Images []string

	// Timing
	ReceivedAt time.Time // When Extract() was called; used by the Time gate
}

// Extract builds a SCQOSPacket from an AdmissionReview.
// Returns an error only for structural failures (nil review, malformed JSON).
// Missing Supreme Computation annotations are NOT errors here — gates evaluate those.
func Extract(review *admissionv1.AdmissionReview) (*SCQOSPacket, error) {
	if review == nil {
		return nil, fmt.Errorf("AdmissionReview is nil")
	}
	req := review.Request
	if req == nil {
		return nil, fmt.Errorf("AdmissionReview.Request is nil")
	}

	p := &SCQOSPacket{
		UID:          string(req.UID),
		Operation:    req.Operation,
		Resource:     req.Resource.Resource,
		Namespace:    req.Namespace,
		Name:         req.Name,
		UserInfo:     req.UserInfo,
		ObjectRaw:    copyBytes(req.Object.Raw),
		OldObjectRaw: copyBytes(req.OldObject.Raw),
		ReceivedAt:   time.Now().UTC(),
		Labels:       map[string]string{},
		Annotations:  map[string]string{},
	}

	if len(p.ObjectRaw) > 0 {
		meta, err := extractMeta(p.ObjectRaw)
		if err != nil {
			return nil, fmt.Errorf("extracting object metadata: %w", err)
		}
		if meta.Labels != nil {
			p.Labels = meta.Labels
		}
		if meta.Annotations != nil {
			p.Annotations = meta.Annotations
		}
	}

	// Promote Supreme Computation annotation values to first-class fields.
	p.Observer = strings.TrimSpace(p.Annotations[AnnotationObserver])
	p.Lineage = strings.TrimSpace(p.Annotations[AnnotationLineage])
	p.Purpose = strings.TrimSpace(p.Annotations[AnnotationPurpose])

	// Extract image references. Non-fatal on parse error — ReferenceGate
	// handles an empty Images slice appropriately.
	if len(p.ObjectRaw) > 0 {
		images, err := extractImages(p.Resource, p.ObjectRaw)
		if err == nil {
			p.Images = images
		}
	}

	return p, nil
}

// --- Internal helpers ---

type objectMeta struct {
	Metadata struct {
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
}

func extractMeta(raw []byte) (*objectMeta, error) {
	var m objectMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("unmarshal object metadata: %w", err)
	}
	return &m, nil
}

func extractImages(resource string, raw []byte) ([]string, error) {
	switch resource {
	case "pods":
		return imagesFromPodSpec(raw)
	case "deployments", "replicasets", "statefulsets", "daemonsets", "jobs", "cronjobs":
		return imagesFromPodTemplateSpec(raw)
	default:
		return []string{}, nil
	}
}

type podSpecShim struct {
	Spec corev1.PodSpec `json:"spec"`
}

func imagesFromPodSpec(raw []byte) ([]string, error) {
	var pod podSpecShim
	if err := json.Unmarshal(raw, &pod); err != nil {
		return nil, fmt.Errorf("unmarshal pod spec: %w", err)
	}
	return collectImages(pod.Spec), nil
}

type podTemplateShim struct {
	Spec struct {
		Template struct {
			Spec corev1.PodSpec `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

func imagesFromPodTemplateSpec(raw []byte) ([]string, error) {
	var obj podTemplateShim
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("unmarshal pod template spec: %w", err)
	}
	return collectImages(obj.Spec.Template.Spec), nil
}

func collectImages(spec corev1.PodSpec) []string {
	var images []string
	for _, c := range spec.InitContainers {
		images = append(images, c.Image)
	}
	for _, c := range spec.Containers {
		images = append(images, c.Image)
	}
	for _, c := range spec.EphemeralContainers {
		images = append(images, c.Image)
	}
	return images
}

// copyBytes returns a copy of b, or nil if b is empty.
func copyBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
