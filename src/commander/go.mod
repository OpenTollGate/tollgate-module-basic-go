module github.com/OpenTollGate/tollgate-module-basic-go/src/commander

go 1.22.0

replace github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager => ../config_manager

require (
	github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager v0.0.0-00010101000000-000000000000
	github.com/nbd-wtf/go-nostr v0.42.2
)
