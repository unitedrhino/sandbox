import type {
  Event,
  ExecRequest,
  ExecResult,
  HealthResponse,
  ReadyzResponse,
  ReloadSkillsResponse,
  SkillActivateRequest,
  SkillActivateResponse,
  SkillInfo,
  StartStopResponse,
  StatusPayload,
  Task,
} from "./types";

export class SandboxError extends Error {
  constructor(
    public readonly statusCode: number,
    message: string,
  ) {
    super(`[${statusCode}] ${message}`);
    this.name = "SandboxError";
  }
}

export class SandboxClient {
  private readonly baseUrl: string;

  constructor(baseUrl: string) {
    this.baseUrl = baseUrl.replace(/\/$/, "");
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const url = `${this.baseUrl}${path}`;
    const init: RequestInit = {
      method,
      headers: body ? { "Content-Type": "application/json" } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    };
    const resp = await fetch(url, init);
    if (!resp.ok) {
      const text = await resp.text();
      throw new SandboxError(resp.status, text);
    }
    return resp.json() as Promise<T>;
  }

  /** 检查服务健康状态 */
  async healthz(): Promise<HealthResponse> {
    return this.request<HealthResponse>("GET", "/healthz");
  }

  /** 检查服务就绪状态 */
  async readyz(): Promise<ReadyzResponse> {
    return this.request<ReadyzResponse>("GET", "/readyz");
  }

  /** 获取运行时状态 */
  async getStatus(): Promise<StatusPayload> {
    return this.request<StatusPayload>("GET", "/runtime/status");
  }

  /** 列出所有 Skill */
  async listSkills(): Promise<SkillInfo[]> {
    return this.request<SkillInfo[]>("GET", "/runtime/skills");
  }

  /** 重新加载 Skill 列表 */
  async reloadSkills(): Promise<ReloadSkillsResponse> {
    return this.request<ReloadSkillsResponse>("POST", "/runtime/skills/reload");
  }

  /** 获取指定 Skill 信息 */
  async getSkill(name: string): Promise<SkillInfo> {
    return this.request<SkillInfo>("GET", `/runtime/skills/${encodeURIComponent(name)}`);
  }

  /** 激活指定 Skill 的指定版本 */
  async activateSkill(name: string, version: string): Promise<SkillActivateResponse> {
    return this.request<SkillActivateResponse>("POST", `/runtime/skills/${encodeURIComponent(name)}/activate`, {
      version,
    } as SkillActivateRequest);
  }

  /** 启动运行时 */
  async startRuntime(): Promise<StartStopResponse> {
    return this.request<StartStopResponse>("POST", "/runtime/start");
  }

  /** 停止运行时 */
  async stopRuntime(): Promise<StartStopResponse> {
    return this.request<StartStopResponse>("POST", "/runtime/stop");
  }

  /** 同步执行命令 */
  async exec(req: ExecRequest): Promise<ExecResult> {
    return this.request<ExecResult>("POST", "/runtime/exec", req);
  }

  /** 列出所有任务 */
  async listTasks(): Promise<Task[]> {
    return this.request<Task[]>("GET", "/runtime/tasks");
  }

  /** 提交异步任务 */
  async submitTask(req: ExecRequest): Promise<Task> {
    return this.request<Task>("POST", "/runtime/tasks", req);
  }

  /** 获取任务详情 */
  async getTask(taskId: string): Promise<Task> {
    return this.request<Task>("GET", `/runtime/tasks/${encodeURIComponent(taskId)}`);
  }

  /** 取消任务 */
  async cancelTask(taskId: string): Promise<Task> {
    return this.request<Task>("POST", `/runtime/tasks/${encodeURIComponent(taskId)}/cancel`);
  }

  /** 订阅 SSE 事件流 */
  async *subscribeEvents(signal?: AbortSignal): AsyncGenerator<Event, void, unknown> {
    const resp = await fetch(`${this.baseUrl}/runtime/stream`, {
      method: "GET",
      signal,
    });
    if (!resp.ok) {
      const text = await resp.text();
      throw new SandboxError(resp.status, text);
    }

    const reader = resp.body!.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() ?? "";

      for (const line of lines) {
        if (line.startsWith("data: ")) {
          const data = line.slice(6);
          try {
            const event: Event = JSON.parse(data);
            yield event;
          } catch {
            // ignore malformed JSON
          }
        }
      }
    }
  }
}
