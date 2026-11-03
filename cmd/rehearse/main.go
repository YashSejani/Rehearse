// Command rehearse is the Rehearse CLI. All logic lives in internal/.
package main

import (
	"os"

	"github.com/YashSejani/Rehearse/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
