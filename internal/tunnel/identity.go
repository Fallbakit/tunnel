package tunnel

import (
	"fmt"
	"strings"
)

const defaultRunnerID = "local-runner"

// Identity is the tenant scope bound to a tunnel connection.
type Identity struct {
	UserID    string
	AccountID string
	RunnerID  string
}

func (i Identity) Valid() bool {
	return i.UserID != "" && i.AccountID != ""
}

func (i Identity) key() string {
	return i.scopeKey() + "\x00" + i.RunnerIDOrDefault()
}

func (i Identity) scopeKey() string {
	return i.AccountID
}

func (i Identity) RunnerIDOrDefault() string {
	runnerID := strings.TrimSpace(i.RunnerID)
	if runnerID == "" {
		return defaultRunnerID
	}
	return runnerID
}

func (i Identity) String() string {
	return fmt.Sprintf("account=%s runner=%s", i.AccountID, i.RunnerIDOrDefault())
}
