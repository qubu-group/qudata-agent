package security

import (
	"fmt"
	"os"
	"os/exec"

	domainerrors "github.com/magicaleks/qudata-agent-alpha/internal/domain/errors"
)

// ApplyAppArmorProfile создает и активирует минимальный профиль AppArmor.
func ApplyAppArmorProfile(profileName, agentPath string) error {
	profilePath := fmt.Sprintf("/etc/apparmor.d/%s", profileName)
	profile := fmt.Sprintf(`#include <tunables/global>
profile %s flags=(attach_disconnected,mediate_deleted) {
  %s mr,
  /usr/lib/** rmix,
  /lib/** rmix,
  /lib64/** rmix,
  /usr/bin/** rmix,
  deny ptrace (read,trace),
  deny /proc/** w,
  deny /sys/** w,
  deny mount,
  deny umount,
  deny mknod,
}
`, profileName, agentPath)

	if err := os.WriteFile(profilePath, []byte(profile), 0644); err != nil {
		return domainerrors.AppArmorProfileWriteError{Err: err}
	}

	cmd := exec.Command("apparmor_parser", "-r", profilePath)
	if err := cmd.Run(); err != nil {
		return domainerrors.AppArmorProfileApplyError{Err: err}
	}

	return nil
}

// RemoveAppArmorProfile удаляет профиль.
func RemoveAppArmorProfile(profileName string) error {
	profilePath := fmt.Sprintf("/etc/apparmor.d/%s", profileName)
	return os.Remove(profilePath)
}
