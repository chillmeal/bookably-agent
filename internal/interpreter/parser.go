package interpreter

import (
	"encoding/json"
	"regexp"
	"strings"
)

var fencedJSONRegex = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")

// ParseActionPlan parses model output into ActionPlan.
// Invalid/unexpected payloads are treated as unknown intent with no hard error.
func ParseActionPlan(raw string) (*ActionPlan, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return unknownActionPlan(), nil
	}

	for _, candidate := range parseCandidates(trimmed) {
		plan := ActionPlan{}
		if err := json.Unmarshal([]byte(candidate), &plan); err != nil {
			continue
		}
		normalizeActionPlan(&plan)
		return &plan, nil
	}

	return unknownActionPlan(), nil
}

func parseCandidates(raw string) []string {
	candidates := make([]string, 0, 4)
	seen := make(map[string]struct{})
	appendCandidate := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		candidates = append(candidates, s)
	}

	appendCandidate(raw)

	fenced := fencedJSONRegex.FindAllStringSubmatch(raw, -1)
	for _, match := range fenced {
		if len(match) > 1 {
			appendCandidate(match[1])
		}
	}

	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			appendCandidate(raw[start : end+1])
		}
	}

	return candidates
}

func normalizeActionPlan(plan *ActionPlan) {
	plan.Intent = normalizeIntent(string(plan.Intent))
	plan.RequiresConfirm = plan.Intent.RequiresConfirm()

	if plan.Confidence < 0.50 {
		*plan = *unknownActionPlan()
	}
}

func unknownActionPlan() *ActionPlan {
	return &ActionPlan{
		Intent:          IntentUnknown,
		Confidence:      0,
		RequiresConfirm: false,
		Params:          ActionParams{},
		Clarifications:  nil,
	}
}
