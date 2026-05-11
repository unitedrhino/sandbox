package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitee.com/unitedrhino/sandbox/internal/config"
	"gitee.com/unitedrhino/sandbox/internal/runtime"
)

type runtimeTestExecutor struct {
	meta          runtime.ExecutionMetadata
	result        runtime.ExecResult
	err           error
	skills        []runtime.SkillInfo
	activateSkill runtime.SkillInfo
	activateErr   error
}

func (e *runtimeTestExecutor) Execute(ctx context.Context, req runtime.ExecRequest) (runtime.ExecResult, error) {
	return e.result, e.err
}

func (e *runtimeTestExecutor) Describe(req runtime.ExecRequest) (runtime.ExecutionMetadata, error) {
	return e.meta, nil
}

func (e *runtimeTestExecutor) ListSkills() []runtime.SkillInfo {
	return append([]runtime.SkillInfo(nil), e.skills...)
}

func (e *runtimeTestExecutor) GetSkill(name string) (runtime.SkillInfo, bool) {
	for _, skill := range e.skills {
		if skill.Name == name {
			return skill, true
		}
	}
	return runtime.SkillInfo{}, false
}

func (e *runtimeTestExecutor) ActivateSkill(name, version string) (runtime.SkillInfo, error) {
	return e.activateSkill, e.activateErr
}

func newTestHandler(t *testing.T) (http.Handler, *runtime.Manager) {
	return newTestHandlerWithExecutor(t, runtime.NewOSExecutor(config.ExecOptions{}))
}

func newTestHandlerWithExecutor(t *testing.T, executor runtime.Executor) (http.Handler, *runtime.Manager) {
	t.Helper()
	workspace := t.TempDir()
	cfg := config.Config{
		RuntimeID:  "rt-1",
		TenantCode: "tenant-a",
		CloneID:    "clone-1",
		CloneKey:   "clone-key",
		Workspace:  workspace,
	}
	manager := runtime.NewManager(cfg, executor)
	return NewHTTPHandler(manager), manager
}

func TestStatusEndpoint(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/runtime/status", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["runtimeId"] != "rt-1" {
		t.Fatalf("runtimeId = %v", body["runtimeId"])
	}
}

func TestHealthEndpoints(t *testing.T) {
	h, _ := newTestHandler(t)

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s code = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestSkillsEndpoint(t *testing.T) {
	exec := &runtimeTestExecutor{skills: []runtime.SkillInfo{{Name: "ur-api", Source: "builtin", TrustLevel: "builtin", Actions: []string{"check", "get-self"}, Enabled: true, ScanVerdict: "safe"}}}
	h, _ := newTestHandlerWithExecutor(t, exec)
	req := httptest.NewRequest(http.MethodGet, "/runtime/skills", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("skills code = %d body=%s", rec.Code, rec.Body.String())
	}

	var skills []struct {
		Name       string `json:"name"`
		Source     string `json:"source"`
		TrustLevel string `json:"trustLevel"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &skills); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(skills) == 0 {
		t.Fatal("skills should not be empty")
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

func TestSkillDetailEndpoint(t *testing.T) {
	exec := &runtimeTestExecutor{skills: []runtime.SkillInfo{{Name: "ur-api", Source: "builtin", TrustLevel: "builtin", Actions: []string{"check", "get-self"}, Enabled: true, ScanVerdict: "safe"}}}
	h, _ := newTestHandlerWithExecutor(t, exec)
	req := httptest.NewRequest(http.MethodGet, "/runtime/skills/ur-api", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("skill detail code = %d body=%s", rec.Code, rec.Body.String())
	}

	var skill struct {
		Name       string   `json:"name"`
		Source     string   `json:"source"`
		TrustLevel string   `json:"trustLevel"`
		Actions    []string `json:"actions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &skill); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if skill.Name != "ur-api" {
		t.Fatalf("skill name = %q", skill.Name)
	}
	if skill.Source != "builtin" {
		t.Fatalf("skill source = %q", skill.Source)
	}
	if skill.TrustLevel != "builtin" {
		t.Fatalf("skill trust level = %q", skill.TrustLevel)
	}
	if len(skill.Actions) == 0 {
		t.Fatalf("actions should not be empty: %+v", skill)
	}
}

func TestSkillsEndpointIncludesBlockedMappedSkillMetadata(t *testing.T) {
	exec := &runtimeTestExecutor{
		skills: []runtime.SkillInfo{
			{
				Name:          "danger-skill",
				Source:        "mapped",
				TrustLevel:    "learned",
				Enabled:       false,
				ScanVerdict:   "dangerous",
				BlockedReason: "blocked by skill guard",
				Actions:       []string{"run"},
			},
		},
	}
	h, _ := newTestHandlerWithExecutor(t, exec)
	req := httptest.NewRequest(http.MethodGet, "/runtime/skills", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("skills code = %d body=%s", rec.Code, rec.Body.String())
	}

	var skills []struct {
		Name          string `json:"name"`
		Enabled       bool   `json:"enabled"`
		ScanVerdict   string `json:"scanVerdict"`
		BlockedReason string `json:"blockedReason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &skills); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills len = %d", len(skills))
	}
	if skills[0].Enabled {
		t.Fatalf("blocked skill should not be enabled: %+v", skills[0])
	}
	if skills[0].ScanVerdict != "dangerous" {
		t.Fatalf("scan verdict = %q", skills[0].ScanVerdict)
	}
}

func TestSkillsReloadEndpoint(t *testing.T) {
	exec := &runtimeTestExecutor{}
	h, _ := newTestHandlerWithExecutor(t, exec)
	req := httptest.NewRequest(http.MethodPost, "/runtime/skills/reload", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("skills reload code = %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("reload ok = %v", body["ok"])
	}
}

func TestSkillActivateEndpoint(t *testing.T) {
	exec := &runtimeTestExecutor{
		activateSkill: runtime.SkillInfo{
			Name:       "team-skill",
			Source:     "shared",
			TrustLevel: "distributed",
			Version:    "v2",
			Versions:   []string{"v1", "v2"},
			Enabled:    true,
		},
	}
	h, _ := newTestHandlerWithExecutor(t, exec)
	req := httptest.NewRequest(http.MethodPost, "/runtime/skills/team-skill/activate", bytes.NewBufferString(`{"version":"v2"}`))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("skill activate code = %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		OK    bool `json:"ok"`
		Skill struct {
			Version string `json:"version"`
		} `json:"skill"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.OK || body.Skill.Version != "v2" {
		t.Fatalf("activate body = %+v", body)
	}
}

func TestStartAndExecEndpoint(t *testing.T) {
	h, _ := newTestHandler(t)

	startReq := httptest.NewRequest(http.MethodPost, "/runtime/start", nil)
	startRec := httptest.NewRecorder()
	h.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start code = %d body=%s", startRec.Code, startRec.Body.String())
	}

	payload := map[string]any{
		"command":        []string{"/bin/sh", "-lc", "printf test-ok"},
		"timeoutSeconds": 2,
	}
	data, _ := json.Marshal(payload)
	execReq := httptest.NewRequest(http.MethodPost, "/runtime/exec", bytes.NewReader(data))
	execRec := httptest.NewRecorder()
	h.ServeHTTP(execRec, execReq)
	if execRec.Code != http.StatusOK {
		t.Fatalf("exec code = %d body=%s", execRec.Code, execRec.Body.String())
	}

	var body struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal(execRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Stdout != "test-ok" {
		t.Fatalf("stdout = %q", body.Stdout)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/runtime/stop", nil)
	stopRec := httptest.NewRecorder()
	h.ServeHTTP(stopRec, stopReq)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("stop code = %d body=%s", stopRec.Code, stopRec.Body.String())
	}

	time.Sleep(10 * time.Millisecond)
}

func TestSubmitTaskAndGetTaskEndpoints(t *testing.T) {
	h, _ := newTestHandler(t)

	startReq := httptest.NewRequest(http.MethodPost, "/runtime/start", nil)
	startRec := httptest.NewRecorder()
	h.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start code = %d body=%s", startRec.Code, startRec.Body.String())
	}

	payload := map[string]any{
		"command":        []string{"/bin/sh", "-lc", "printf task-ok"},
		"timeoutSeconds": 2,
	}
	data, _ := json.Marshal(payload)
	taskReq := httptest.NewRequest(http.MethodPost, "/runtime/tasks", bytes.NewReader(data))
	taskRec := httptest.NewRecorder()
	h.ServeHTTP(taskRec, taskReq)
	if taskRec.Code != http.StatusOK {
		t.Fatalf("task submit code = %d body=%s", taskRec.Code, taskRec.Body.String())
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(taskRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("task id should not be empty")
	}

	time.Sleep(100 * time.Millisecond)

	getReq := httptest.NewRequest(http.MethodGet, "/runtime/tasks/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("task get code = %d body=%s", getRec.Code, getRec.Body.String())
	}

	var got struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Result struct {
			Stdout string `json:"stdout"`
		} `json:"result"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("task id = %s want %s", got.ID, created.ID)
	}
	if got.Status != string(runtime.TaskSucceeded) {
		t.Fatalf("task status = %s", got.Status)
	}
	if got.Result.Stdout != "task-ok" {
		t.Fatalf("task stdout = %q", got.Result.Stdout)
	}
}

func TestTaskEndpointsExposeSkillMetadata(t *testing.T) {
	exec := &runtimeTestExecutor{
		meta: runtime.ExecutionMetadata{
			Backend:         "sandbox",
			Skill:           "demo-skill",
			SkillSource:     "mapped",
			SkillTrustLevel: "learned",
		},
		result: runtime.ExecResult{
			ExitCode: 0,
			Stdout:   "mapped-ok",
		},
	}
	h, _ := newTestHandlerWithExecutor(t, exec)

	startReq := httptest.NewRequest(http.MethodPost, "/runtime/start", nil)
	startRec := httptest.NewRecorder()
	h.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start code = %d body=%s", startRec.Code, startRec.Body.String())
	}

	taskReq := httptest.NewRequest(
		http.MethodPost,
		"/runtime/tasks",
		bytes.NewBufferString(`{"command":["claw-skill","demo-skill","run"],"timeoutSeconds":2}`),
	)
	taskRec := httptest.NewRecorder()
	h.ServeHTTP(taskRec, taskReq)
	if taskRec.Code != http.StatusOK {
		t.Fatalf("task submit code = %d body=%s", taskRec.Code, taskRec.Body.String())
	}

	var created struct {
		ID              string `json:"id"`
		Backend         string `json:"backend"`
		Skill           string `json:"skill"`
		SkillSource     string `json:"skillSource"`
		SkillTrustLevel string `json:"skillTrustLevel"`
	}
	if err := json.Unmarshal(taskRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if created.SkillSource != "mapped" || created.SkillTrustLevel != "learned" {
		t.Fatalf("created metadata = %+v", created)
	}

	time.Sleep(50 * time.Millisecond)

	getReq := httptest.NewRequest(http.MethodGet, "/runtime/tasks/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("task get code = %d body=%s", getRec.Code, getRec.Body.String())
	}

	var got struct {
		SkillSource     string `json:"skillSource"`
		SkillTrustLevel string `json:"skillTrustLevel"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	if got.SkillSource != "mapped" || got.SkillTrustLevel != "learned" {
		t.Fatalf("task metadata = %+v", got)
	}
}

func TestListTasksEndpoint(t *testing.T) {
	h, _ := newTestHandler(t)

	startReq := httptest.NewRequest(http.MethodPost, "/runtime/start", nil)
	startRec := httptest.NewRecorder()
	h.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start code = %d body=%s", startRec.Code, startRec.Body.String())
	}

	for _, body := range []string{
		`{"command":["/bin/sh","-lc","printf first"],"timeoutSeconds":2}`,
		`{"command":["/bin/sh","-lc","printf second"],"timeoutSeconds":2}`,
	} {
		taskReq := httptest.NewRequest(http.MethodPost, "/runtime/tasks", bytes.NewBufferString(body))
		taskRec := httptest.NewRecorder()
		h.ServeHTTP(taskRec, taskReq)
		if taskRec.Code != http.StatusOK {
			t.Fatalf("task submit code = %d body=%s", taskRec.Code, taskRec.Body.String())
		}
		time.Sleep(100 * time.Millisecond)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/runtime/tasks", nil)
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("task list code = %d body=%s", listRec.Code, listRec.Body.String())
	}

	var tasks []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("task list len = %d", len(tasks))
	}
}

func TestCancelTaskEndpoint(t *testing.T) {
	h, _ := newTestHandler(t)

	startReq := httptest.NewRequest(http.MethodPost, "/runtime/start", nil)
	startRec := httptest.NewRecorder()
	h.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start code = %d body=%s", startRec.Code, startRec.Body.String())
	}

	taskReq := httptest.NewRequest(
		http.MethodPost,
		"/runtime/tasks",
		bytes.NewBufferString(`{"command":["/bin/sh","-lc","sleep 10"],"timeoutSeconds":20}`),
	)
	taskRec := httptest.NewRecorder()
	h.ServeHTTP(taskRec, taskReq)
	if taskRec.Code != http.StatusOK {
		t.Fatalf("task submit code = %d body=%s", taskRec.Code, taskRec.Body.String())
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(taskRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/runtime/tasks/"+created.ID+"/cancel", nil)
	cancelRec := httptest.NewRecorder()
	h.ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("task cancel code = %d body=%s", cancelRec.Code, cancelRec.Body.String())
	}

	time.Sleep(100 * time.Millisecond)

	getReq := httptest.NewRequest(http.MethodGet, "/runtime/tasks/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("task get code = %d body=%s", getRec.Code, getRec.Body.String())
	}

	var got struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	if got.Status != string(runtime.TaskCanceled) {
		t.Fatalf("task status = %s", got.Status)
	}
}

func TestStreamEndpointHeaders(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/runtime/stream", nil)
	rec := httptest.NewRecorder()

	go h.ServeHTTP(rec, req)
	time.Sleep(50 * time.Millisecond)

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %s", got)
	}
}
