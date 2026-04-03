package pathutil

import (
	"path"
	"strings"
)

func NormalizeSourcePath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(raw, "/"))
	if cleaned == "." {
		return "/"
	}
	return cleaned
}
