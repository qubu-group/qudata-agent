package paths

import (
	"os"
	"path/filepath"

	"github.com/magicaleks/qudata-agent-alpha/internal/infra/logger"
)

// Resolve выбирает доступный путь: сначала пытается создать предпочтительный,
// при ошибке откатывается в каталог пользователя ~/.qudata-agent/<fallbackRel>.
func Resolve(preferred string, fallbackRel string) string {
	if err := os.MkdirAll(filepath.Dir(preferred), 0o755); err == nil {
		return preferred
	}

	home, err := os.UserHomeDir()
	fallbackDir := "."
	if err == nil && home != "" {
		fallbackDir = filepath.Join(home, ".qudata-agent")
	} else {
		logger.LogWarn("home dir unavailable, using current directory for fallback storage")
	}

	fallbackPath := filepath.Join(fallbackDir, fallbackRel)
	if mkErr := os.MkdirAll(filepath.Dir(fallbackPath), 0o755); mkErr != nil {
		logger.LogError("failed to create fallback dir %s: %v", filepath.Dir(fallbackPath), mkErr)
		return preferred
	}

	logger.LogWarn("using fallback path %s for %s", fallbackPath, preferred)
	return fallbackPath
}
