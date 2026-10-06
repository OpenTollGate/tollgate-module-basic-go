module github.com/OpenTollGate/tollgate-module-basic-go/src/valve

go 1.26.0

require github.com/sirupsen/logrus v1.9.3

require (
	github.com/stretchr/testify v1.12.1 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager => ../config_manager
