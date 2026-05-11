package otel

import (
	"os"
	"regexp"
	"strings"
)

const (
	AttrSystem         = "gen_ai.system"
	AttrRequestModel   = "gen_ai.request.model"
	AttrUsageInput     = "gen_ai.usage.input_tokens"
	AttrUsageOutput    = "gen_ai.usage.output_tokens"
	AttrUsageSource    = "gen_ai.usage.source"
	AttrFinishReason   = "gen_ai.response.finish_reason"
	AttrOperation      = "gen_ai.operation.name"
	AttrServerAddr     = "server.address"
	AttrErrorType      = "error.type"
	AttrUserID         = "user.id"
	AttrSessionID      = "session.id"
	AttrInputMessages  = "gen_ai.input.messages"
	AttrOutputMessages = "gen_ai.output.messages"

	EventFirstToken = "gen_ai.first_token"

	MetricClientTokenUsage        = "gen_ai.client.token.usage"
	MetricClientOperationDuration = "gen_ai.client.operation.duration"
	MetricClientOperationTTFT     = "gen_ai.client.operation.time_to_first_chunk"
	MetricAgentIterations         = "agent.iterations"
	MetricAgentToolInvocations    = "agent.tool.invocations"

	semconvOptInValue = "gen_ai_latest_experimental"
	contentCaptureEnv = "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"
	semconvOptInEnv   = "OTEL_SEMCONV_STABILITY_OPT_IN"
)

func SemconvEnabled() bool {
	return strings.Contains(os.Getenv(semconvOptInEnv), semconvOptInValue)
}

func ContentCaptureEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(contentCaptureEnv)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

var (
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	keyPattern   = regexp.MustCompile(`\b(?:sk|api[_-]?key)[-_A-Za-z0-9]*\b`)
)

func RedactText(s string) string {
	if s == "" {
		return s
	}
	s = emailPattern.ReplaceAllString(s, "[REDACTED_EMAIL]")
	s = keyPattern.ReplaceAllString(s, "[REDACTED_SECRET]")
	return s
}
