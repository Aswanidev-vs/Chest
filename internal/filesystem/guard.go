package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// IsSystemPath returns true if target path is a root volume, drive root, or OS system directory.
func IsSystemPath(targetPath string) (bool, string) {
	abs, err := filepath.Abs(targetPath)
	if err != nil {
		abs = filepath.Clean(targetPath)
	}
	clean := filepath.Clean(abs)

	// Check 1: Root volume / drive root
	// On Linux/Unix: "/"
	// On Windows: "C:\", "D:\", etc.
	vol := filepath.VolumeName(clean)
	if clean == "/" || clean == "\\" || (vol != "" && (clean == vol || clean == vol+string(filepath.Separator))) {
		return true, fmt.Sprintf("root volume (%s)", clean)
	}

	if runtime.GOOS == "windows" {
		windir := os.Getenv("WINDIR")
		if windir == "" {
			windir = os.Getenv("SystemRoot")
		}
		if windir == "" {
			windir = `C:\Windows`
		}

		progFiles := os.Getenv("ProgramFiles")
		if progFiles == "" {
			progFiles = `C:\Program Files`
		}

		progFilesX86 := os.Getenv("ProgramFiles(x86)")
		if progFilesX86 == "" {
			progFilesX86 = `C:\Program Files (x86)`
		}

		progData := os.Getenv("ProgramData")
		if progData == "" {
			progData = `C:\ProgramData`
		}

		sysDrive := os.Getenv("SystemDrive")
		if sysDrive == "" {
			sysDrive = "C:"
		}

		systemDirs := []string{
			windir,
			progFiles,
			progFilesX86,
			progData,
			filepath.Join(sysDrive, "Recovery"),
			filepath.Join(sysDrive, "Boot"),
			filepath.Join(sysDrive, "System Volume Information"),
			filepath.Join(sysDrive, "$Recycle.Bin"),
		}

		for _, sysDir := range systemDirs {
			if isSubpath(clean, sysDir) {
				return true, fmt.Sprintf("Windows system directory (%s)", sysDir)
			}
		}
	} else {
		// Linux / Unix / macOS system paths
		systemDirs := []string{
			"/bin",
			"/sbin",
			"/boot",
			"/dev",
			"/etc",
			"/lib",
			"/lib32",
			"/lib64",
			"/libx32",
			"/proc",
			"/sys",
			"/usr",
			"/root",
			"/run",
			"/var",
		}

		for _, sysDir := range systemDirs {
			if isSubpath(clean, sysDir) {
				return true, fmt.Sprintf("system directory (%s)", sysDir)
			}
		}
	}

	return false, ""
}

// CheckSafeDirectory verifies directory can be modified by CHEST.
// Returns an error if target is a system directory and allowSystem is false.
func CheckSafeDirectory(targetDir string, allowSystem bool) error {
	isSys, reason := IsSystemPath(targetDir)
	if isSys && !allowSystem {
		return fmt.Errorf("access denied: '%s' is located in protected %s.\nCHEST restricts modifications to OS and system directories.\nUse --allow-system to override if intentional", targetDir, reason)
	}
	return nil
}

func isSubpath(target, base string) bool {
	t := filepath.Clean(target)
	b := filepath.Clean(base)

	if runtime.GOOS == "windows" {
		t = strings.ToLower(t)
		b = strings.ToLower(b)
	}

	if t == b {
		return true
	}

	rel, err := filepath.Rel(b, t)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
