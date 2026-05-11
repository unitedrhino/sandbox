package runtime

import (
	"os/exec"
	"testing"
)

func TestWrapCommandWithPrlimit(t *testing.T) {
	tests := []struct {
		name     string
		limits   ResourceLimits
		wantPath string
		wantArgs []string
	}{
		{
			name:     "memory only",
			limits:   ResourceLimits{MemoryMB: 256},
			wantPath: "prlimit",
			wantArgs: []string{"prlimit", "--as=268435456", "--", "echo", "hello"},
		},
		{
			name:     "memory with nproc ignored",
			limits:   ResourceLimits{MemoryMB: 256, MaxProcesses: 32},
			wantPath: "prlimit",
			wantArgs: []string{"prlimit", "--as=268435456", "--", "echo", "hello"},
		},
		{
			name:     "no limits",
			limits:   ResourceLimits{},
			wantPath: "echo",
			wantArgs: []string{"echo", "hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("echo", "hello")
			err := WrapCommandWithPrlimit(cmd, tt.limits)
			if err != nil {
				t.Fatalf("WrapCommandWithPrlimit failed: %v", err)
			}
			// exec.Command 会解析绝对路径，所以只检查后缀
			if len(cmd.Path) < len(tt.wantPath) || cmd.Path[len(cmd.Path)-len(tt.wantPath):] != tt.wantPath {
				t.Errorf("Path = %q, want suffix %q", cmd.Path, tt.wantPath)
			}
			if len(cmd.Args) != len(tt.wantArgs) {
				t.Errorf("Args length = %d, want %d", len(cmd.Args), len(tt.wantArgs))
			}
			for i := range tt.wantArgs {
				if i >= len(cmd.Args) {
					t.Errorf("Args[%d] missing, want %q", i, tt.wantArgs[i])
					continue
				}
				// 同样处理绝对路径差异
				want := tt.wantArgs[i]
				got := cmd.Args[i]
				if len(got) >= len(want) && got[len(got)-len(want):] == want {
					continue
				}
				if got != want {
					t.Errorf("Args[%d] = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestIsCgroupWritable(t *testing.T) {
	// 该测试依赖运行环境，在 Docker 容器中应返回 false，在宿主机上可能返回 true
	// 这里仅验证函数不会 panic
	_ = isCgroupWritable()
}
