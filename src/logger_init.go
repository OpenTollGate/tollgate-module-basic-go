package main

import (
	"strings"

	"github.com/sirupsen/logrus"
)

// InitializeGlobalLogger configures logrus with the specified log level for the entire application
// This should be called once at application startup
func InitializeGlobalLogger(logLevel string) {
	level, err := logrus.ParseLevel(strings.ToLower(logLevel))
	if err != nil {
		// Default to info level if parsing fails
		level = logrus.InfoLevel
		logrus.WithError(err).Warn("Failed to parse log level, defaulting to info")
	}

	logrus.SetLevel(level)

	// Set a consistent formatter for the entire application
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
		ForceColors:   true,
	})

	logrus.WithField("log_level", level.String()).Info("Global logger initialized")
}
