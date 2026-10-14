module tollgate-module-basic-go

go 1.26.0

require (
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.12.1
)

require (
	github.com/stretchr/objx v0.5.3 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager => ../config_manager
