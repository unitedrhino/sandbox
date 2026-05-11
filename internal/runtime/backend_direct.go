package runtime

import (
	"context"

	"gitee.com/unitedrhino/sandbox/internal/config"
)

type DirectBackend struct {
	opts config.ExecOptions
}

func NewDirectBackend(opts config.ExecOptions) *DirectBackend {
	return &DirectBackend{opts: opts}
}

func (b *DirectBackend) Name() string {
	return "direct"
}

func (b *DirectBackend) Execute(ctx context.Context, spec ExecutionSpec) (ExecResult, error) {
	return executeCommand(ctx, spec, b.opts, backendFeatures{})
}
