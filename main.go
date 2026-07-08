package main

import (
	"os"

	"github.com/PixiBixi/kubectl-klens/internal/cli"

	_ "k8s.io/client-go/plugin/pkg/client/auth" // register oidc/gcp/azure/exec auth providers
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
