module cdkinterop

go 1.25.0

require github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet v0.0.0

replace github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet => <path-to>/src/tollwallet
replace github.com/OpenTollGate/tollgate-module-basic-go/src/lightning => <path-to>/src/lightning
