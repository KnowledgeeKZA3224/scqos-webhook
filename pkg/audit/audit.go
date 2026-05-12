/*
Package audit provides an append-only structured audit log for every
admission decision made by the Supreme Computation gate chain.

Design invariants:
  - Every ALLOW and DENY produces exactly one Entry.
  - Entries are written as JSON lines (one per line).
  - The file is opened in append+create mode; no entry is ever overwritten.
  - Write errors are reported to stderr but never panic — a logging failure
    must never silently change an admission decision.
  - Logger is safe for concurrent use.
*/
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Entry is a single admission decision record.
type Entry struct {
	Timestamp string `json:"timestamp"`          // RFC3339Nano UTC
	UID       string `json:"uid"`                // AdmissionReview UID
	Operation string `json:"operation"`          // CREATE | UPDATE
	Resource  string `json:"resource"`           // pods | deployments | ...
	Namespace string `json:"namespace"`
	Name      string `json:"name,omitempty"`
	Observer  string `json:"observer,omitempty"` // scqos.io/observer value
	Lineage   string `json:"lineage,omitempty"`  // scqos.io/lineage value
	Decision  string `json:"decision"`           // ALLOW | DENY
	Gate      string `json:"gate,omitempty"`     // First failing gate name (DENY only)
	Reason    string `json:"reason,omitempty"`   // Machine-readable denial code
	Message   string `json:"message,omitempty"`  // Human-readable denial message
}

// Logger writes audit entries to an append-only file.
// Safe for concurrent use.
type Logger struct {
	mu   sync.Mutex
	file *os.File
}

// NewLogger opens or creates the audit log file at path.
// The file is opened in append mode; existing content is preserved.
func NewLogger(path string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening audit log %q: %w", path, err)
	}
	return &Logger{file: f}, nil
}

// Record writes one audit entry as a JSON line.
// Errors are printed to stderr but do not affect admission decisions.
func (l *Logger) Record(e Entry) {
	e.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)

	line, err := json.Marshal(e)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scqos/audit: marshal error uid=%s: %v\n", e.UID, err)
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, err := fmt.Fprintf(l.file, "%s\n", line); err != nil {
		fmt.Fprintf(os.Stderr, "scqos/audit: write error uid=%s: %v\n", e.UID, err)
	}
}

// Close flushes and closes the audit log file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}
