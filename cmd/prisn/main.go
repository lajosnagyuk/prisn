// prisn - just run my shit
//
// A single-binary serverless platform for running scripts and services
// on Kubernetes, bare metal, or anywhere else.
package main

import (
	"fmt"
	"os"

	"github.com/lajosnagyuk/prisn/pkg/cli"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cmd := cli.NewRootCmd(cli.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	})

	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
