/*
cmd/webhook/main.go — Supreme Computation Kubernetes Admission Gate

Runs the TLS HTTPS server that Kubernetes calls on every admission request
matching the ValidatingWebhookConfiguration rules.

This is the receiver side of the Kubernetes admission artery:

  Kubernetes API server
    → POST /validate (AdmissionReview)
      → Extract SCQOSPacket
        → Evaluator runs nine gates (fail-fast)
          → AdmissionResponse { allowed: true/false }
            → Kubernetes admits or rejects the resource

Environment variables:
  SCQOS_PORT       — listen port (default: 8443)
  SCQOS_TLS_CERT   — path to TLS certificate (default: /tls/tls.crt)
  SCQOS_TLS_KEY    — path to TLS private key (default: /tls/tls.key)
  SCQOS_AUDIT_LOG  — path to audit log file (default: /audit/scqos-audit.log)
*/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/your-org/scqos-webhook/pkg/audit"
	"github.com/your-org/scqos-webhook/pkg/evaluator"
	"github.com/your-org/scqos-webhook/pkg/gates"
	"github.com/your-org/scqos-webhook/pkg/packet"
)

const (
	defaultPort     = "8443"
	defaultTLSCert  = "/tls/tls.crt"
	defaultTLSKey   = "/tls/tls.key"
	defaultAuditLog = "/audit/scqos-audit.log"
	maxBodyBytes    = 1 << 20 // 1 MB
	readTimeout     = 5 * time.Second
	writeTimeout    = 10 * time.Second
	shutdownTimeout = 10 * time.Second
)

func main() {
	port := envOr("SCQOS_PORT", defaultPort)
	certFile := envOr("SCQOS_TLS_CERT", defaultTLSCert)
	keyFile := envOr("SCQOS_TLS_KEY", defaultTLSKey)
	auditPath := envOr("SCQOS_AUDIT_LOG", defaultAuditLog)

	auditLog, err := audit.NewLogger(auditPath)
	if err != nil {
		log.Fatalf("scqos-webhook: failed to open audit log: %v", err)
	}
	defer auditLog.Close()

	eval := evaluator.New(gates.DefaultChain, auditLog)

	mux := http.NewServeMux()
	mux.HandleFunc("/validate", validateHandler(eval))
	mux.HandleFunc("/healthz", healthzHandler)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		log.Printf("scqos-webhook: listening on :%s (TLS)", port)
		if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
			log.Fatalf("scqos-webhook: server error: %v", err)
		}
	}()

	<-stop
	log.Println("scqos-webhook: received shutdown signal")

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("scqos-webhook: shutdown error: %v", err)
	}
	log.Println("scqos-webhook: stopped")
}

// validateHandler handles POST /validate.
// This is the integration point: Kubernetes sends AdmissionReview here,
// Supreme Computation evaluates it, and returns AdmissionReview with allow/deny response.
func validateHandler(eval *evaluator.Evaluator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var review admissionv1.AdmissionReview
		if err := json.Unmarshal(body, &review); err != nil {
			http.Error(w,
				fmt.Sprintf("failed to decode AdmissionReview: %v", err),
				http.StatusBadRequest)
			return
		}

		pkt, err := packet.Extract(&review)
		if err != nil {
			// Structural failure in the review itself — deny immediately.
			writeResponse(w, &review, malformedResponse(&review, err))
			return
		}

		resp := eval.Evaluate(r.Context(), pkt)
		writeResponse(w, &review, resp)
	}
}

// healthzHandler handles GET /healthz for readiness and liveness probes.
func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

// writeResponse encodes the AdmissionResponse into an AdmissionReview and writes it.
func writeResponse(w http.ResponseWriter, review *admissionv1.AdmissionReview, resp *admissionv1.AdmissionResponse) {
	out := admissionv1.AdmissionReview{
		TypeMeta: review.TypeMeta,
		Response: resp,
	}
	// Kubernetes requires TypeMeta to be set on the response.
	if out.APIVersion == "" {
		out.APIVersion = "admission.k8s.io/v1"
	}
	if out.Kind == "" {
		out.Kind = "AdmissionReview"
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		log.Printf("scqos-webhook: encode response error: %v", err)
	}
}

// malformedResponse produces a deny response for a structurally broken AdmissionReview.
func malformedResponse(review *admissionv1.AdmissionReview, err error) *admissionv1.AdmissionResponse {
	uid := admissionv1.UID("")
	if review.Request != nil {
		uid = review.Request.UID
	}
	return &admissionv1.AdmissionResponse{
		UID:     uid,
		Allowed: false,
		Result: &metav1.Status{
			Code:    400,
			Message: fmt.Sprintf("[SCQOS:MALFORMED_REQUEST] %v", err),
		},
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
