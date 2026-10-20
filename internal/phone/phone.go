// Package phone holds the policy shared by every server that hands messages to
// termux-sms-send (mcp-sms, and mcp-termux's termux_sms_send): recipient
// validation and the default send rate limit, so both enforce the same rules,
// tested in one place.
package phone

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kahz12/droidmcp/internal/config"
)

// SendLimit is the built-in rate limit on the tools that send SMS. Messages are
// billable and cannot be recalled, so a runaway or prompt-injected agent is
// capped even when the operator configured nothing;
// DROIDMCP_<SERVER>_RATE_LIMITS overrides it or switches it off.
var SendLimit = config.RateLimit{Calls: 10, Per: time.Hour}

// numberRe accepts a bare phone number: an optional leading + and at least
// three digits, nothing else. Rejecting spaces and punctuation keeps a
// recipient from being split by termux-sms-send's unquoted expansion and blocks
// any attempt to smuggle option-like tokens into the recipient list.
var numberRe = regexp.MustCompile(`^\+?[0-9]{3,}$`)

// ParseRecipients splits a comma-separated recipient string, validates each as
// a phone number, and returns the cleaned comma-joined form plus the slice.
func ParseRecipients(raw string) (string, []string, error) {
	var nums []string
	for _, p := range strings.Split(raw, ",") {
		n := strings.TrimSpace(p)
		if n == "" {
			continue
		}
		if !numberRe.MatchString(n) {
			return "", nil, fmt.Errorf("invalid recipient number %q: expected digits with an optional leading +, comma-separated", n)
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return "", nil, errors.New("`number` must contain at least one recipient")
	}
	return strings.Join(nums, ","), nums, nil
}
