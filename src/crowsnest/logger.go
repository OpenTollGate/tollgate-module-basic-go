package crowsnest

import "github.com/sirupsen/logrus"

// Module-level logger with pre-configured module field
var logger = logrus.WithField("module", "crowsnest")
