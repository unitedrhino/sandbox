package runtime

import (
	"context"

	"gitee.com/unitedrhino/sandbox/internal/config"
)

type SandboxBackend struct {
	opts config.ExecOptions
}

func NewSandboxBackend(opts config.ExecOptions) *SandboxBackend {
	return &SandboxBackend{opts: opts}
}

func (b *SandboxBackend) Name() string {
	return "sandbox"
}

func (b *SandboxBackend) Execute(ctx context.Context, spec ExecutionSpec) (ExecResult, error) {
	return executeCommand(ctx, spec, b.opts, backendFeatures{
		mountSandbox: b.opts.MountSandboxEnable,
		network:      b.opts.SandboxNetEnable,
		resources:    b.opts.CPUQuota > 0 || b.opts.MemoryLimitMB > 0 || b.opts.MaxProcesses > 0,
	})
}
