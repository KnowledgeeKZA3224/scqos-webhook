package gates

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	admissionv1 "k8s.io/api/admission/v1"

	"github.com/your-org/scqos-webhook/pkg/packet"
)

// DefaultChain is the ordered gate sequence applied to every admission request.
// Cheap annotation checks run first. Structural checks run last.
// The evaluator iterates this slice; first failure terminates evaluation.
var DefaultChain = []Gate{
	TimeGate{},
	GenesisGate{},
	CausalityGate{},
	PurposeGate{},
	BoundaryGate{},
	ReferenceGate{},
	ContinuityGate{},
	AlignmentGate{},
	CoherenceGate{},
}

// ---------------------------------------------------------------------------
// Gate 1 — Time
// Verifies the request arrived within an acceptable freshness window.
// Protects against replayed or stale admission requests.
// ---------------------------------------------------------------------------

const maxRequestAge = 30 * time.Second

// TimeGate checks request freshness.
type TimeGate struct{}

func (TimeGate) Name() string { return "Time" }

func (TimeGate) Evaluate(_ context.Context, p *packet.SCQOSPacket) GateResult {
	age := time.Since(p.ReceivedAt)
	if age > maxRequestAge {
		return Deny("REQUEST_TOO_OLD",
			fmt.Sprintf("request timestamp is %s old; maximum allowed age is %s",
				age.Round(time.Millisecond), maxRequestAge))
	}
	return Allow()
}

// ---------------------------------------------------------------------------
// Gate 2 — Genesis
// Verifies an accountable observer is declared.
// No anonymous state enters the cluster.
// ---------------------------------------------------------------------------

// GenesisGate checks that scqos.io/observer is present and non-empty.
type GenesisGate struct{}

func (GenesisGate) Name() string { return "Genesis" }

func (GenesisGate) Evaluate(_ context.Context, p *packet.SCQOSPacket) GateResult {
	if p.Observer == "" {
		return Deny("MISSING_OBSERVER",
			fmt.Sprintf("annotation %q is required; set it to the accountable "+
				"human or system actor initiating this change", packet.AnnotationObserver))
	}
	return Allow()
}

// ---------------------------------------------------------------------------
// Gate 3 — Causality
// Verifies the change has a declared cause: ticket, CI run, PR ref, or pipeline URL.
// State without traceable cause is incoherent.
// ---------------------------------------------------------------------------

// CausalityGate checks that scqos.io/lineage is present and non-empty.
type CausalityGate struct{}

func (CausalityGate) Name() string { return "Causality" }

func (CausalityGate) Evaluate(_ context.Context, p *packet.SCQOSPacket) GateResult {
	if p.Lineage == "" {
		return Deny("MISSING_LINEAGE",
			fmt.Sprintf("annotation %q is required; set it to a ticket ID, CI run URL, "+
				"PR reference, or pipeline identifier", packet.AnnotationLineage))
	}
	return Allow()
}

// ---------------------------------------------------------------------------
// Gate 4 — Purpose
// Verifies a declared intent is present.
// Resources without declared purpose cannot be evaluated for alignment.
// ---------------------------------------------------------------------------

// PurposeGate checks that scqos.io/purpose is present and non-empty.
type PurposeGate struct{}

func (PurposeGate) Name() string { return "Purpose" }

func (PurposeGate) Evaluate(_ context.Context, p *packet.SCQOSPacket) GateResult {
	if p.Purpose == "" {
		return Deny("MISSING_PURPOSE",
			fmt.Sprintf("annotation %q is required; describe the intended "+
				"function of this resource", packet.AnnotationPurpose))
	}
	return Allow()
}

// ---------------------------------------------------------------------------
// Gate 5 — Boundary
// Verifies namespace is permitted and containers declare resource limits.
// Unbounded resources threaten cluster stability.
// ---------------------------------------------------------------------------

// DeniedNamespaces lists namespaces that reject arbitrary workloads.
// Override at startup via environment configuration if needed.
var DeniedNamespaces = map[string]bool{
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,
}

// BoundaryGate checks namespace safety and container resource limits.
type BoundaryGate struct{}

func (BoundaryGate) Name() string { return "Boundary" }

func (BoundaryGate) Evaluate(_ context.Context, p *packet.SCQOSPacket) GateResult {
	if DeniedNamespaces[p.Namespace] {
		return Deny("DENIED_NAMESPACE",
			fmt.Sprintf("namespace %q is protected; workloads may not be admitted here",
				p.Namespace))
	}

	if !isContainerResource(p.Resource) {
		return Allow()
	}

	containers, err := extractContainers(p.Resource, p.ObjectRaw)
	if err != nil {
		return Deny("BOUNDARY_PARSE_ERROR",
			fmt.Sprintf("could not parse container specs: %v", err))
	}

	for _, c := range containers {
		if len(c.Resources.Limits) == 0 {
			return Deny("MISSING_RESOURCE_LIMITS",
				fmt.Sprintf("container %q declares no resource limits; "+
					"CPU and memory limits are required on all containers", c.Name))
		}
	}

	return Allow()
}

// ---------------------------------------------------------------------------
// Gate 6 — Reference
// Verifies all image references are pinned to digests.
// Tags are mutable; only digests guarantee stable references.
// ---------------------------------------------------------------------------

// ReferenceGate checks that every container image is digest-pinned.
type ReferenceGate struct{}

func (ReferenceGate) Name() string { return "Reference" }

func (ReferenceGate) Evaluate(_ context.Context, p *packet.SCQOSPacket) GateResult {
	if !isContainerResource(p.Resource) {
		return Allow()
	}

	if len(p.Images) == 0 {
		return Deny("NO_IMAGE_REFERENCES",
			"no container images found in spec; at least one container is required")
	}

	for _, image := range p.Images {
		if !isDigestPinned(image) {
			return Deny("UNPINNED_IMAGE_REFERENCE",
				fmt.Sprintf("image %q must be pinned to a digest (@sha256:...) "+
					"rather than a mutable tag; resolve with "+
					"'docker inspect --format={{.RepoDigests}} <image>'", image))
		}
	}

	return Allow()
}

func isDigestPinned(image string) bool {
	return strings.Contains(image, "@sha256:")
}

// ---------------------------------------------------------------------------
// Gate 7 — Continuity
// On UPDATE, verifies the new object is structurally compatible with the old.
// Immutable fields — namespace and kind — must not drift.
// ---------------------------------------------------------------------------

// ContinuityGate checks structural compatibility between old and new state on UPDATE.
type ContinuityGate struct{}

func (ContinuityGate) Name() string { return "Continuity" }

func (ContinuityGate) Evaluate(_ context.Context, p *packet.SCQOSPacket) GateResult {
	if p.Operation != admissionv1.Update {
		return Allow()
	}

	if p.OldObjectRaw == nil {
		return Deny("CONTINUITY_NO_OLD_OBJECT",
			"UPDATE operation received no prior object state; cannot verify continuity")
	}

	oldMeta, err := extractMinimalMeta(p.OldObjectRaw)
	if err != nil {
		return Deny("CONTINUITY_PARSE_ERROR",
			fmt.Sprintf("could not parse prior object metadata: %v", err))
	}
	newMeta, err := extractMinimalMeta(p.ObjectRaw)
	if err != nil {
		return Deny("CONTINUITY_PARSE_ERROR",
			fmt.Sprintf("could not parse new object metadata: %v", err))
	}

	if oldMeta.Metadata.Namespace != newMeta.Metadata.Namespace {
		return Deny("NAMESPACE_DRIFT",
			fmt.Sprintf("namespace changed from %q to %q; namespace is immutable",
				oldMeta.Metadata.Namespace, newMeta.Metadata.Namespace))
	}

	if oldMeta.Kind != "" && newMeta.Kind != "" && oldMeta.Kind != newMeta.Kind {
		return Deny("KIND_DRIFT",
			fmt.Sprintf("resource kind changed from %q to %q; kind is immutable",
				oldMeta.Kind, newMeta.Kind))
	}

	return Allow()
}

// ---------------------------------------------------------------------------
// Gate 8 — Alignment
// Verifies labels are consistent with declared purpose.
// A resource that declares one purpose but labels another is incoherent.
// ---------------------------------------------------------------------------

// AlignmentGate checks label-annotation consistency and identifying label presence.
type AlignmentGate struct{}

func (AlignmentGate) Name() string { return "Alignment" }

func (AlignmentGate) Evaluate(_ context.Context, p *packet.SCQOSPacket) GateResult {
	if labelPurpose, ok := p.Labels["scqos.io/purpose"]; ok {
		if labelPurpose != p.Purpose {
			return Deny("PURPOSE_LABEL_MISMATCH",
				fmt.Sprintf("label scqos.io/purpose=%q does not match "+
					"annotation scqos.io/purpose=%q; they must agree",
					labelPurpose, p.Purpose))
		}
	}

	_, hasApp := p.Labels["app"]
	_, hasK8sName := p.Labels["app.kubernetes.io/name"]
	if !hasApp && !hasK8sName {
		return Deny("MISSING_IDENTIFYING_LABEL",
			"resource must carry an 'app' or 'app.kubernetes.io/name' label "+
				"to enable workload identification and alignment verification")
	}

	return Allow()
}

// ---------------------------------------------------------------------------
// Gate 9 — Coherence
// The aggregate gate. If evaluation reaches here, gates 1–8 all passed.
// Coherence is structural: it records that the packet is fully coherent.
// ---------------------------------------------------------------------------

// CoherenceGate is the terminal gate. It always passes.
// Its presence in the chain means the audit log records "all nine gates evaluated."
type CoherenceGate struct{}

func (CoherenceGate) Name() string { return "Coherence" }

func (CoherenceGate) Evaluate(_ context.Context, _ *packet.SCQOSPacket) GateResult {
	return Allow()
}

// ---------------------------------------------------------------------------
// Shared internal helpers
// ---------------------------------------------------------------------------

func isContainerResource(resource string) bool {
	switch resource {
	case "pods", "deployments", "replicasets", "statefulsets",
		"daemonsets", "jobs", "cronjobs":
		return true
	}
	return false
}

// minimalMeta is used by ContinuityGate to compare old vs new object identity.
type minimalMeta struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
}

func extractMinimalMeta(raw []byte) (*minimalMeta, error) {
	var m minimalMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	return &m, nil
}

// minimalContainer is used by BoundaryGate to check resource limits.
type minimalContainer struct {
	Name      string `json:"name"`
	Resources struct {
		Limits map[string]interface{} `json:"limits"`
	} `json:"resources"`
}

type podContainerShim struct {
	Spec struct {
		Containers     []minimalContainer `json:"containers"`
		InitContainers []minimalContainer `json:"initContainers"`
	} `json:"spec"`
}

type podTemplateContainerShim struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers     []minimalContainer `json:"containers"`
				InitContainers []minimalContainer `json:"initContainers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

func extractContainers(resource string, raw []byte) ([]minimalContainer, error) {
	if resource == "pods" {
		var s podContainerShim
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("unmarshal pod containers: %w", err)
		}
		return append(s.Spec.Containers, s.Spec.InitContainers...), nil
	}
	var s podTemplateContainerShim
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("unmarshal pod template containers: %w", err)
	}
	return append(
		s.Spec.Template.Spec.Containers,
		s.Spec.Template.Spec.InitContainers...,
	), nil
}
