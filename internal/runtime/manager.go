package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"gitee.com/unitedrhino/sandbox/internal/config"
)

type Status string
type TaskStatus string

const (
	StatusCreated Status = "created"
	StatusReady   Status = "ready"
	StatusBusy    Status = "busy"
	StatusStopped Status = "stopped"
)

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCanceled  TaskStatus = "canceled"
)

type ExecRequest struct {
	Command   []string          `json:"command"`
	Env       map[string]string `json:"env,omitempty"`
	Timeout   time.Duration     `json:"-"`
	Workspace string            `json:"-"`
}

type ExecResult struct {
	ExitCode  int       `json:"exitCode"`
	Stdout    string    `json:"stdout"`
	Stderr    string    `json:"stderr"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
}

type StatusPayload struct {
	RuntimeID   string    `json:"runtimeId"`
	TenantCode  string    `json:"tenantCode"`
	CloneID     string    `json:"cloneId"`
	CloneKey    string    `json:"cloneKey"`
	SessionID   string    `json:"sessionId"`
	Workspace   string    `json:"workspace"`
	Status      Status    `json:"status"`
	StartedAt   time.Time `json:"startedAt"`
	LastEventAt time.Time `json:"lastEventAt"`
}

type Executor interface {
	Execute(ctx context.Context, req ExecRequest) (ExecResult, error)
}

type Task struct {
	ID              string      `json:"id"`
	Command         []string    `json:"command"`
	Backend         string      `json:"backend,omitempty"`
	Skill           string      `json:"skill,omitempty"`
	SkillSource     string      `json:"skillSource,omitempty"`
	SkillTrustLevel string      `json:"skillTrustLevel,omitempty"`
	Status          TaskStatus  `json:"status"`
	CreatedAt       time.Time   `json:"createdAt"`
	StartedAt       time.Time   `json:"startedAt,omitempty"`
	EndedAt         time.Time   `json:"endedAt,omitempty"`
	Error           string      `json:"error,omitempty"`
	Result          *ExecResult `json:"result,omitempty"`
}

type Event struct {
	Type string         `json:"type"`
	Time time.Time      `json:"time"`
	Data map[string]any `json:"data,omitempty"`
}

type executionDescriber interface {
	Describe(req ExecRequest) (ExecutionMetadata, error)
}

type skillCatalogProvider interface {
	ListSkills() []SkillInfo
}

type skillLookupProvider interface {
	GetSkill(name string) (SkillInfo, bool)
}

type skillReloadProvider interface {
	ReloadSkills() error
}

type skillActivateProvider interface {
	ActivateSkill(name, version string) (SkillInfo, error)
}

type Manager struct {
	cfg       config.Config
	executor  Executor
	startedAt time.Time

	mu            sync.Mutex
	status        Status
	lastEventAt   time.Time
	idleTimer     *time.Timer
	activeTasks   map[string]*Task
	taskCancels   map[string]context.CancelFunc
	subscribers   map[chan Event]struct{}
	tasks         map[string]*Task
	taskSeq       uint64
}

func NewManager(cfg config.Config, executor Executor) *Manager {
	now := time.Now()
	return &Manager{
		cfg:         cfg,
		executor:    executor,
		status:      StatusCreated,
		lastEventAt: now,
		subscribers: make(map[chan Event]struct{}),
		tasks:       make(map[string]*Task),
		activeTasks: make(map[string]*Task),
		taskCancels: make(map[string]context.CancelFunc),
	}
}

func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status == StatusReady || m.status == StatusBusy {
		m.scheduleIdleStopLocked()
		return nil
	}
	m.status = StatusReady
	m.startedAt = time.Now()
	m.lastEventAt = m.startedAt
	m.emitLocked(Event{
		Type: "runtime_started",
		Time: m.startedAt,
		Data: map[string]any{"runtimeId": m.cfg.RuntimeID, "cloneId": m.cfg.CloneID},
	})
	m.scheduleIdleStopLocked()
	return nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
	for _, cancel := range m.taskCancels {
		cancel()
	}
	for _, task := range m.activeTasks {
		if !isTerminalTaskStatus(task.Status) {
			task.Status = TaskCanceled
			task.EndedAt = time.Now()
			task.Error = context.Canceled.Error()
		}
	}
	m.activeTasks = make(map[string]*Task)
	m.taskCancels = make(map[string]context.CancelFunc)
	m.status = StatusStopped
	m.lastEventAt = time.Now()
	m.emitLocked(Event{
		Type: "runtime_stopped",
		Time: m.lastEventAt,
		Data: map[string]any{"runtimeId": m.cfg.RuntimeID},
	})
	return nil
}

func (m *Manager) Status() StatusPayload {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.status
	if len(m.activeTasks) > 0 {
		status = StatusBusy
	}
	return StatusPayload{
		RuntimeID:   m.cfg.RuntimeID,
		TenantCode:  m.cfg.TenantCode,
		CloneID:     m.cfg.CloneID,
		CloneKey:    m.cfg.CloneKey,
		SessionID:   m.cfg.SessionID,
		Workspace:   m.cfg.Workspace,
		Status:      status,
		StartedAt:   m.startedAt,
		LastEventAt: m.lastEventAt,
	}
}

func (m *Manager) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	task, err := m.SubmitTask(ctx, req)
	if err != nil {
		return ExecResult{}, err
	}

	for {
		current, ok := m.GetTask(task.ID)
		if !ok {
			return ExecResult{}, fmt.Errorf("task %s not found", task.ID)
		}
		switch current.Status {
		case TaskSucceeded, TaskFailed, TaskCanceled:
			if current.Result != nil {
				if current.Error != "" {
					return *current.Result, errors.New(current.Error)
				}
				return *current.Result, nil
			}
			if current.Error != "" {
				return ExecResult{}, errors.New(current.Error)
			}
			return ExecResult{}, nil
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}

const (
	maxConcurrentTasks = 32
	maxTaskTimeout     = 30 * time.Minute
	defaultTaskTimeout = 5 * time.Minute
)

func (m *Manager) SubmitTask(ctx context.Context, req ExecRequest) (*Task, error) {
	m.mu.Lock()
	if m.status == StatusStopped {
		m.mu.Unlock()
		return nil, errors.New("runtime stopped")
	}
	if len(m.activeTasks) >= maxConcurrentTasks {
		m.mu.Unlock()
		return nil, fmt.Errorf("too many concurrent tasks (max %d)", maxConcurrentTasks)
	}
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
	m.status = StatusBusy
	m.lastEventAt = time.Now()

	if req.Timeout <= 0 {
		req.Timeout = defaultTaskTimeout
	}
	if req.Timeout > maxTaskTimeout {
		req.Timeout = maxTaskTimeout
	}

	execCtx, cancel := context.WithCancel(ctx)
	taskID := strconv.FormatUint(atomic.AddUint64(&m.taskSeq, 1), 10)
	task := &Task{
		ID:        taskID,
		Command:   append([]string(nil), req.Command...),
		Status:    TaskPending,
		CreatedAt: time.Now(),
	}
	if describer, ok := m.executor.(executionDescriber); ok {
		if meta, err := describer.Describe(req); err == nil {
			task.Backend = meta.Backend
			task.Skill = meta.Skill
			task.SkillSource = meta.SkillSource
			task.SkillTrustLevel = meta.SkillTrustLevel
		}
	}
	m.tasks[taskID] = task
	m.activeTasks[taskID] = task
	m.taskCancels[taskID] = cancel
	m.emitLocked(Event{
		Type: "exec_started",
		Time: m.lastEventAt,
		Data: map[string]any{
			"taskId":          taskID,
			"command":         req.Command,
			"status":          task.Status,
			"backend":         task.Backend,
			"skill":           task.Skill,
			"skillSource":     task.SkillSource,
			"skillTrustLevel": task.SkillTrustLevel,
		},
	})
	m.mu.Unlock()

	go m.runTask(execCtx, taskID, req)
	return cloneTask(task), nil
}

func (m *Manager) runTask(execCtx context.Context, taskID string, req ExecRequest) {
	m.mu.Lock()
	task, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	task.Status = TaskRunning
	task.StartedAt = time.Now()
	m.lastEventAt = task.StartedAt
	m.emitLocked(Event{
		Type: "task_running",
		Time: task.StartedAt,
		Data: map[string]any{
			"taskId":          taskID,
			"command":         req.Command,
			"status":          task.Status,
			"backend":         task.Backend,
			"skill":           task.Skill,
			"skillSource":     task.SkillSource,
			"skillTrustLevel": task.SkillTrustLevel,
		},
	})
	m.mu.Unlock()

	req.Workspace = m.cfg.Workspace
	if req.Env == nil {
		req.Env = make(map[string]string, len(m.cfg.TaskBaseEnv))
	}
	for k, v := range m.cfg.TaskBaseEnv {
		if _, ok := req.Env[k]; !ok {
			req.Env[k] = v
		}
	}
	result, err := m.executor.Execute(execCtx, req)

	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok = m.tasks[taskID]
	if !ok {
		return
	}
	task.EndedAt = time.Now()
	task.Result = &result
	task.Error = errString(err)
	switch {
	case errors.Is(err, context.Canceled):
		task.Status = TaskCanceled
	case err != nil:
		task.Status = TaskFailed
	default:
		task.Status = TaskSucceeded
	}

	delete(m.activeTasks, taskID)
	delete(m.taskCancels, taskID)

	if len(m.activeTasks) == 0 && m.status != StatusStopped {
		m.status = StatusReady
	}
	m.lastEventAt = time.Now()
	m.emitLocked(Event{
		Type: "exec_finished",
		Time: time.Now(),
		Data: map[string]any{
			"taskId":          taskID,
			"command":         req.Command,
			"exitCode":        result.ExitCode,
			"error":           errString(err),
			"status":          task.Status,
			"backend":         task.Backend,
			"skill":           task.Skill,
			"skillSource":     task.SkillSource,
			"skillTrustLevel": task.SkillTrustLevel,
		},
	})
	if len(m.activeTasks) == 0 {
		m.scheduleIdleStopLocked()
	}
}

func (m *Manager) GetTask(taskID string) (*Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[taskID]
	if !ok {
		return nil, false
	}
	return cloneTask(task), true
}

func (m *Manager) ListTasks() []*Task {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]*Task, 0, len(m.tasks))
	for _, task := range m.tasks {
		out = append(out, cloneTask(task))
	}
	slices.SortFunc(out, func(a, b *Task) int {
		return stringsCompareNumericID(a.ID, b.ID)
	})
	return out
}

func (m *Manager) ListSkills() []SkillInfo {
	if provider, ok := m.executor.(skillCatalogProvider); ok {
		return provider.ListSkills()
	}
	return nil
}

func (m *Manager) GetSkill(name string) (SkillInfo, bool) {
	if provider, ok := m.executor.(skillLookupProvider); ok {
		return provider.GetSkill(name)
	}
	return SkillInfo{}, false
}

func (m *Manager) ReloadSkills() error {
	if provider, ok := m.executor.(skillReloadProvider); ok {
		return provider.ReloadSkills()
	}
	return nil
}

func (m *Manager) ActivateSkill(name, version string) (SkillInfo, error) {
	if provider, ok := m.executor.(skillActivateProvider); ok {
		return provider.ActivateSkill(name, version)
	}
	return SkillInfo{}, fmt.Errorf("skill activation unsupported")
}

func (m *Manager) CancelTask(taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	if isTerminalTaskStatus(task.Status) {
		return nil
	}
	cancel, ok := m.taskCancels[taskID]
	if !ok {
		return fmt.Errorf("task %s is not cancelable", taskID)
	}
	cancel()
	return nil
}

func (m *Manager) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 16)
	m.mu.Lock()
	m.subscribers[ch] = struct{}{}
	m.mu.Unlock()

	return ch, func() {
		m.mu.Lock()
		if _, ok := m.subscribers[ch]; ok {
			delete(m.subscribers, ch)
			close(ch)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) emitLocked(event Event) {
	for ch := range m.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (m *Manager) scheduleIdleStopLocked() {
	if m.cfg.IdleTimeout <= 0 {
		return
	}
	if m.status != StatusReady || len(m.activeTasks) > 0 {
		return
	}
	if m.idleTimer != nil {
		m.idleTimer.Stop()
	}
	timeout := m.cfg.IdleTimeout
	runtimeID := m.cfg.RuntimeID
	m.idleTimer = time.AfterFunc(timeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.status != StatusReady || len(m.activeTasks) > 0 {
			return
		}
		if time.Since(m.lastEventAt) < timeout {
			m.scheduleIdleStopLocked()
			return
		}
		m.status = StatusStopped
		m.lastEventAt = time.Now()
		m.emitLocked(Event{
			Type: "runtime_stopped",
			Time: m.lastEventAt,
			Data: map[string]any{
				"runtimeId": runtimeID,
				"reason":    "idle_timeout",
			},
		})
		m.idleTimer = nil
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}

func cloneTask(in *Task) *Task {
	if in == nil {
		return nil
	}
	out := *in
	if in.Result != nil {
		result := *in.Result
		out.Result = &result
	}
	out.Command = append([]string(nil), in.Command...)
	return &out
}

func isTerminalTaskStatus(status TaskStatus) bool {
	switch status {
	case TaskSucceeded, TaskFailed, TaskCanceled:
		return true
	default:
		return false
	}
}

func stringsCompareNumericID(a, b string) int {
	ai, aErr := strconv.ParseUint(a, 10, 64)
	bi, bErr := strconv.ParseUint(b, 10, 64)
	switch {
	case aErr == nil && bErr == nil:
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		default:
			return 0
		}
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
