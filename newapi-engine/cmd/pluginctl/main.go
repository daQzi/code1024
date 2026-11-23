package main

import (
	"os"

	"github.com/Wei-Shaw/sub2api/newapi-engine/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
