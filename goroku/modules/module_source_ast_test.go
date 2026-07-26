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
		t.Fatalf("struct names = %v", names)
	}
}
