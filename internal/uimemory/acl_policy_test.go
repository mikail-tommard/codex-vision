package uimemory

import "testing"

func TestPrivateACLRejectsBroadOrUnknownAccess(t *testing.T) {
	const user = "S-1-5-21-1-2-3-1001"
	base := privateACL{Owner: user, Protected: true, Present: true, Entries: []privateACE{{Mask: fileAllAccess, SID: user}, {Mask: fileAllAccess, SID: "S-1-5-18"}}}
	for _, tc := range []struct {
		name  string
		alter func(*privateACL)
		valid bool
	}{
		{"private", func(*privateACL) {}, true},
		{"directory_inheritance", func(a *privateACL) { a.Entries[0].Flags = 3; a.Entries[1].Flags = 3 }, true},
		{"wrong_owner", func(a *privateACL) { a.Owner = "S-1-5-32-544" }, false},
		{"inherited_dacl", func(a *privateACL) { a.Protected = false }, false},
		{"missing_dacl", func(a *privateACL) { a.Present = false }, false},
		{"null_dacl", func(a *privateACL) { a.Entries = nil }, false},
		{"world_read", func(a *privateACL) { a.Entries = append(a.Entries, privateACE{Mask: 1, SID: "S-1-1-0"}) }, false},
		{"admin_grant", func(a *privateACL) {
			a.Entries = append(a.Entries, privateACE{Mask: fileAllAccess, SID: "S-1-5-32-544"})
		}, false},
		{"inherit_only", func(a *privateACL) { a.Entries[0].Flags = 8 }, false},
		{"inherited_ace", func(a *privateACL) { a.Entries[0].Flags = 16 }, false},
		{"deny_ace", func(a *privateACL) { a.Entries[0].Type = 1 }, false},
		{"callback_ace", func(a *privateACL) { a.Entries[0].Type = 9 }, false},
		{"owner_read_only", func(a *privateACL) { a.Entries[0].Mask = 1 }, false},
		{"system_only", func(a *privateACL) { a.Entries = a.Entries[1:] }, false},
		{"too_many", func(a *privateACL) {
			for range 8 {
				a.Entries = append(a.Entries, a.Entries[0])
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acl := base
			acl.Entries = append([]privateACE(nil), base.Entries...)
			tc.alter(&acl)
			if err := validatePrivateACL(acl, user); (err == nil) != tc.valid {
				t.Fatal("incorrect ACL decision", err)
			}
		})
	}
}
