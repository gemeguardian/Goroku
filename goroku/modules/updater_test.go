package modules

import "testing"

func TestRestartOptions(t *testing.T) {
	force, secureBoot := restartOptions("-f --secure-boot")
	if !force || !secureBoot {
		t.Fatalf("restartOptions() = force:%t secureBoot:%t, want both true", force, secureBoot)
	}
	force, secureBoot = restartOptions("-sb")
	if force || !secureBoot {
		t.Fatalf("restartOptions(-sb) = force:%t secureBoot:%t", force, secureBoot)
	}
}
