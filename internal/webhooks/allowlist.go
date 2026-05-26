package webhooks

import (
	"fmt"
	"regexp"
)

type CallbackAllowlist struct {
	patterns []*regexp.Regexp
}

func NewCallbackAllowlist(patterns []string) (*CallbackAllowlist, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid callback allowlist pattern %q: %w", p, err)
		}
		compiled = append(compiled, re)
	}
	return &CallbackAllowlist{patterns: compiled}, nil
}

func (a *CallbackAllowlist) Allows(callbackURL string) bool {
	for _, re := range a.patterns {
		if re.MatchString(callbackURL) {
			return true
		}
	}
	return false
}
