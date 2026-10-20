package platform

import (
	"os"
	"strings"
)

func LoadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && !strings.HasPrefix(key, "#") && os.Getenv(key) == "" {
			os.Setenv(key, strings.Trim(value, `"'`))
		}
	}
}
