package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// schedule says when a task fires: a five-field cron expression evaluated in
// the server's time zone, or a fixed interval. Cron is parsed here instead of
// pulled in as a dependency: the grammar is small, and computing the next fire
// time is the only operation the scheduler needs.
type schedule struct {
	cron  *cronSpec
	every time.Duration
	loc   *time.Location
}

// parseSchedule builds a schedule from exactly one of cronExpr or every.
func parseSchedule(cronExpr string, every time.Duration, loc *time.Location) (schedule, error) {
	switch {
	case cronExpr != "" && every != 0:
		return schedule{}, fmt.Errorf("give either cron or interval_seconds, not both")
	case cronExpr != "":
		spec, err := parseCron(cronExpr)
		if err != nil {
			return schedule{}, err
		}
		return schedule{cron: spec, loc: loc}, nil
	case every != 0:
		if every < minInterval || every > maxInterval {
			return schedule{}, fmt.Errorf("interval_seconds must be between %d and %d", int(minInterval/time.Second), int(maxInterval/time.Second))
		}
		return schedule{every: every, loc: loc}, nil
	default:
		return schedule{}, fmt.Errorf("a schedule is required: give cron or interval_seconds")
	}
}

// next returns the first fire time strictly after now. prev is the previous
// fire time of an interval schedule: fire times stay on prev + k*every, so a
// restart or a late tick does not shift the phase of the task. The zero time
// means the schedule never fires (e.g. cron "0 0 30 2 *").
func (s schedule) next(now, prev time.Time) time.Time {
	if s.cron != nil {
		return s.cron.next(now, s.loc)
	}
	if prev.IsZero() || prev.After(now) {
		return now.Add(s.every)
	}
	missed := now.Sub(prev) / s.every
	return prev.Add((missed + 1) * s.every)
}

// cronSpec holds one bitset per field. domStar/dowStar record whether the
// day-of-month and day-of-week fields started with "*": as in Vixie cron, when
// both are restricted a day matches if either one does.
type cronSpec struct {
	minute, hour, dom, month, dow uint64
	domStar, dowStar              bool
}

var cronMacros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

type cronField struct {
	name     string
	min, max int
	names    map[string]int
}

var cronFields = [5]cronField{
	{name: "minute", min: 0, max: 59},
	{name: "hour", min: 0, max: 23},
	{name: "day of month", min: 1, max: 31},
	{name: "month", min: 1, max: 12, names: map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}},
	// 7 is Sunday too, as in every common cron.
	{name: "day of week", min: 0, max: 7, names: map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}},
}

// parseCron accepts "minute hour day-of-month month day-of-week" with numbers,
// names (jan, mon), "*", ranges "a-b", steps "*/n" or "a-b/n", and lists "a,b",
// or one of the @hourly/@daily/@weekly/@monthly/@yearly macros.
func parseCron(expr string) (*cronSpec, error) {
	expr = strings.TrimSpace(expr)
	if m, ok := cronMacros[strings.ToLower(expr)]; ok {
		expr = m
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron %q must have 5 fields (minute hour day-of-month month day-of-week) or be @hourly, @daily, @weekly, @monthly or @yearly", expr)
	}
	var bits [5]uint64
	for i, f := range fields {
		b, err := cronFields[i].parse(f)
		if err != nil {
			return nil, fmt.Errorf("cron %s field %q: %w", cronFields[i].name, f, err)
		}
		bits[i] = b
	}
	if bits[4]&(1<<7) != 0 {
		bits[4] = bits[4]&^(1<<7) | 1
	}
	return &cronSpec{
		minute: bits[0], hour: bits[1], dom: bits[2], month: bits[3], dow: bits[4],
		domStar: strings.HasPrefix(fields[2], "*"),
		dowStar: strings.HasPrefix(fields[4], "*"),
	}, nil
}

func (f cronField) parse(s string) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(s, ",") {
		rng, stepText, hasStep := strings.Cut(part, "/")
		lo, hi := f.min, f.max
		switch {
		case rng == "*":
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			var err error
			if lo, err = f.value(a); err != nil {
				return 0, err
			}
			if hi, err = f.value(b); err != nil {
				return 0, err
			}
			if lo > hi {
				return 0, fmt.Errorf("range %q runs backwards", rng)
			}
		default:
			v, err := f.value(rng)
			if err != nil {
				return 0, err
			}
			lo = v
			if !hasStep {
				hi = v
			}
		}
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("step %q must be a positive number", stepText)
			}
			step = n
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

func (f cronField) value(s string) (int, error) {
	if n, ok := f.names[strings.ToLower(s)]; ok {
		return n, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < f.min || n > f.max {
		return 0, fmt.Errorf("%q is not a %s (%d-%d)", s, f.name, f.min, f.max)
	}
	return n, nil
}

// next returns the first minute strictly after t that matches, or the zero
// time if nothing matches within five years. It jumps a whole month, day or
// hour at a time when that field does not match, so it never walks minutes
// across a gap.
func (c *cronSpec) next(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	start := t
	t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute()+1, 0, 0, loc)
	limit := start.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case c.month&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		case c.hour&(1<<uint(t.Hour())) == 0:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
		case c.minute&(1<<uint(t.Minute())) == 0:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute()+1, 0, 0, loc)
		case !t.After(start):
			// A daylight-saving fall-back can map a wall time back to before
			// start; step forward in absolute time instead.
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

func (c *cronSpec) dayMatches(t time.Time) bool {
	dom := c.dom&(1<<uint(t.Day())) != 0
	dow := c.dow&(1<<uint(t.Weekday())) != 0
	if c.domStar || c.dowStar {
		return dom && dow
	}
	return dom || dow
}
