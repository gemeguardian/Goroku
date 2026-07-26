package modules

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

func rewriteModulePackage(source []byte, packageName string) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "module.go", source, parser.AllErrors)
	if err != nil {
		return nil, fmt.Errorf("parse module source: %w", err)
	}
	if file.Name == nil {
		return nil, fmt.Errorf("module source has no package declaration")
	}
	start := fset.Position(file.Name.Pos()).Offset
	end := fset.Position(file.Name.End()).Offset
	if start < 0 || end < start || end > len(source) {
		return nil, fmt.Errorf("invalid package declaration position")
	}
	rewritten := make([]byte, 0, len(source)-end+start+len(packageName))
	rewritten = append(rewritten, source[:start]...)
	rewritten = append(rewritten, packageName...)
	rewritten = append(rewritten, source[end:]...)
	return rewritten, nil
}

func moduleStructNames(source []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "module.go", source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse module source: %w", err)
	}
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Assign.IsValid() {
				continue
			}
			if _, ok := typeSpec.Type.(*ast.StructType); ok {
				names = append(names, typeSpec.Name.Name)
			}
		}
	}
	return names, nil
}

func moduleSourceDeclaresStruct(source []byte, name string) bool {
	names, err := moduleStructNames(source)
	if err != nil {
		return false
	}
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}
