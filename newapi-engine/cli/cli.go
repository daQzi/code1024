package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

func Run(args []string, input io.Reader, output, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "init" {
		return initPlugin(args, output, stderr)
	}
	if len(args) == 1 && args[0] == "worker" {
		if err := runner.Serve(context.Background(), input, output); err != nil {
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "builtins" {
		for _, key := range builtin.Keys() {
			_, _ = fmt.Fprintln(output, key)
		}
		return 0
	}
	return jsplugin.RunCLI(args, output, stderr)
}
