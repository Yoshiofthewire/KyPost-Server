package rules

import (
	"fmt"
	"strconv"
	"strings"
)

// allowedCapabilities is the fixed set of "require" capability strings
// ParseRuleText (Task 4) accepts. Anything else fails loud (with a line
// number) — llama-labels' Sieve subset is hand-rolled precisely so
// unsupported capabilities are never silently ignored.
var allowedCapabilities = map[string]bool{
	"fileinto":   true, // fileinto (RFC 5228 section 4.1)
	"body":       true, // body test (RFC 5173)
	"regex":      true, // :regex match comparator (RFC 3894)
	"imap4flags": true, // addflag/removeflag/hasflag (RFC 5232)
	"llamalabs":  true, // invented markread/archive/markspam actions
}

// CompileRule renders one Rule as a Sieve-subset script: an optional
// require statement followed by exactly one "if <test> { <actions> }"
// block. Rule metadata (Name/Enabled/Order/Scope) is GUI-only and never
// appears in the script.
func CompileRule(r Rule) (string, error) {
	var caps []string
	if usesCapability(r, "fileinto") {
		caps = append(caps, "fileinto")
	}
	if usesCapability(r, "body") {
		caps = append(caps, "body")
	}
	if usesCapability(r, "regex") {
		caps = append(caps, "regex")
	}
	if usesCapability(r, "imap4flags") {
		caps = append(caps, "imap4flags")
	}
	if usesCapability(r, "llamalabs") {
		caps = append(caps, "llamalabs")
	}

	var sb strings.Builder
	if len(caps) > 0 {
		quoted := make([]string, len(caps))
		for i, c := range caps {
			quoted[i] = strconv.Quote(c)
		}
		sb.WriteString("require [" + strings.Join(quoted, ", ") + "];\n\n")
	}

	testSrc, err := compileMatchGroup(r.Match)
	if err != nil {
		return "", err
	}
	sb.WriteString("if " + testSrc + " {\n")
	if len(r.Actions) == 0 {
		sb.WriteString("    keep;\n")
	}
	for _, a := range r.Actions {
		line, err := compileAction(a)
		if err != nil {
			return "", err
		}
		sb.WriteString("    " + line + "\n")
	}
	sb.WriteString("}\n")
	return sb.String(), nil
}

func usesCapability(r Rule, capability string) bool {
	switch capability {
	case "fileinto":
		for _, a := range r.Actions {
			if a.Type == "move" {
				return true
			}
		}
		return false
	case "imap4flags":
		for _, a := range r.Actions {
			if a.Type == "keyword" || a.Type == "unkeyword" {
				return true
			}
		}
		return matchGroupUsesField(r.Match, "keyword")
	case "llamalabs":
		for _, a := range r.Actions {
			if a.Type == "read" || a.Type == "archive" || a.Type == "spam" {
				return true
			}
		}
		return false
	case "body":
		return matchGroupUsesField(r.Match, "body")
	case "regex":
		return matchGroupUsesComparator(r.Match, "regex")
	default:
		return false
	}
}

func matchGroupUsesField(g MatchGroup, field string) bool {
	for _, c := range g.Conditions {
		if c.Group != nil {
			if matchGroupUsesField(*c.Group, field) {
				return true
			}
			continue
		}
		if strings.EqualFold(c.Field, field) {
			return true
		}
	}
	return false
}

func matchGroupUsesComparator(g MatchGroup, comparator string) bool {
	for _, c := range g.Conditions {
		if c.Group != nil {
			if matchGroupUsesComparator(*c.Group, comparator) {
				return true
			}
			continue
		}
		if c.Comparator == comparator {
			return true
		}
	}
	return false
}

func compileMatchGroup(g MatchGroup) (string, error) {
	op := strings.ToLower(strings.TrimSpace(g.Op))
	if op != "anyof" && op != "allof" {
		op = "allof"
	}
	parts := make([]string, 0, len(g.Conditions))
	for _, c := range g.Conditions {
		part, err := compileCondition(c)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	return op + "(" + strings.Join(parts, ", ") + ")", nil
}

func compileCondition(c Condition) (string, error) {
	var inner string
	var err error
	if c.Group != nil {
		inner, err = compileMatchGroup(*c.Group)
	} else {
		inner, err = compileLeafCondition(c)
	}
	if err != nil {
		return "", err
	}
	if c.Negate {
		return "not " + inner, nil
	}
	return inner, nil
}

func compileLeafCondition(c Condition) (string, error) {
	field := strings.ToLower(strings.TrimSpace(c.Field))
	switch field {
	case "from", "to", "cc", "bcc", "subject":
		if strings.EqualFold(c.Comparator, "exists") {
			return fmt.Sprintf("exists [%s]", strconv.Quote(field)), nil
		}
		return fmt.Sprintf("header :%s [%s] %s", comparatorTag(c.Comparator), strconv.Quote(field), strconv.Quote(c.Value)), nil
	case "body":
		return fmt.Sprintf("body :%s %s", comparatorTag(c.Comparator), strconv.Quote(c.Value)), nil
	case "keyword":
		return fmt.Sprintf("hasflag :%s %s", comparatorTag(c.Comparator), strconv.Quote(c.Value)), nil
	default:
		return "", fmt.Errorf("unsupported condition field %q", c.Field)
	}
}

func comparatorTag(comparator string) string {
	switch strings.ToLower(strings.TrimSpace(comparator)) {
	case "contains", "is", "matches", "regex":
		return strings.ToLower(comparator)
	default:
		return "is"
	}
}

func compileAction(a Action) (string, error) {
	switch a.Type {
	case "keyword":
		return fmt.Sprintf("addflag %s;", strconv.Quote(a.Value)), nil
	case "unkeyword":
		return fmt.Sprintf("removeflag %s;", strconv.Quote(a.Value)), nil
	case "move":
		return fmt.Sprintf("fileinto %s;", strconv.Quote(a.Value)), nil
	case "read":
		return "markread;", nil
	case "archive":
		return "archive;", nil
	case "spam":
		return "markspam;", nil
	case "delete":
		return "discard;", nil
	case "stop":
		return "stop;", nil
	default:
		return "", fmt.Errorf("unsupported action type %q", a.Type)
	}
}
