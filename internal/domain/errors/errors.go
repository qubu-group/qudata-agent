package errors

type InstanceAlreadyRunningError struct{}

func (e InstanceAlreadyRunningError) Error() string {
	return "instance already running"
}

type NoInstanceRunningError struct{}

func (e NoInstanceRunningError) Error() string {
	return "no instance running"
}

type LUKSVolumeCreateError struct{}

func (e LUKSVolumeCreateError) Error() string {
	return "failed to create LUKS volume"
}

type LUKSVolumeNotActiveError struct{}

func (e LUKSVolumeNotActiveError) Error() string {
	return "LUKS volume not active"
}

type InstanceStartError struct {
	Err error
}

func (e InstanceStartError) Error() string {
	return "failed to start instance: " + e.Err.Error()
}

type InstanceManageError struct {
	Err error
}

func (e InstanceManageError) Error() string {
	return "failed to manage instance: " + e.Err.Error()
}

type UnknownCommandError struct {
	Command string
}

func (e UnknownCommandError) Error() string {
	return "unknown command: " + e.Command
}

type SSHInitError struct {
	Err error
}

func (e SSHInitError) Error() string {
	return "failed to init ssh: " + e.Err.Error()
}

type SSHKeyAddError struct {
	Err error
}

func (e SSHKeyAddError) Error() string {
	return "failed to add ssh key: " + e.Err.Error()
}

type SSHKeyRemoveError struct {
	Err error
}

func (e SSHKeyRemoveError) Error() string {
	return "failed to remove ssh key: " + e.Err.Error()
}

type AppArmorProfileWriteError struct {
	Err error
}

func (e AppArmorProfileWriteError) Error() string {
	return "write profile: " + e.Err.Error()
}

type AppArmorProfileApplyError struct {
	Err error
}

func (e AppArmorProfileApplyError) Error() string {
	return "apply profile: " + e.Err.Error()
}
