package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/dagql/dagui"
)

// CheckLogRenderer searches for check spans in telemetry and renders their logs
type CheckLogRenderer struct {
	profile termenv.Profile
}

// NewCheckLogRenderer creates a new check log renderer
func NewCheckLogRenderer() *CheckLogRenderer {
	return &CheckLogRenderer{
		profile: termenv.ColorProfile(),
	}
}

// RenderHook returns a hook function suitable for Frontend.RegisterPostRunHook
func (clr *CheckLogRenderer) RenderHook() func(*dagui.DB, io.Writer) {
	return func(db *dagui.DB, w io.Writer) {
		if err := clr.renderCheckLogs(db, w); err != nil {
			fmt.Fprintf(w, "Warning: Failed to render check logs: %v\n", err)
		}
	}
}

// renderCheckLogs renders logs for check spans found in the database
func (clr *CheckLogRenderer) renderCheckLogs(db *dagui.DB, w io.Writer) error {
	if db == nil {
		return fmt.Errorf("no database available")
	}

	// Find all check spans by searching for the check attribute
	checkSpans := clr.findCheckSpans(db)

	if len(checkSpans) == 0 {
		return nil // No check spans found
	}

	// Render header
	fmt.Fprintf(w, "\n%s\n",
		termenv.String("📋 Check Execution Logs").Bold().Foreground(clr.profile.Color("6")))
	fmt.Fprintf(w, "%s\n\n",
		strings.Repeat("=", 50))

	// Render logs for each check span
	for _, span := range checkSpans {
		if err := clr.renderSpanLogs(db, w, span); err != nil {
			fmt.Fprintf(w, "Error rendering logs for span %s: %v\n", span.Name, err)
			continue
		}
	}

	return nil
}

// CheckSpanInfo holds information about a check span
type CheckSpanInfo struct {
	Span     *dagui.Span
	Name     string
	Path     string
	Passed   *bool
	ErrorMsg string
}

// findCheckSpans finds all spans that have the check attribute
func (clr *CheckLogRenderer) findCheckSpans(db *dagui.DB) []*CheckSpanInfo {
	var checkSpans []*CheckSpanInfo

	// Iterate through all spans to find check-related ones
	for _, span := range db.Spans.Order {
		if span == nil {
			continue
		}

		// Check if this span has the check attribute
		if checkInfo := clr.extractCheckInfo(span); checkInfo != nil {
			checkSpans = append(checkSpans, checkInfo)
		}
	}

	return checkSpans
}

// extractCheckInfo extracts check information from span attributes
func (clr *CheckLogRenderer) extractCheckInfo(span *dagui.Span) *CheckSpanInfo {
	// Look for the check attribute in ExtraAttributes
	if span.ExtraAttributes == nil {
		return nil
	}
	info := &CheckSpanInfo{
		Span: span,
		Name: span.Name, // Default name
	}
	if checkNameRaw, hasCheckName := span.ExtraAttributes["dagger.io/check.name"]; !hasCheckName {
		return nil
	} else {
		var name string
		if err := json.Unmarshal(checkNameRaw, &name); err == nil {
			info.Name = name
		}
	}
	return info
}

// renderSpanLogs renders logs for a specific check span
func (clr *CheckLogRenderer) renderSpanLogs(db *dagui.DB, w io.Writer, checkInfo *CheckSpanInfo) error {
	span := checkInfo.Span

	// Determine status and color
	status := "⚪"
	statusColor := clr.profile.Color("8") // gray

	if checkInfo.Passed != nil {
		if *checkInfo.Passed {
			status = "🟢"
			statusColor = clr.profile.Color("2") // green
		} else {
			status = "🔴"
			statusColor = clr.profile.Color("1") // red
		}
	} else if span.Status.Code.String() == "ERROR" {
		status = "🔴"
		statusColor = clr.profile.Color("1") // red
	}

	// Calculate duration
	duration := ""
	if !span.EndTime.IsZero() {
		duration = fmt.Sprintf(" (%v)", span.EndTime.Sub(span.StartTime).Round(time.Millisecond))
	}

	// Use the extracted check name, falling back to span name
	displayName := checkInfo.Name
	if displayName == "" {
		displayName = span.Name
	}

	fmt.Fprintf(w, "%s %s%s\n",
		status,
		termenv.String(displayName).Bold().Foreground(statusColor),
		termenv.String(duration).Faint(),
	)

	// Show path if available and different from name
	if checkInfo.Path != "" && checkInfo.Path != displayName {
		fmt.Fprintf(w, "  %s %s\n",
			termenv.String("Path:").Faint(),
			termenv.String(checkInfo.Path).Faint(),
		)
	}

	// Show error message if available
	if checkInfo.ErrorMsg != "" {
		fmt.Fprintf(w, "  %s %s\n",
			termenv.String("Error:").Foreground(clr.profile.Color("1")),
			checkInfo.ErrorMsg,
		)
	}

	// Check for logs associated with this span
	logs, hasLogs := db.PrimaryLogs[span.ID]
	if !hasLogs || len(logs) == 0 {
		fmt.Fprintf(w, "  %s\n\n",
			termenv.String("No logs available").Faint().Italic())
		return nil
	}

	// Render the logs
	for _, logRecord := range logs {
		if err := clr.renderLogRecord(w, logRecord); err != nil {
			fmt.Fprintf(w, "  Error rendering log: %v\n", err)
		}
	}

	fmt.Fprintf(w, "\n")
	return nil
}

// renderLogRecord renders a single log record
func (clr *CheckLogRenderer) renderLogRecord(w io.Writer, record sdklog.Record) error {
	timestamp := record.Timestamp().Format("15:04:05")

	// Determine log level color
	levelColor := clr.profile.Color("8") // default gray
	levelStr := "INFO"

	if record.Severity() >= log.SeverityError {
		levelColor = clr.profile.Color("1") // red
		levelStr = "ERROR"
	} else if record.Severity() >= log.SeverityWarn {
		levelColor = clr.profile.Color("3") // yellow
		levelStr = "WARN"
	} else if record.Severity() >= log.SeverityDebug {
		levelColor = clr.profile.Color("4") // blue
		levelStr = "DEBUG"
	}

	// Format the log message
	message := record.Body().AsString()

	// Add indentation and format
	fmt.Fprintf(w, "  %s %s %s\n",
		termenv.String(timestamp).Faint(),
		termenv.String(levelStr).Foreground(levelColor),
		message,
	)

	// Render attributes if any
	record.WalkAttributes(func(kv log.KeyValue) bool {
		if kv.Key != "" && kv.Value.AsString() != "" {
			fmt.Fprintf(w, "    %s=%s\n",
				termenv.String(string(kv.Key)).Faint(),
				kv.Value.AsString(),
			)
		}
		return true
	})

	return nil
}
