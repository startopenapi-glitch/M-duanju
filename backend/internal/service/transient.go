package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

type UpstreamHTTPError struct {
	Status  int
	Message string
}

func (e *UpstreamHTTPError) Error() string {
	return fmt.Sprintf("力萌 HTTP %d: %s", e.Status, e.Message)
}
func IsTransientError(err error) bool {
	var h *UpstreamHTTPError
	if errors.As(err, &h) {
		return h.Status == 429 || h.Status >= 500 && h.Status <= 599
	}
	var n net.Error
	if errors.As(err, &n) {
		return n.Timeout() || n.Temporary()
	}
	return errors.Is(err, context.DeadlineExceeded) || err != nil && IsTransientUpstream(err.Error())
}

var transientWords = regexp.MustCompile(`(?i)\b(oom|eof|429|500|502|503|504)\b`)

func IsTransientUpstream(msg string) bool {
	l := strings.ToLower(msg)
	if transientWords.MatchString(l) {
		return true
	}
	for _, k := range []string{"out of memory", "timeout", "timed out", "deadline exceeded", "connection reset", "connection refused", "no such host", "rate limit", "too many requests", "overload", "internal server error", "service unavailable", "bad gateway", "temporarily unavailable", "排队", "超时", "繁忙", "限流", "抖动"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}
