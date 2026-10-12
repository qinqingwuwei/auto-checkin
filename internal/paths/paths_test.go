package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOverrideAndLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "测试 space")
	t.Setenv("CHECKIN_HOME", root)
	if err := Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{Config(), State(), Points(), Log(), TraeCred(), TraeProfile(), LegacyTrae()} {
		if !filepath.IsAbs(path) || filepath.Dir(path) == "." {
			t.Fatal(path)
		}
	}
	if WorkBuddyCreds() != filepath.Join(root, "credentials", "workbuddy") {
		t.Fatal(WorkBuddyCreds())
	}
	if info, err := os.Stat(root); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("root permissions: %v", err)
	}
	t.Setenv("CHECKIN_HOME", "~/.checkin-test")
	if Root() != filepath.Join(Home(), ".checkin-test") {
		t.Fatal(Root())
	}
}
