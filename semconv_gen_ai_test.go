package otel

import (
	"os"
	"testing"
)

func TestContentCapture_DefaultOff(t *testing.T) {
	t.Setenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", "")
	if ContentCaptureEnabled() {
		t.Fatal("ContentCaptureEnabled() = true, want false by default")
	}
}

func TestContentCapture_EnabledByEnv(t *testing.T) {
	t.Setenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", "true")
	if !ContentCaptureEnabled() {
		t.Fatal("ContentCaptureEnabled() = false, want true")
	}
}

func TestSemconvOptInGate(t *testing.T) {
	t.Setenv("OTEL_SEMCONV_STABILITY_OPT_IN", "")
	if SemconvEnabled() {
		t.Fatal("SemconvEnabled() = true, want false without opt-in")
	}
	t.Setenv("OTEL_SEMCONV_STABILITY_OPT_IN", "gen_ai_latest_experimental")
	if !SemconvEnabled() {
		t.Fatal("SemconvEnabled() = false, want true with opt-in")
	}
}

func TestRedactText(t *testing.T) {
	t.Setenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", "true")
	in := "api key sk-1234567890 and email test@example.com"
	out := RedactText(in)
	if out == in {
		t.Fatal("RedactText() did not redact input")
	}
	if len(out) == 0 {
		t.Fatal("RedactText() returned empty string")
	}
}

func TestEnvIsolation(t *testing.T) {
	old := os.Getenv("OTEL_SEMCONV_STABILITY_OPT_IN")
	t.Setenv("OTEL_SEMCONV_STABILITY_OPT_IN", old)
}
