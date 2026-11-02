package commands

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
	"github.com/rorJeremy/promise-calendar-cli/internal/prompt"
)

// errTagDeleteCanceled is a delete confirmation the person canceled with
// Esc or Ctrl-C: one line on stderr, exit 130, no envelope. It is not
// errLoginCanceled, whose words are the login's, and it is not "no":
// answering No deletes nothing and exits 0 (Jeremy's Gate 4 decision).
// basecamp-cli treats a canceled confirmation as "no" and exits 0
// (internal/commands/chat.go:1284, skill.go:166); pcal does not.
var errTagDeleteCanceled = &output.Error{
	Code:    output.CodeInterrupted,
	Message: "Canceled. Nothing was deleted.",
}

// errTagDeleteTerminated is errTagDeleteCanceled for a SIGTERM: the same
// line, exit 143.
var errTagDeleteTerminated = &output.Error{
	Code:    output.CodeTerminated,
	Message: "Canceled. Nothing was deleted.",
}

// tagDeletion is the data of `tags delete`: whether the tag was deleted.
// Answering No is {"deleted": false}, a result, not an error.
type tagDeletion struct {
	Deleted bool `json:"deleted"`
}

// newTagsCmd builds `pcal tags`.
func newTagsCmd(a *app) *cobra.Command {
	cmd := newGroupCmd("tags", "List, create, update, and delete tags")
	cmd.AddCommand(
		newTagsListCmd(a),
		newTagsCreateCmd(a),
		newTagsUpdateCmd(a),
		newTagsDeleteCmd(a),
	)
	return cmd
}

func newTagsListCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tags",
		Long:  "List every tag, in the calendar's order: position, then name.",
		Args:  unknownCommand,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.open()
			if err != nil {
				return err
			}
			tags, err := a.fetchTags(cmd, s)
			if err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    tags,
				Summary: tagsSummary(len(tags)),
				Table:   tagsTable(tags),
			})
		},
	}
	cmd.Flags().BoolVar(&a.flags.csv, "csv", false, "print the list as CSV")
	return cmd
}

func newTagsCreateCmd(a *app) *cobra.Command {
	var color string
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a tag",
		Long: "Create a tag named NAME. --color is optional, #RRGGBB when given.\n" +
			"A duplicate name or a bad color comes back as the calendar's error.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.open()
			if err != nil {
				return err
			}
			attrs := map[string]any{"name": args[0]}
			if cmd.Flags().Changed("color") {
				attrs["color"] = color
			}
			var created client.Tag
			if err := s.client.Post(cmd.Context(), "/tags", client.Body("tag", attrs), &created); err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    created,
				Summary: fmt.Sprintf("Created tag %s.", strconv.Quote(created.Name)),
				Card:    tagCard(created),
			})
		},
	}
	cmd.Flags().StringVar(&color, "color", "", "color, as `#RRGGBB`")
	return cmd
}

func newTagsUpdateCmd(a *app) *cobra.Command {
	var name, color string
	var position int
	cmd := &cobra.Command{
		Use:   "update ID|NAME",
		Short: "Update a tag",
		Long: "Change a tag's name, color, or position. Only the flags given are sent.\n" +
			"ID|NAME is the tag's ID or its name (an exact match, or a unique\n" +
			"case-insensitive one).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.open()
			if err != nil {
				return err
			}
			tag, err := a.resolveTag(cmd, s, args[0])
			if err != nil {
				return err
			}
			attrs := map[string]any{}
			if cmd.Flags().Changed("name") {
				attrs["name"] = name
			}
			if cmd.Flags().Changed("color") {
				attrs["color"] = color
			}
			if cmd.Flags().Changed("position") {
				attrs["position"] = position
			}
			var updated client.Tag
			if err := s.client.Patch(cmd.Context(), "/tags/"+tag.ID, client.Body("tag", attrs), &updated); err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    updated,
				Summary: fmt.Sprintf("Updated tag %s.", strconv.Quote(updated.Name)),
				Card:    tagCard(updated),
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new `name`")
	cmd.Flags().StringVar(&color, "color", "", "color, as `#RRGGBB` (empty clears it)")
	cmd.Flags().IntVar(&position, "position", 0, "`position` in the list (lower comes first)")
	return cmd
}

func newTagsDeleteCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "delete ID|NAME",
		Short: "Delete a tag",
		Long: "Delete a tag by its ID or its name (an exact match, or a unique\n" +
			"case-insensitive one). pcal asks first unless --yes is set. The\n" +
			"question opens on No, so Enter deletes nothing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A confirmation that can't be shown fails before any request,
			// so a script can't delete a tag by forgetting --yes.
			if !a.flags.yes && (!a.streams.InTTY || !a.streams.ErrTTY) {
				return &output.Error{
					Code:    output.CodeUsage,
					Message: "can't ask before deleting a tag, and --yes wasn't set",
					Hint:    "Pass --yes to delete it without asking, or run pcal in a terminal so it can ask.",
				}
			}
			s, err := a.open()
			if err != nil {
				return err
			}
			tag, err := a.resolveTag(cmd, s, args[0])
			if err != nil {
				return err
			}
			if !a.flags.yes {
				ok, err := a.confirmTagDelete(cmd, fmt.Sprintf("Delete tag %s?", strconv.Quote(tag.Name)))
				if err != nil {
					return err
				}
				if !ok {
					return a.printer.Print(output.Result{
						Data:    tagDeletion{Deleted: false},
						Summary: "Nothing was deleted.",
					})
				}
			}
			if err := s.client.Delete(cmd.Context(), "/tags/"+tag.ID, nil); err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    tagDeletion{Deleted: true},
				Summary: fmt.Sprintf("Deleted tag %s.", strconv.Quote(tag.Name)),
			})
		},
	}
}

// fetchTags calls GET /tags and returns the tags in the calendar's order.
// The result is never nil, so a list prints [] rather than null.
func (a *app) fetchTags(cmd *cobra.Command, s *session) ([]client.Tag, error) {
	tags := []client.Tag{}
	if err := s.client.Get(cmd.Context(), "/tags", nil, &tags); err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []client.Tag{}
	}
	return tags, nil
}

// resolveTag finds the tag args names: an ID, or a name matched the way
// --tag will match names. An exact name wins over a case-insensitive one.
// Two that differ only by case is exit 8. No match is exit 2.
func (a *app) resolveTag(cmd *cobra.Command, s *session, arg string) (client.Tag, error) {
	tags, err := a.fetchTags(cmd, s)
	if err != nil {
		return client.Tag{}, err
	}
	for _, tag := range tags {
		if tag.ID == arg {
			return tag, nil
		}
	}
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
			Code:    output.CodeNotFound,
			Message: "no tag " + displayTagArg(arg),
			Hint:    "Run `pcal tags list`.",
		}
	default:
		return client.Tag{}, ambiguousTag(arg, folded)
	}
}

// confirmTagDelete asks title, opening on No, and reports a cancel as
// errTagDeleteCanceled or errTagDeleteTerminated. The caller has already
// checked that stdin and stderr are both terminals.
func (a *app) confirmTagDelete(cmd *cobra.Command, title string) (bool, error) {
	ctx, stop := a.interruptible(cmd.Context())
	defer stop()
	ok, err := a.prompter().Confirm(ctx, title, false)
	if err != nil {
		if errors.Is(err, prompt.ErrCanceled) {
			if terminated(ctx) {
				return false, errTagDeleteTerminated
			}
			return false, errTagDeleteCanceled
		}
		return false, err
	}
	return ok, nil
}

func tagsSummary(n int) string {
	if n == 1 {
		return "1 tag"
	}
	return fmt.Sprintf("%d tags", n)
}

func tagsTable(tags []client.Tag) *output.Table {
	rows := make([][]string, len(tags))
	for i, tag := range tags {
		rows[i] = []string{tag.Name, stringOrEmpty(tag.Color), positionString(tag.Position), tag.ID}
	}
	return &output.Table{Headers: []string{"Name", "Color", "Position", "ID"}, Rows: rows}
}

func tagCard(tag client.Tag) *output.Card {
	return &output.Card{Fields: []output.Field{
		{Label: "Name", Value: tag.Name},
		{Label: "Color", Value: stringOrEmpty(tag.Color)},
		{Label: "Position", Value: positionString(tag.Position)},
		{Label: "ID", Value: tag.ID},
	}}
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func positionString(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

func ambiguousTag(arg string, tags []client.Tag) error {
	named := make([]string, len(tags))
	for i, tag := range tags {
		named[i] = strconv.Quote(tag.Name) + " (" + tag.ID + ")"
	}
	return &output.Error{
		Code:    output.CodeAmbiguous,
		Message: "tag " + displayTagArg(arg) + " matches more than one tag",
		Hint:    orList(named) + ". Run `pcal tags list`.",
	}
}

// displayTagArg quotes arg for an error. A word that looks like a token
// isn't repeated.
func displayTagArg(arg string) string {
	if looksLikeToken(arg) {
		return "(not shown: it looks like a token)"
	}
	return strconv.Quote(arg)
}
