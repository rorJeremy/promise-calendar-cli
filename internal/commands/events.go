package commands

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/config"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
	"github.com/rorJeremy/promise-calendar-cli/internal/parse"
)

// newEventsCmd builds `pcal events`. List and show are Phase 10; later
// phases add the write commands to this group.
func newEventsCmd(a *app) *cobra.Command {
	cmd := newGroupCmd("events", "List and show events")
	cmd.AddCommand(
		newEventsListCmd(a),
		newEventsShowCmd(a),
	)
	return cmd
}

func newEventsListCmd(a *app) *cobra.Command {
	var from, to, search string
	var tags []string
	var unpublished, website bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List events",
		Long: "List occurrences between --from and --to (today through 30 days\n" +
			"from then, when omitted). A recurring event is one row per occurrence.",
		Args: unknownCommand,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Absolute dates don't need the calendar's zone, so an inverted
			// pair is a usage error before any request. "today" and weekdays
			// are checked in eventRange, after the zone is known.
			if err := rejectInvertedDates(from, to, cmd.Flags().Changed("from"), cmd.Flags().Changed("to")); err != nil {
				return err
			}
			s, err := a.open()
			if err != nil {
				return err
			}
			loc, err := a.calendarLocation(cmd, s)
			if err != nil {
				return err
			}
			start, end, err := a.eventRange(cmd, loc, from, to)
			if err != nil {
				return err
			}
			names := nonBlank(tags)
			if cmd.Flags().Changed("tag") && len(names) == 0 {
				return &output.Error{
					Code:    output.CodeUsage,
					Message: "need a tag name",
					Hint:    "Pass --tag with the tag's name. An empty --tag clears tags on update, and it doesn't filter the list.",
				}
			}
			ids, err := a.resolveTagNames(cmd, s, names)
			if err != nil {
				return err
			}
			query := eventListQuery(start, end, search, unpublished, website, ids)
			occurrences, err := a.fetchOccurrences(cmd, s, query)
			if err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    occurrences,
				Summary: eventsSummary(len(occurrences), start, end, a.now()),
				Table:   eventsTable(occurrences, loc, a.now()),
			})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "first date (`YYYY-MM-DD`, today, sat, …)")
	cmd.Flags().StringVar(&to, "to", "", "last date, inclusive")
	cmd.Flags().StringVar(&search, "search", "", "words from the title")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag `name` (repeatable; any of them)")
	cmd.Flags().BoolVar(&unpublished, "unpublished", false, "only drafts")
	cmd.Flags().BoolVar(&website, "website", false, "only events shown on the website")
	cmd.Flags().BoolVar(&a.flags.csv, "csv", false, "print the list as CSV")
	return cmd
}

func newEventsShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show ID",
		Short: "Show an event",
		Long: "Show one event. ID is its UUID, or a title: an exact title, or a\n" +
			"unique title that starts with it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.open()
			if err != nil {
				return err
			}
			loc, err := a.calendarLocation(cmd, s)
			if err != nil {
				return err
			}
			id := args[0]
			if !isUUID(id) {
				found, err := a.resolveEvent(cmd, s, loc, id)
				if err != nil {
					return err
				}
				id = found.id
			}
			var event client.Event
			if err := s.client.Get(cmd.Context(), "/events/"+url.PathEscape(id), nil, &event); err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    event,
				Summary: fmt.Sprintf("Event %s.", strconv.Quote(event.Title)),
				Card:    eventCard(event, loc, a.now()),
			})
		},
	}
}

// calendarLocation is the calendar's time zone: the cached time_zone, or
// GET /my/identity when it isn't cached (Gate 4).
func (a *app) calendarLocation(cmd *cobra.Command, s *session) (*time.Location, error) {
	if dir, err := a.configDir(); err == nil {
		if cfg, err := config.Load(dir); err == nil && cfg.TimeZone != "" {
			return parse.Location(cfg.TimeZone)
		}
	}
	id, err := a.identity(cmd.Context(), s)
	if err != nil {
		return nil, err
	}
	return parse.Location(id.TimeZone)
}

// rejectInvertedDates reports --to before --from when both flags are
// calendar dates (YYYY-MM-DD). Relative words stay for eventRange, which
// needs the calendar's time zone to know which day "today" is.
func rejectInvertedDates(from, to string, fromSet, toSet bool) error {
	if !fromSet || !toSet {
		return nil
	}
	start, err1 := time.Parse(time.DateOnly, strings.TrimSpace(from))
	end, err2 := time.Parse(time.DateOnly, strings.TrimSpace(to))
	if err1 != nil || err2 != nil {
		return nil
	}
	if end.Before(start) {
		return invertedRange()
	}
	return nil
}

func invertedRange() error {
	return &output.Error{
		Code:    output.CodeUsage,
		Message: "--to is before --from",
		Hint:    "Pass a --to on or after --from. The last day is included.",
	}
}

// eventRange is --from through --to, defaulting to today and 30 days on,
// in loc. --to before --from is a usage error before the list request.
func (a *app) eventRange(cmd *cobra.Command, loc *time.Location, from, to string) (time.Time, time.Time, error) {
	now := a.now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if cmd.Flags().Changed("from") {
		parsed, err := parse.Date(from, now)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		start = parsed
	}
	end := start.AddDate(0, 0, 30)
	if cmd.Flags().Changed("to") {
		parsed, err := parse.Date(to, now)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		end = parsed
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, invertedRange()
	}
	return start, end, nil
}

// resolveTagNames maps tag names to ids the way --tag matches: exact case,
// then one case-insensitive match. No match is exit 1. Two that differ only
// by case is exit 8.
func (a *app) resolveTagNames(cmd *cobra.Command, s *session, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	tags, err := a.fetchTags(cmd, s)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		tag, err := matchTagName(name, tags)
		if err != nil {
			return nil, err
		}
		ids = append(ids, tag.ID)
	}
	return ids, nil
}

func matchTagName(arg string, tags []client.Tag) (client.Tag, error) {
	var exact []client.Tag
	for _, tag := range tags {
		if tag.Name == arg {
			exact = append(exact, tag)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return client.Tag{}, ambiguousTag(arg, exact)
	}
	var folded []client.Tag
	for _, tag := range tags {
		if strings.EqualFold(tag.Name, arg) {
			folded = append(folded, tag)
		}
	}
	switch len(folded) {
	case 1:
		return folded[0], nil
	case 0:
		return client.Tag{}, &output.Error{
			Code:    output.CodeUsage,
			Message: "no tag " + displayTagArg(arg),
			Hint:    "Run `pcal tags list`.",
		}
	default:
		return client.Tag{}, ambiguousTag(arg, folded)
	}
}

func eventListQuery(start, end time.Time, search string, unpublished, website bool, tagIDs []string) url.Values {
	query := url.Values{}
	query.Set("start", start.Format(time.DateOnly))
	query.Set("end", end.Format(time.DateOnly))
	if search != "" {
		query.Set("q", search)
	}
	if unpublished {
		query.Set("published", "false")
	}
	if website {
		query.Set("website", "true")
	}
	for _, id := range tagIDs {
		query.Add("tag_ids[]", id)
	}
	return query
}

func (a *app) fetchOccurrences(cmd *cobra.Command, s *session, query url.Values) ([]client.Occurrence, error) {
	occurrences := []client.Occurrence{}
	if err := s.client.Get(cmd.Context(), "/events", query, &occurrences); err != nil {
		return nil, err
	}
	if occurrences == nil {
		occurrences = []client.Occurrence{}
	}
	return occurrences, nil
}

func nonBlank(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func eventsSummary(n int, start, end, now time.Time) string {
	word := "events"
	if n == 1 {
		word = "event"
	}
	return fmt.Sprintf("%d %s %s", n, word, formatDayRange(start, end, now))
}

func eventsTable(occurrences []client.Occurrence, loc *time.Location, now time.Time) *output.Table {
	rows := make([][]string, len(occurrences))
	for i, occurrence := range occurrences {
		rows[i] = []string{
			occurrenceDate(occurrence, loc, now),
			occurrenceTime(occurrence, loc),
			occurrence.Title,
			tagNames(occurrence.Tags),
			eventFlags(occurrence.Event),
		}
	}
	return &output.Table{
		Headers: []string{"Date", "Time", "Title", "Tags", "Flags"},
		Rows:    rows,
	}
}

func eventCard(event client.Event, loc *time.Location, now time.Time) *output.Card {
	fields := []output.Field{
		{Label: "Title", Value: event.Title},
		{Label: "Date", Value: seriesDate(event, loc, now)},
		{Label: "Time", Value: seriesTime(event, loc)},
	}
	if event.RecurrenceText != nil && *event.RecurrenceText != "" {
		fields = append(fields, output.Field{Label: "Repeats", Value: *event.RecurrenceText})
	}
	fields = append(fields,
		output.Field{Label: "Location", Value: stringOrEmpty(event.Location.Name)},
		output.Field{Label: "Address", Value: stringOrEmpty(event.Location.Address)},
		output.Field{Label: "Map", Value: stringOrEmpty(event.Location.MapURL)},
		output.Field{Label: "Description", Value: stringOrEmpty(event.Description)},
		output.Field{Label: "Notes", Value: stringOrEmpty(event.InternalNotes)},
		output.Field{Label: "Tags", Value: tagNames(event.Tags)},
		output.Field{Label: "Flags", Value: eventFlags(event)},
		output.Field{Label: "RSVPs", Value: strconv.Itoa(event.RSVPCount)},
		output.Field{Label: "ID", Value: event.ID},
	)
	return &output.Card{Fields: fields}
}

func eventFlags(event client.Event) string {
	var flags []string
	if !event.Published {
		flags = append(flags, "draft")
	}
	if event.ShowOnWebsite {
		flags = append(flags, "web")
	}
	if event.RSVPEnabled {
		flags = append(flags, "rsvp")
	}
	return strings.Join(flags, ", ")
}

func tagNames(tags []client.EventTag) string {
	names := make([]string, len(tags))
	for i, tag := range tags {
		names[i] = tag.Name
	}
	return strings.Join(names, ", ")
}

func occurrenceDate(occurrence client.Occurrence, loc *time.Location, now time.Time) string {
	start, ok := parseDay(occurrence.OccurrenceDate, loc)
	if !ok {
		return occurrence.OccurrenceDate
	}
	endDay := spanEnd(occurrence.OccurrenceDate, occurrence.OccursOn, occurrence.EndsOn)
	end, ok := parseDay(endDay, loc)
	if !ok {
		return formatWhen(start, now)
	}
	return formatSpan(start, end, now)
}

func seriesDate(event client.Event, loc *time.Location, now time.Time) string {
	start, ok := parseDay(event.OccursOn, loc)
	if !ok {
		return event.OccursOn
	}
	endDay := event.OccursOn
	if event.EndsOn != nil && *event.EndsOn != "" {
		endDay = *event.EndsOn
	}
	end, ok := parseDay(endDay, loc)
	if !ok {
		return formatWhen(start, now)
	}
	return formatSpan(start, end, now)
}

func occurrenceTime(occurrence client.Occurrence, loc *time.Location) string {
	return formatTimeRange(occurrence.OccurrenceStartsAt, occurrence.OccurrenceEndsAt, loc)
}

func seriesTime(event client.Event, loc *time.Location) string {
	return formatTimeRange(event.StartsAt, event.EndsAt, loc)
}

// spanEnd is the last day of this occurrence: occurrence_date plus the
// series' length (ends_on − occurs_on). A one-day event stays on its date.
func spanEnd(occurrenceDate, occursOn string, endsOn *string) string {
	if endsOn == nil || *endsOn == "" || *endsOn == occursOn {
		return occurrenceDate
	}
	start, err1 := time.Parse(time.DateOnly, occursOn)
	end, err2 := time.Parse(time.DateOnly, *endsOn)
	occ, err3 := time.Parse(time.DateOnly, occurrenceDate)
	if err1 != nil || err2 != nil || err3 != nil {
		return occurrenceDate
	}
	days := int(end.Sub(start).Hours() / 24)
	return occ.AddDate(0, 0, days).Format(time.DateOnly)
}

func parseDay(value string, loc *time.Location) (time.Time, bool) {
	day, err := time.ParseInLocation(time.DateOnly, value, loc)
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}

func formatSpan(start, end, now time.Time) string {
	if start.Format(time.DateOnly) == end.Format(time.DateOnly) {
		return formatWhen(start, now)
	}
	return formatWhen(start, now) + "–" + formatWhen(end, now)
}

// formatWhen is "Sun Nov 1", with the year when it isn't now's year.
func formatWhen(day, now time.Time) string {
	if day.Year() != now.In(day.Location()).Year() {
		return day.Format("Mon Jan 2, 2006")
	}
	return day.Format("Mon Jan 2")
}

func formatDayRange(start, end, now time.Time) string {
	withYear := start.Year() != now.In(start.Location()).Year() || end.Year() != now.In(end.Location()).Year() || start.Year() != end.Year()
	if start.Format(time.DateOnly) == end.Format(time.DateOnly) {
		return shortDay(start, withYear)
	}
	if start.Year() == end.Year() && start.Month() == end.Month() && !withYear {
		return start.Format("Jan 2") + "–" + end.Format("2")
	}
	if start.Year() == end.Year() && !withYear {
		return start.Format("Jan 2") + "–" + end.Format("Jan 2")
	}
	return shortDay(start, true) + "–" + shortDay(end, true)
}

func shortDay(day time.Time, withYear bool) string {
	if withYear {
		return day.Format("Jan 2, 2006")
	}
	return day.Format("Jan 2")
}

func formatTimeRange(start, end *string, loc *time.Location) string {
	left, ok := formatClock(start, loc)
	if !ok {
		return "all day"
	}
	if right, ok := formatClock(end, loc); ok {
		return left + "–" + right
	}
	return left
}

func formatClock(raw *string, loc *time.Location) (string, bool) {
	if raw == nil || *raw == "" {
		return "", false
	}
	parsed, err := time.Parse(time.RFC3339Nano, *raw)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, *raw)
		if err != nil {
			return "", false
		}
	}
	t := parsed.In(loc)
	hour := t.Hour()
	suffix := "am"
	if hour >= 12 {
		suffix = "pm"
	}
	hour %= 12
	if hour == 0 {
		hour = 12
	}
	return fmt.Sprintf("%d:%02d%s", hour, t.Minute(), suffix), true
}
