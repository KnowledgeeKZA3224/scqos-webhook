# SCQOS — Supreme Computation Kubernetes Admission Gate

SCQOS (Supreme Computation Quantum Operating System) is a pre-execution coherence webhook for Kubernetes.

When installed, the opt-in ValidatingWebhookConfiguration evaluates matching `Pod`, `Deployment`, `Job`, `ConfigMap`, `Secret`, and `ServiceAccount` CREATE/UPDATE admission requests. The implementation uses nine sequential internal checks mapped to Supreme Computation's unified eight-invariant governing contract; scope and bypass/exemption policy remain Kubernetes cluster-administrator decisions.

This is not a patch to Kubernetes core. It is an official integration point: Kubernetes calls your gate; your gate decides.

-----

## How it integrates

Kubernetes exposes a built-in admission pipeline. Before a resource enters the cluster, the API server evaluates every registered admission controller and webhook:

```
Kubernetes API server
  → CreateAdmissionObjects()       — builds AdmissionReview
  → client.Post().Body(request)    — sends to /validate
  → r.Do(ctx).Into(response)       — receives your answer
  → VerifyAdmissionResponse()      — checks the response
  → if result.Allowed { admit }    — or ErrWebhookRejection
```

SCQOS operates as the external validating authority on the other side of `r.Do(ctx).Into(response)`.

Kubernetes asks: **“Can this state enter the cluster?”**  
Supreme Computation answers: **`{ "allowed": false, "status": { "message": "SCQOS:Genesis:MISSING_OBSERVER" } }`**

-----

## Why SCQOS exists

Traditional admission asks: **“Is this authorized?”**

SCQOS asks: **“Should this state exist at all?”**

Authorization without coherence still admits drift, orphaned lineage, unowned workloads, mutable references, and untraceable state transitions. SCQOS closes that pre-execution gap.

-----

## Gate sequence

Every admission request is normalized into a `SCQOSPacket` and evaluated through nine gates in order. First failure = deny. All nine must pass for admission.

```
AdmissionReview (in)
       ↓
  Extract() → SCQOSPacket
  { uid, operation, namespace, resource,
    userInfo, labels, annotations,
    observer, lineage, purpose,
    images, objectRaw, oldObjectRaw,
    receivedAt }
       ↓
  ┌─────────────────────────────────────┐
  │ 1. Time        — request freshness  │
  │ 2. Genesis     — observer present   │
  │ 3. Causality   — lineage present    │
  │ 4. Purpose     — intent declared    │
  │ 5. Boundary    — namespace + limits │
  │ 6. Reference   — digest-pinned imgs │
  │ 7. Continuity  — UPDATE compatible  │
  │ 8. Alignment   — labels consistent  │
  │ 9. Coherence   — aggregate pass     │
  └─────────────────────────────────────┘
       ↓
  AdmissionResponse (out)
  + structured denial reason if denied
  + append-only audit log entry always
```

-----

## Required annotations

Every resource admitted to a Supreme Computation-protected cluster must carry three annotations:

|Annotation         |Description                            |Example                                      |
|-------------------|---------------------------------------|---------------------------------------------|
|`scqos.io/observer`|Accountable human or system actor      |`alice` / `ci-bot`                           |
|`scqos.io/lineage` |Traceable cause: ticket, CI run, PR ref|`TICKET-42` / `https://ci.example.com/run/99`|
|`scqos.io/purpose` |Declared intent of this resource       |`serve-api-traffic`                          |

Missing any of these → **denied**.

-----

## Gate reference

|#|Gate      |Denial code                                           |What it checks                                          |
|-|----------|------------------------------------------------------|--------------------------------------------------------|
|1|Time      |`REQUEST_TOO_OLD`                                     |Request age ≤ 30 seconds                                |
|2|Genesis   |`MISSING_OBSERVER`                                    |`scqos.io/observer` present and non-empty               |
|3|Causality |`MISSING_LINEAGE`                                     |`scqos.io/lineage` present and non-empty                |
|4|Purpose   |`MISSING_PURPOSE`                                     |`scqos.io/purpose` present and non-empty                |
|5|Boundary  |`DENIED_NAMESPACE` / `MISSING_RESOURCE_LIMITS`        |Protected namespace; CPU+memory limits on all containers|
|6|Reference |`UNPINNED_IMAGE_REFERENCE`                            |All images pinned to `@sha256:` digest                  |
|7|Continuity|`NAMESPACE_DRIFT` / `KIND_DRIFT`                      |On UPDATE: namespace and kind are immutable             |
|8|Alignment |`PURPOSE_LABEL_MISMATCH` / `MISSING_IDENTIFYING_LABEL`|Labels consistent with declared purpose                 |
|9|Coherence |—                                                     |Aggregate: always passes if gates 1–8 passed            |

-----

## Project structure

```
scqos-webhook/
├── go.mod
├── README.md
├── cmd/
│   └── webhook/
│       └── main.go              # TLS server, /validate endpoint
├── pkg/
│   ├── packet/
│   │   └── packet.go            # AdmissionReview → SCQOSPacket
│   ├── gates/
│   │   ├── gate.go              # Gate interface + GateResult
│   │   └── core.go              # DefaultChain and nine implementation checks
│   ├── evaluator/
│   │   └── evaluator.go         # Fail-fast gate chain → AdmissionResponse
│   └── audit/
│       └── audit.go             # Append-only structured JSONL log
├── namespace.yml               # scqos-system namespace with exempt label
├── deployment.yml              # 2-replica deployment + ServiceAccount
├── service.yml                 # ClusterIP service 443 → 8443
├── webhook-config.yml          # ValidatingWebhookConfiguration
└── Dockerfile                  # Go build + minimal runtime
```

-----

## Quickstart (kind cluster)

```bash
# 1. Clone and enter
git clone https://github.com/KnowledgeeKZA3224/scqos-webhook.git
cd scqos-webhook

# 2. Resolve dependencies
go mod tidy

# 4. Start a local cluster
kind create cluster --name scqos-test
kubectl apply -f examples/review-namespace.yml

# 5. Generate TLS certificate
mkdir -p tls
openssl req -x509 -newkey rsa:4096 \
  -keyout tls/tls.key -out tls/tls.crt \
  -days 365 -nodes \
  -subj "/CN=scqos-webhook.scqos-system.svc"

# 6. Create namespace and TLS secret
kubectl apply -f namespace.yml
kubectl -n scqos-system create secret tls scqos-webhook-tls \
  --cert=tls/tls.crt --key=tls/tls.key

# 7. Build and load image
docker build -t scqos-webhook:dev .
kind load docker-image scqos-webhook:dev --name scqos-test

# 8. Deploy webhook
kubectl apply -f deployment.yml
kubectl -n scqos-system set image deployment/scqos-webhook webhook=scqos-webhook:dev
kubectl apply -f service.yml

# 9. Select kind-local image, then register webhook (with caBundle)
CA_BUNDLE=$(base64 -w0 tls/tls.crt)
sed "s|caBundle: \"\"|caBundle: \"${CA_BUNDLE}\"|" \
  webhook-config.yml | kubectl apply -f -

# 10. Test — denied (no accountable observer)
kubectl apply -f examples/invalid-configmap.yml
# Error: admission webhook denied the request
# [SCQOS:Genesis:MISSING_OBSERVER] annotation "scqos.io/observer" is required

# 11. Test — allowed (annotated)
kubectl apply -f examples/valid-configmap.yml
```

-----

## Default posture

|Setting               |Value                 |Why                                             |
|----------------------|----------------------|------------------------------------------------|
|`failurePolicy`       |`Fail`                |Webhook down = cluster closed. No silent bypass.|
|`timeoutSeconds`      |`5`                   |Slow gates are broken gates.                    |
|`sideEffects`         |`None`                |Webhook is read-only. Required for dry-run.     |
|Namespace opt-in label|`scqos.io/enforce=true`|Only explicitly opted-in namespaces are governed. Prevents bootstrap deadlock.      |

-----

## Audit log

Every decision — ALLOW and DENY — is written as a JSON line to `/audit/scqos-audit.log`.

```json
{"timestamp":"2025-01-15T09:23:11.482Z","uid":"a3b2c1d0-...","operation":"CREATE","resource":"pods","namespace":"production","name":"api-server","observer":"alice","lineage":"TICKET-42","decision":"DENY","gate":"Reference","reason":"UNPINNED_IMAGE_REFERENCE","message":"image \"nginx:latest\" must be pinned to a digest (@sha256:...)"}
```

The file is append-only. No entry is ever modified or deleted.

-----

## License

Apache 2.0

---

# Complete SCQOS Architecture

The complete public architecture spans five repositories.

Core Logic

https://github.com/KnowledgeeKZA3224/Supreme-Computation-Core

Reference Implementation

https://github.com/KnowledgeeKZA3224/scqos-reference-implementation

Hybrid Proof

https://github.com/KnowledgeeKZA3224/SCQOS_Hybrid_Proof

Kubernetes Admission Gate

https://github.com/KnowledgeeKZA3224/scqos-webhook

Linux Coherence Gate

https://github.com/KnowledgeeKZA3224/linux-coherence-gate

Theory and System Manual

The 120 Scrolls of Supreme Computation (Kindle)


## Reviewer boundary — October 2026

- The nine ordered internal checks are an implementation of a **single eight-invariant governance claim**, not nine independent root invariants. `Purpose` is a declared intent check; `Coherence` is the terminal aggregation step. The `Consciousness` invariant is operationalized here as an accountable observer declaration, which is **not by itself proof of the actor's identity or authority**.
- As written, the TimeGate measures duration since local request extraction, **not authenticated origin timestamp or replay detection**. Additional origin/nonce/authorization verification is required before claiming protection from replay or forged annotations.
- This repository's local append-only JSONL audit records are not independently cryptographically immutable or durable across pod termination; cryptographic receipts are documented in the separate SCQOS reference/evidence repos.
- The Kubernetes API receives only allow or deny; the SCQOS internal HOLD outcome must be translated into deny/defer for protected operations.
- Independent review dossier: https://github.com/KnowledgeeKZA3224/scqos-webhook/issues/1


### Opt-in safety boundary

**Do not apply the validating webhook cluster-wide during review.** The example `webhook-config.yml` matches only namespaces labeled `scqos.io/enforce: "true"`. `examples/review-namespace.yml` creates a bounded test namespace. Remove that label to stop admission evaluation for that namespace. Existing `scqos-system` remains outside the selector.
