// Command wip tracks parallel workstreams as plain Markdown files.
package main

import (
	"os"

	"go.guillerg.dev/wip/internal/cli"
)

// version is set at release build time.
var version = "dev"

func main() { os.Exit(cli.Main(version)) }
