export type SandboxStatus = "created" | "ready" | "busy" | "stopped";

export type TaskStatus = "pending" | "running" | "succeeded" | "failed" | "canceled";

export interface HealthResponse {
  ok: boolean;
}

export interface ReadyzResponse {
  ok: boolean;
  status: string;
}

export interface StatusPayload {
  runtimeId: string;
  tenantCode: string;
  cloneId: string;
  cloneKey: string;
  sessionId: string;
  workspace: string;
  status: SandboxStatus;
  startedAt: string;
  lastEventAt: string;
}

export interface ExecRequest {
  command: string[];
  env?: Record<string, string>;
  timeoutSeconds?: number;
}

export interface ExecResult {
  exitCode: number;
  stdout: string;
  stderr: string;
  startedAt: string;
  endedAt: string;
}

export interface Task {
  id: string;
  command: string[];
  backend?: string;
  skill?: string;
  skillSource?: string;
  skillTrustLevel?: string;
  status: TaskStatus;
  createdAt: string;
  startedAt?: string;
  endedAt?: string;
  error?: string;
  result?: ExecResult;
}

export interface SkillInfo {
  name: string;
  source?: string;
  trustLevel?: string;
  rootDir?: string;
  activeDir?: string;
  cliPath?: string;
  actions?: string[];
  version?: string;
  versions?: string[];
  enabled: boolean;
  scanVerdict?: string;
  blockedReason?: string;
}

export interface Event {
  type: string;
  time: string;
  data?: Record<string, unknown>;
}

export interface SkillActivateRequest {
  version: string;
}

export interface SkillActivateResponse {
  ok: boolean;
  skill: SkillInfo;
}

export interface ReloadSkillsResponse {
  ok: boolean;
  skills: SkillInfo[];
}

export interface StartStopResponse {
  ok: boolean;
  status: StatusPayload;
}
