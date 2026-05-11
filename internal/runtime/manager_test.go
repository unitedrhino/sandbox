package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gitee.com/unitedrhino/sandbox/internal/config"
)

type fakeExecutor struct {
	result ExecResult
	err    error
	last   ExecRequest
	run    func(ctx context.Context, req ExecRequest) (ExecResult, error)
}

func (f *fakeExecutor) Execute(ctx context.Context, req ExecRequest) (ExecResult, error) {
	f.last = req
	if f.run != nil {
		return f.run(ctx, req)
	}
	return f.result, f.err
}

type fakeRuntimeExecutor struct {
	fakeExecutor
	meta      ExecutionMetadata
	skills    []SkillInfo
	reloadErr error
	reloaded  bool
}

func (f *fakeRuntimeExecutor) Describe(req ExecRequest) (ExecutionMetadata, error) {
	return f.meta, nil
}

func (f *fakeRuntimeExecutor) ListSkills() []SkillInfo {
	return slices.Clone(f.skills)
}

func (f *fakeRuntimeExecutor) ReloadSkills() error {
	f.reloaded = true
	return f.reloadErr
}

func TestManager_StartAndStatus(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	m := NewManager(cfg, &fakeExecutor{})

	status := m.Status()
	if status.Status != StatusCreated {
		t.Fatalf("initial status = %s", status.Status)
	}

	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	status = m.Status()
	if status.Status != StatusReady {
		t.Fatalf("status after start = %s", status.Status)
	}
	if status.RuntimeID != "rt-1" || status.CloneID != "clone-1" {
		t.Fatalf("unexpected status payload: %+v", status)
	}
}

func TestManager_Exec(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	exec := &fakeExecutor{
		result: ExecResult{
			ExitCode:  0,
			Stdout:    "ok\n",
			StartedAt: time.Unix(10, 0),
			EndedAt:   time.Unix(11, 0),
		},
	}
	m := NewManager(cfg, exec)
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	result, err := m.Exec(context.Background(), ExecRequest{
		Command: []string{"echo", "ok"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.Stdout != "ok\n" || result.ExitCode != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(exec.last.Command) != 2 || exec.last.Command[0] != "echo" {
		t.Fatalf("executor did not receive request: %+v", exec.last)
	}
	if exec.last.Workspace != "/workspace" {
		t.Fatalf("workspace not propagated: %+v", exec.last)
	}
}

func TestManager_InjectsTaskBaseEnv(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
		TaskBaseEnv: map[string]string{
			"OPENAI_API_KEY": "sk-test",
			"OPENAI_MODEL":   "gpt-test",
		},
	}
	exec := &fakeExecutor{result: ExecResult{ExitCode: 0}}
	m := NewManager(cfg, exec)
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if _, err := m.Exec(context.Background(), ExecRequest{
		Command: []string{"echo", "ok"},
		Env: map[string]string{
			"OPENAI_MODEL": "override-model",
		},
	}); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	if exec.last.Env["OPENAI_API_KEY"] != "sk-test" {
		t.Fatalf("OPENAI_API_KEY = %q", exec.last.Env["OPENAI_API_KEY"])
	}
	if exec.last.Env["OPENAI_MODEL"] != "override-model" {
		t.Fatalf("OPENAI_MODEL = %q", exec.last.Env["OPENAI_MODEL"])
	}
}

func TestManager_ConcurrentExec(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	block := make(chan struct{})
	exec := &fakeExecutor{
		run: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
			<-block
			return ExecResult{Stdout: req.Command[1]}, nil
		},
	}
	m := NewManager(cfg, exec)
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// 同时提交两个任务，应该都成功
	resultCh := make(chan ExecResult, 2)
	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			res, err := m.Exec(context.Background(), ExecRequest{
				Command: []string{"echo", fmt.Sprintf("task-%d", idx)},
				Timeout: 2 * time.Second,
			})
			if err != nil {
				errCh <- err
				return
			}
			resultCh <- res
		}(i)
	}

	time.Sleep(100 * time.Millisecond)
	// 两个任务都应该在等待 block
	status := m.Status()
	if status.Status != StatusBusy {
		t.Fatalf("expected busy, got %s", status.Status)
	}
	close(block)

	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			t.Fatalf("concurrent exec failed: %v", err)
		case res := <-resultCh:
			if res.Stdout != "task-0" && res.Stdout != "task-1" {
				t.Fatalf("unexpected result: %q", res.Stdout)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting concurrent exec results")
		}
	}
}

func TestManager_StopCancelsRunningExec(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	exec := &fakeExecutor{
		run: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
			<-ctx.Done()
			return ExecResult{}, ctx.Err()
		},
	}
	m := NewManager(cfg, exec)
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := m.Exec(context.Background(), ExecRequest{
			Command: []string{"sleep", "10"},
			Timeout: 10 * time.Second,
		})
		errCh <- err
	}()

	time.Sleep(100 * time.Millisecond)
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil || (!errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), context.Canceled.Error())) {
			t.Fatalf("expected context canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("running exec was not canceled by Stop()")
	}
}

func TestManager_Events(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	m := NewManager(cfg, &fakeExecutor{
		result: ExecResult{ExitCode: 0, Stdout: "ok", StartedAt: time.Now(), EndedAt: time.Now()},
	})
	events, unsubscribe := m.Subscribe()
	defer unsubscribe()

	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := m.Exec(context.Background(), ExecRequest{Command: []string{"echo", "ok"}}); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	var got []string
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		timeout := time.After(2 * time.Second)
		for len(got) < 4 {
			select {
			case event := <-events:
				mu.Lock()
				got = append(got, event.Type)
				mu.Unlock()
			case <-timeout:
				close(done)
				return
			}
		}
		close(done)
	}()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(got, []string{"runtime_started", "exec_started", "task_running", "exec_finished"}) &&
		!slices.Equal(got, []string{"runtime_started", "exec_started", "task_running", "exec_finished", "runtime_stopped"}) {
		t.Fatalf("unexpected event sequence: %v", got)
	}
}

func TestManager_AutoStopsAfterIdleTimeout(t *testing.T) {
	cfg := config.Config{
		RuntimeID:    "rt-idle",
		TenantCode:   "tenant-a",
		CloneID:      "clone-1",
		CloneKey:     "clone-key",
		Workspace:    "/workspace",
		IdleTimeout:  50 * time.Millisecond,
	}
	m := NewManager(cfg, &fakeExecutor{
		result: ExecResult{ExitCode: 0, Stdout: "ok", StartedAt: time.Now(), EndedAt: time.Now()},
	})
	events, unsubscribe := m.Subscribe()
	defer unsubscribe()

	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	time.Sleep(120 * time.Millisecond)

	status := m.Status()
	if status.Status != StatusStopped {
		t.Fatalf("status = %s, want stopped", status.Status)
	}

	var got Event
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case evt := <-events:
			if evt.Type == "runtime_stopped" {
				got = evt
				goto done
			}
		case <-timeout:
			t.Fatal("timeout waiting runtime_stopped event")
		}
	}
done:
	if got.Data["reason"] != "idle_timeout" {
		t.Fatalf("runtime_stopped reason = %v", got.Data["reason"])
	}
}

func TestManager_StartReadyStateReschedulesIdleTimeout(t *testing.T) {
	cfg := config.Config{
		RuntimeID:   "rt-idle-resume",
		TenantCode:  "tenant-a",
		CloneID:     "clone-1",
		CloneKey:    "clone-key",
		Workspace:   "/workspace",
		IdleTimeout: 50 * time.Millisecond,
	}
	m := NewManager(cfg, &fakeExecutor{
		result: ExecResult{ExitCode: 0, Stdout: "ok", StartedAt: time.Now(), EndedAt: time.Now()},
	})
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := m.Start(); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	if status := m.Status(); status.Status != StatusStopped {
		t.Fatalf("status = %s, want stopped", status.Status)
	}
}

func TestManager_StartCanRecoverFromStopped(t *testing.T) {
	cfg := config.Config{
		RuntimeID:   "rt-restart",
		TenantCode:  "tenant-a",
		CloneID:     "clone-1",
		CloneKey:    "clone-key",
		Workspace:   "/workspace",
		IdleTimeout: 50 * time.Millisecond,
	}
	m := NewManager(cfg, &fakeExecutor{
		result: ExecResult{ExitCode: 0, Stdout: "ok", StartedAt: time.Now(), EndedAt: time.Now()},
	})
	if err := m.Start(); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if status := m.Status(); status.Status != StatusStopped {
		t.Fatalf("status after stop = %s", status.Status)
	}
	if err := m.Start(); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if status := m.Status(); status.Status != StatusReady {
		t.Fatalf("status after restart = %s", status.Status)
	}
}

func TestManager_SubmitTaskAndGetTask(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	started := make(chan struct{})
	release := make(chan struct{})
	exec := &fakeExecutor{
		run: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
			close(started)
			<-release
			return ExecResult{
				ExitCode:  0,
				Stdout:    "done",
				StartedAt: time.Now(),
				EndedAt:   time.Now(),
			}, nil
		},
	}
	m := NewManager(cfg, exec)
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	task, err := m.SubmitTask(context.Background(), ExecRequest{
		Command: []string{"echo", "ok"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("SubmitTask() error = %v", err)
	}
	if task.ID == "" {
		t.Fatal("task id should not be empty")
	}
	if task.Status != TaskPending && task.Status != TaskRunning {
		t.Fatalf("unexpected initial task status: %s", task.Status)
	}

	<-started
	current, ok := m.GetTask(task.ID)
	if !ok {
		t.Fatalf("GetTask(%s) not found", task.ID)
	}
	if current.Status != TaskRunning {
		t.Fatalf("expected running, got %s", current.Status)
	}

	close(release)
	time.Sleep(100 * time.Millisecond)

	doneTask, ok := m.GetTask(task.ID)
	if !ok {
		t.Fatalf("GetTask(%s) not found after finish", task.ID)
	}
	if doneTask.Status != TaskSucceeded {
		t.Fatalf("expected succeeded, got %s", doneTask.Status)
	}
	if doneTask.Result == nil || doneTask.Result.Stdout != "done" {
		t.Fatalf("unexpected task result: %+v", doneTask.Result)
	}
}

func TestManager_StopMarksRunningTaskCanceled(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	started := make(chan struct{})
	exec := &fakeExecutor{
		run: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
			close(started)
			<-ctx.Done()
			return ExecResult{}, ctx.Err()
		},
	}
	m := NewManager(cfg, exec)
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	task, err := m.SubmitTask(context.Background(), ExecRequest{
		Command: []string{"sleep", "10"},
		Timeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("SubmitTask() error = %v", err)
	}

	<-started
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	current, ok := m.GetTask(task.ID)
	if !ok {
		t.Fatalf("GetTask(%s) not found", task.ID)
	}
	if current.Status != TaskCanceled {
		t.Fatalf("expected canceled, got %s", current.Status)
	}
}

func TestManager_ListTasks(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	started := make(chan struct{})
	release := make(chan struct{})
	exec := &fakeExecutor{
		run: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
			if len(req.Command) > 2 && req.Command[2] == "sleeping" {
				close(started)
				<-release
			}
			return ExecResult{ExitCode: 0, Stdout: req.Command[len(req.Command)-1]}, nil
		},
	}
	m := NewManager(cfg, exec)
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	first, err := m.SubmitTask(context.Background(), ExecRequest{
		Command: []string{"/bin/sh", "-lc", "sleeping"},
	})
	if err != nil {
		t.Fatalf("SubmitTask(first) error = %v", err)
	}
	<-started
	close(release)
	time.Sleep(100 * time.Millisecond)

	second, err := m.SubmitTask(context.Background(), ExecRequest{
		Command: []string{"/bin/sh", "-lc", "second"},
	})
	if err != nil {
		t.Fatalf("SubmitTask(second) error = %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	tasks := m.ListTasks()
	if len(tasks) != 2 {
		t.Fatalf("ListTasks() len = %d", len(tasks))
	}
	ids := []string{tasks[0].ID, tasks[1].ID}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{first.ID, second.ID}) {
		t.Fatalf("ListTasks() ids = %v want [%s %s]", ids, first.ID, second.ID)
	}
}

func TestManager_CancelTask(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	started := make(chan struct{})
	exec := &fakeExecutor{
		run: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
			close(started)
			<-ctx.Done()
			return ExecResult{}, ctx.Err()
		},
	}
	m := NewManager(cfg, exec)
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	task, err := m.SubmitTask(context.Background(), ExecRequest{
		Command: []string{"sleep", "10"},
		Timeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("SubmitTask() error = %v", err)
	}
	<-started

	if err := m.CancelTask(task.ID); err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	current, ok := m.GetTask(task.ID)
	if !ok {
		t.Fatalf("GetTask(%s) not found", task.ID)
	}
	if current.Status != TaskCanceled {
		t.Fatalf("expected canceled, got %s", current.Status)
	}
	if current.Error == "" || !strings.Contains(current.Error, context.Canceled.Error()) {
		t.Fatalf("expected canceled error, got %q", current.Error)
	}
}

func TestManager_TaskEventsIncludeTaskContext(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	started := make(chan struct{})
	release := make(chan struct{})
	exec := &fakeExecutor{
		run: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
			close(started)
			<-release
			return ExecResult{ExitCode: 0, Stdout: "ok"}, nil
		},
	}
	m := NewManager(cfg, exec)
	events, unsubscribe := m.Subscribe()
	defer unsubscribe()
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	task, err := m.SubmitTask(context.Background(), ExecRequest{
		Command: []string{"/bin/sh", "-lc", "printf ok"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("SubmitTask() error = %v", err)
	}
	<-started
	close(release)

	var sawStarted, sawFinished bool
	timeout := time.After(2 * time.Second)
	for !(sawStarted && sawFinished) {
		select {
		case event := <-events:
			taskID, _ := event.Data["taskId"].(string)
			status, _ := event.Data["status"].(TaskStatus)
			if status == "" {
				if s, ok := event.Data["status"].(string); ok {
					status = TaskStatus(s)
				}
			}
			if taskID != task.ID {
				continue
			}
			switch event.Type {
			case "exec_started":
				sawStarted = status == TaskPending
			case "exec_finished":
				sawFinished = status == TaskSucceeded
			}
		case <-timeout:
			t.Fatalf("did not observe expected task events")
		}
	}
}

func TestManager_TaskMetadataFromRuntimeExecutor(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	exec := &fakeRuntimeExecutor{
		fakeExecutor: fakeExecutor{
			result: ExecResult{ExitCode: 0, Stdout: "ok"},
		},
		meta: ExecutionMetadata{
			Backend:         "sandbox",
			Skill:           "ur-api",
			SkillSource:     "builtin",
			SkillTrustLevel: "builtin",
		},
	}
	m := NewManager(cfg, exec)
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	task, err := m.SubmitTask(context.Background(), ExecRequest{
		Command: []string{"claw-skill", "ur-api", "get-self"},
	})
	if err != nil {
		t.Fatalf("SubmitTask() error = %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	got, ok := m.GetTask(task.ID)
	if !ok {
		t.Fatalf("GetTask(%s) not found", task.ID)
	}
	if got.Backend != "sandbox" {
		t.Fatalf("backend = %q", got.Backend)
	}
	if got.Skill != "ur-api" {
		t.Fatalf("skill = %q", got.Skill)
	}
	if got.SkillSource != "builtin" {
		t.Fatalf("skillSource = %q", got.SkillSource)
	}
	if got.SkillTrustLevel != "builtin" {
		t.Fatalf("skillTrustLevel = %q", got.SkillTrustLevel)
	}
}

func TestManager_TaskEventsIncludeSkillSourceAndTrustLevel(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	started := make(chan struct{})
	release := make(chan struct{})
	exec := &fakeRuntimeExecutor{
		fakeExecutor: fakeExecutor{
			run: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
				close(started)
				<-release
				return ExecResult{ExitCode: 0, Stdout: "ok"}, nil
			},
		},
		meta: ExecutionMetadata{
			Backend:         "sandbox",
			Skill:           "demo-skill",
			SkillSource:     "mapped",
			SkillTrustLevel: "learned",
		},
	}
	m := NewManager(cfg, exec)
	events, unsubscribe := m.Subscribe()
	defer unsubscribe()
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	_, err := m.SubmitTask(context.Background(), ExecRequest{
		Command: []string{"claw-skill", "demo-skill", "run"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("SubmitTask() error = %v", err)
	}
	<-started
	close(release)

	timeout := time.After(2 * time.Second)
	var sawStart, sawFinish bool
	for !(sawStart && sawFinish) {
		select {
		case event := <-events:
			switch event.Type {
			case "exec_started", "exec_finished":
				if event.Data["skillSource"] != "mapped" {
					t.Fatalf("%s skillSource = %v", event.Type, event.Data["skillSource"])
				}
				if event.Data["skillTrustLevel"] != "learned" {
					t.Fatalf("%s skillTrustLevel = %v", event.Type, event.Data["skillTrustLevel"])
				}
				if event.Type == "exec_started" {
					sawStart = true
				}
				if event.Type == "exec_finished" {
					sawFinish = true
				}
			}
		case <-timeout:
			t.Fatal("did not observe task events with skill source/trust level")
		}
	}
}

func TestManager_ListSkills(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	exec := &fakeRuntimeExecutor{
		skills: []SkillInfo{
			{Name: "ur-api", Source: "builtin", TrustLevel: "builtin", Actions: []string{"check", "get-self"}},
		},
	}
	m := NewManager(cfg, exec)

	skills := m.ListSkills()
	if len(skills) != 1 {
		t.Fatalf("skills len = %d", len(skills))
	}
	if skills[0].Name != "ur-api" {
		t.Fatalf("skill name = %q", skills[0].Name)
	}
	if skills[0].Source != "builtin" {
		t.Fatalf("skill source = %q", skills[0].Source)
	}
	if skills[0].TrustLevel != "builtin" {
		t.Fatalf("skill trust level = %q", skills[0].TrustLevel)
	}
}

func TestManager_ReloadSkills(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	exec := &fakeRuntimeExecutor{}
	m := NewManager(cfg, exec)

	if err := m.ReloadSkills(); err != nil {
		t.Fatalf("ReloadSkills() error = %v", err)
	}
	if !exec.reloaded {
		t.Fatal("executor should be reloaded")
	}
}

func TestOSExecutor_CancelKillsProcessGroup(t *testing.T) {
	workspace := t.TempDir()
	exec := NewOSExecutor(config.ExecOptions{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := exec.Execute(ctx, ExecRequest{
			Command:   []string{"/bin/sh", "-lc", "sleep 10"},
			Timeout:   20 * time.Second,
			Workspace: workspace,
		})
		done <- err
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OSExecutor did not cancel shell process group")
	}
}

func TestManager_ConcurrentTenTasks(t *testing.T) {
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  "/workspace",
	}
	block := make(chan struct{})
	exec := &fakeExecutor{
		run: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
			<-block
			return ExecResult{Stdout: req.Command[1]}, nil
		},
	}
	m := NewManager(cfg, exec)
	if err := m.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	const n = 10
	resultCh := make(chan ExecResult, n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			res, err := m.Exec(context.Background(), ExecRequest{
				Command: []string{"echo", fmt.Sprintf("task-%d", idx)},
				Timeout: 5 * time.Second,
			})
			if err != nil {
				errCh <- err
				return
			}
			resultCh <- res
		}(i)
	}

	time.Sleep(200 * time.Millisecond)
	status := m.Status()
	if status.Status != StatusBusy {
		t.Fatalf("expected busy with %d tasks, got %s", n, status.Status)
	}
	close(block)

	for i := 0; i < n; i++ {
		select {
		case err := <-errCh:
			t.Fatalf("concurrent exec failed: %v", err)
		case <-resultCh:
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting result %d/%d", i+1, n)
		}
	}

	// 所有任务完成后状态应恢复为 Ready
	time.Sleep(100 * time.Millisecond)
	status = m.Status()
	if status.Status != StatusReady {
		t.Fatalf("expected ready after all tasks done, got %s", status.Status)
	}
}
