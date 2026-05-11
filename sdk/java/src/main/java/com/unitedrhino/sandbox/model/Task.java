package com.unitedrhino.sandbox.model;

import com.fasterxml.jackson.annotation.JsonProperty;
import java.util.List;

public class Task {
    @JsonProperty("id")
    private String id;
    @JsonProperty("command")
    private List<String> command;
    @JsonProperty("backend")
    private String backend;
    @JsonProperty("skill")
    private String skill;
    @JsonProperty("skillSource")
    private String skillSource;
    @JsonProperty("skillTrustLevel")
    private String skillTrustLevel;
    @JsonProperty("status")
    private String status;
    @JsonProperty("createdAt")
    private String createdAt;
    @JsonProperty("startedAt")
    private String startedAt;
    @JsonProperty("endedAt")
    private String endedAt;
    @JsonProperty("error")
    private String error;
    @JsonProperty("result")
    private ExecResult result;

    public String getId() { return id; }
    public void setId(String id) { this.id = id; }

    public List<String> getCommand() { return command; }
    public void setCommand(List<String> command) { this.command = command; }

    public String getBackend() { return backend; }
    public void setBackend(String backend) { this.backend = backend; }

    public String getSkill() { return skill; }
    public void setSkill(String skill) { this.skill = skill; }

    public String getSkillSource() { return skillSource; }
    public void setSkillSource(String skillSource) { this.skillSource = skillSource; }

    public String getSkillTrustLevel() { return skillTrustLevel; }
    public void setSkillTrustLevel(String skillTrustLevel) { this.skillTrustLevel = skillTrustLevel; }

    public String getStatus() { return status; }
    public void setStatus(String status) { this.status = status; }

    public String getCreatedAt() { return createdAt; }
    public void setCreatedAt(String createdAt) { this.createdAt = createdAt; }

    public String getStartedAt() { return startedAt; }
    public void setStartedAt(String startedAt) { this.startedAt = startedAt; }

    public String getEndedAt() { return endedAt; }
    public void setEndedAt(String endedAt) { this.endedAt = endedAt; }

    public String getError() { return error; }
    public void setError(String error) { this.error = error; }

    public ExecResult getResult() { return result; }
    public void setResult(ExecResult result) { this.result = result; }
}
