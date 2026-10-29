package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

const (
	configDirName  = "jira-cli"
	configFileName = "config.env"
)

func readDotEnv(path string) (map[string]string, error) {
	values := make(map[string]string)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: want KEY=VALUE", path, i+1)
		}
		values[strings.TrimSpace(key)] = unquote(strings.TrimSpace(value))
	}
	return values, nil
}

func readDotEnvs(paths ...string) (map[string]string, error) {
	merged := make(map[string]string)
	for _, path := range paths {
		values, err := readDotEnv(path)
		if err != nil {
			return nil, err
		}
		maps.Copy(merged, values)
	}
	return merged, nil
}

func userConfigFile(getenv func(string) string) (string, error) {
	dir := getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, configDirName, configFileName), nil
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func envLookup(fallback map[string]string) func(string) string {
	return func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return fallback[key]
	}
}
