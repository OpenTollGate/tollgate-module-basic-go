package wireless_gateway_manager

import (
	"github.com/sirupsen/logrus"
)

// Module-level logger with pre-configured module field
var logger = logrus.WithField("module", "wireless_gateway_manager")

// GetLogger returns a logger instance for the wireless_gateway_manager module
func GetLogger() *logrus.Entry {
	return logger
}
