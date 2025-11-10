module github.com/OpenTollGate/tollgate-module-basic-go/src/cli

go 1.23

require (
	github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager v0.0.0
	github.com/OpenTollGate/tollgate-module-basic-go/src/merchant v0.0.0
	github.com/sirupsen/logrus v1.9.3
)

require (
	golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8 // indirect
)

replace (
	github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager => ../config_manager
	github.com/OpenTollGate/tollgate-module-basic-go/src/merchant => ../merchant
)