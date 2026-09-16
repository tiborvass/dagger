package git

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine/ebpf/nettracer"
	"github.com/dagger/dagger/engine/realm"
	"golang.org/x/sys/unix"
)

func runWithStandardUmask(ctx context.Context, cmd *exec.Cmd) error {
	errCh := make(chan error)

	go func() {
		defer close(errCh)
		runtime.LockOSThread()

		if err := unshareAndRun(ctx, cmd); err != nil {
			errCh <- err
		}
	}()

	return <-errCh
}

// unshareAndRun needs to be called in a locked thread.
func unshareAndRun(ctx context.Context, cmd *exec.Cmd) error {
	if err := syscall.Unshare(syscall.CLONE_FS); err != nil {
		return err
	}
	syscall.Umask(0022)
	cleanup, err := nettracer.PrepareCommand(cmd, realm.Userland)
	if err != nil {
		return err
	}
	return errors.Join(runProcessGroup(ctx, cmd), cleanup())
}

func runProcessGroup(ctx context.Context, cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = new(unix.SysProcAttr)
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = unix.SIGTERM
	if err := cmd.Start(); err != nil {
		return err
	}
	waitDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = unix.Kill(-cmd.Process.Pid, unix.SIGTERM)
			go func() {
				select {
				case <-waitDone:
				case <-time.After(10 * time.Second):
					_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
				}
			}()
		case <-waitDone:
		}
	}()
	err := cmd.Wait()
	close(waitDone)
	return err
}
