package uimemory

import "fmt"

// Normalized Win32 descriptor data keeps the privacy rule independently testable.
// Unknown ACE forms/flags fail closed; this is not a general ACL interpreter.
type privateACE struct {
	Type  uint8
	Flags uint8
	Mask  uint32
	SID   string
}

type privateACL struct {
	Owner     string
	Protected bool
	Present   bool
	Entries   []privateACE
}

const fileAllAccess uint32 = 0x001f01ff

func validatePrivateACL(acl privateACL, user string) error {
	if user == "" || acl.Owner != user || !acl.Protected || !acl.Present || len(acl.Entries) == 0 || len(acl.Entries) > 8 {
		return fmt.Errorf("UI memory requires current-user ownership and a protected non-null DACL")
	}
	ownerAccess := false
	for _, ace := range acl.Entries {
		// Only ordinary allow ACEs, with optional object/container inheritance.
		if ace.Type != 0 || ace.Flags & ^uint8(3) != 0 || (ace.SID != user && ace.SID != "S-1-5-18") {
			return fmt.Errorf("UI memory DACL contains unexpected access or inheritance")
		}
		ownerAccess = ownerAccess || (ace.SID == user && ace.Mask&fileAllAccess == fileAllAccess)
	}
	if !ownerAccess {
		return fmt.Errorf("UI memory DACL must grant the current user full file access")
	}
	return nil
}
