// Command app is the darvin-agent process entry point.
package main

import (
	"os"

	"darvin-cowork/backend/internal/runtime"
)

// runApp is a var so tests can swap the entry point; production
// wires it to runtime.Run at package init.
var runApp = runtime.Run

func main() {
	os.Exit(runApp(os.Args[1:]))
}
