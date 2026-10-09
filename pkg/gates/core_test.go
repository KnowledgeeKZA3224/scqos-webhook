package gates

import (
    "context"
    "testing"
    "time"

    admissionv1 "k8s.io/api/admission/v1"

    "github.com/KnowledgeeKZA3224/scqos-webhook/pkg/packet"
)

func goodPacket() *packet.SCQOSPacket {
    return &packet.SCQOSPacket{
        UID: "review-test",
        Operation: admissionv1.Create,
        Resource: "configmaps",
        Namespace: "review",
        Observer: "test-owner",
        Lineage: "case-001",
        Purpose: "test-governed-release",
        Labels: map[string]string{"app": "review-example"},
        Annotations: map[string]string{},
        ReceivedAt: time.Now().UTC(),
    }
}

func TestDefaultChainPermitsCompletePacket(t *testing.T) {
    p := goodPacket()
    for _, gate := range DefaultChain {
        if result := gate.Evaluate(context.Background(), p); !result.Passed {
            t.Fatalf("gate %s unexpectedly denied: %s: %s", gate.Name(), result.Reason, result.Message)
        }
    }
}

func TestGenesisDeniesMissingObserver(t *testing.T) {
    p := goodPacket()
    p.Observer = ""
    r := (GenesisGate{}).Evaluate(context.Background(), p)
    if r.Passed || r.Reason != "MISSING_OBSERVER" {
        t.Fatalf("expected MISSING_OBSERVER denial, got %+v", r)
    }
}

func TestAlignmentDeniesConflictingPurpose(t *testing.T) {
    p := goodPacket()
    p.Labels["scqos.io/purpose"] = "different"
    r := (AlignmentGate{}).Evaluate(context.Background(), p)
    if r.Passed || r.Reason != "PURPOSE_LABEL_MISMATCH" {
        t.Fatalf("expected purpose mismatch denial, got %+v", r)
    }
}

func TestBoundaryDeniesProtectedNamespace(t *testing.T) {
    p := goodPacket()
    p.Namespace = "kube-system"
    r := (BoundaryGate{}).Evaluate(context.Background(), p)
    if r.Passed || r.Reason != "DENIED_NAMESPACE" {
        t.Fatalf("expected namespace denial, got %+v", r)
    }
}
