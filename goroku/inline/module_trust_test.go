package inline

import "testing"

// fakeSecurityChecker grants module access to everyone, so the only thing that
// can deny a delegate is the privileged-module rule itself.
type fakeSecurityChecker struct {
	owners     map[int64]bool
	privileged map[string]bool
}

func (f *fakeSecurityChecker) IsOwner(userID int64) bool { return f.owners[userID] }
func (f *fakeSecurityChecker) CheckModuleAccess(userID int64, moduleName string) bool {
	return true
}
func (f *fakeSecurityChecker) IsPrivilegedModule(moduleName string) bool {
	return f.privileged[moduleName]
}

// A module-scoped grant on a privileged module must not let a delegate press
// the buttons that confirm handing out owner rights.
func TestIsUserOwnerOrTrustedForModuleDeniesPrivilegedModules(t *testing.T) {
	im := &InlineManager{client: &mockOwnerClient{ownerID: 1}}
	sm := &fakeSecurityChecker{
		owners:     map[int64]bool{1: true},
		privileged: map[string]bool{"GorokuSecurity": true},
	}

	if im.isUserOwnerOrTrustedForModule(sm, 100, "GorokuSecurity") {
		t.Fatal("module access alone unlocked a privileged module's buttons")
	}
	if !im.isUserOwnerOrTrustedForModule(sm, 100, "GorokuInfo") {
		t.Fatal("module access stopped unlocking an ordinary module's buttons")
	}
	if !im.isUserOwnerOrTrustedForModule(sm, 1, "GorokuSecurity") {
		t.Fatal("the owner lost access to a privileged module's buttons")
	}
}
