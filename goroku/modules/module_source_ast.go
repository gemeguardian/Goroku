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

// moduleTypeNames returns the concrete struct types that implement goroku.Module
// from a single source file. Runtime module loading used to select the first
// declared struct, which breaks any module that declares helper structs before
// its exported module type. Interface conformance is checked from the AST, so
// this remains source-only and does not execute untrusted module code.
func moduleTypeNames(source []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "module.go", source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse module source: %w", err)
	}

	structs := make(map[string]struct{})
	embedsBase := make(map[string]bool)
	methods := make(map[string]map[string]bool)
	var declarationOrder []string
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			if decl.Tok != token.TYPE {
				continue
			}
			for _, spec := range decl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok || typeSpec.Assign.IsValid() {
					continue
				}
				if structType, ok := typeSpec.Type.(*ast.StructType); ok {
					structs[typeSpec.Name.Name] = struct{}{}
					declarationOrder = append(declarationOrder, typeSpec.Name.Name)
					for _, field := range structType.Fields.List {
						if len(field.Names) == 0 && isGorokuBase(field.Type) {
							embedsBase[typeSpec.Name.Name] = true
						}
					}
				}
			}
		case *ast.FuncDecl:
			if decl.Recv == nil || len(decl.Recv.List) != 1 || decl.Name == nil {
				continue
			}
			receiver := receiverTypeName(decl.Recv.List[0].Type)
			if receiver == "" {
				continue
			}
			if methods[receiver] == nil {
				methods[receiver] = make(map[string]bool)
			}
			methods[receiver][decl.Name.Name] = true
		}
	}

	var names []string
	for _, name := range declarationOrder {
		if _, declared := structs[name]; !declared {
			continue
		}
		set := methods[name]
		matches := set["Name"] && set["Commands"] && embedsBase[name]
		if !matches {
			matches = true
			for _, method := range []string{"Name", "Strings", "Init", "ClientReady", "OnUnload", "OnDlmod", "Commands", "Watchers"} {
				if !set[method] {
					matches = false
					break
				}
			}
		}
		if matches {
			names = append(names, name)
		}
	}
	return names, nil
}

func isGorokuBase(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name == "Base"
	case *ast.SelectorExpr:
		return value.Sel != nil && value.Sel.Name == "Base"
	default:
		return false
	}
}

func receiverTypeName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return receiverTypeName(value.X)
	default:
		return ""
	}
}

func moduleSourceModuleStructNames(source []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "module.go", source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse module source: %w", err)
	}
	candidates, err := moduleTypeNames(source)
	if err != nil || len(candidates) == 0 {
		return candidates, err
	}
	ordered := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		ordered[candidate] = true
	}
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if ok && ordered[typeSpec.Name.Name] {
				names = append(names, typeSpec.Name.Name)
			}
		}
	}
	return names, nil
}

func moduleSourceDeclaresStruct(source []byte, name string) bool {
	names, err := moduleSourceModuleStructNames(source)
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
