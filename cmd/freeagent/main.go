package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/timbrinded/freeagent-cli/internal/cli"
	"github.com/timbrinded/freeagent-cli/internal/config"
	"github.com/timbrinded/freeagent-cli/internal/credential"
	appruntime "github.com/timbrinded/freeagent-cli/internal/runtime"
)

var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	os.Exit(run())
}

func run() int {
	ignoreSIGPIPE()
	resolvedVersion := buildVersion(version)
	path, err := config.DefaultPath()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error: the configuration path could not be determined")
		return 10
	}
	userAgent := "freeagent-cli/" + resolvedVersion
	services := appruntime.New(config.Repository{Path: path}, credential.NewKeyringStore(""), userAgent)
	app := cli.New(cli.Dependencies{
		Profiles: services, Auth: services, API: services,
		Build:  cli.BuildInfo{Version: resolvedVersion, Commit: commit, Date: date, Go: runtime.Version()},
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Execute(ctx, os.Args[1:])
}

func buildVersion(linked string) string {
	if linked != "" && linked != "dev" {
		return strings.TrimPrefix(linked, "v")
	}
	info, ok := debug.ReadBuildInfo()
	if ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}
