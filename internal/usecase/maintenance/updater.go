package maintenance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/magicaleks/qudata-agent-alpha/internal/impls"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/logger"
)

// Updater запускает удалённый install.sh под sudo.
type Updater struct {
	store  impls.AgentStore
	logger *logger.FileLogger
	mu     sync.Mutex
}

func NewUpdater(store impls.AgentStore, log *logger.FileLogger) *Updater {
	return &Updater{
		store:  store,
		logger: log,
	}
}

func (u *Updater) Run(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	key, err := u.store.APIKey(ctx)
	if err != nil {
		return fmt.Errorf("read api key: %w", err)
	}
	if key == "" {
		key = strings.TrimSpace(os.Getenv("QUDATA_API_KEY"))
		if key == "" {
			return errors.New("QUDATA_API_KEY is not configured")
		}
		if err := u.store.SaveAPIKey(ctx, key); err != nil {
			u.logger.Warn("failed to store api key: %v", err)
		}
	}

	cmdStr := fmt.Sprintf("wget -qO- https://github.com/qubu-group/qudata-agent/main/install.sh | sudo bash -s %s", shellQuote(key))
	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Run(); err != nil {
		output := buf.String()
		u.logger.Error("self-update failed: %v; output: %s", err, output)
		return fmt.Errorf("self-update failed: %w", err)
	}

	u.logger.Info("self-update completed successfully")
	return nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
