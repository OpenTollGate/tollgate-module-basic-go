module github.com/OpenTollGate/tollgate-module-basic-go/src/cli

go 1.24.2

require (
	github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager v0.0.0
	github.com/OpenTollGate/tollgate-module-basic-go/src/merchant v0.0.0
	github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager v0.0.0
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.10.0
)

require (
	github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet v0.0.0
	github.com/OpenTollGate/tollgate-module-basic-go/src/utils v0.0.0
	github.com/OpenTollGate/tollgate-module-basic-go/src/valve v0.0.0
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager => ../config_manager
	github.com/OpenTollGate/tollgate-module-basic-go/src/merchant => ../merchant
	github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager => ../wireless_gateway_manager
	github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet => ../tollwallet
	github.com/OpenTollGate/tollgate-module-basic-go/src/utils => ../utils
	github.com/OpenTollGate/tollgate-module-basic-go/src/valve => ../valve
)
