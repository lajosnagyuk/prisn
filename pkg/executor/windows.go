//go:build windows

package executor

import (
	"context"
	"errors"
)

// windowsExecutor is a placeholder for Windows support.
// Windows is not currently a supported platform for prisn workers.
type windowsExecutor struct{}

func newPlatformExecutor() Executor {
	return &windowsExecutor{}
}

func (e *windowsExecutor) Platform() string {
	return "windows"
}

func (e *windowsExecutor) Capabilities() Capabilities {
	return Capabilities{
		MaxIsolationLevel: IsolationNone,
	}
}

func (e *windowsExecutor) Execute(ctx context.Context, cfg Config) Result {
	return Result{
		Error: errors.New("windows is not currently supported as a worker platform; use WSL2 or a Linux VM"),
	}
}
