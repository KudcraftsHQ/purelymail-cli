// Command purelymail is a command line interface to the Purelymail API.
package main

import (
	"os"

	"github.com/KudcraftsHQ/purelymail-cli/internal/dispatch"
)

func main() {
	os.Exit(dispatch.Execute())
}
