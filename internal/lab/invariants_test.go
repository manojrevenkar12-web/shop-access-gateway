package lab

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The invariants in AGENTS.md, checked on the source.
func TestInvariants(t *testing.T) {
	root := Root()
	const mod = "github.com/manojrevenkar12-web/shop-access-gateway/internal/"
	product := []string{"shop", "store", "ingest", "vat", "support", "api"}
	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		pkg := filepath.Base(filepath.Dir(path))
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			for _, prod := range product {
				// The product never reads the truth: no product package imports the world or the lab.
				if pkg == prod && (p == mod+"world" || p == mod+"lab") {
					t.Errorf("%s imports %s: the product must not see the simulated world", path, p)
				}
			}
			// The world does not read the product's settings or code (only the shared vocabulary in shop).
			if pkg == "world" && strings.HasPrefix(p, mod) && p != mod+"shop" && p != mod+"world" {
				t.Errorf("%s imports %s: the world must not depend on the product", path, p)
			}
		}
		if pkg == "world" {
			b, _ := os.ReadFile(path)
			if strings.Contains(string(b), "shop.json") {
				t.Errorf("%s mentions shop.json: the world must not read the product's settings", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
