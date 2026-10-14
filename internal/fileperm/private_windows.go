package fileperm

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FILE_ALL_ACCESS; SDDL FA resolves to this file-system access mask.
const fileAllAccess windows.ACCESS_MASK = 0x001f01ff
const systemSID = "S-1-5-18"

func ownerSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

// Windows chmod does not implement Unix privacy bits. Use a protected DACL
// granting access to this user and SYSTEM; directories pass it to children.
func Restrict(path string, _ os.FileMode) error {
	owner, err := ownerSID()
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	inherit := ""
	if info.IsDir() {
		inherit = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + owner + ")(A;" + inherit + ";FA;;;SY)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func Private(path string, _ os.FileMode) bool {
	owner, err := ownerSID()
	if err != nil {
		return false
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 2 {
		return false
	}
	seenOwner, seenSystem := false, false
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, i, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != fileAllAccess {
			return false
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if sid != owner && sid != systemSID {
			return false
		}
		seenOwner = seenOwner || sid == owner
		seenSystem = seenSystem || sid == systemSID
	}
	return seenOwner && seenSystem
}
