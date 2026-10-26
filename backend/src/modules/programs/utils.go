package programs

import (
	"strings"
	"time"
)

func FilterMap[T any](ss []T, test func(T) bool) (ret []T) {
	for _, s := range ss {
		if test(s) {
			ret = append(ret, s)
		}
	}
	return
}

func IterMap[T any, E any](fun func(T) E, arr []T) []E {
	applied := []E{}
	for _, item := range arr {
		applied = append(applied, fun(item))
	}
	return applied
}

func GetUntilDateFromRule(rRule string) string {
	for _, rRulePart := range strings.Split(rRule, ";") {
		rRule, hasUntil := strings.CutPrefix(rRulePart, "UNTIL=")
		if hasUntil {
			return rRule
		}
	}
	return ""
}

func ReplaceOrAddUntilDate(rRule, untilDate string) string {
	rRuleParts := strings.Split(rRule, ";")
	untilExists := false
	for i, part := range rRuleParts {
		if strings.HasPrefix(part, "UNTIL=") {
			rRuleParts[i] = "UNTIL=" + untilDate
			untilExists = true
			break
		}
	}
	if !untilExists {
		rRuleParts = append(rRuleParts, "UNTIL="+untilDate)
	}
	return strings.Join(rRuleParts, ";")
}

func FormatDateForUntil(t time.Time) string {
	endOfDay := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.UTC)
	return endOfDay.Format("20060102T150405Z")
}
