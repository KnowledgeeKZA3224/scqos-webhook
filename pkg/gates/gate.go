/*
Package gates defines the Gate interface and GateResult type.

Every Supreme Computation gate receives one SCQOSPacket and returns one GateResult.
Gates are stateless, deterministic, and side-effect free.
The evaluator owns sequencing and fail-fast logic — gates know nothing
about each other.
*/
package gates

import (
	"context"

	"github.com/your-org/scqos-webhook/pkg/packet"
)

// Gate is the contract every Supreme Computation gate must satisfy.
type Gate interface {
	// Name returns the gate identifier used in audit logs and denial reasons.
	Name() string

	// Evaluate inspects the packet and returns a GateResult.
	// Must not mutate the packet.
	// Must not produce side effects.
	Evaluate(ctx context.Context, p *packet.SCQOSPacket) GateResult
}

// GateResult is the outcome of a single gate evaluation.
type GateResult struct {
	Passed  bool   // true if the gate is satisfied
	Reason  string // short machine-readable denial code, e.g. "MISSING_OBSERVER"
	Message string // human-readable explanation, shown in kubectl output
}

// Allow returns a passing GateResult.
func Allow() GateResult {
	return GateResult{Passed: true}
}

// Deny returns a failing GateResult with the given reason and message.
func Deny(reason, message string) GateResult {
	return GateResult{Passed: false, Reason: reason, Message: message}
}
