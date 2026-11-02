package commands

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
	"github.com/rorJeremy/promise-calendar-cli/internal/parse"
	"github.com/rorJeremy/promise-calendar-cli/internal/prompt"
)

// A description is at most 1 MiB, the same kind of bound as --with-token's
// 4096 bytes (Gate 4). An upload is at most 10 MB, which is the server's
// 10.megabytes (byte_size > 10.megabytes is the 422).
const (
	maxDescriptionBytes = 1 << 20
	maxImageBytes       = 10 << 20
)

// errEventDeleteCanceled is a delete the person canceled with Esc or
// Ctrl-C. Same words as a tag delete, and not the login's cancel.
var errEventDeleteCanceled = &output.Error{
	Code:    output.CodeInterrupted,
	Message: "Canceled. Nothing was deleted.",
}

var errEventDeleteTerminated = &output.Error{
	Code:    output.CodeTerminated,
	Message: "Canceled. Nothing was deleted.",
}

// errEventCreateCanceled is a create form the person canceled.
var errEventCreateCanceled = &output.Error{
	Code:    output.CodeInterrupted,
	Message: "Canceled. Nothing was created.",
}

var errEventCreateTerminated = &output.Error{
	Code:    output.CodeTerminated,
	Message: "Canceled. Nothing was created.",
}

type eventDeletion struct {
	Deleted bool `json:"deleted"`
}

// imageRemoval is the data of `events image rm`. The server answers 204
// with no body, including when the event had no image.
type imageRemoval struct {
	Removed bool `json:"removed"`
}

// eventOpts are the flags create and update share.
type eventOpts struct {
	title, date, endDate, start, end    string
	allDay                              bool
	repeat, rrule                       string
	locationName, address, mapURL       string
	tags                                []string
	description, descriptionFile, notes string
	image, imageAlt, imageURL           string
	website, rsvp, draft                bool
}

func bindEventOpts(cmd *cobra.Command, o *eventOpts) {
	f := cmd.Flags()
	f.StringVar(&o.title, "title", "", "event `title`")
	f.StringVar(&o.date, "date", "", "first date (`YYYY-MM-DD`, today, sat, …)")
	f.StringVar(&o.endDate, "end-date", "", "last date, when it runs past the first day")
	f.StringVar(&o.start, "start", "", "start `time` (10am, 6:30pm, 18:30)")
	f.StringVar(&o.end, "end", "", "end `time`")
	f.BoolVar(&o.allDay, "all-day", false, "no start or end time")
	f.StringVar(&o.repeat, "repeat", "", "how often it repeats (`weekly on sun`, …)")
	f.StringVar(&o.rrule, "rrule", "", "recurrence `rule`, sent unchanged")
	f.StringVar(&o.locationName, "location-name", "", "place `name`")
	f.StringVar(&o.address, "address", "", "street `address`")
	f.StringVar(&o.mapURL, "map-url", "", "map `URL`")
	f.StringArrayVar(&o.tags, "tag", nil, "tag `name` (repeatable; replaces the event's tags)")
	f.StringVar(&o.description, "description", "", "public `description`")
	f.StringVar(&o.descriptionFile, "description-file", "", "read the description from `file` (`-` is stdin)")
	f.StringVar(&o.notes, "notes", "", "private `notes`")
	f.StringVar(&o.image, "image", "", "image `file` to upload")
	f.StringVar(&o.imageAlt, "image-alt", "", "image `text`")
	f.StringVar(&o.imageURL, "image-url", "", "link an image at `URL` instead of uploading one")
	f.BoolVar(&o.website, "website", false, "show the event on the website")
	f.BoolVar(&o.rsvp, "rsvp", false, "accept RSVPs")
	f.BoolVar(&o.draft, "draft", false, "save the event unpublished")
}

func newEventsCreateCmd(a *app) *cobra.Command {
	var o eventOpts
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an event",
		Long: "Create an event. --title and --date are required; in a terminal,\n" +
			"pcal asks for whichever is missing. --repeat becomes a recurrence\n" +
			"rule. --rrule is sent unchanged.",
		Args: unknownCommand,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := eventFlagConflicts(cmd, &o); err != nil {
				return err
			}
			if err := a.prepareEventFiles(cmd, &o, true); err != nil {
				return err
			}
			if err := a.askForCreate(cmd, &o); err != nil {
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
			attrs := map[string]any{}
			if err := a.applyEventFields(cmd, s, &o, attrs); err != nil {
				return err
			}
			if err := applyCreateWhen(cmd, &o, loc, a.now(), attrs); err != nil {
				return err
			}
			var created client.Event
			if err := a.sendEvent(cmd, s, httpCreate, "/events", &o, attrs, &created); err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    created,
				Summary: fmt.Sprintf("Created %s.", strconv.Quote(created.Title)),
				Card:    eventCard(created, loc, a.now()),
			})
		},
	}
	bindEventOpts(cmd, &o)
	return cmd
}

func newEventsUpdateCmd(a *app) *cobra.Command {
	var o eventOpts
	cmd := &cobra.Command{
		Use:   "update ID",
		Short: "Update an event",
		Long: "Update an event. Only the flags you pass are sent. An empty value\n" +
			"clears that field (--end-date '', --tag '', --rrule '').",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := eventFlagConflicts(cmd, &o); err != nil {
				return err
			}
			if err := a.prepareEventFiles(cmd, &o, false); err != nil {
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
			attrs := map[string]any{}
			if err := a.applyEventFields(cmd, s, &o, attrs); err != nil {
				return err
			}
			if err := a.applyUpdateWhen(cmd, s, loc, id, &o, attrs); err != nil {
				return err
			}
			var updated client.Event
			if err := a.sendEvent(cmd, s, httpUpdate, "/events/"+url.PathEscape(id), &o, attrs, &updated); err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    updated,
				Summary: fmt.Sprintf("Updated %s.", strconv.Quote(updated.Title)),
				Card:    eventCard(updated, loc, a.now()),
			})
		},
	}
	bindEventOpts(cmd, &o)
	return cmd
}

func newEventsDeleteCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "delete ID",
		Short: "Delete an event",
		Long: "Delete an event, and its whole series when it repeats. pcal asks\n" +
			"first unless --yes is set. The question opens on No.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.flags.yes && (!a.streams.InTTY || !a.streams.ErrTTY) {
				return &output.Error{
					Code:    output.CodeUsage,
					Message: "can't ask before deleting an event, and --yes wasn't set",
					Hint:    "Pass --yes to delete it without asking, or run pcal in a terminal so it can ask.",
				}
			}
			s, err := a.open()
			if err != nil {
				return err
			}
			loc, err := a.calendarLocation(cmd, s)
			if err != nil {
				return err
			}
			event, err := a.loadEvent(cmd, s, loc, args[0])
			if err != nil {
				return err
			}
			if !a.flags.yes {
				ok, err := a.confirmEventDelete(cmd, deleteQuestion(event, loc, a.now()))
				if err != nil {
					return err
				}
				if !ok {
					return a.printer.Print(output.Result{
						Data:    eventDeletion{Deleted: false},
						Summary: "Nothing was deleted.",
					})
				}
			}
			if err := s.client.Delete(cmd.Context(), "/events/"+url.PathEscape(event.ID), nil); err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    eventDeletion{Deleted: true},
				Summary: fmt.Sprintf("Deleted %s.", strconv.Quote(event.Title)),
			})
		},
	}
}

func newEventsPublishCmd(a *app, publish bool) *cobra.Command {
	use, short, summary := "publish ID", "Publish an event", "Published %s."
	if !publish {
		use, short, summary = "unpublish ID", "Unpublish an event", "Unpublished %s."
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.patchEventBool(cmd, args[0], "published", publish, summary)
		},
	}
}

func newEventsToggleCmd(a *app, use, attr string) *cobra.Command {
	short := "Show an event on the website"
	onSummary, offSummary := "Showing %s on the website.", "Hiding %s from the website."
	if use == "rsvp" {
		short = "Turn RSVPs on or off"
		onSummary, offSummary = "RSVPs are on for %s.", "RSVPs are off for %s."
	}
	cmd := newGroupCmd(use, short)
	cmd.AddCommand(
		newEventsOnOffCmd(a, "on", attr, true, onSummary),
		newEventsOnOffCmd(a, "off", attr, false, offSummary),
	)
	return cmd
}

func newEventsOnOffCmd(a *app, use, attr string, value bool, summary string) *cobra.Command {
	return &cobra.Command{
		Use:   use + " ID",
		Short: use,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.patchEventBool(cmd, args[0], attr, value, summary)
		},
	}
}

func newEventsImageCmd(a *app) *cobra.Command {
	cmd := newGroupCmd("image", "Set or remove an event's image")
	var alt, imageURL string
	set := &cobra.Command{
		Use:   "set ID [FILE]",
		Short: "Set an event's image",
		Long: "Upload FILE, or pass --url to link an image. Exactly one of\n" +
			"those. Either replaces the other kind of image.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := ""
			if len(args) == 2 {
				file = args[1]
			}
			if (file == "") == !cmd.Flags().Changed("url") {
				return &output.Error{
					Code:    output.CodeUsage,
					Message: "pass an image file or --url, and not both",
					Hint:    "Run `pcal events image set --help` for usage.",
				}
			}
			if file != "" {
				if err := checkImageSize(file); err != nil {
					return err
				}
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
			attrs := map[string]any{}
			if cmd.Flags().Changed("alt") {
				if alt == "" {
					attrs["image_alt"] = nil
				} else {
					attrs["image_alt"] = alt
				}
			}
			var updated client.Event
			path := "/events/" + url.PathEscape(id)
			if file != "" {
				if err := a.uploadEvent(cmd, s, httpUpdate, path, file, attrs, &updated); err != nil {
					return err
				}
			} else {
				attrs["image_url"] = imageURL
				if err := s.client.Patch(cmd.Context(), path, client.Body("event", attrs), &updated); err != nil {
					return err
				}
			}
			return a.printer.Print(output.Result{
				Data:    updated,
				Summary: fmt.Sprintf("Updated the image for %s.", strconv.Quote(updated.Title)),
				Card:    eventCard(updated, loc, a.now()),
			})
		},
	}
	set.Flags().StringVar(&imageURL, "url", "", "image `URL` to link")
	set.Flags().StringVar(&alt, "alt", "", "image `text`")
	rm := &cobra.Command{
		Use:   "rm ID",
		Short: "Remove an event's image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if err := s.client.Delete(cmd.Context(), "/events/"+url.PathEscape(id)+"/image", nil); err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    imageRemoval{Removed: true},
				Summary: "Removed the image.",
			})
		},
	}
	cmd.AddCommand(set, rm)
	return cmd
}

const (
	httpCreate = "POST"
	httpUpdate = "PATCH"
)

func eventFlagConflicts(cmd *cobra.Command, o *eventOpts) error {
	switch {
	case cmd.Flags().Changed("repeat") && cmd.Flags().Changed("rrule"):
		return &output.Error{Code: output.CodeUsage, Message: "--repeat and --rrule can't be used together"}
	case cmd.Flags().Changed("image") && cmd.Flags().Changed("image-url"):
		return &output.Error{Code: output.CodeUsage, Message: "--image and --image-url can't be used together"}
	case cmd.Flags().Changed("description") && cmd.Flags().Changed("description-file"):
		return &output.Error{Code: output.CodeUsage, Message: "--description and --description-file can't be used together"}
	case o.allDay && (cmd.Flags().Changed("start") || cmd.Flags().Changed("end")):
		return &output.Error{Code: output.CodeUsage, Message: "--all-day can't be used with --start or --end"}
	default:
		return nil
	}
}

// prepareEventFiles checks the description and the image before any
// request. A description on stdin can't also be the answers to the create
// form, so that combination is refused.
func (a *app) prepareEventFiles(cmd *cobra.Command, o *eventOpts, creating bool) error {
	if cmd.Flags().Changed("description-file") && o.descriptionFile == "-" && creating && (o.title == "" || o.date == "") {
		return &output.Error{
			Code:    output.CodeUsage,
			Message: "pass --title and --date when --description-file is -",
			Hint:    "The description is read from stdin, which is also where a prompt would read.",
		}
	}
	if cmd.Flags().Changed("description-file") {
		text, err := readDescription(o.descriptionFile, a.streams.In)
		if err != nil {
			return err
		}
		o.description = text
	}
	if cmd.Flags().Changed("image") && o.image != "" {
		if err := checkImageSize(o.image); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) askForCreate(cmd *cobra.Command, o *eventOpts) error {
	if strings.TrimSpace(o.title) != "" && strings.TrimSpace(o.date) != "" {
		return nil
	}
	if !a.streams.InTTY || !a.streams.ErrTTY {
		return &output.Error{
			Code:    output.CodeUsage,
			Message: "need a title and a date",
			Hint:    "Pass --title and --date, or run pcal in a terminal so it can ask.",
		}
	}
	var fields []prompt.TextField
	if strings.TrimSpace(o.title) == "" {
		fields = append(fields, prompt.TextField{Title: "Title", Value: &o.title})
	}
	if strings.TrimSpace(o.date) == "" {
		fields = append(fields, prompt.TextField{Title: "Date", Value: &o.date})
	}
	ctx, stop := a.interruptible(cmd.Context())
	defer stop()
	if err := a.prompter().Text(ctx, fields...); err != nil {
		if errors.Is(err, prompt.ErrCanceled) {
			if terminated(ctx) {
				return errEventCreateTerminated
			}
			return errEventCreateCanceled
		}
		return err
	}
	if strings.TrimSpace(o.title) == "" || strings.TrimSpace(o.date) == "" {
		return &output.Error{
			Code:    output.CodeUsage,
			Message: "need a title and a date",
			Hint:    "Pass --title and --date.",
		}
	}
	return nil
}

func (a *app) applyEventFields(cmd *cobra.Command, s *session, o *eventOpts, attrs map[string]any) error {
	if cmd.Flags().Changed("title") {
		attrs["title"] = o.title
	}
	putClearable(cmd, attrs, "description", "description", o.description)
	if cmd.Flags().Changed("description-file") {
		attrs["description"] = o.description
	}
	putClearable(cmd, attrs, "notes", "internal_notes", o.notes)
	putClearable(cmd, attrs, "location-name", "location_name", o.locationName)
	putClearable(cmd, attrs, "address", "location_address", o.address)
	putClearable(cmd, attrs, "map-url", "location_map_url", o.mapURL)
	putClearable(cmd, attrs, "image-alt", "image_alt", o.imageAlt)
	putClearable(cmd, attrs, "image-url", "image_url", o.imageURL)
	if cmd.Flags().Changed("repeat") {
		rule, err := parse.Repeat(o.repeat)
		if err != nil {
			return err
		}
		attrs["recurrence_rule"] = rule
	}
	if cmd.Flags().Changed("rrule") {
		if o.rrule == "" {
			attrs["recurrence_rule"] = nil
		} else {
			attrs["recurrence_rule"] = o.rrule
		}
	}
	if cmd.Flags().Changed("website") {
		attrs["show_on_website"] = o.website
	}
	if cmd.Flags().Changed("rsvp") {
		attrs["rsvp_enabled"] = o.rsvp
	}
	if cmd.Flags().Changed("draft") {
		attrs["published"] = !o.draft
	}
	if cmd.Flags().Changed("tag") {
		names := nonBlank(o.tags)
		if len(names) == 0 {
			attrs["tag_ids"] = []string{}
			return nil
		}
		ids, err := a.resolveTagNames(cmd, s, names)
		if err != nil {
			return err
		}
		attrs["tag_ids"] = ids
	}
	return nil
}

func putClearable(cmd *cobra.Command, attrs map[string]any, flag, key, value string) {
	if !cmd.Flags().Changed(flag) {
		return
	}
	if value == "" {
		attrs[key] = nil
		return
	}
	attrs[key] = value
}

func applyCreateWhen(cmd *cobra.Command, o *eventOpts, loc *time.Location, now time.Time, attrs map[string]any) error {
	occurs, err := parse.Date(o.date, now.In(loc))
	if err != nil {
		return err
	}
	attrs["title"] = o.title
	attrs["occurs_on"] = occurs.Format(time.DateOnly)
	endDay := occurs
	if cmd.Flags().Changed("end-date") {
		if o.endDate == "" {
			attrs["ends_on"] = nil
		} else {
			endDay, err = parse.Date(o.endDate, now.In(loc))
			if err != nil {
				return err
			}
			if endDay.Before(occurs) {
				return &output.Error{Code: output.CodeUsage, Message: "--end-date is before --date"}
			}
			attrs["ends_on"] = endDay.Format(time.DateOnly)
		}
	}
	if o.allDay {
		attrs["starts_at"] = nil
		attrs["ends_at"] = nil
		return nil
	}
	if cmd.Flags().Changed("start") && o.start != "" {
		at, err := clockOn(occurs, o.start)
		if err != nil {
			return err
		}
		attrs["starts_at"] = parse.Timestamp(at)
	}
	if cmd.Flags().Changed("end") && o.end != "" {
		at, err := clockOn(endDay, o.end)
		if err != nil {
			return err
		}
		attrs["ends_at"] = parse.Timestamp(at)
	}
	return nil
}

// applyUpdateWhen rebuilds starts_at and ends_at when a date or a time
// changes, so a 6pm event moved to Saturday stays at 6pm. The event is
// fetched only when the flags don't already name every date and time.
func (a *app) applyUpdateWhen(cmd *cobra.Command, s *session, loc *time.Location, id string, o *eventOpts, attrs map[string]any) error {
	dateChanged := cmd.Flags().Changed("date")
	endChanged := cmd.Flags().Changed("end-date")
	startChanged := cmd.Flags().Changed("start")
	stopChanged := cmd.Flags().Changed("end")
	allDay := o.allDay && cmd.Flags().Changed("all-day")
	if !dateChanged && !endChanged && !startChanged && !stopChanged && !allDay {
		return nil
	}
	if allDay {
		attrs["starts_at"] = nil
		attrs["ends_at"] = nil
		if !dateChanged && !endChanged {
			return nil
		}
	}
	now := a.now().In(loc)
	// Both dates and both clocks are in the flags, so nothing has to be
	// read off the event. --all-day already cleared the times; a date that
	// isn't in the flags is the only reason left to fetch.
	flagsAreEnough := dateChanged && o.date != "" && endChanged && (allDay || (startChanged && stopChanged))
	var event client.Event
	if !flagsAreEnough && (dateChanged || endChanged || startChanged || stopChanged) {
		if err := s.client.Get(cmd.Context(), "/events/"+url.PathEscape(id), nil, &event); err != nil {
			return err
		}
	}
	occurs, err := dateFrom(o.date, dateChanged, event.OccursOn, now)
	if err != nil {
		return err
	}
	if dateChanged {
		attrs["occurs_on"] = occurs.Format(time.DateOnly)
	}
	endDay := occurs
	switch {
	case endChanged && o.endDate == "":
		attrs["ends_on"] = nil
	case endChanged:
		endDay, err = parse.Date(o.endDate, now)
		if err != nil {
			return err
		}
		if endDay.Before(occurs) {
			return &output.Error{Code: output.CodeUsage, Message: "--end-date is before --date"}
		}
		attrs["ends_on"] = endDay.Format(time.DateOnly)
	case event.EndsOn != nil && *event.EndsOn != "":
		endDay, err = time.ParseInLocation(time.DateOnly, *event.EndsOn, loc)
		if err != nil {
			return err
		}
	}
	if o.allDay && cmd.Flags().Changed("all-day") {
		return nil
	}
	startClock, hasStart, err := clockFrom(o.start, startChanged, event.StartsAt, loc)
	if err != nil {
		return err
	}
	endClock, hasEnd, err := clockFrom(o.end, stopChanged, event.EndsAt, loc)
	if err != nil {
		return err
	}
	// Moving an all-day event's dates sends no times.
	if !hasStart && !hasEnd && !startChanged && !stopChanged {
		return nil
	}
	if startChanged && o.start == "" {
		attrs["starts_at"] = nil
	} else if hasStart {
		at, err := parse.At(occurs, startClock)
		if err != nil {
			return err
		}
		attrs["starts_at"] = parse.Timestamp(at)
	}
	if stopChanged && o.end == "" {
		attrs["ends_at"] = nil
	} else if hasEnd {
		at, err := parse.At(endDay, endClock)
		if err != nil {
			return err
		}
		attrs["ends_at"] = parse.Timestamp(at)
	}
	return nil
}

func dateFrom(flag string, changed bool, current string, now time.Time) (time.Time, error) {
	if changed {
		if strings.TrimSpace(flag) == "" {
			return time.Time{}, &output.Error{Code: output.CodeUsage, Message: "invalid date \"\""}
		}
		return parse.Date(flag, now)
	}
	return time.ParseInLocation(time.DateOnly, current, now.Location())
}

func clockFrom(flag string, changed bool, current *string, loc *time.Location) (parse.Clock, bool, error) {
	if changed {
		if flag == "" {
			return parse.Clock{}, false, nil
		}
		c, err := parse.Time(flag)
		return c, err == nil, err
	}
	c, ok := clockOf(current, loc)
	return c, ok, nil
}

func clockOf(raw *string, loc *time.Location) (parse.Clock, bool) {
	if raw == nil || *raw == "" {
		return parse.Clock{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, *raw)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, *raw)
		if err != nil {
			return parse.Clock{}, false
		}
	}
	t := parsed.In(loc)
	return parse.Clock{Hour: t.Hour(), Minute: t.Minute()}, true
}

func clockOn(day time.Time, text string) (time.Time, error) {
	c, err := parse.Time(text)
	if err != nil {
		return time.Time{}, err
	}
	return parse.At(day, c)
}

func (a *app) sendEvent(cmd *cobra.Command, s *session, method, path string, o *eventOpts, attrs map[string]any, out *client.Event) error {
	if cmd.Flags().Changed("image") && o.image != "" {
		return a.uploadEvent(cmd, s, method, path, o.image, attrs, out)
	}
	body := client.Body("event", attrs)
	if method == httpCreate {
		return s.client.Post(cmd.Context(), path, body, out)
	}
	return s.client.Patch(cmd.Context(), path, body, out)
}

func (a *app) uploadEvent(cmd *cobra.Command, s *session, method, path, file string, attrs map[string]any, out *client.Event) error {
	f, err := os.Open(file) //nolint:gosec // G304: the person named this file
	if err != nil {
		return &output.Error{Code: output.CodeUsage, Message: err.Error()}
	}
	defer func() { _ = f.Close() }()
	buf, contentType, err := eventMultipart(attrs, file, f)
	if err != nil {
		return err
	}
	return s.client.Upload(cmd.Context(), method, path, buf, contentType, out)
}

func eventMultipart(attrs map[string]any, filename string, file io.Reader) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := writeEventField(w, key, attrs[key]); err != nil {
			return nil, "", err
		}
	}
	part, err := w.CreateFormFile("event[featured_image]", filepath.Base(filename))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

func writeEventField(w *multipart.Writer, key string, value any) error {
	switch v := value.(type) {
	case []string:
		if len(v) == 0 {
			return w.WriteField("event[tag_ids][]", "")
		}
		for _, id := range v {
			if err := w.WriteField("event[tag_ids][]", id); err != nil {
				return err
			}
		}
		return nil
	case bool:
		return w.WriteField("event["+key+"]", strconv.FormatBool(v))
	case nil:
		return w.WriteField("event["+key+"]", "")
	case string:
		return w.WriteField("event["+key+"]", v)
	default:
		return w.WriteField("event["+key+"]", fmt.Sprint(v))
	}
}

func readDescription(path string, stdin io.Reader) (string, error) {
	var r io.Reader
	if path == "-" {
		r = stdin
	} else {
		f, err := os.Open(path) //nolint:gosec // G304: the person named this file
		if err != nil {
			return "", &output.Error{Code: output.CodeUsage, Message: err.Error()}
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxDescriptionBytes+1))
	if err != nil {
		return "", &output.Error{Code: output.CodeUsage, Message: err.Error()}
	}
	if len(data) > maxDescriptionBytes {
		return "", &output.Error{
			Code:    output.CodeUsage,
			Message: "description is larger than 1 MiB",
			Hint:    "Shorten the file, or pass a shorter --description.",
		}
	}
	return string(data), nil
}

func checkImageSize(path string) error {
	info, err := os.Stat(path) //nolint:gosec // G304: the person named this file
	if err != nil {
		return &output.Error{Code: output.CodeUsage, Message: err.Error()}
	}
	if info.IsDir() {
		return &output.Error{Code: output.CodeUsage, Message: path + " is a directory"}
	}
	if info.Size() > maxImageBytes {
		return &output.Error{
			Code:    output.CodeUsage,
			Message: "image is larger than 10 MB",
			Hint:    "Use an image of 10 MB or smaller. The calendar rejects anything over that.",
		}
	}
	return nil
}

func (a *app) eventID(cmd *cobra.Command, s *session, loc *time.Location, arg string) (string, error) {
	if isUUID(arg) {
		return arg, nil
	}
	found, err := a.resolveEvent(cmd, s, loc, arg)
	if err != nil {
		return "", err
	}
	return found.id, nil
}

func (a *app) loadEvent(cmd *cobra.Command, s *session, loc *time.Location, arg string) (client.Event, error) {
	id, err := a.eventID(cmd, s, loc, arg)
	if err != nil {
		return client.Event{}, err
	}
	var event client.Event
	if err := s.client.Get(cmd.Context(), "/events/"+url.PathEscape(id), nil, &event); err != nil {
		return client.Event{}, err
	}
	return event, nil
}

func deleteQuestion(event client.Event, loc *time.Location, now time.Time) string {
	q := fmt.Sprintf("Delete %s on %s?", strconv.Quote(event.Title), seriesDate(event, loc, now))
	if event.RecurrenceRule != nil && *event.RecurrenceRule != "" {
		q += " This deletes the whole series."
	}
	return q
}

func (a *app) confirmEventDelete(cmd *cobra.Command, title string) (bool, error) {
	ctx, stop := a.interruptible(cmd.Context())
	defer stop()
	ok, err := a.prompter().Confirm(ctx, title, false)
	if err != nil {
		if errors.Is(err, prompt.ErrCanceled) {
			if terminated(ctx) {
				return false, errEventDeleteTerminated
			}
			return false, errEventDeleteCanceled
		}
		return false, err
	}
	return ok, nil
}

func (a *app) patchEventBool(cmd *cobra.Command, arg, attr string, value bool, summary string) error {
	s, err := a.open()
	if err != nil {
		return err
	}
	loc, err := a.calendarLocation(cmd, s)
	if err != nil {
		return err
	}
	id, err := a.eventID(cmd, s, loc, arg)
	if err != nil {
		return err
	}
	var event client.Event
	body := client.Body("event", map[string]any{attr: value})
	if err := s.client.Patch(cmd.Context(), "/events/"+url.PathEscape(id), body, &event); err != nil {
		return err
	}
	return a.printer.Print(output.Result{
		Data:    event,
		Summary: fmt.Sprintf(summary, strconv.Quote(event.Title)),
		Card:    eventCard(event, loc, a.now()),
	})
}
