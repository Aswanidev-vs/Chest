package filesystem

import (
	"runtime"
	"testing"
)

func TestIsSystemPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		tests := []struct {
			path     string
			isSystem bool
		}{
			{`C:\`, true},
			{`C:\Windows`, true},
			{`C:\Windows\System32`, true},
			{`C:\Program Files`, true},
			{`C:\Program Files\Common Files`, true},
			{`C:\Program Files (x86)`, true},
			{`C:\ProgramData`, true},
			{`E:\Chest\testing`, false},
			{`C:\Users\test\Downloads`, false},
		}

		for _, tt := range tests {
			isSys, reason := IsSystemPath(tt.path)
			if isSys != tt.isSystem {
				t.Errorf("IsSystemPath(%q) = %v (reason: %q), want %v", tt.path, isSys, reason, tt.isSystem)
			}
		}
	} else {
		tests := []struct {
			path     string
			isSystem bool
		}{
			{"/", true},
			{"/bin", true},
			{"/etc", true},
			{"/etc/nginx", true},
			{"/usr", true},
			{"/usr/local/bin", true},
			{"/var/log", true},
			{"/home/user/Downloads", false},
			{"/tmp/test", false},
		}

		for _, tt := range tests {
			isSys, reason := IsSystemPath(tt.path)
			if isSys != tt.isSystem {
				t.Errorf("IsSystemPath(%q) = %v (reason: %q), want %v", tt.path, isSys, reason, tt.isSystem)
			}
		}
	}
}
