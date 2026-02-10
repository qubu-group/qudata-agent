package domain

import "fmt"

type ErrInstanceAlreadyRunning struct{}

func (e ErrInstanceAlreadyRunning) Error() string {
	return "instance is already running"
}

type ErrNoInstanceRunning struct{}

func (e ErrNoInstanceRunning) Error() string {
	return "no instance is currently running"
}

type ErrUnknownCommand struct {
	Command string
}

func (e ErrUnknownCommand) Error() string {
	return fmt.Sprintf("unknown command: %s", e.Command)
}

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
