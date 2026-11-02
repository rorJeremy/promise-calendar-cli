package commands

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// uuidText is an event id. Title resolution doesn't search for one: it
// passes straight through to GET /events/:id.
var uuidText = regexp.MustCompile(`(?i)\A[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\z`)

func isUUID(s string) bool {
	return uuidText.MatchString(s)
}

// eventRef is one event title resolution found, with the earliest
// occurrence date in the window.
type eventRef struct {
	id        string
	title     string
	firstDate string
}

// resolveEvent finds the event arg names, by an exact title or a unique
// title that starts with it. The server's q matches anywhere, so the list
// is filtered here. Events outside today−1y..today+2y need their id.
func (a *app) resolveEvent(cmd *cobra.Command, s *session, loc *time.Location, arg string) (eventRef, error) {
	now := a.now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	query := url.Values{}
	query.Set("q", arg)
	query.Set("start", today.AddDate(-1, 0, 0).Format(time.DateOnly))
	query.Set("end", today.AddDate(2, 0, 0).Format(time.DateOnly))
	occurrences, err := a.fetchOccurrences(cmd, s, query)
	if err != nil {
		return eventRef{}, err
	}
	refs := dedupeEvents(occurrences)
	if found, ok, err := matchEvent(arg, refs); err != nil || ok {
		return found, err
	}
	hint := "use the event's ID (`pcal events list --search " + arg + "` shows it)"
	if looksLikeToken(arg) {
		hint = "use the event's ID (`pcal events list` shows it)"
	}
	return eventRef{}, &output.Error{
		Code:    output.CodeNotFound,
		Message: "no event " + displayTagArg(arg),
		Hint:    hint,
	}
}

func dedupeEvents(occurrences []client.Occurrence) []eventRef {
	index := map[string]int{}
	var refs []eventRef
	for _, occurrence := range occurrences {
		at, ok := index[occurrence.ID]
		if !ok {
			index[occurrence.ID] = len(refs)
			refs = append(refs, eventRef{
				id:        occurrence.ID,
				title:     occurrence.Title,
				firstDate: occurrence.OccurrenceDate,
			})
			continue
		}
		if occurrence.OccurrenceDate != "" && (refs[at].firstDate == "" || occurrence.OccurrenceDate < refs[at].firstDate) {
			refs[at].firstDate = occurrence.OccurrenceDate
		}
	}
	return refs
}

// matchEvent applies the title rules. ok is false when nothing matched and
// err is nil, so the caller can build the not-found error.
func matchEvent(arg string, refs []eventRef) (eventRef, bool, error) {
	var exact []eventRef
	for _, ref := range refs {
		if strings.EqualFold(ref.title, arg) {
			exact = append(exact, ref)
		}
	}
	if len(exact) == 1 {
		return exact[0], true, nil
	}
	if len(exact) > 1 {
		return eventRef{}, false, ambiguousEvent(arg, exact)
	}
	needle := strings.ToLower(arg)
	var prefix []eventRef
	for _, ref := range refs {
		if strings.HasPrefix(strings.ToLower(ref.title), needle) {
			prefix = append(prefix, ref)
		}
	}
	switch len(prefix) {
	case 1:
		return prefix[0], true, nil
	case 0:
		return eventRef{}, false, nil
	default:
		return eventRef{}, false, ambiguousEvent(arg, prefix)
	}
}

func ambiguousEvent(arg string, refs []eventRef) error {
	named := make([]string, len(refs))
	for i, ref := range refs {
		named[i] = strconv.Quote(ref.title) + " (" + ref.id + ", " + ref.firstDate + ")"
	}
	return &output.Error{
		Code:    output.CodeAmbiguous,
		Message: "event " + displayTagArg(arg) + " matches more than one event",
		Hint:    orList(named) + ".",
	}
}
