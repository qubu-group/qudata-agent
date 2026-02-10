package domain

import "fmt"

// ErrInstanceAlreadyRunning is returned when trying to create an instance
// while one is already active.
type ErrInstanceAlreadyRunning struct{}

func (e ErrInstanceAlreadyRunning) Error() string {
	return "instance is already running"
}

// ErrNoInstanceRunning is returned when trying to manage a non-existent instance.
type ErrNoInstanceRunning struct{}

func (e ErrNoInstanceRunning) Error() string {
	return "no instance is currently running"
}

// ErrUnknownCommand is returned for unrecognized instance commands.
type ErrUnknownCommand struct {
	Command string
}

func (e ErrUnknownCommand) Error() string {
	return fmt.Sprintf("unknown command: %s", e.Command)
}

// ErrInstanceManage wraps errors from Docker container management operations.
type ErrInstanceManage struct {
	Err error
}

func (e ErrInstanceManage) Error() string {
	return fmt.Sprintf("instance management failed: %v", e.Err)
}

func (e ErrInstanceManage) Unwrap() error {
	return e.Err
}

// ErrImagePull wraps errors from pulling Docker images.
type ErrImagePull struct {
	Image string
	Err   error
}

func (e ErrImagePull) Error() string {
	return fmt.Sprintf("failed to pull image %s: %v", e.Image, e.Err)
}

func (e ErrImagePull) Unwrap() error {
	return e.Err
}

// ErrFRPC wraps errors from the FRPC tunnel manager.
type ErrFRPC struct {
	Op  string
	Err error
}

func (e ErrFRPC) Error() string {
	return fmt.Sprintf("frpc %s: %v", e.Op, e.Err)
}

func (e ErrFRPC) Unwrap() error {
	return e.Err
}

// ErrVFIO wraps errors from VFIO GPU binding operations.
type ErrVFIO struct {
	Op   string
	Addr string
	Err  error
}

func (e ErrVFIO) Error() string {
	return fmt.Sprintf("vfio %s [%s]: %v", e.Op, e.Addr, e.Err)
}

func (e ErrVFIO) Unwrap() error {
	return e.Err
}

// ErrQEMU wraps errors from QEMU VM operations.
type ErrQEMU struct {
	Op  string
	Err error
}

func (e ErrQEMU) Error() string {
	return fmt.Sprintf("qemu %s: %v", e.Op, e.Err)
}

func (e ErrQEMU) Unwrap() error {
	return e.Err
}
