//go:build ignore

// archcheck enforces the mechanical go-func rules on the Go module in the
// current directory. It uses only the standard library and needs no install.
//
//	cd <module root>
//	go run <skill dir>/scripts/archcheck.go [-shared platform,config]
//
// Prints "archcheck: clean" and exits 0 when every rule passes. Violations and
// run failures exit non-zero.
//
// -shared names packages directly under internal/ that any domain may import.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type pkg struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Imports    []string
	Module     *struct{ Path string }
}

// location is where a package sits in the go-func layout.
type location struct {
	domain      string // first segment under internal/
	integration string // <system> under integrations/, if any
	layer       string // domain, port, application, adapter; "" in a flat package
	sub         string // path below the layer
}

var allowedLayers = map[string][]string{
	"domain":      {"domain"},
	"port":        {"domain", "port"},
	"application": {"domain", "port", "application"},
	"adapter":     {"domain", "port", "application", "adapter"},
}

// ioImports are standard packages Pure Logic has no business importing.
var ioImports = []string{"database/sql", "net", "net/http", "os", "os/exec"}

// frameworkPrefixes are import prefixes barred from domain/ and port/.
var frameworkPrefixes = []string{
	"gorm.io/", "github.com/jmoiron/sqlx", "github.com/jackc/pgx",
	"go.mongodb.org/", "github.com/redis/", "github.com/gin-gonic/",
	"github.com/labstack/echo", "github.com/gofiber/",
}

// impureCalls maps an import path to the functions that make a caller impure.
// A nil list means every function in the package does.
var impureCalls = map[string][]string{
	"time":                   {"Now", "Since", "Until", "Sleep", "After", "Tick"},
	"math/rand":              nil,
	"math/rand/v2":           nil,
	"crypto/rand":            nil,
	"github.com/google/uuid": {"New", "NewString", "NewRandom", "NewUUID", "NewV6", "NewV7"},
}

// mutatingPrefixes are method prefixes barred from Advanced repository ports.
var mutatingPrefixes = []string{"Update", "Delete", "Remove", "Save", "Upsert"}

func main() {
	shared := flag.String("shared", "platform", "comma-separated packages under internal/ that any domain may import")
	flag.Parse()

	pkgs, err := listPackages()
	if err != nil {
		fmt.Fprintln(os.Stderr, "archcheck:", err)
		os.Exit(2)
	}
	if len(pkgs) == 0 {
		fmt.Fprintln(os.Stderr, "archcheck: no packages under ./internal; run it from a module root")
		os.Exit(2)
	}

	sharedSet := strings.Split(*shared, ",")
	var found []string
	for _, p := range pkgs {
		found = append(found, checkPackage(p, sharedSet)...)
	}
	slices.Sort(found)
	found = slices.Compact(found)
	for _, f := range found {
		fmt.Println(f)
	}
	if len(found) > 0 {
		fmt.Printf("archcheck: %d violation(s) in %d package(s) checked\n", len(found), len(pkgs))
		os.Exit(1)
	}
	fmt.Printf("archcheck: clean, %d package(s) checked\n", len(pkgs))
}

func listPackages() ([]pkg, error) {
	cmd := exec.Command("go", "list", "-e", "-json=ImportPath,Dir,GoFiles,Imports,Module", "./internal/...")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var pkgs []pkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p pkg
		if err := dec.Decode(&p); err == io.EOF {
			return pkgs, nil
		} else if err != nil {
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		if p.Module != nil {
			pkgs = append(pkgs, p)
		}
	}
}

func locate(rel string) location {
	segs := strings.Split(rel, "/")
	loc := location{domain: segs[0]}
	rest := segs[1:]
	if len(rest) >= 2 && rest[0] == "integrations" {
		loc.integration = rest[1]
		rest = rest[2:]
	}
	if len(rest) > 0 && allowedLayers[rest[0]] != nil {
		loc.layer = rest[0]
		loc.sub = strings.Join(rest[1:], "/")
	}
	return loc
}

func isPure(loc location) bool {
	if loc.layer == "domain" {
		return true
	}
	return loc.layer == "application" &&
		(strings.HasPrefix(loc.sub, "builders") || strings.HasPrefix(loc.sub, "mappers"))
}

func checkPackage(p pkg, shared []string) []string {
	prefix := p.Module.Path + "/internal/"
	rel, ok := strings.CutPrefix(p.ImportPath, prefix)
	if !ok {
		return nil
	}
	loc := locate(rel)
	if slices.Contains(shared, loc.domain) {
		return nil
	}

	var found []string
	report := func(rule, format string, args ...any) {
		found = append(found, fmt.Sprintf("%s: %s: %s", rel, rule, fmt.Sprintf(format, args...)))
	}

	for _, imp := range p.Imports {
		if (isPure(loc) || loc.layer == "port") && slices.ContainsFunc(frameworkPrefixes, func(f string) bool {
			return strings.HasPrefix(imp, f)
		}) {
			report("framework-import", "%s layer imports %s", loc.layer, imp)
		}
		targetRel, internal := strings.CutPrefix(imp, prefix)
		if !internal {
			continue
		}
		target := locate(targetRel)
		switch {
		case slices.Contains(shared, target.domain):
		case target.domain != loc.domain:
			if loc.layer == "adapter" && targetRel == target.domain+"/domain/events" {
				continue // event types are the published contract
			}
			report("cross-domain", "imports another domain's internals: %s", targetRel)
		case target.integration != "" && loc.integration != "" && target.integration != loc.integration:
			report("cross-integration", "integration %s imports integration %s", loc.integration, target.integration)
		case loc.layer != "" && target.layer != "" && !slices.Contains(allowedLayers[loc.layer], target.layer):
			report("dependency-direction", "%s imports %s: %s", loc.layer, target.layer, targetRel)
		}
	}

	for _, name := range p.GoFiles {
		file := filepath.Join(p.Dir, name)
		switch {
		case isPure(loc), loc.layer == "" && name == "model.go":
			found = append(found, checkPureFile(rel, name, file)...)
		case loc.layer == "port":
			found = append(found, checkPortFile(rel, name, file)...)
		}
	}
	return found
}

func parse(file string) (*token.FileSet, *ast.File, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	return fset, f, err
}

func checkPureFile(rel, name, file string) []string {
	fset, f, err := parse(file)
	if err != nil {
		return []string{fmt.Sprintf("%s/%s: parse: %v", rel, name, err)}
	}
	var found []string
	report := func(pos token.Pos, rule, msg string) {
		found = append(found, fmt.Sprintf("%s/%s:%d: %s: %s", rel, name, fset.Position(pos).Line, rule, msg))
	}

	imports := map[string]string{} // local name -> import path
	for _, spec := range f.Imports {
		ip, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		local := path.Base(ip)
		if local == "v2" {
			local = path.Base(path.Dir(ip))
		}
		if spec.Name != nil {
			local = spec.Name.Name
		}
		imports[local] = ip
		if ip == "context" {
			report(spec.Pos(), "pure-context", "Pure Logic takes no context.Context")
		}
		if slices.Contains(ioImports, ip) {
			report(spec.Pos(), "pure-io", "Pure Logic file imports "+ip)
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		ip := imports[x.Name]
		funcs, listed := impureCalls[ip]
		if listed && (funcs == nil || slices.Contains(funcs, sel.Sel.Name)) {
			report(sel.Pos(), "pure-nondeterminism", fmt.Sprintf("%s.%s belongs in Orchestration; pass the value in", x.Name, sel.Sel.Name))
		}
		return true
	})
	return found
}

func checkPortFile(rel, name, file string) []string {
	fset, f, err := parse(file)
	if err != nil {
		return []string{fmt.Sprintf("%s/%s: parse: %v", rel, name, err)}
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || !strings.Contains(spec.Name.Name, "Repository") {
			return true
		}
		iface, ok := spec.Type.(*ast.InterfaceType)
		if !ok {
			return true
		}
		for _, m := range iface.Methods.List {
			for _, id := range m.Names {
				if slices.ContainsFunc(mutatingPrefixes, func(p string) bool {
					return strings.HasPrefix(id.Name, p)
				}) {
					found = append(found, fmt.Sprintf("%s/%s:%d: append-only: %s.%s mutates; create a version or tombstone instead",
						rel, name, fset.Position(id.Pos()).Line, spec.Name.Name, id.Name))
				}
			}
		}
		return true
	})
	return found
}
