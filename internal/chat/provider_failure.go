package chat

import (
	"fmt"
	"regexp"
	"strings"
)

var rejectedRequestStatus = regexp.MustCompile(`(?i)\b(?:status(?: code)?|http|bad request|not found|unprocessable entity)[\s:=]*(?:400|404|422)\b`)

// IsProviderRequestRejected identifies requests that cannot succeed unchanged.
// Stream adapters may flatten SDK errors into text before they reach the loop.
func IsProviderRequestRejected(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return rejectedRequestStatus.MatchString(text) ||
		strings.Contains(text, "unsupported_value") ||
		strings.Contains(text, "unsupported_parameter") ||
		strings.Contains(text, "invalid_request_error")
}

// ProviderFailure is an application explanation, not a generated answer.
// Keep the provider's diagnostic in Details and the continuation choices in
// plain language so the same failure can be shown live and after a reload.
func ProviderFailure(err error, model, messageID string) ChatError {
	code := ErrorCodeProviderError
	if err != nil {
		code = ClassifyError(err)
	}
	message := "The provider stopped before completing this response. Your request and any partial output are saved. You can retry or choose another model."
	if code == ErrorCodeRateLimit {
		message = "The provider is temporarily limiting requests. Your request and any partial output are saved. Wait before retrying, or choose another model."
	}
	if IsProviderRequestRejected(err) {
		message = "The provider rejected the request settings. Your request and any partial output are saved. Choose another model, or retry after the settings have been corrected."
	}
	if model != "" {
		message = fmt.Sprintf("%s could not complete this request. ", model) + message
	}
	raw := ""
	if err != nil {
		raw = err.Error()
	}
	return NewChatError(code, message, map[string]interface{}{
		"raw": raw, "model": model, "message_id": messageID,
		"request_rejected": IsProviderRequestRejected(err), "source": "nanite",
	})
}
