package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/mhingston/skillet/internal/auditexport"
)

const (
	auditExportSinkEnv   = "SKILLET_AUDIT_EXPORT_SINK"
	auditExportTargetEnv = "SKILLET_AUDIT_EXPORT_TARGET"
)

func configuredAuditExporter(log *slog.Logger) (*auditexport.Exporter, io.Closer, error) {
	sinkName := strings.TrimSpace(os.Getenv(auditExportSinkEnv))
	target := strings.TrimSpace(os.Getenv(auditExportTargetEnv))
	if sinkName == "" {
		if target != "" {
			return nil, nil, fmt.Errorf("%s requires %s", auditExportTargetEnv, auditExportSinkEnv)
		}
		return nil, nil, nil
	}
	if sinkName != "jsonl" {
		return nil, nil, fmt.Errorf("%s %q is unsupported; choose jsonl", auditExportSinkEnv, sinkName)
	}
	if target == "" {
		return nil, nil, fmt.Errorf("%s is required when audit export is enabled", auditExportTargetEnv)
	}

	var writer io.Writer
	var closer io.Closer
	if target == "stdout" {
		writer = os.Stdout
	} else {
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return nil, nil, fmt.Errorf("open audit export target: %w", err)
		}
		writer, closer = file, file
	}
	sink, err := auditexport.NewJSONLinesSink(writer)
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		return nil, nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	var exporter *auditexport.Exporter
	exporter = auditexport.New(sink, func(event auditexport.Event, exportErr error) {
		log.Error("audit export failed",
			"event", event.EventType,
			"organization", event.OrganizationID,
			"request_id", event.RequestID,
			"failures", exporter.Failures(),
			"error", exportErr,
		)
	})
	return exporter, closer, nil
}
