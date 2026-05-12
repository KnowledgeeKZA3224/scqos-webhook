/*
Package evaluator runs the Supreme Computation gate chain against a SCQOSPacket.

The evaluator is the bridge between the packet and the AdmissionResponse.
It owns fail-fast sequencing and audit log writes.
Gates know nothing about each other or about audit logging.

Flow:
  SCQOSPacket → evaluator.Evaluate() → gates (fail-fast) → AdmissionResponse
                                                          → audit.Entry
*/
package evaluator

import (
	"context"
	"fmt"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/your-org/scqos-webhook/pkg/audit"
	"github.com/your-org/scqos-webhook/pkg/gates"
	"github.com/your-org/scqos-webhook/pkg/packet"
)

// Evaluator runs a gate chain against Supreme Computation packets.
type Evaluator struct {
	chain []gates.Gate
	log   *audit.Logger
}

// New returns an Evaluator using the provided gate chain and audit logger.
// Pass gates.DefaultChain for standard Supreme Computation evaluation.
func New(chain []gates.Gate, log *audit.Logger) *Evaluator {
	return &Evaluator{chain: chain, log: log}
}

// Evaluate runs all gates against the packet in order.
// The first gate to fail terminates evaluation.
// Every decision — ALLOW and DENY — is written to the audit log.
// The returned AdmissionResponse is ready to encode directly into the AdmissionReview reply.
func (e *Evaluator) Evaluate(ctx context.Context, p *packet.SCQOSPacket) *admissionv1.AdmissionResponse {
	for _, gate := range e.chain {
		result := gate.Evaluate(ctx, p)
		if !result.Passed {
			e.log.Record(audit.Entry{
				UID:       p.UID,
				Operation: string(p.Operation),
				Resource:  p.Resource,
				Namespace: p.Namespace,
				Name:      p.Name,
				Observer:  p.Observer,
				Lineage:   p.Lineage,
				Decision:  "DENY",
				Gate:      gate.Name(),
				Reason:    result.Reason,
				Message:   result.Message,
			})
			return denyResponse(p.UID, gate.Name(), result.Reason, result.Message)
		}
	}

	e.log.Record(audit.Entry{
		UID:       p.UID,
		Operation: string(p.Operation),
		Resource:  p.Resource,
		Namespace: p.Namespace,
		Name:      p.Name,
		Observer:  p.Observer,
		Lineage:   p.Lineage,
		Decision:  "ALLOW",
	})

	return allowResponse(p.UID)
}

// ---------------------------------------------------------------------------
// Response constructors
// ---------------------------------------------------------------------------

func allowResponse(uid string) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		UID:     admissionv1.UID(uid),
		Allowed: true,
	}
}

func denyResponse(uid, gate, reason, message string) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		UID:     admissionv1.UID(uid),
		Allowed: false,
		Result: &metav1.Status{
			Code:    403,
			Message: fmt.Sprintf("[SCQOS:%s:%s] %s", gate, reason, message),
		},
	}
}
