// Command acat is the lightweight chat + video client.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ape-dev-de/acat/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String("acat"))
		return
	}

	fmt.Fprintln(os.Stderr, "acat: UI not implemented yet (see docs/plan.md, M2/M3)")
	os.Exit(1)
}
