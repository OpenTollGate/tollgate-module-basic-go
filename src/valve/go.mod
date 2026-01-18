module github.com/OpenTollGate/tollgate-module-basic-go/src/valve

go 1.24.2

require github.com/sirupsen/logrus v1.9.3

require (
	github.com/stretchr/testify v1.10.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
)

replace github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager => ../config_manager
