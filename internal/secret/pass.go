package secret

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Pass stores secrets through the pass(1) CLI, which drives the user's gpg
// agent. clouder must run as the user (never root) for that agent to answer:
// a root process talking to the user's keyboxd is a known lockup on this
// machine.
type Pass struct{}

// Get returns the first line of `pass show <key>`.
func (Pass) Get(key string) (string, error) {
	cmd := exec.Command("pass", "show", key)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pass show %s: %v: %s", key, err, strings.TrimSpace(errb.String()))
	}
	sc := bufio.NewScanner(&out)
	if sc.Scan() {
		return strings.TrimSpace(sc.Text()), nil
	}
	return "", fmt.Errorf("pass show %s: empty secret", key)
}

// Set writes value under key with `pass insert -m -f`.
func (Pass) Set(key, value string) error {
	cmd := exec.Command("pass", "insert", "-m", "-f", key)
	cmd.Stdin = strings.NewReader(value + "\n")
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pass insert %s: %v: %s", key, err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// Delete removes key with `pass rm -f`. A missing key is not an error.
func (Pass) Delete(key string) error {
	cmd := exec.Command("pass", "rm", "-f", key)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if strings.Contains(msg, "not in the password store") || strings.Contains(msg, "is not in") {
			return nil
		}
		return fmt.Errorf("pass rm %s: %v: %s", key, err, msg)
	}
	return nil
}

// Keys lists the entries directly under prefix by reading the password store on
// disk (listing needs no gpg). Names are returned as full keys.
func (Pass) Keys(prefix string) ([]string, error) {
	store := os.Getenv("PASSWORD_STORE_DIR")
	if store == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		store = filepath.Join(home, ".password-store")
	}
	entries, err := os.ReadDir(filepath.Join(store, filepath.FromSlash(prefix)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var keys []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gpg") {
			continue
		}
		keys = append(keys, prefix+"/"+strings.TrimSuffix(e.Name(), ".gpg"))
	}
	sort.Strings(keys)
	return keys, nil
}
