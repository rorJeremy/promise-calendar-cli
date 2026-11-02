package commands

import (
	"runtime"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/output"
	"github.com/rorJeremy/promise-calendar-cli/internal/version"
)

// buildInfo is the version, commit, date, and Go version of this binary.
type buildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
}

func newVersionCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print pcal's version",
		Long: "Print the version, the commit, the build date, and the Go version.\n" +
			"A release build stamps the version with -ldflags. A go install build\n" +
			"has none, so pcal uses the module version from the build info.",
		Args: unknownCommand,
		RunE: func(*cobra.Command, []string) error {
			info := buildInfo{
				Version: version.Shown(),
				Commit:  version.Commit,
				Date:    version.Date,
				Go:      runtime.Version(),
			}
			return a.printer.Print(output.Result{
				Data:    info,
				Summary: "pcal " + info.Version,
				Card: &output.Card{Fields: []output.Field{
					{Label: "Version", Value: info.Version},
					{Label: "Commit", Value: info.Commit},
					{Label: "Date", Value: info.Date},
					{Label: "Go", Value: info.Go},
				}},
			})
		},
	}
}
