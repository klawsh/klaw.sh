package main

import (
	"os"

	"github.com/eachlabs/klaw/cmd/klaw/commands"
)

var version = "dev"

func main() {
	if err := commands.Execute(version); err != nil {
		os.Exit(1)
	}
}
