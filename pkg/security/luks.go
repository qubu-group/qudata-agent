//go:build linux && cgo

package security

/*
#cgo CFLAGS: -D_GNU_SOURCE
#include <stdlib.h>

int luks_create_volume(const char *device_path, char *key, size_t key_len);
int luks_open_volume(const char *device_path, const char *mapper_name,
                     const char *mount_point, char *key, size_t key_len);
int luks_close_volume(const char *mount_point, const char *mapper_name);
int luks_is_open(const char *mapper_name);

static inline void secure_zero(void *ptr, size_t len) {
    volatile unsigned char *p = ptr;
    while (len--) *p++ = 0;
}
*/
import "C"
import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"github.com/magicaleks/qudata-agent-alpha/pkg/utils"
)

const (
	mapperName = "qudata_secure"
	keySize    = 64
)

var (
	mgr = &volumeManager{}
)

type volumeManager struct {
	mu         sync.Mutex
	active     bool
	devicePath string
	mountPoint string
	loopDevice string
}

type VolumeConfig struct {
	MountPoint string
	SizeMB     int64
	Key        string
}

func CreateVolume(config VolumeConfig) string {
	if os.Geteuid() != 0 {
		utils.LogWarn("LUKS: not running as root")
		return ""
	}

	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if mgr.active {
		utils.LogWarn("LUKS: volume already active")
		return ""
	}

	if config.MountPoint == "" {
		utils.LogError("LUKS: mount point not specified")
		return ""
	}
	if config.SizeMB <= 0 {
		config.SizeMB = 10240
	}

	if config.Key == "" {
		key := make([]byte, keySize)
		rand.Read(key)
		config.Key = base64.StdEncoding.EncodeToString(key)
	}

	keyBytes, _ := base64.StdEncoding.DecodeString(config.Key)

	containerPath := "/var/lib/qudata/qudata_secure.img"
	os.MkdirAll("/var/lib/qudata", 0700)

	f, err := os.Create(containerPath)
	if err != nil {
		return ""
	}
	f.Truncate(config.SizeMB * 1024 * 1024)
	f.Close()

	cmd := exec.Command("losetup", "-f", "--show", containerPath)
	output, err := cmd.Output()
	if err != nil {
		os.Remove(containerPath)
		return ""
	}
	loopDevice := string(output[:len(output)-1])

	cleanup := func() {
		exec.Command("losetup", "-d", loopDevice).Run()
		os.Remove(containerPath)
	}

	cKey := C.CBytes(keyBytes)
	defer func() {
		C.secure_zero(cKey, C.size_t(len(keyBytes)))
		C.free(cKey)
	}()

	cDevice := C.CString(loopDevice)
	defer C.free(unsafe.Pointer(cDevice))

	if C.luks_create_volume(cDevice, (*C.char)(cKey), C.size_t(len(keyBytes))) != 0 {
		cleanup()
		return ""
	}

	cKey2 := C.CBytes(keyBytes)
	defer func() {
		C.secure_zero(cKey2, C.size_t(len(keyBytes)))
		C.free(cKey2)
	}()

	cMapper := C.CString(mapperName)
	defer C.free(unsafe.Pointer(cMapper))

	cMountPoint := C.CString(config.MountPoint)
	defer C.free(unsafe.Pointer(cMountPoint))

	if C.luks_open_volume(cDevice, cMapper, cMountPoint, (*C.char)(cKey2), C.size_t(len(keyBytes))) != 0 {
		cleanup()
		utils.LogError("LUKS: failed to open volume")
		return ""
	}

	mgr.active = true
	mgr.devicePath = containerPath
	mgr.mountPoint = config.MountPoint
	mgr.loopDevice = loopDevice

	return config.Key
}

func DeleteVolume() {
	if os.Geteuid() != 0 {
		utils.LogWarn("LUKS: not running as root")
		return
	}

	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if !mgr.active {
		utils.LogWarn("LUKS: no active volume to delete")
		return
	}

	cMapper := C.CString(mapperName)
	defer C.free(unsafe.Pointer(cMapper))

	if C.luks_is_open(cMapper) > 0 {
		cMountPoint := C.CString(mgr.mountPoint)
		defer C.free(unsafe.Pointer(cMountPoint))
		C.luks_close_volume(cMountPoint, cMapper)
	}

	if mgr.loopDevice != "" {
		exec.Command("losetup", "-d", mgr.loopDevice).Run()
		os.Remove(mgr.devicePath)
	}

	mgr.active = false
	mgr.devicePath = ""
	mgr.mountPoint = ""
	mgr.loopDevice = ""

}

func IsActive() bool {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.active
}

func GetMountPoint() string {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if !mgr.active {
		return ""
	}
	return mgr.mountPoint
}
