// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package catalog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Writing a constructor but forgetting to list it in definitions, or listing a
// name with no constructor, makes the two sets differ and fails the build.
var _ = Describe("catalog completeness", func() {
	It("wires every exported kartas constructor into the definitions list", func() {
		defined := constructorsFromSource()
		wired := constructorsFromDefinitions()

		for name := range defined {
			Expect(wired).To(HaveKey(name),
				"constructor kartas.%s is defined but not wired into definitions in catalog.go", name)
		}
		for name := range wired {
			Expect(defined).To(HaveKey(name),
				"definitions references kartas.%s but no such constructor exists", name)
		}
	})
})

// constructorsFromDefinitions returns the short names of the functions in the
// definitions list, derived at runtime from each func value.
func constructorsFromDefinitions() map[string]bool {
	names := make(map[string]bool, len(definitions))
	for _, def := range definitions {
		full := runtime.FuncForPC(reflect.ValueOf(def).Pointer()).Name()
		names[full[strings.LastIndex(full, ".")+1:]] = true
	}
	return names
}

// constructorsFromSource parses pkg/catalog/kartas/*.go (excluding _test.go) and
// returns every exported top-level func with signature func() *v1alpha1.Karta.
func constructorsFromSource() map[string]bool {
	_, thisFile, _, ok := runtime.Caller(0)
	Expect(ok).To(BeTrue(), "cannot locate test file")
	kartasDir := filepath.Join(filepath.Dir(thisFile), "kartas")

	entries, err := os.ReadDir(kartasDir)
	Expect(err).NotTo(HaveOccurred(), "read kartas dir")

	fset := token.NewFileSet()
	names := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(kartasDir, e.Name()), nil, 0)
		Expect(err).NotTo(HaveOccurred(), "parse %s", e.Name())
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			if isKartaConstructor(fn.Type) {
				names[fn.Name.Name] = true
			}
		}
	}
	Expect(names).NotTo(BeEmpty(), "no constructors found in kartas package")
	return names
}

// isKartaConstructor reports whether the signature is func() *v1alpha1.Karta.
func isKartaConstructor(sig *ast.FuncType) bool {
	if sig.Params != nil && len(sig.Params.List) != 0 {
		return false
	}
	if sig.Results == nil || len(sig.Results.List) != 1 {
		return false
	}
	star, ok := sig.Results.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	return ok && pkgIdent.Name == "v1alpha1" && sel.Sel.Name == "Karta"
}
