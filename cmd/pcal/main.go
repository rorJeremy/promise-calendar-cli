// Command pcal manages the Promise Church calendar from the terminal.
package main

import (
	"os"

	"github.com/rorJeremy/promise-calendar-cli/internal/commands"
)

func main() {
	os.Exit(commands.Execute())
}
