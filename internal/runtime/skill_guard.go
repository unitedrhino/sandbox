package runtime

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type SkillGuardResult struct {
	Enabled       bool
	ScanVerdict   string
	BlockedReason string
}

var mappedThreatPatterns = []struct {
	re          *regexp.Regexp
	verdict     string
	description string
}{
	{regexp.MustCompile(`curl\s+[^\n]*\$\{?\w*(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)`), "dangerous", "curl command interpolating secret environment variable"},
	{regexp.MustCompile(`wget\s+[^\n]*\$\{?\w*(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)`), "dangerous", "wget command interpolating secret environment variable"},
	{regexp.MustCompile(`printenv|env\s*\|`), "caution", "dumps environment variables"},
	{regexp.MustCompile(`ignore\s+(?:\w+\s+)*(previous|all|above|prior)\s+instructions`), "dangerous", "prompt injection instruction"},
	{regexp.MustCompile(`rm\s+-rf\s+/`), "dangerous", "recursive delete from root"},
	{regexp.MustCompile(`chmod\s+777`), "caution", "world-writable permissions"},
	{regexp.MustCompile(`>\s*/etc/`), "dangerous", "overwrites system configuration file"},
}

func ScanMappedSkill(skillDir string) SkillGuardResult {
	if strings.TrimSpace(skillDir) == "" {
		return SkillGuardResult{Enabled: false, ScanVerdict: "dangerous", BlockedReason: "empty skill dir"}
	}
	if !fileExists(filepath.Join(skillDir, "SKILL.md")) {
		return SkillGuardResult{Enabled: false, ScanVerdict: "dangerous", BlockedReason: "missing SKILL.md"}
	}

	var blocked string
	verdict := "safe"
	fileCount := 0

	walkErr := filepath.WalkDir(skillDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			blocked = fmt.Sprintf("walk error: %v", err)
			verdict = "dangerous"
			return fs.SkipAll
		}
		if d.Type()&os.ModeSymlink != 0 {
			blocked = fmt.Sprintf("symlink not allowed: %s", path)
			verdict = "dangerous"
			return fs.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		fileCount++
		if fileCount > 128 {
			blocked = "too many files"
			verdict = "dangerous"
			return fs.SkipAll
		}
		if fileCount > 0 {
			current := scanMappedSkillFile(path)
			if current == "dangerous" {
				blocked = fmt.Sprintf("dangerous pattern in %s", filepath.Base(path))
				verdict = "dangerous"
				return fs.SkipAll
			}
			if current == "caution" && verdict == "safe" {
				verdict = "caution"
			}
		}
		return nil
	})
	if walkErr != nil && blocked == "" {
		blocked = walkErr.Error()
		verdict = "dangerous"
	}

	if verdict == "dangerous" {
		return SkillGuardResult{
			Enabled:       false,
			ScanVerdict:   verdict,
			BlockedReason: blocked,
		}
	}

	return SkillGuardResult{
		Enabled:       true,
		ScanVerdict:   verdict,
		BlockedReason: "",
	}
}

func scanMappedSkillFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "dangerous"
	}
	defer f.Close()

	verdict := "safe"
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		for _, pattern := range mappedThreatPatterns {
			if pattern.re.MatchString(line) {
				if pattern.verdict == "dangerous" {
					return "dangerous"
				}
				verdict = "caution"
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "dangerous"
	}
	return verdict
}
