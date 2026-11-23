package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Wei-Shaw/sub2api/newapi-engine/templates"
)

func initPlugin(args []string, output, stderr io.Writer) int {
	if len(args) != 5 || args[3] != "--key" || args[2] == "" {
		_, _ = fmt.Fprintln(stderr, "usage: pluginctl init <task|provider|ingress> <new-directory> --key <plugin-key>")
		return 2
	}
	kind, directory, key := args[1], filepath.Clean(args[2]), args[4]
	source, err := templates.Source(kind, key)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "plugin scaffold: %v\n", err)
		return 1
	}
	fixture, err := templates.Fixture(kind)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "plugin scaffold fixture: %v\n", err)
		return 1
	}
	// Creation is exclusive: never overwrite a user's existing project, even
	// an empty directory or a symlink. The parent directory must exist.
	if err = os.Mkdir(directory, 0755); err != nil {
		_, _ = fmt.Fprintf(stderr, "create new plugin directory: %v\n", err)
		return 1
	}
	readme := fmt.Sprintf(`# %s

This %s template targets the supported New API API v1 subset.
Its vendor, models, connection and fixture credentials are fictional.

1. Replace the request/response mapping using the actual upstream documentation.
2. Update fixture.json with successful, failed, unknown and missing-usage cases.
3. Run offline checks from this directory:

    pluginctl lint plugin.js
    pluginctl test plugin.js --fixture fixture.json

Upload plugin.js through the host's developer page. Use the namespace shown
by that page; increment meta.version for each immutable published revision.
Never include real credentials, contact paid upstreams in a fixture, or modify
the host wallet from a script. Accounts provide the actual connection/config.

Passing a fixture proves deterministic hooks only. Verify host capability,
permissions, files, pricing, recovery and the real upstream separately.
`, key, kind)
	for _, file := range []struct{ name, content string }{
		{"plugin.js", source}, {"fixture.json", string(fixture)}, {"README.md", readme},
	} {
		if err = writeNewFile(filepath.Join(directory, file.name), file.content); err != nil {
			_, _ = fmt.Fprintf(stderr, "plugin scaffold is partial; inspect %s: %v\n", directory, err)
			return 1
		}
	}
	_, _ = fmt.Fprintf(output, "created %s plugin %s in %s\n", kind, key, directory)
	return 0
}

func writeNewFile(path, content string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, content)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
