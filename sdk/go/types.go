package sandboxsdk

import "time"

type Status string

const (
	StatusCreated Status = "created"
	StatusReady   Status = "ready"
	StatusBusy    Status = "busy"
	StatusStopped Status = "stopped"
)

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCanceled  TaskStatus = "canceled"
)

type SkillSource string

const (
	SkillSourceBuiltin SkillSource = "builtin"
	SkillSourceShared  SkillSource = "shared"
	SkillSourceMapped  SkillSource = "mapped"
)

type SkillTrustLevel string

const (
	SkillTrustLevelBuiltin     SkillTrustLevel = "builtin"
	SkillTrustLevelDistributed SkillTrustLevel = "distributed"
	SkillTrustLevelLearned     SkillTrustLevel = "learned"
)

type HealthResponse struct {
	OK bool `json:"ok"`
}

type ReadyzResponse struct {
	OK     bool   `json:"ok"`
	Status string `json:"status"`
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

type ExecRequest struct {
	Command        []string          `json:"command"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
}

type ExecResult struct {
	ExitCode  int       `json:"exitCode"`
	Stdout    string    `json:"stdout"`
	Stderr    string    `json:"stderr"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
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

type SkillInfo struct {
	Name          string   `json:"name"`
	Source        string   `json:"source,omitempty"`
	TrustLevel    string   `json:"trustLevel,omitempty"`
	RootDir       string   `json:"rootDir,omitempty"`
	ActiveDir     string   `json:"activeDir,omitempty"`
	CLIPath       string   `json:"cliPath,omitempty"`
	Actions       []string `json:"actions,omitempty"`
	Version       string   `json:"version,omitempty"`
	Versions      []string `json:"versions,omitempty"`
	Enabled       bool     `json:"enabled"`
	ScanVerdict   string   `json:"scanVerdict,omitempty"`
	BlockedReason string   `json:"blockedReason,omitempty"`
}

type Event struct {
	Type string         `json:"type"`
	Time time.Time      `json:"time"`
	Data map[string]any `json:"data,omitempty"`
}

type SkillActivateRequest struct {
	Version string `json:"version"`
}

type SkillActivateResponse struct {
	OK    bool      `json:"ok"`
	Skill SkillInfo `json:"skill"`
}

type ReloadSkillsResponse struct {
	OK     bool        `json:"ok"`
	Skills []SkillInfo `json:"skills"`
}

type StartStopResponse struct {
	OK     bool          `json:"ok"`
	Status StatusPayload `json:"status"`
}
