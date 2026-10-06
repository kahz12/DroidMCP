package main

import (
	"testing"
	"time"
)

func TestParseCronRejects(t *testing.T) {
	for _, expr := range []string{
		"", "* * * *", "* * * * * *", "@reboot",
		"60 * * * *", "* 24 * * *", "* * 0 * *", "* * 32 * *", "* * * 13 *", "* * * * 8",
		"5-1 * * * *", "*/0 * * * *", "*/x * * * *", "a * * * *", "1,,2 * * * *", "* * * foo *",
	} {
		if _, err := parseCron(expr); err == nil {
			t.Errorf("parseCron(%q) accepted", expr)
		}
	}
}

func TestCronNext(t *testing.T) {
	at := func(y int, mo time.Month, d, h, mi int) time.Time { return time.Date(y, mo, d, h, mi, 0, 0, time.UTC) }
	base := time.Date(2026, 10, 6, 10, 30, 15, 0, time.UTC) // a Tuesday
	cases := []struct {
		expr string
		want time.Time
	}{
		{"* * * * *", at(2026, 10, 6, 10, 31)},
		{"*/15 * * * *", at(2026, 10, 6, 10, 45)},
		{"0 * * * *", at(2026, 10, 6, 11, 0)},
		{"30 10 * * *", at(2026, 10, 7, 10, 30)}, // strictly after base
		{"0 8 * * mon-fri", at(2026, 10, 7, 8, 0)},
		{"0 8 * * sat,sun", at(2026, 10, 10, 8, 0)},
		{"0 0 * * 7", at(2026, 10, 11, 0, 0)}, // 7 is Sunday
		{"0 0 1 * *", at(2026, 11, 1, 0, 0)},
		{"0 0 29 2 *", at(2028, 2, 29, 0, 0)},
		{"15 14 1 jan *", at(2027, 1, 1, 14, 15)},
		{"0 0 */10 * *", at(2026, 10, 11, 0, 0)}, // 1, 11, 21, 31
		// Both day fields restricted: either one matching is enough (Friday 9th).
		{"0 0 13 * 5", at(2026, 10, 9, 0, 0)},
		{"10-20/5 9 * * *", at(2026, 10, 7, 9, 10)},
		{"@hourly", at(2026, 10, 6, 11, 0)},
		{"@daily", at(2026, 10, 7, 0, 0)},
		{"@weekly", at(2026, 10, 11, 0, 0)},
		{"@monthly", at(2026, 11, 1, 0, 0)},
		{"@yearly", at(2027, 1, 1, 0, 0)},
	}
	for _, tc := range cases {
		spec, err := parseCron(tc.expr)
		if err != nil {
			t.Errorf("parseCron(%q): %v", tc.expr, err)
			continue
		}
		if got := spec.next(base, time.UTC); !got.Equal(tc.want) {
			t.Errorf("%q: next = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestCronNextNeverFires(t *testing.T) {
	spec, err := parseCron("0 0 30 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.next(time.Now(), time.UTC); !got.IsZero() {
		t.Fatalf("30 February fired at %v", got)
	}
}

func TestCronNextUsesTimeZone(t *testing.T) {
	bogota, err := time.LoadLocation("America/Bogota") // UTC-5, no DST
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := parseCron("0 8 * * *")
	got := spec.next(time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC), bogota) // 07:00 in Bogotá
	if want := time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("08:00 Bogotá = %v, want %v", got, want)
	}
}

// Across daylight-saving changes a daily job fires once a day: a wall time
// that does not exist is skipped, and one that happens twice fires once.
func TestCronNextAcrossDaylightSaving(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := parseCron("30 2 * * *")

	// 2026-03-29 jumps from 02:00 to 03:00: there is no 02:30 that day.
	got := spec.next(time.Date(2026, 3, 28, 12, 0, 0, 0, madrid), madrid)
	if want := time.Date(2026, 3, 30, 2, 30, 0, 0, madrid); !got.Equal(want) {
		t.Errorf("spring forward: next = %v, want %v", got, want)
	}

	// 2026-10-25 repeats 02:00-03:00: 02:30 happens twice but fires once.
	first := spec.next(time.Date(2026, 10, 24, 12, 0, 0, 0, madrid), madrid)
	if first.Day() != 25 || first.Hour() != 2 || first.Minute() != 30 {
		t.Fatalf("fall back: first = %v, want 02:30 on the 25th", first)
	}
	second := spec.next(first, madrid)
	if want := time.Date(2026, 10, 26, 2, 30, 0, 0, madrid); !second.Equal(want) {
		t.Errorf("fall back: next after %v = %v, want %v", first, second, want)
	}
}

func TestParseScheduleRequiresExactlyOne(t *testing.T) {
	if _, err := parseSchedule("", 0, time.UTC); err == nil {
		t.Error("accepted no schedule")
	}
	if _, err := parseSchedule("* * * * *", time.Hour, time.UTC); err == nil {
		t.Error("accepted cron and interval together")
	}
	for _, every := range []time.Duration{30 * time.Second, maxInterval + time.Second} {
		if _, err := parseSchedule("", every, time.UTC); err == nil {
			t.Errorf("accepted interval %v", every)
		}
	}
}

// Interval tasks stay on prev + k*every: a late tick or a restart does not
// shift them, and missed slots are skipped rather than run in a burst.
func TestIntervalNextKeepsPhase(t *testing.T) {
	s, err := parseSchedule("", time.Hour, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	prev := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	for now, want := range map[time.Time]time.Time{
		prev:                        prev.Add(time.Hour),
		prev.Add(time.Second):       prev.Add(time.Hour),
		prev.Add(150 * time.Minute): prev.Add(3 * time.Hour),
	} {
		if got := s.next(now, prev); !got.Equal(want) {
			t.Errorf("next(%v) = %v, want %v", now, got, want)
		}
	}
	if got := s.next(prev, time.Time{}); !got.Equal(prev.Add(time.Hour)) {
		t.Errorf("first run = %v, want one interval after now", got)
	}
}
