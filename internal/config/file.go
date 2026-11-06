package config

import (
	"fmt"
	"os"
	"strings"
)

// CheckPathExists checks if a folder exists and is a directory
func CheckPathExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// CheckFileExists: checks if a file exists and is not a directory.
func CheckFileExists(filename string) bool {
	info, err := os.Stat(filename)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// BackupConfigFile creates a backup of the provided config file
func BackupConfigFile(filename string) error {
	return os.Rename(filename, filename+".bak")
}

func TrimmedFileContents(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	return strings.TrimSpace(string(content)), nil
}
