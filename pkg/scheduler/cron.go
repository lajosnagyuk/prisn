// Package scheduler handles scheduled job execution.
//
// Uses standard cron expression format (5 fields: min hour dom mon dow).
package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronExpr represents a parsed cron expression.
type CronExpr struct {
	Minutes    []int // 0-59
	Hours      []int // 0-23
	DaysOfMonth []int // 1-31
	Months     []int // 1-12
	DaysOfWeek []int // 0-6 (0 = Sunday)
	raw        string
}

// ParseCron parses a cron expression.
// Supports: *, */n, n, n-m, n,m,o
func ParseCron(expr string) (*CronExpr, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields: %s", expr)
	}

	c := &CronExpr{raw: expr}
	var err error

	c.Minutes, err = parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("invalid minute field: %w", err)
	}

	c.Hours, err = parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("invalid hour field: %w", err)
	}

	c.DaysOfMonth, err = parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("invalid day-of-month field: %w", err)
	}

	c.Months, err = parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("invalid month field: %w", err)
	}

	c.DaysOfWeek, err = parseField(fields[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("invalid day-of-week field: %w", err)
	}

	return c, nil
}

// parseField parses a single cron field.
func parseField(field string, min, max int) ([]int, error) {
	var result []int

	// Handle *
	if field == "*" {
		for i := min; i <= max; i++ {
			result = append(result, i)
		}
		return result, nil
	}

	// Handle */n
	if strings.HasPrefix(field, "*/") {
		step, err := strconv.Atoi(field[2:])
		if err != nil {
			return nil, fmt.Errorf("invalid step: %s", field)
		}
		for i := min; i <= max; i += step {
			result = append(result, i)
		}
		return result, nil
	}

	// Handle comma-separated values and ranges
	for _, part := range strings.Split(field, ",") {
		if strings.Contains(part, "-") {
			// Range: n-m
			bounds := strings.Split(part, "-")
			if len(bounds) != 2 {
				return nil, fmt.Errorf("invalid range: %s", part)
			}
			start, err := strconv.Atoi(bounds[0])
			if err != nil {
				return nil, fmt.Errorf("invalid range start: %s", bounds[0])
			}
			end, err := strconv.Atoi(bounds[1])
			if err != nil {
				return nil, fmt.Errorf("invalid range end: %s", bounds[1])
			}
			if start < min || end > max || start > end {
				return nil, fmt.Errorf("range out of bounds: %s", part)
			}
			for i := start; i <= end; i++ {
				result = append(result, i)
			}
		} else {
			// Single value
			val, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid value: %s", part)
			}
			if val < min || val > max {
				return nil, fmt.Errorf("value out of bounds: %d", val)
			}
			result = append(result, val)
		}
	}

	return result, nil
}

// NextAfter returns the next time after 'after' that matches the cron expression.
func (c *CronExpr) NextAfter(after time.Time) time.Time {
	// Start from the next minute
	t := after.Truncate(time.Minute).Add(time.Minute)

	// Search for up to 4 years
	endSearch := after.AddDate(4, 0, 0)

	for t.Before(endSearch) {
		if c.matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}

	// Should never happen with valid cron expressions
	return time.Time{}
}

// matches returns true if the time matches the cron expression.
func (c *CronExpr) matches(t time.Time) bool {
	if !contains(c.Minutes, t.Minute()) {
		return false
	}
	if !contains(c.Hours, t.Hour()) {
		return false
	}
	if !contains(c.Months, int(t.Month())) {
		return false
	}

	// Day matching: either day-of-month OR day-of-week must match
	// (this is how standard cron works)
	dayOfMonthMatch := contains(c.DaysOfMonth, t.Day())
	dayOfWeekMatch := contains(c.DaysOfWeek, int(t.Weekday()))

	// If both are restricted (not *), need either to match
	// If only one is restricted, only that one needs to match
	domRestricted := len(c.DaysOfMonth) < 31
	dowRestricted := len(c.DaysOfWeek) < 7

	if domRestricted && dowRestricted {
		return dayOfMonthMatch || dayOfWeekMatch
	}
	if domRestricted {
		return dayOfMonthMatch
	}
	if dowRestricted {
		return dayOfWeekMatch
	}
	return true
}

// contains checks if a slice contains a value.
func contains(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

// String returns the original cron expression.
func (c *CronExpr) String() string {
	return c.raw
}

// Common cron shortcuts
var shortcuts = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// ExpandShortcut expands a cron shortcut like @daily to its cron expression.
func ExpandShortcut(s string) string {
	if expanded, ok := shortcuts[s]; ok {
		return expanded
	}
	return s
}
