package com.unitedrhino.sandbox.model;

import com.fasterxml.jackson.annotation.JsonProperty;

public class StatusPayload {
    @JsonProperty("runtimeId")
    private String runtimeId;
    @JsonProperty("tenantCode")
    private String tenantCode;
    @JsonProperty("cloneId")
    private String cloneId;
    @JsonProperty("cloneKey")
    private String cloneKey;
    @JsonProperty("sessionId")
    private String sessionId;
    @JsonProperty("workspace")
    private String workspace;
    @JsonProperty("status")
    private String status;
    @JsonProperty("startedAt")
    private String startedAt;
    @JsonProperty("lastEventAt")
    private String lastEventAt;

    public String getRuntimeId() { return runtimeId; }
    public void setRuntimeId(String runtimeId) { this.runtimeId = runtimeId; }

    public String getTenantCode() { return tenantCode; }
    public void setTenantCode(String tenantCode) { this.tenantCode = tenantCode; }

    public String getCloneId() { return cloneId; }
    public void setCloneId(String cloneId) { this.cloneId = cloneId; }

    public String getCloneKey() { return cloneKey; }
    public void setCloneKey(String cloneKey) { this.cloneKey = cloneKey; }

    public String getSessionId() { return sessionId; }
    public void setSessionId(String sessionId) { this.sessionId = sessionId; }

    public String getWorkspace() { return workspace; }
    public void setWorkspace(String workspace) { this.workspace = workspace; }

    public String getStatus() { return status; }
    public void setStatus(String status) { this.status = status; }

    public String getStartedAt() { return startedAt; }
    public void setStartedAt(String startedAt) { this.startedAt = startedAt; }

    public String getLastEventAt() { return lastEventAt; }
    public void setLastEventAt(String lastEventAt) { this.lastEventAt = lastEventAt; }
}
