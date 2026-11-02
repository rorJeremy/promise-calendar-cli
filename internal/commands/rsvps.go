package commands

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
	"github.com/rorJeremy/promise-calendar-cli/internal/parse"
	"github.com/rorJeremy/promise-calendar-cli/internal/prompt"
)

// errRSVPDeleteCanceled is an RSVP delete the person canceled. Same words
// as a tag delete, and not the login's cancel.
var errRSVPDeleteCanceled = &output.Error{
	Code:    output.CodeInterrupted,
	Message: "Canceled. Nothing was deleted.",
}

var errRSVPDeleteTerminated = &output.Error{
	Code:    output.CodeTerminated,
	Message: "Canceled. Nothing was deleted.",
}

// errRSVPAddCanceled is an add form the person canceled.
var errRSVPAddCanceled = &output.Error{
	Code:    output.CodeInterrupted,
	Message: "Canceled. Nothing was added.",
}

var errRSVPAddTerminated = &output.Error{
	Code:    output.CodeTerminated,
	Message: "Canceled. Nothing was added.",
}

// deletion is the data of a confirmed delete or revoke. Answering No is
// false: a result, not an error.
type deletion struct {
	Deleted bool `json:"deleted"`
}

func newRSVPsCmd(a *app) *cobra.Command {
	cmd := newGroupCmd("rsvps", "List, add, and delete RSVPs")
	cmd.AddCommand(
		newRSVPsListCmd(a),
		newRSVPsAddCmd(a),
		newRSVPsDeleteCmd(a),
	)
	return cmd
}

func newRSVPsListCmd(a *app) *cobra.Command {
	var date string
	cmd := &cobra.Command{
		Use:   "list EVENT",
		Short: "List an event's RSVPs",
		Long: "List an event's RSVPs, newest first. EVENT is an ID or a title.\n" +
			"--date limits the list to one occurrence (YYYY-MM-DD, today, sat, …).",
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
			var occurrence string
			if cmd.Flags().Changed("date") {
				occurrence, err = occurrenceDay(date, a.now().In(loc))
				if err != nil {
					return err
				}
			}
			id, err := a.eventID(cmd, s, loc, args[0])
			if err != nil {
				return err
			}
			rsvps, err := a.fetchRSVPs(cmd, s, id, occurrence)
			if err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    rsvps,
				Summary: countSummary(len(rsvps), "RSVP", "RSVPs"),
				Table:   rsvpsTable(rsvps, loc, a.now()),
			})
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "only RSVPs for this occurrence")
	cmd.Flags().BoolVar(&a.flags.csv, "csv", false, "print the list as CSV")
	return cmd
}

func newRSVPsAddCmd(a *app) *cobra.Command {
	var name, email, phone, date string
	cmd := &cobra.Command{
		Use:   "add EVENT",
		Short: "Add an RSVP",
		Long: "Add an RSVP to an event. --name and --email are required; in a\n" +
			"terminal, pcal asks for whichever is missing. --date is which\n" +
			"occurrence, and a recurring event needs one.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.askForRSVP(cmd, &name, &email); err != nil {
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
			attrs := map[string]any{"name": name, "email": email}
			if cmd.Flags().Changed("phone") {
				if phone == "" {
					attrs["phone"] = nil
				} else {
					attrs["phone"] = phone
				}
			}
			if cmd.Flags().Changed("date") {
				day, err := occurrenceDay(date, a.now().In(loc))
				if err != nil {
					return err
				}
				attrs["occurrence_date"] = day
			}
			id, err := a.eventID(cmd, s, loc, args[0])
			if err != nil {
				return err
			}
			var created client.RSVP
			if err := s.client.Post(cmd.Context(), "/events/"+url.PathEscape(id)+"/rsvps", client.Body("rsvp", attrs), &created); err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    created,
				Summary: fmt.Sprintf("Added an RSVP from %s.", strconv.Quote(created.Name)),
				Card:    rsvpCard(created, loc, a.now()),
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "person's `name`")
	cmd.Flags().StringVar(&email, "email", "", "email `address`")
	cmd.Flags().StringVar(&phone, "phone", "", "phone `number`")
	cmd.Flags().StringVar(&date, "date", "", "occurrence `date`")
	return cmd
}

func newRSVPsDeleteCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "delete EVENT RSVP_ID",
		Short: "Delete an RSVP",
		Long: "Delete one RSVP. RSVP_ID is the RSVP's ID, not the person's name.\n" +
			"pcal asks first unless --yes is set. The question opens on No.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requirePrompt("deleting an RSVP"); err != nil {
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
			id, err := a.eventID(cmd, s, loc, args[0])
			if err != nil {
				return err
			}
			rsvps, err := a.fetchRSVPs(cmd, s, id, "")
			if err != nil {
				return err
			}
			rsvp, ok := findRSVP(rsvps, args[1])
			if !ok {
				return &output.Error{
					Code:    output.CodeNotFound,
					Message: "no RSVP " + displayArg(args[1]),
					Hint:    "Run `pcal rsvps list " + id + "`.",
				}
			}
			if !a.flags.yes {
				ok, err := a.confirmNo(cmd, fmt.Sprintf("Delete the RSVP from %s?", strconv.Quote(rsvp.Name)), errRSVPDeleteCanceled, errRSVPDeleteTerminated)
				if err != nil {
					return err
				}
				if !ok {
					return a.printer.Print(output.Result{
						Data:    deletion{Deleted: false},
						Summary: "Nothing was deleted.",
					})
				}
			}
			if err := s.client.Delete(cmd.Context(), "/events/"+url.PathEscape(id)+"/rsvps/"+url.PathEscape(rsvp.ID), nil); err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    deletion{Deleted: true},
				Summary: fmt.Sprintf("Deleted the RSVP from %s.", strconv.Quote(rsvp.Name)),
			})
		},
	}
}

// requirePrompt fails, before any request, when a confirmation would have
// to be asked and neither stdin nor stderr is a terminal and --yes is unset.
func (a *app) requirePrompt(action string) error {
	if a.flags.yes || (a.streams.InTTY && a.streams.ErrTTY) {
		return nil
	}
	return &output.Error{
		Code:    output.CodeUsage,
		Message: "can't ask before " + action + ", and --yes wasn't set",
		Hint:    "Pass --yes to skip the question, or run pcal in a terminal so it can ask.",
	}
}

func (a *app) askForRSVP(cmd *cobra.Command, name, email *string) error {
	if strings.TrimSpace(*name) != "" && strings.TrimSpace(*email) != "" {
		return nil
	}
	if !a.streams.InTTY || !a.streams.ErrTTY {
		return &output.Error{
			Code:    output.CodeUsage,
			Message: "need a name and an email",
			Hint:    "Pass --name and --email, or run pcal in a terminal so it can ask.",
		}
	}
	var fields []prompt.TextField
	if strings.TrimSpace(*name) == "" {
		fields = append(fields, prompt.TextField{Title: "Name", Value: name})
	}
	if strings.TrimSpace(*email) == "" {
		fields = append(fields, prompt.TextField{Title: "Email", Value: email})
	}
	ctx, stop := a.interruptible(cmd.Context())
	defer stop()
	if err := a.prompter().Text(ctx, fields...); err != nil {
		if errors.Is(err, prompt.ErrCanceled) {
			if terminated(ctx) {
				return errRSVPAddTerminated
			}
			return errRSVPAddCanceled
		}
		return err
	}
	if strings.TrimSpace(*name) == "" || strings.TrimSpace(*email) == "" {
		return &output.Error{
			Code:    output.CodeUsage,
			Message: "need a name and an email",
			Hint:    "Pass --name and --email.",
		}
	}
	return nil
}

func (a *app) fetchRSVPs(cmd *cobra.Command, s *session, id, occurrence string) ([]client.RSVP, error) {
	var query url.Values
	if occurrence != "" {
		query = url.Values{"occurrence": {occurrence}}
	}
	rsvps := []client.RSVP{}
	if err := s.client.Get(cmd.Context(), "/events/"+url.PathEscape(id)+"/rsvps", query, &rsvps); err != nil {
		return nil, err
	}
	if rsvps == nil {
		rsvps = []client.RSVP{}
	}
	return rsvps, nil
}

func findRSVP(rsvps []client.RSVP, id string) (client.RSVP, bool) {
	for _, rsvp := range rsvps {
		if rsvp.ID == id {
			return rsvp, true
		}
	}
	return client.RSVP{}, false
}

func occurrenceDay(text string, now time.Time) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", &output.Error{Code: output.CodeUsage, Message: "invalid date \"\""}
	}
	day, err := parse.Date(text, now)
	if err != nil {
		return "", err
	}
	return day.Format(time.DateOnly), nil
}

func countSummary(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func rsvpsTable(rsvps []client.RSVP, loc *time.Location, now time.Time) *output.Table {
	rows := make([][]string, len(rsvps))
	for i, rsvp := range rsvps {
		rows[i] = []string{rsvp.Name, rsvp.Email, stringOrEmpty(rsvp.Phone), humanDay(stringOrEmpty(rsvp.OccurrenceDate), loc, now), rsvp.ID}
	}
	return &output.Table{Headers: []string{"Name", "Email", "Phone", "Date", "ID"}, Rows: rows}
}

func rsvpCard(rsvp client.RSVP, loc *time.Location, now time.Time) *output.Card {
	return &output.Card{Fields: []output.Field{
		{Label: "Name", Value: rsvp.Name},
		{Label: "Email", Value: rsvp.Email},
		{Label: "Phone", Value: stringOrEmpty(rsvp.Phone)},
		{Label: "Date", Value: humanDay(stringOrEmpty(rsvp.OccurrenceDate), loc, now)},
		{Label: "ID", Value: rsvp.ID},
	}}
}

func humanDay(raw string, loc *time.Location, now time.Time) string {
	if raw == "" {
		return ""
	}
	day, ok := parseDay(raw, loc)
	if !ok {
		return raw
	}
	return formatWhen(day, now)
}

func displayArg(arg string) string {
	if looksLikeToken(arg) {
		return "that id"
	}
	return strconv.Quote(arg)
}
