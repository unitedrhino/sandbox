package com.unitedrhino.sandbox.model;

import com.fasterxml.jackson.annotation.JsonProperty;
import java.util.List;

public class SkillInfo {
    @JsonProperty("name")
    private String name;
    @JsonProperty("source")
    private String source;
    @JsonProperty("trustLevel")
    private String trustLevel;
    @JsonProperty("rootDir")
    private String rootDir;
    @JsonProperty("activeDir")
    private String activeDir;
    @JsonProperty("cliPath")
    private String cliPath;
    @JsonProperty("actions")
    private List<String> actions;
    @JsonProperty("version")
    private String version;
    @JsonProperty("versions")
    private List<String> versions;
    @JsonProperty("enabled")
    private boolean enabled;
    @JsonProperty("scanVerdict")
    private String scanVerdict;
    @JsonProperty("blockedReason")
    private String blockedReason;

    public String getName() { return name; }
    public void setName(String name) { this.name = name; }

    public String getSource() { return source; }
    public void setSource(String source) { this.source = source; }

    public String getTrustLevel() { return trustLevel; }
    public void setTrustLevel(String trustLevel) { this.trustLevel = trustLevel; }

    public String getRootDir() { return rootDir; }
    public void setRootDir(String rootDir) { this.rootDir = rootDir; }

    public String getActiveDir() { return activeDir; }
    public void setActiveDir(String activeDir) { this.activeDir = activeDir; }

    public String getCliPath() { return cliPath; }
    public void setCliPath(String cliPath) { this.cliPath = cliPath; }

    public List<String> getActions() { return actions; }
    public void setActions(List<String> actions) { this.actions = actions; }

    public String getVersion() { return version; }
    public void setVersion(String version) { this.version = version; }

    public List<String> getVersions() { return versions; }
    public void setVersions(List<String> versions) { this.versions = versions; }

    public boolean isEnabled() { return enabled; }
    public void setEnabled(boolean enabled) { this.enabled = enabled; }

    public String getScanVerdict() { return scanVerdict; }
    public void setScanVerdict(String scanVerdict) { this.scanVerdict = scanVerdict; }

    public String getBlockedReason() { return blockedReason; }
    public void setBlockedReason(String blockedReason) { this.blockedReason = blockedReason; }
}
