package main

import (
	"os"

	"github.com/PixiBixi/kubectl-klens/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	app := cli.NewApp(cli.BuildInfo{Version: version, Commit: commit, Date: date})
	os.Exit(app.Run(os.Args[1:]))
}
