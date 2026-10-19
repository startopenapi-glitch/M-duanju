package config

import (
	"fmt"
	"os"
	"strings"
)

// Require never falls back from an explicitly configured but unreadable secret file.
func Require(key string) (string, error) {
	value := os.Getenv(key)
	if path := os.Getenv(key + "_FILE"); path != "" {
		b, e := os.ReadFile(path)
		if e != nil {
			return "", fmt.Errorf("%s_FILE 不可读", key)
		}
		value = string(b)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s 未配置", key)
	}
	return value, nil
}
func DatabaseDSN(owner bool) (string, error) {
	if owner && (os.Getenv("DATABASE_URL_OWNER") != "" || os.Getenv("DATABASE_URL_OWNER_FILE") != "") {
		return Require("DATABASE_URL_OWNER")
	}
	return Require("DATABASE_URL")
}
