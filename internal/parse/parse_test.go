package parse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// chicago is the calendar's time zone (identity.json's time_zone).
func chicago(t *testing.T) *time.Location {
	t.Helper()
	loc, err := Location("America/Chicago")
	require.NoError(t, err)
	return loc
}

// day returns midnight on y-m-d in loc.
func day(loc *time.Location, y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// requireUsage asserts err is a usage error (exit 1) with message and hint.
func requireUsage(t *testing.T, err error, message, hint string) {
	t.Helper()
	var e *output.Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, output.CodeUsage, e.Code)
	require.Equal(t, message, e.Message)
	require.Equal(t, hint, e.Hint)
}

func TestDate(t *testing.T) {
	loc := chicago(t)
	// Wednesday, 2026-10-28, mid-afternoon.
	wednesday := time.Date(2026, 10, 28, 15, 4, 0, 0, loc)

	tests := []struct {
		in   string
		now  time.Time
		want time.Time
	}{
		{"2026-10-04", wednesday, day(loc, 2026, 10, 4)},
		{" 2027-01-31 ", wednesday, day(loc, 2027, 1, 31)},
		{"today", wednesday, day(loc, 2026, 10, 28)},
		{"Today", wednesday, day(loc, 2026, 10, 28)},
		{"tomorrow", wednesday, day(loc, 2026, 10, 29)},
		// A bare weekday is its next occurrence, today included.
		{"wed", wednesday, day(loc, 2026, 10, 28)},
		{"thu", wednesday, day(loc, 2026, 10, 29)},
		{"sat", wednesday, day(loc, 2026, 10, 31)},
		{"saturday", wednesday, day(loc, 2026, 10, 31)},
		{"SUN", wednesday, day(loc, 2026, 11, 1)},
		{"tuesday", wednesday, day(loc, 2026, 11, 3)},
		// next <weekday> is strictly after this week's: a week after the
		// bare weekday.
		{"next sun", wednesday, day(loc, 2026, 11, 8)},
		{"next wed", wednesday, day(loc, 2026, 11, 4)},
		{"next  Thursday", wednesday, day(loc, 2026, 11, 5)},
		// Days are calendar days, not 24 hours: 2026-11-01 lasts 25 hours
		// in Chicago (DST ends), so midnight plus 24 hours is still Nov 1.
		{"tomorrow", time.Date(2026, 11, 1, 0, 30, 0, 0, loc), day(loc, 2026, 11, 2)},
		{"tomorrow", time.Date(2026, 10, 31, 23, 30, 0, 0, loc), day(loc, 2026, 11, 1)},
		{"mon", time.Date(2026, 11, 1, 12, 0, 0, 0, loc), day(loc, 2026, 11, 2)},
		{"next mon", time.Date(2026, 10, 31, 12, 0, 0, 0, loc), day(loc, 2026, 11, 9)},
		// And in spring, when a day lasts 23 hours.
		{"tomorrow", time.Date(2027, 3, 13, 23, 30, 0, 0, loc), day(loc, 2027, 3, 14)},
		{"next sat", time.Date(2027, 3, 13, 9, 0, 0, 0, loc), day(loc, 2027, 3, 20)},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s on %s", tt.in, tt.now.Format("Mon Jan 2")), func(t *testing.T) {
			got, err := Date(tt.in, tt.now)

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, loc, got.Location())
		})
	}
}

func TestDateErrors(t *testing.T) {
	now := time.Date(2026, 10, 28, 15, 4, 0, 0, chicago(t))
	const hint = "Use YYYY-MM-DD, today, tomorrow, a weekday (sat or saturday), or next and a weekday (next sun)."

	for _, in := range []string{"", "frobday", "2026-02-30", "2026-13-01", "10/04/2026", "next", "next week", "next 2026-10-04", "sat sun", "yesterday"} {
		t.Run(in, func(t *testing.T) {
			_, err := Date(in, now)

			requireUsage(t, err, fmt.Sprintf("invalid date %q", in), hint)
		})
	}
}

func TestTime(t *testing.T) {
	tests := []struct {
		in   string
		want Clock
	}{
		{"10am", Clock{10, 0}},
		{"10:30am", Clock{10, 30}},
		{"6:30pm", Clock{18, 30}},
		{"6:30 PM", Clock{18, 30}},
		{"6 pm", Clock{18, 0}},
		{"18:30", Clock{18, 30}},
		{"08:05", Clock{8, 5}},
		{"0:00", Clock{0, 0}},
		{"23:59", Clock{23, 59}},
		{"noon", Clock{12, 0}},
		{" Noon ", Clock{12, 0}},
		// 12am is midnight and 12pm is noon.
		{"12am", Clock{0, 0}},
		{"12:15am", Clock{0, 15}},
		{"12pm", Clock{12, 0}},
		{"12:45pm", Clock{12, 45}},
		{"1am", Clock{1, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Time(tt.in)

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTimeErrors(t *testing.T) {
	const hint = "Use a time such as 10am, 10:30am, 6:30pm, 18:30, or noon."

	for _, in := range []string{"", "10", "1030", "13pm", "0am", "10:60am", "24:00", "18:5", "10:30:00", "half past ten", "10a", "-1:00", "10:30amish"} {
		t.Run(in, func(t *testing.T) {
			_, err := Time(in)

			requireUsage(t, err, fmt.Sprintf("invalid time %q", in), hint)
		})
	}
}

func TestClockString(t *testing.T) {
	require.Equal(t, "10am", Clock{10, 0}.String())
	require.Equal(t, "6:30pm", Clock{18, 30}.String())
	require.Equal(t, "12am", Clock{0, 0}.String())
	require.Equal(t, "12:15am", Clock{0, 15}.String())
	require.Equal(t, "12pm", Clock{12, 0}.String())
	require.Equal(t, "2:30am", Clock{2, 30}.String())
}

// At puts a time on a date in the date's zone, and Timestamp writes it as
// RFC 3339 with the offset in force that day. The server would read a time
// without an offset as Chicago time, but pcal always sends one. DST ends on
// 2026-11-01, so 6pm on Oct 31 is -05:00 and on Nov 2 is -06:00 (Go 1.27.3).
func TestAtAndTimestamp(t *testing.T) {
	loc := chicago(t)
	tests := []struct {
		date  time.Time
		clock Clock
		want  string
	}{
		{day(loc, 2026, 10, 31), Clock{18, 0}, "2026-10-31T18:00:00-05:00"},
		{day(loc, 2026, 11, 2), Clock{18, 0}, "2026-11-02T18:00:00-06:00"},
		{day(loc, 2026, 10, 4), Clock{10, 30}, "2026-10-04T10:30:00-05:00"},
		{day(loc, 2027, 1, 10), Clock{0, 0}, "2027-01-10T00:00:00-06:00"},
		// 1:30am happens twice on 2026-11-01; it's the first one, still
		// daylight time (Jeremy's Gate 3 decision; what Go picks).
		{day(loc, 2026, 11, 1), Clock{1, 30}, "2026-11-01T01:30:00-05:00"},
		{day(loc, 2026, 11, 1), Clock{2, 0}, "2026-11-01T02:00:00-06:00"},
		// The hours around the spring gap exist.
		{day(loc, 2027, 3, 14), Clock{1, 59}, "2027-03-14T01:59:00-06:00"},
		{day(loc, 2027, 3, 14), Clock{3, 0}, "2027-03-14T03:00:00-05:00"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got, err := At(tt.date, tt.clock)

			require.NoError(t, err)
			require.Equal(t, tt.want, Timestamp(got))
		})
	}
}

// A time the clocks skip doesn't exist; Go would quietly turn 2:30am into
// 1:30am, an hour early, so it's an error (Jeremy's Gate 3 decision).
func TestAtRejectsATimeThatDoesntExist(t *testing.T) {
	loc := chicago(t)

	for _, c := range []Clock{{2, 0}, {2, 30}, {2, 59}} {
		t.Run(c.String(), func(t *testing.T) {
			_, err := At(day(loc, 2027, 3, 14), c)

			requireUsage(t, err,
				c.String()+" doesn't exist on 2027-03-14 in America/Chicago: the clocks skip that hour for daylight saving time",
				"Pick a time before 2am or from 3am on.")
		})
	}
}

func TestLocation(t *testing.T) {
	loc, err := Location("America/Chicago")
	require.NoError(t, err)
	require.Equal(t, "America/Chicago", loc.String())

	_, err = Location("Mars/Olympus_Mons")
	require.ErrorContains(t, err, `unknown time zone "Mars/Olympus_Mons"`)
}

// --repeat: each phrase and the RRULE it becomes.
func TestRepeat(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		// The plan's forms.
		{"daily", "FREQ=DAILY"},
		{"weekly on sun", "FREQ=WEEKLY;BYDAY=SU"},
		{"every 2 weeks on tue,thu", "FREQ=WEEKLY;INTERVAL=2;BYDAY=TU,TH"},
		{"monthly on the 1st sun", "FREQ=MONTHLY;BYDAY=1SU"},
		{"monthly on the last fri", "FREQ=MONTHLY;BYDAY=-1FR"},
		{"yearly", "FREQ=YEARLY"},
		// The small grammar around them (Jeremy's Gate 3 decision).
		{"weekly", "FREQ=WEEKLY"},
		{"monthly", "FREQ=MONTHLY"},
		{"every day", "FREQ=DAILY"},
		{"every week", "FREQ=WEEKLY"},
		{"every month", "FREQ=MONTHLY"},
		{"every year", "FREQ=YEARLY"},
		{"every 3 days", "FREQ=DAILY;INTERVAL=3"},
		{"every 1 week", "FREQ=WEEKLY"},
		{"every 2 weeks", "FREQ=WEEKLY;INTERVAL=2"},
		{"every 6 months", "FREQ=MONTHLY;INTERVAL=6"},
		{"every 2 years", "FREQ=YEARLY;INTERVAL=2"},
		{"every other day", "FREQ=DAILY;INTERVAL=2"},
		{"every other week on sat", "FREQ=WEEKLY;INTERVAL=2;BYDAY=SA"},
		{"every other month on the 2nd wed", "FREQ=MONTHLY;INTERVAL=2;BYDAY=2WE"},
		{"every week on mon", "FREQ=WEEKLY;BYDAY=MO"},
		{"weekly on sunday", "FREQ=WEEKLY;BYDAY=SU"},
		{"weekly on mon,wed,fri", "FREQ=WEEKLY;BYDAY=MO,WE,FR"},
		{"weekly on mon, wed, fri", "FREQ=WEEKLY;BYDAY=MO,WE,FR"},
		{"weekly on tuesday and thursday", "FREQ=WEEKLY;BYDAY=TU,TH"},
		{"weekly on thu tue", "FREQ=WEEKLY;BYDAY=TH,TU"},
		{"weekly on sun,sun", "FREQ=WEEKLY;BYDAY=SU"},
		{"Weekly On SUN", "FREQ=WEEKLY;BYDAY=SU"},
		{"  every   2  weeks  on tue ", "FREQ=WEEKLY;INTERVAL=2;BYDAY=TU"},
		{"monthly on the first sunday", "FREQ=MONTHLY;BYDAY=1SU"},
		{"monthly on 3rd thu", "FREQ=MONTHLY;BYDAY=3TH"},
		{"monthly on the 4th sat", "FREQ=MONTHLY;BYDAY=4SA"},
		{"monthly on the fifth mon", "FREQ=MONTHLY;BYDAY=5MO"},
		{"every 3 months on the last tue", "FREQ=MONTHLY;INTERVAL=3;BYDAY=-1TU"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Repeat(tt.in)

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.NoError(t, serverAccepts(got), "the server would reject %s", got)
		})
	}
}

func TestRepeatErrors(t *testing.T) {
	const hint = "Use daily, weekly, monthly, yearly, or every N days, weeks, months, or years " +
		"(every other week), with days for weekly (weekly on sun, every 2 weeks on tue,thu) or " +
		"a position for monthly (monthly on the 1st sun, monthly on the last fri). " +
		"For anything else, or a series that ends, use --rrule, e.g. FREQ=WEEKLY;BYDAY=SU;UNTIL=20271231."

	for _, in := range []string{
		"", "fortnightly", "every", "every weekly", "every 2 daily", "every other monthly", "every 0 weeks", "every -1 weeks", "every two weeks", "every 2", "every 2 fortnights",
		// BYDAY only goes with weekly or monthly.
		"daily on mon", "yearly on sun", "every 2 days on tue",
		// Weekly takes plain days; monthly one day with a position.
		"weekly on", "weekly on the 1st sun", "weekly on funday", "monthly on sun", "monthly on the 6th sun",
		"monthly on the 1st sun,mon", "monthly on the 1st", "monthly on the", "monthly on the last day", "weekly sun", "every other",
		"every 99999999999999999999 days",
	} {
		t.Run(in, func(t *testing.T) {
			_, err := Repeat(in)

			requireUsage(t, err, fmt.Sprintf("--repeat %q isn't a form pcal knows", in), hint)
		})
	}
}

// Every phrase the grammar can make, checked against the server's rules:
// each unit, with and without an interval, with every day list shape for
// weekly and every position and day for monthly.
func TestEveryRepeatTheServerAccepts(t *testing.T) {
	days := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	positions := []string{"1st", "2nd", "3rd", "4th", "5th", "first", "second", "third", "fourth", "fifth", "last"}
	var phrases []string
	for _, lead := range []string{"", "every 1 ", "every 2 ", "every 52 ", "every other "} {
		for unit, single := range map[string]string{"day": "daily", "week": "weekly", "month": "monthly", "year": "yearly"} {
			base := single
			if lead != "" {
				base = lead + unit
				if lead != "every 1 " && lead != "every other " {
					base += "s"
				}
			}
			phrases = append(phrases, base)
			switch unit {
			case "week":
				for i := range days {
					phrases = append(phrases, base+" on "+days[i], base+" on "+strings.Join(days[:i+1], ","))
				}
			case "month":
				for _, p := range positions {
					for _, d := range days {
						phrases = append(phrases, base+" on the "+p+" "+d)
					}
				}
			}
		}
	}

	require.Greater(t, len(phrases), 300)
	for _, phrase := range phrases {
		rule, err := Repeat(phrase)
		require.NoError(t, err, phrase)
		require.NoError(t, serverAccepts(rule), "%q → %s", phrase, rule)
		require.NotContains(t, rule, "COUNT", "--repeat has no end")
		require.NotContains(t, rule, "UNTIL", "--repeat has no end")
	}
}

// serverAccepts ports the server's check on a recurrence rule (app
// 85a00a0, RecurrenceExpandable#recurrence_rule_must_be_expandable and
// #validate_byday), and returns the first message it would give.
func serverAccepts(rule string) error {
	day := `(?:SU|MO|TU|WE|TH|FR|SA)`
	weeklyByday := regexp.MustCompile(`\A` + day + `(?:,` + day + `)*\z`)
	monthlyByday := regexp.MustCompile(`\A-?[1-5]` + day + `\z`)
	supported := map[string]bool{"FREQ": true, "INTERVAL": true, "BYDAY": true, "COUNT": true, "UNTIL": true}

	parts := map[string]string{}
	for _, component := range strings.Split(rule, ";") {
		pair := strings.Split(component, "=")
		if len(pair) != 2 || !supported[pair[0]] {
			return fmt.Errorf("can only have FREQ, INTERVAL, BYDAY, COUNT, and UNTIL")
		}
		if _, dup := parts[pair[0]]; dup {
			return fmt.Errorf("each part once")
		}
		parts[pair[0]] = pair[1]
	}
	switch parts["FREQ"] {
	case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
	default:
		return fmt.Errorf("must have a FREQ of DAILY, WEEKLY, MONTHLY, or YEARLY")
	}
	for _, key := range []string{"INTERVAL", "COUNT"} {
		if v, ok := parts[key]; ok {
			if n, err := strconv.Atoi(v); err != nil || n < 1 || strings.TrimLeft(v, "0123456789") != "" {
				return fmt.Errorf("must have a %s that is a positive whole number", key)
			}
		}
	}
	if byday, ok := parts["BYDAY"]; ok {
		switch parts["FREQ"] {
		case "WEEKLY":
			if !weeklyByday.MatchString(byday) {
				return fmt.Errorf("must have a weekly BYDAY of days")
			}
		case "MONTHLY":
			if !monthlyByday.MatchString(byday) {
				return fmt.Errorf("must have a monthly BYDAY of one day with a position from -5 to 5")
			}
		default:
			return fmt.Errorf("can only use BYDAY with FREQ=WEEKLY or FREQ=MONTHLY")
		}
	}
	return nil
}

// The port itself, against rules the server rejects (docs/api.md →
// Recurrence rules), so a bug in it can't hide one in Repeat.
func TestServerAcceptsRejectsWhatTheServerRejects(t *testing.T) {
	for _, rule := range []string{
		"RRULE:FREQ=DAILY", "FREQ=HOURLY", "FREQ=WEEKLY;INTERVAL=0", "FREQ=WEEKLY;INTERVAL=x", "FREQ=WEEKLY;FREQ=DAILY",
		"FREQ=DAILY;BYDAY=MO", "FREQ=YEARLY;BYDAY=SU", "FREQ=WEEKLY;BYDAY=1SU", "FREQ=MONTHLY;BYDAY=SU",
		"FREQ=MONTHLY;BYDAY=6SU", "FREQ=MONTHLY;BYDAY=1SU,2MO", "FREQ=WEEKLY;BYMONTH=1", "FREQ=WEEKLY;BYDAY",
	} {
		require.Error(t, serverAccepts(rule), rule)
	}
	require.NoError(t, serverAccepts("FREQ=WEEKLY;BYDAY=SU;UNTIL=20271231"))
}
