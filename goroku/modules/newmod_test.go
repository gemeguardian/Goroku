package modules

import (
	"strings"
	"testing"
)

func TestValidateNewModuleNameAccepts(t *testing.T) {
	for _, name := range []string{"Weather", "W", "MyModule2", "With_Underscore", "ABC"} {
		if err := validateNewModuleName(name); err != nil {
			t.Errorf("validateNewModuleName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateNewModuleNameRejects(t *testing.T) {
	for _, tc := range []struct{ name, because string }{
		{"", "empty"},
		{"weather", "starts lowercase, would not be an exported Go type"},
		{"2Fast", "starts with a digit"},
		{"My-Module", "hyphen is not valid in an identifier"},
		{"My Module", "space is not valid in an identifier"},
		{"Модуль", "non-ASCII"},
		{strings.Repeat("A", maxNewModuleNameLen+1), "too long"},
	} {
		if err := validateNewModuleName(tc.name); err == nil {
			t.Errorf("validateNewModuleName(%q) = nil, want error (%s)", tc.name, tc.because)
		}
	}
}

// The generated skeleton must survive exactly the checks the loader applies to
// an installed module, or .newmod would hand back something .loadmod rejects.
func TestNewModuleSourcePassesLoaderValidation(t *testing.T) {
	const name = "Weather"
	source := []byte(newModuleSource(name))

	structName, err := checkModuleSource(name, name+".go", source)
	if err != nil {
		t.Fatalf("checkModuleSource() = %v, want nil", err)
	}
	if structName != name {
		t.Errorf("declared struct = %q, want %q", structName, name)
	}

	names, err := moduleStructNames(source)
	if err != nil {
		t.Fatalf("moduleStructNames() = %v", err)
	}
	if len(names) == 0 || names[0] != name {
		t.Errorf("moduleStructNames() = %v, want first element %q", names, name)
	}
}

// localRuntimeModules keys installed modules by names[0], so the skeleton must
// not declare a helper struct ahead of the module type.
func TestNewModuleSourceDeclaresExactlyOneStruct(t *testing.T) {
	names, err := moduleStructNames([]byte(newModuleSource("Weather")))
	if err != nil {
		t.Fatalf("moduleStructNames() = %v", err)
	}
	if len(names) != 1 {
		t.Errorf("skeleton declares %d structs (%v), want exactly 1", len(names), names)
	}
}

func TestNewModuleSourceWiresNameAndCommand(t *testing.T) {
	source := newModuleSource("Weather")

	for _, want := range []string{
		"type Weather struct {",
		"goroku.Base",
		`func (m *Weather) Name() string { return "Weather" }`,
		`"weather": m.WeatherCmd,`,
		"func (m *Weather) WeatherCmd(msg *goroku.Message) error {",
		"msg.ArgsOrReply()",
		`m.T("no_args"`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("skeleton is missing %q\n---\n%s", want, source)
		}
	}
}

// The skeleton is the first thing a new author reads, so it must not carry the
// boilerplate Base exists to remove.
func TestNewModuleSourceOmitsBoilerplate(t *testing.T) {
	source := newModuleSource("Weather")

	for _, unwanted := range []string{
		"func (m *Weather) ClientReady()",
		"func (m *Weather) OnUnload()",
		"func (m *Weather) OnDlmod()",
		"func (m *Weather) Watchers()",
		"func (m *Weather) Init(",
		"getTrans",
		"NewTranslator",
	} {
		if strings.Contains(source, unwanted) {
			t.Errorf("skeleton should not contain %q", unwanted)
		}
	}
}
