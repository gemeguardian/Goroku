package modules

import (
	"strings"
	"testing"
)

func TestRewriteModulePackageUsesSyntaxPosition(t *testing.T) {
	source := []byte("// package fake\npackage modules // package modules\n\nconst text = `package modules`\n")
	rewritten, err := rewriteModulePackage(source, "main")
	if err != nil {
		t.Fatal(err)
	}
	got := string(rewritten)
	if !strings.Contains(got, "// package fake\npackage main // package modules") {
		t.Fatalf("package clause was not rewritten: %q", got)
	}
	if !strings.Contains(got, "`package modules`") {
		t.Fatalf("non-clause source was changed: %q", got)
	}
}

func TestModuleStructNamesOnlyReturnsDeclaredStructTypes(t *testing.T) {
	source := []byte(`package modules
// type Commented struct{}
type (
	First struct{}
	Alias = First
	Number int
)
type Second struct { Value string }
`)
	names, err := moduleStructNames(source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "First,Second" {
		t.Fatalf("struct names = %v, want first element %q", names, "First")
	}
}

func TestModuleSourceModuleStructNamesSkipsHelperStructs(t *testing.T) {
	source := []byte(`package modules

import "goroku/goroku"

type ToolPermissionRule struct { Action string }
type OpenCodeFull struct { goroku.Base }
type SessionState struct { ID string }

func (m *OpenCodeFull) Name() string { return "OpenCodeFull" }
func (m *OpenCodeFull) Commands() map[string]goroku.CommandHandler { return nil }
`)

	names, err := moduleSourceModuleStructNames(source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "OpenCodeFull" {
		t.Fatalf("module structs = %v, want [OpenCodeFull]", names)
	}
	if got := extractStructName(source, "fallback"); got != "OpenCodeFull" {
		t.Fatalf("extractStructName() = %q, want OpenCodeFull", got)
	}
	if moduleSourceDeclaresStruct(source, "ToolPermissionRule") {
		t.Fatal("helper struct must not be treated as a module")
	}
	if !moduleSourceDeclaresStruct(source, "OpenCodeFull") {
		t.Fatal("OpenCodeFull must be recognized as a module")
	}
}

func TestModuleSourceModuleStructNamesSupportsExplicitFullModule(t *testing.T) {
	source := []byte(`package modules

type FullModule struct{}
type Helper struct{}

func (m *FullModule) Name() string { return "FullModule" }
func (m *FullModule) Strings() map[string]string { return nil }
func (m *FullModule) Init(any, any) error { return nil }
func (m *FullModule) ClientReady() error { return nil }
func (m *FullModule) OnUnload() error { return nil }
func (m *FullModule) OnDlmod() error { return nil }
func (m *FullModule) Commands() map[string]any { return nil }
func (m *FullModule) Watchers() []any { return nil }
`)

	// The AST selector deliberately recognizes a conventional goroku.Base module
	// without type-checking untrusted source. An explicit implementation still
	// needs exact Goroku method signatures at plugin build time.
	names, err := moduleSourceModuleStructNames(source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "FullModule" {
		t.Fatalf("module structs = %v, want [FullModule]", names)
	}
}
