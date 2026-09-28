package vault

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// AgentTokenEnv is the environment variable an agent token is read from first, and the key of the line an
// agent.env file carries it in.
const AgentTokenEnv = "QATLAS_AGENT_TOKEN"

// agentEnvPath is where an agent.env file sits below a project directory or the home directory.
var agentEnvPath = filepath.Join(".qatlas", "local", "agent.env")

// maxAgentEnv bounds what is read of one agent.env file.
const maxAgentEnv = 64 << 10

// FindAgentToken returns the agent token a run in dir presents, and where it came from: AgentTokenEnv when
// getenv has a value for it; else the nearest .qatlas/local/agent.env in dir or a directory above it; else
// ~/.qatlas/local/agent.env below home. The nearest file found decides alone: one without a
// QATLAS_AGENT_TOKEN line yields no token, rather than one from further up. value is "" when there is none.
// Qatlas only ever reads these files; a person writes them. An error never quotes a file's content.
func FindAgentToken(dir, home string, getenv func(string) string) (value, source string, err error) {
	if getenv != nil {
		if value := strings.TrimSpace(getenv(AgentTokenEnv)); value != "" {
			return value, AgentTokenEnv, nil
		}
	}
	var candidates []string
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			for current := abs; ; {
				candidates = append(candidates, filepath.Join(current, agentEnvPath))
				parent := filepath.Dir(current)
				if parent == current {
					break
				}
				current = parent
			}
		}
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, agentEnvPath))
	}
	for _, path := range candidates {
		value, found, err := readAgentEnv(path)
		if err != nil {
			return "", path, err
		}
		if found {
			return value, path, nil
		}
	}
	return "", "", nil
}

// readAgentEnv reads the QATLAS_AGENT_TOKEN line of the agent.env file at path. found is false when the file
// does not exist; an existing file without the line yields "" and found true.
func readAgentEnv(path string) (value string, found bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("cannot read %s", path)
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.IsDir() {
		return "", false, fmt.Errorf("cannot read %s: not a file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAgentEnv))
	if err != nil {
		return "", false, fmt.Errorf("cannot read %s", path)
	}
	defer clear(data)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, rest, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != AgentTokenEnv {
			continue
		}
		value = strings.TrimSpace(rest)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
	}
	return value, true, nil
}
