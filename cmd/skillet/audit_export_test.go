package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestConfiguredAuditExporterDisabledByDefault(t *testing.T) {
	t.Setenv(auditExportSinkEnv, "")
	t.Setenv(auditExportTargetEnv, "")
	exporter, closer, err := configuredAuditExporter(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if exporter != nil || closer != nil {
		t.Fatalf("exporter=%v closer=%v", exporter, closer)
	}
}

func TestConfiguredAuditExporterJSONLFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	t.Setenv(auditExportSinkEnv, "jsonl")
	t.Setenv(auditExportTargetEnv, path)
	exporter, closer, err := configuredAuditExporter(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if exporter == nil || closer == nil {
		t.Fatalf("exporter=%v closer=%v", exporter, closer)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("audit file permissions = %o", info.Mode().Perm())
	}
}

func TestConfiguredAuditExporterRejectsPartialOrUnknownConfig(t *testing.T) {
	t.Setenv(auditExportSinkEnv, "")
	t.Setenv(auditExportTargetEnv, "stdout")
	if _, _, err := configuredAuditExporter(nil); err == nil {
		t.Fatal("expected target-without-sink error")
	}
	t.Setenv(auditExportSinkEnv, "vendor-siem")
	if _, _, err := configuredAuditExporter(nil); err == nil {
		t.Fatal("expected unsupported sink error")
	}
}
