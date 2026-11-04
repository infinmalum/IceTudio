// icetudio-cli is the GUI-independent host/client entry for headless deployment.
package main

import (
	"icetudio/internal/cli"
	"os"
)

func main() { os.Exit(cli.Run(os.Args[1:])) }
