package main

import (
	"os"

	"github.com/b1rd33/tgctl-go/internal/commands"
)

func main() {
	os.Exit(commands.Execute())
}
