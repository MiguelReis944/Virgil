package privacy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var forbiddenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bsk-[a-z0-9]{12,}`),
	regexp.MustCompile(`(?i)\bghp_[a-z0-9]{20,}`),
	regexp.MustCompile(`-----BEGIN (?:RSA|OPENSSH|EC|DSA|PRIVATE) KEY-----`),
	regexp.MustCompile(`(?i)https?://[^/\s:@]+:[^/\s@]+@`),
}

func ScanBytes(content []byte) string {
	text := string(content)
	for _, pattern := range forbiddenPatterns {
		if match := pattern.FindString(text); match != "" {
			return pattern.String()
		}
	}
	return ""
}

func ScanTrackedRepository(root string) error {
	cmd := exec.Command(`git`, `-C`, root, `ls-files`, `-z`)
	output, err := cmd.Output()
	if err != nil {
		return err
	}
	for _, name := range strings.Split(string(output), "\x00") {
		if name == "" || filepath.Base(name) == "scan.go" || filepath.Base(name) == "scan_test.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if finding := ScanBytes(content); finding != "" {
			return errors.New("forbidden secret pattern in " + name)
		}
	}
	return nil
}
