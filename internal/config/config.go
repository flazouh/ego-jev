// Package config finds API keys in the environment or in a dotenv file.
package config

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DefaultEnvFile is $XDG_CONFIG_HOME/ego-jev/.env, or ~/.config/ego-jev/.env.
func DefaultEnvFile() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "ego-jev", ".env")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ego-jev", ".env")
}

// Keys reads names from the environment first, then from the dotenv file. A missing file is not an error.
type Keys struct {
	file map[string]string
}

func Load(envFile string) (Keys, error) {
	values, err := readDotenv(envFile)
	if errors.Is(err, fs.ErrNotExist) {
		return Keys{}, nil
	}
	return Keys{file: values}, err
}

func (k Keys) Get(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return k.file[name]
}

func readDotenv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		values[strings.TrimSpace(name)] = value
	}
	return values, scanner.Err()
}
