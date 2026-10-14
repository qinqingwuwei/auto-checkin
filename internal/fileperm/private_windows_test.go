package fileperm

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsDACLRejectsEveryoneAndRestoresPrivacy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "私有 space")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Restrict(root, 0700); err != nil || !Private(root, 0700) {
		t.Fatalf("private directory ACL: %v", err)
	}
	path := filepath.Join(root, "credentials.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	public, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, _ := public.DACL()
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	if Private(path, 0600) {
		t.Fatal("Everyone ACL accepted as private")
	}
	if err := Restrict(path, 0600); err != nil || !Private(path, 0600) {
		t.Fatalf("private file ACL: %v", err)
	}
}
