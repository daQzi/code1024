package engine_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A source-level check also includes tests, tools and build-tagged files that
// a normal build of one platform would not necessarily visit.
func TestEngineDoesNotImportHost(t *testing.T) {
	const host = "github.com/Wei-Shaw/sub2api"
	const engine = host + "/newapi-engine"
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			dependency, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if dependency == "github.com/gin-gonic/gin" || strings.HasPrefix(dependency, "github.com/gin-gonic/gin/") {
				t.Errorf("%s imports the host web framework", path)
			}
			if (dependency == host || strings.HasPrefix(dependency, host+"/")) && dependency != engine && !strings.HasPrefix(dependency, engine+"/") {
				t.Errorf("%s imports host package %s", path, dependency)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
