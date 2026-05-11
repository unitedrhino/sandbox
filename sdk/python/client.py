"""Sandbox HTTP API Python SDK"""

import json
import time
from dataclasses import dataclass, field
from typing import Any, Dict, Generator, List, Optional
from urllib.parse import urljoin

import requests


class SandboxAPIError(Exception):
    """Sandbox API 调用异常"""

    def __init__(self, status_code: int, message: str):
        self.status_code = status_code
        self.message = message
        super().__init__(f"[{status_code}] {message}")


@dataclass
class HealthResponse:
    ok: bool


@dataclass
class ReadyzResponse:
    ok: bool
    status: str


@dataclass
class StatusPayload:
    runtimeId: str
    tenantCode: str
    cloneId: str
    cloneKey: str
    sessionId: str
    workspace: str
    status: str
    startedAt: str
    lastEventAt: str


@dataclass
class ExecResult:
    exitCode: int
    stdout: str
    stderr: str
    startedAt: str
    endedAt: str


@dataclass
class Task:
    id: str
    command: List[str]
    status: str
    createdAt: str
    backend: Optional[str] = None
    skill: Optional[str] = None
    skillSource: Optional[str] = None
    skillTrustLevel: Optional[str] = None
    startedAt: Optional[str] = None
    endedAt: Optional[str] = None
    error: Optional[str] = None
    result: Optional[ExecResult] = None


@dataclass
class SkillInfo:
    name: str
    source: Optional[str] = None
    trustLevel: Optional[str] = None
    rootDir: Optional[str] = None
    activeDir: Optional[str] = None
    cliPath: Optional[str] = None
    actions: List[str] = field(default_factory=list)
    version: Optional[str] = None
    versions: List[str] = field(default_factory=list)
    enabled: bool = False
    scanVerdict: Optional[str] = None
    blockedReason: Optional[str] = None


@dataclass
class Event:
    type: str
    time: str
    data: Optional[Dict[str, Any]] = None


class SandboxClient:
    """Sandbox HTTP API 客户端"""

    def __init__(self, base_url: str, timeout: float = 30.0):
        self.base_url = base_url.rstrip("/") + "/"
        self.timeout = timeout
        self.session = requests.Session()

    def _request(self, method: str, path: str, json_body: Optional[Dict] = None) -> Any:
        url = urljoin(self.base_url, path.lstrip("/"))
        resp = self.session.request(method, url, json=json_body, timeout=self.timeout)
        if resp.status_code >= 400:
            raise SandboxAPIError(resp.status_code, resp.text)
        return resp.json()

    def healthz(self) -> HealthResponse:
        """检查服务健康状态"""
        data = self._request("GET", "/healthz")
        return HealthResponse(ok=data.get("ok", False))

    def readyz(self) -> ReadyzResponse:
        """检查服务就绪状态"""
        data = self._request("GET", "/readyz")
        return ReadyzResponse(ok=data.get("ok", False), status=data.get("status", ""))

    def get_status(self) -> StatusPayload:
        """获取运行时状态"""
        data = self._request("GET", "/runtime/status")
        return StatusPayload(**data)

    def list_skills(self) -> List[SkillInfo]:
        """列出所有 Skill"""
        data = self._request("GET", "/runtime/skills")
        return [SkillInfo(**item) for item in data]

    def reload_skills(self) -> Dict[str, Any]:
        """重新加载 Skill 列表"""
        return self._request("POST", "/runtime/skills/reload")

    def get_skill(self, name: str) -> SkillInfo:
        """获取指定 Skill 信息"""
        data = self._request("GET", f"/runtime/skills/{name}")
        return SkillInfo(**data)

    def activate_skill(self, name: str, version: str) -> Dict[str, Any]:
        """激活指定 Skill 的指定版本"""
        return self._request("POST", f"/runtime/skills/{name}/activate", {"version": version})

    def start_runtime(self) -> Dict[str, Any]:
        """启动运行时"""
        return self._request("POST", "/runtime/start")

    def stop_runtime(self) -> Dict[str, Any]:
        """停止运行时"""
        return self._request("POST", "/runtime/stop")

    def exec(self, command: List[str], env: Optional[Dict[str, str]] = None, timeout_seconds: int = 0) -> ExecResult:
        """同步执行命令"""
        body: Dict[str, Any] = {"command": command}
        if env:
            body["env"] = env
        if timeout_seconds > 0:
            body["timeoutSeconds"] = timeout_seconds
        data = self._request("POST", "/runtime/exec", body)
        return ExecResult(**data)

    def list_tasks(self) -> List[Task]:
        """列出所有任务"""
        data = self._request("GET", "/runtime/tasks")
        return [Task(**item) for item in data]

    def submit_task(self, command: List[str], env: Optional[Dict[str, str]] = None, timeout_seconds: int = 0) -> Task:
        """提交异步任务"""
        body: Dict[str, Any] = {"command": command}
        if env:
            body["env"] = env
        if timeout_seconds > 0:
            body["timeoutSeconds"] = timeout_seconds
        data = self._request("POST", "/runtime/tasks", body)
        return Task(**data)

    def get_task(self, task_id: str) -> Task:
        """获取任务详情"""
        data = self._request("GET", f"/runtime/tasks/{task_id}")
        return Task(**data)

    def cancel_task(self, task_id: str) -> Task:
        """取消任务"""
        data = self._request("POST", f"/runtime/tasks/{task_id}/cancel")
        return Task(**data)

    def subscribe_events(self) -> Generator[Event, None, None]:
        """订阅 SSE 事件流（生成器）"""
        url = urljoin(self.base_url, "/runtime/stream")
        resp = self.session.get(url, stream=True, timeout=None)
        if resp.status_code >= 400:
            raise SandboxAPIError(resp.status_code, resp.text)

        for line in resp.iter_lines(decode_unicode=True):
            if line and line.startswith("data: "):
                data = line[6:]
                try:
                    parsed = json.loads(data)
                    yield Event(**parsed)
                except (json.JSONDecodeError, TypeError):
                    continue
