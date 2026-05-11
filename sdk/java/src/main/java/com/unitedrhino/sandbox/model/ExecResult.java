package com.unitedrhino.sandbox.model;

import com.fasterxml.jackson.annotation.JsonProperty;

public class ExecResult {
    @JsonProperty("exitCode")
    private int exitCode;
    @JsonProperty("stdout")
    private String stdout;
    @JsonProperty("stderr")
    private String stderr;
    @JsonProperty("startedAt")
    private String startedAt;
    @JsonProperty("endedAt")
    private String endedAt;

    public int getExitCode() { return exitCode; }
    public void setExitCode(int exitCode) { this.exitCode = exitCode; }

    public String getStdout() { return stdout; }
    public void setStdout(String stdout) { this.stdout = stdout; }

    public String getStderr() { return stderr; }
    public void setStderr(String stderr) { this.stderr = stderr; }

    public String getStartedAt() { return startedAt; }
    public void setStartedAt(String startedAt) { this.startedAt = startedAt; }

    public String getEndedAt() { return endedAt; }
    public void setEndedAt(String endedAt) { this.endedAt = endedAt; }
}
