// Package parse reads the dates, times, and repeat rules people type into
// pcal's flags (--date, --start, --repeat, ...) and turns them into what the
// Staff API takes: dates as YYYY-MM-DD, times as RFC 3339 with the calendar's
// offset, and recurrence rules as bare RRULEs the server accepts. Bad input
// is a usage error (exit 1) that lists the accepted forms.
package parse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	// The calendar's zone must load everywhere: Windows has no zoneinfo for
	// time.LoadLocation to read, and a -trimpath release binary has no
	// GOROOT zoneinfo.zip to fall back to (time/zoneinfo_read.go#loadLocation
	// tries the embedded copy before GOROOT's).
	_ "time/tzdata"

	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// Location loads the calendar's time zone, named as /my/identity's
// time_zone names it (e.g. "America/Chicago").
func Location(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q", name)
	}
	return loc, nil
}

// weekdays maps the day names pcal accepts, short and long, to days.
var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "sunday": time.Sunday,
	"mon": time.Monday, "monday": time.Monday,
	"tue": time.Tuesday, "tuesday": time.Tuesday,
	"wed": time.Wednesday, "wednesday": time.Wednesday,
	"thu": time.Thursday, "thursday": time.Thursday,
	"fri": time.Friday, "friday": time.Friday,
	"sat": time.Saturday, "saturday": time.Saturday,
}

// dateHint lists the dates Date accepts.
const dateHint = "Use YYYY-MM-DD, today, tomorrow, a weekday (sat or saturday), or next and a weekday (next sun)."

// Date parses a date, case-insensitively: YYYY-MM-DD, "today",
// "tomorrow", a weekday ("sat", "saturday"), which is its next occurrence
// with today included, or "next" and a weekday, which is a week after
// that ("next sun" is strictly after this week's). Today is now's date in
// now's location, which should be the calendar's. The result is midnight
// of that date in now's location; days are counted on the calendar, not
// as 24 hours, so a DST change doesn't shift them.
func Date(s string, now time.Time) (time.Time, error) {
	words := strings.Fields(strings.ToLower(s))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	switch {
	case len(words) == 1 && words[0] == "today":
		return today, nil
	case len(words) == 1 && words[0] == "tomorrow":
		return today.AddDate(0, 0, 1), nil
	case len(words) == 1:
		if day, ok := weekdays[words[0]]; ok {
			return today.AddDate(0, 0, daysUntil(today.Weekday(), day)), nil
		}
		if d, err := time.ParseInLocation(time.DateOnly, words[0], now.Location()); err == nil {
			return d, nil
		}
	case len(words) == 2 && words[0] == "next":
		if day, ok := weekdays[words[1]]; ok {
			return today.AddDate(0, 0, daysUntil(today.Weekday(), day)+7), nil
		}
	}
	return time.Time{}, &output.Error{Code: output.CodeUsage, Message: fmt.Sprintf("invalid date %q", s), Hint: dateHint}
}

// daysUntil returns how many days from from to the next to, 0 if they're
// the same day.
func daysUntil(from, to time.Weekday) int {
	return (int(to) - int(from) + 7) % 7
}

// Clock is a time of day on a 24-hour clock.
type Clock struct {
	Hour   int
	Minute int
}

// String writes c the way people type it: "10am", "6:30pm", "12am" for
// midnight, "12pm" for noon.
func (c Clock) String() string {
	suffix := "am"
	if c.Hour >= 12 {
		suffix = "pm"
	}
	hour := c.Hour % 12
	if hour == 0 {
		hour = 12
	}
	if c.Minute == 0 {
		return fmt.Sprintf("%d%s", hour, suffix)
	}
	return fmt.Sprintf("%d:%02d%s", hour, c.Minute, suffix)
}

// The times Time accepts: a 12-hour clock with am or pm (minutes
// optional), or a 24-hour clock with minutes.
var (
	twelveHour     = regexp.MustCompile(`\A(\d{1,2})(?::(\d{2}))?\s*(am|pm)\z`)
	twentyFourHour = regexp.MustCompile(`\A(\d{1,2}):(\d{2})\z`)
)

// timeHint lists the times Time accepts.
const timeHint = "Use a time such as 10am, 10:30am, 6:30pm, 18:30, or noon."

// Time parses a time of day, case-insensitively: "10am", "10:30am",
// "6:30 pm", "18:30", or "noon". 12am is midnight and 12pm is noon. A bare
// hour ("10") is refused, since it could be either.
func Time(s string) (Clock, error) {
	in := strings.ToLower(strings.TrimSpace(s))
	if in == "noon" {
		return Clock{Hour: 12}, nil
	}
	if m := twelveHour.FindStringSubmatch(in); m != nil {
		hour, _ := strconv.Atoi(m[1])
		minute := 0
		if m[2] != "" {
			minute, _ = strconv.Atoi(m[2])
		}
		if hour >= 1 && hour <= 12 && minute <= 59 {
			hour %= 12
			if m[3] == "pm" {
				hour += 12
			}
			return Clock{Hour: hour, Minute: minute}, nil
		}
	}
	if m := twentyFourHour.FindStringSubmatch(in); m != nil {
		hour, _ := strconv.Atoi(m[1])
		minute, _ := strconv.Atoi(m[2])
		if hour <= 23 && minute <= 59 {
			return Clock{Hour: hour, Minute: minute}, nil
		}
	}
	return Clock{}, &output.Error{Code: output.CodeUsage, Message: fmt.Sprintf("invalid time %q", s), Hint: timeHint}
}

// At returns date's day at c, in date's location (the calendar's). A time
// that happens twice that day, in the hour the clocks go back, is the first
// one (Go's choice, pinned by a test). A time the clocks skip, in the hour
// they go forward, doesn't exist: Go would quietly move 2:30am to 1:30am,
// so it's a usage error instead (Jeremy's Gate 3 decision).
func At(date time.Time, c Clock) (time.Time, error) {
	t := time.Date(date.Year(), date.Month(), date.Day(), c.Hour, c.Minute, 0, 0, date.Location())
	if t.Hour() != c.Hour || t.Minute() != c.Minute || t.Day() != date.Day() {
		return time.Time{}, &output.Error{
			Code: output.CodeUsage,
			Message: fmt.Sprintf("%s doesn't exist on %s in %s: the clocks skip that hour for daylight saving time",
				c, date.Format(time.DateOnly), date.Location()),
			Hint: fmt.Sprintf("Pick a time before %s or from %s on.", Clock{Hour: c.Hour}, Clock{Hour: (c.Hour + 1) % 24}),
		}
	}
	return t, nil
}

// Timestamp writes t as the API takes it: RFC 3339 with the offset in
// force at t, e.g. "2026-10-31T18:00:00-05:00". The server would read a
// time without an offset as Chicago time, but pcal always sends one.
func Timestamp(t time.Time) string {
	return t.Format(time.RFC3339)
}

// byday maps day names to RRULE day codes.
var byday = map[time.Weekday]string{
	time.Sunday: "SU", time.Monday: "MO", time.Tuesday: "TU", time.Wednesday: "WE",
	time.Thursday: "TH", time.Friday: "FR", time.Saturday: "SA",
}

// frequencies maps the words for a repeat's unit to its FREQ.
var frequencies = map[string]string{
	"daily": "DAILY", "day": "DAILY", "days": "DAILY",
	"weekly": "WEEKLY", "week": "WEEKLY", "weeks": "WEEKLY",
	"monthly": "MONTHLY", "month": "MONTHLY", "months": "MONTHLY",
	"yearly": "YEARLY", "year": "YEARLY", "years": "YEARLY",
}

// positions maps a monthly repeat's positions to BYDAY's numbers.
var positions = map[string]int{
	"1st": 1, "first": 1, "2nd": 2, "second": 2, "3rd": 3, "third": 3,
	"4th": 4, "fourth": 4, "5th": 5, "fifth": 5, "last": -1,
}

// repeatHint lists the --repeat forms.
const repeatHint = "Use daily, weekly, monthly, yearly, or every N days, weeks, months, or years " +
	"(every other week), with days for weekly (weekly on sun, every 2 weeks on tue,thu) or " +
	"a position for monthly (monthly on the 1st sun, monthly on the last fri). " +
	"For anything else, or a series that ends, use --rrule, e.g. FREQ=WEEKLY;BYDAY=SU;UNTIL=20271231."

// Repeat turns a --repeat phrase into a bare RRULE the server accepts
// (docs/api.md → Recurrence rules), case-insensitively:
//
//	daily | weekly | monthly | yearly
//	every [N | other] day(s) | week(s) | month(s) | year(s)
//
// A weekly one may add "on" and days ("on tue,thu", "on tuesday and
// thursday"), BYDAY; a monthly one may add "on [the]" and a position and a
// day ("on the 1st sun", "on the last fri"), BYDAY=1SU or -1FR. The
// server allows BYDAY with nothing else. There's no end: a series that
// stops needs --rrule with UNTIL or COUNT.
func Repeat(s string) (string, error) {
	fail := &output.Error{Code: output.CodeUsage, Message: fmt.Sprintf("--repeat %q isn't a form pcal knows", s), Hint: repeatHint}
	words := strings.Fields(strings.ToLower(strings.ReplaceAll(s, ",", " ")))
	if len(words) == 0 {
		return "", fail
	}

	interval := 1
	every := words[0] == "every"
	var unit string
	switch {
	case !every:
		// daily, weekly, monthly, yearly
		unit, words = words[0], words[1:]
	case len(words) >= 3 && words[1] == "other":
		interval, unit, words = 2, words[2], words[3:]
	case len(words) >= 3 && isNumber(words[1]):
		n, err := strconv.Atoi(words[1])
		if err != nil || n < 1 {
			return "", fail
		}
		interval, unit, words = n, words[2], words[3:]
	case len(words) >= 2:
		unit, words = words[1], words[2:]
	default:
		return "", fail
	}
	// "weekly" stands alone; "week" and "weeks" follow "every".
	freq, ok := frequencies[unit]
	if !ok || every == strings.HasSuffix(unit, "ly") {
		return "", fail
	}

	rule := "FREQ=" + freq
	if interval > 1 {
		rule += ";INTERVAL=" + strconv.Itoa(interval)
	}
	if len(words) == 0 {
		return rule, nil
	}
	if words[0] != "on" {
		return "", fail
	}

	var days string
	switch freq {
	case "WEEKLY":
		days = weeklyDays(words[1:])
	case "MONTHLY":
		days = monthlyDay(words[1:])
	}
	if days == "" {
		return "", fail
	}
	return rule + ";BYDAY=" + days, nil
}

// isNumber reports whether word is all digits, so a huge one is refused
// rather than read as a unit.
func isNumber(word string) bool {
	return strings.Trim(word, "0123456789") == ""
}

// weeklyDays returns weekly BYDAY for days, which are day names with
// "and" allowed between them ("tue thu", "tuesday and thursday"), in the
// order given, each once, or "" if any isn't a day.
func weeklyDays(words []string) string {
	var codes []string
	seen := map[string]bool{}
	for _, word := range words {
		if word == "and" {
			continue
		}
		day, ok := weekdays[word]
		if !ok {
			return ""
		}
		if code := byday[day]; !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	return strings.Join(codes, ",")
}

// monthlyDay returns monthly BYDAY for "[the] <position> <day>" ("1SU",
// "-1FR"), or "" if words aren't that.
func monthlyDay(words []string) string {
	if len(words) > 0 && words[0] == "the" {
		words = words[1:]
	}
	if len(words) != 2 {
		return ""
	}
	position, ok := positions[words[0]]
	day, isDay := weekdays[words[1]]
	if !ok || !isDay {
		return ""
	}
	return strconv.Itoa(position) + byday[day]
}
