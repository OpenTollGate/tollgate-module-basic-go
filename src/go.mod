 module github.com/OpenTollGate/tollgate-module-basic-go

go 1.24.2

require (
	github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager v0.0.0-20250508155752-c38b5e886bf9
	github.com/OpenTollGate/tollgate-module-basic-go/src/janitor v0.0.0-00010101000000-000000000000
	github.com/OpenTollGate/tollgate-module-basic-go/src/modules v0.0.0-00010101000000-000000000000
	github.com/OpenTollGate/tollgate-module-basic-go/src/bragging v0.0.0-00010101000000-000000000000
	github.com/nbd-wtf/go-nostr v0.51.10
	github.com/stretchr/testify v1.10.0
)

require (
	github.com/ImVexed/fasturl v0.0.0-20230304231329-4e41488060f3 // indirect
	github.com/btcsuite/btcd v0.24.2 // indirect
	github.com/btcsuite/btcd/btcec/v2 v2.3.4 // indirect
	github.com/btcsuite/btcd/btcutil v1.1.5 // indirect
	github.com/btcsuite/btcd/chaincfg/chainhash v1.1.0 // indirect
	github.com/bytedance/sonic v1.13.2 // indirect
	github.com/bytedance/sonic/loader v0.2.4 // indirect
	github.com/cloudwego/base64x v0.1.5 // indirect
	github.com/coder/websocket v1.8.13 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/decred/dcrd/crypto/blake256 v1.1.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.0 // indirect
	github.com/elnosh/gonuts v0.3.1-0.20250123162555-7c0381a585e3 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/hashicorp/go-version v1.7.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/arch v0.17.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/exp v0.0.0-20250506013437-ce4c2cf36ca6 // indirect
	golang.org/x/sys v0.33.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager => ./config_manager
	github.com/OpenTollGate/tollgate-module-basic-go/src/janitor => ./janitor
	github.com/OpenTollGate/tollgate-module-basic-go/src/modules => ./modules
	github.com/OpenTollGate/tollgate-module-basic-go/src/bragging => ./bragging
)
