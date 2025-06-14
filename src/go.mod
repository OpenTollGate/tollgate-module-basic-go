module github.com/OpenTollGate/tollgate-module-basic-go

go 1.22.2

require (
       github.com/OpenTollGate/tollgate-module-basic-go/src/bragging v0.0.0-20250522085419-17692bf154f8
       github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager v0.0.0
       github.com/OpenTollGate/tollgate-module-basic-go/src/janitor v0.0.0-00010101000000-000000000000
       github.com/OpenTollGate/tollgate-module-basic-go/src/merchant v0.0.0-00010101000000-000000000000
       github.com/OpenTollGate/tollgate-module-basic-go/src/relay v0.0.0-00010101000000-000000000000
       github.com/nbd-wtf/go-nostr v0.51.12
)

replace (
       github.com/OpenTollGate/tollgate-module-basic-go/src/bragging => ./bragging
       github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager => ./config_manager
       github.com/OpenTollGate/tollgate-module-basic-go/src/janitor => ./janitor
       github.com/OpenTollGate/tollgate-module-basic-go/src/lightning => ./lightning
       github.com/OpenTollGate/tollgate-module-basic-go/src/merchant => ./merchant
       github.com/OpenTollGate/tollgate-module-basic-go/src/relay => ./relay
       github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet => ./tollwallet
       github.com/OpenTollGate/tollgate-module-basic-go/src/utils => ./utils
       github.com/OpenTollGate/tollgate-module-basic-go/src/valve => ./valve
)

