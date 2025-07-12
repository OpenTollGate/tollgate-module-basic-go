module github.com/OpenTollGate/tollgate-module-basic-go

go 1.24.2

require (
	github.com/OpenTollGate/tollgate-module-basic-go/src/chandler v0.0.0-00010101000000-000000000000
	github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager v0.0.0
	github.com/OpenTollGate/tollgate-module-basic-go/src/crowsnest v0.0.0-00010101000000-000000000000
	github.com/OpenTollGate/tollgate-module-basic-go/src/janitor v0.0.0-00010101000000-000000000000
	github.com/OpenTollGate/tollgate-module-basic-go/src/merchant v0.0.0-20250526110008-c13aa6acd08e
	github.com/OpenTollGate/tollgate-module-basic-go/src/relay v0.0.0-00010101000000-000000000000
	github.com/btcsuite/btcd/btcutil v1.1.6
	github.com/nbd-wtf/go-nostr v0.51.12
)

replace (
	github.com/OpenTollGate/tollgate-module-basic-go/src/chandler => ./chandler
	github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager => ./config_manager
	github.com/OpenTollGate/tollgate-module-basic-go/src/crowsnest => ./crowsnest
	github.com/OpenTollGate/tollgate-module-basic-go/src/janitor => ./janitor
	github.com/OpenTollGate/tollgate-module-basic-go/src/lightning => ./lightning
	github.com/OpenTollGate/tollgate-module-basic-go/src/merchant => ./merchant
	github.com/OpenTollGate/tollgate-module-basic-go/src/relay => ./relay
	github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol => ./tollgate_protocol
	github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet => ./tollwallet
	github.com/OpenTollGate/tollgate-module-basic-go/src/utils => ./utils
	github.com/OpenTollGate/tollgate-module-basic-go/src/valve => ./valve
)

require (
	github.com/ImVexed/fasturl v0.0.0-20230304231329-4e41488060f3 // indirect
	github.com/OpenTollGate/tollgate-module-basic-go/src/lightning v0.0.0-00010101000000-000000000000 // indirect
	github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol v0.0.0-00010101000000-000000000000 // indirect
	github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet v0.0.0 // indirect
	github.com/OpenTollGate/tollgate-module-basic-go/src/utils v0.0.0 // indirect
	github.com/OpenTollGate/tollgate-module-basic-go/src/valve v0.0.0 // indirect
	github.com/Origami74/gonuts-tollgate v0.6.0 // indirect
	github.com/aead/chacha20 v0.0.0-20180709150244-8b13a72661da // indirect
	github.com/aead/siphash v1.0.1 // indirect
	github.com/andybalholm/brotli v1.1.1 // indirect
	github.com/bep/debounce v1.2.1 // indirect
	github.com/btcsuite/btcd v0.24.3-0.20250318170759-4f4ea81776d6 // indirect
	github.com/btcsuite/btcd/btcec/v2 v2.3.4 // indirect
	github.com/btcsuite/btcd/btcutil/psbt v1.1.10 // indirect
	github.com/btcsuite/btcd/chaincfg/chainhash v1.1.0 // indirect
	github.com/btcsuite/btclog v0.0.0-20241003133417-09c4e92e319c // indirect
	github.com/btcsuite/btclog/v2 v2.0.1-0.20250602222548-9967d19bb084 // indirect
	github.com/btcsuite/btcwallet v0.16.14 // indirect
	github.com/btcsuite/btcwallet/wallet/txauthor v1.3.5 // indirect
	github.com/btcsuite/btcwallet/wallet/txrules v1.2.2 // indirect
	github.com/btcsuite/btcwallet/wallet/txsizes v1.2.5 // indirect
	github.com/btcsuite/btcwallet/walletdb v1.5.1 // indirect
	github.com/btcsuite/btcwallet/wtxmgr v1.5.6 // indirect
	github.com/btcsuite/go-socks v0.0.0-20170105172521-4720035b7bfd // indirect
	github.com/btcsuite/websocket v0.0.0-20150119174127-31079b680792 // indirect
	github.com/btcsuite/winsvc v1.0.0 // indirect
	github.com/bytedance/sonic v1.13.2 // indirect
	github.com/bytedance/sonic/loader v0.2.4 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cloudwego/base64x v0.1.5 // indirect
	github.com/coder/websocket v1.8.13 // indirect
	github.com/containerd/continuity v0.4.3 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/decred/dcrd/crypto/blake256 v1.1.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.0 // indirect
	github.com/decred/dcrd/lru v1.1.3 // indirect
	github.com/fasthttp/websocket v1.5.12 // indirect
	github.com/fiatjaf/eventstore v0.16.2 // indirect
	github.com/fiatjaf/khatru v0.18.1 // indirect
	github.com/fxamacker/cbor/v2 v2.8.0 // indirect
	github.com/go-errors/errors v1.5.1 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.22.0 // indirect
	github.com/hashicorp/go-version v1.7.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jessevdk/go-flags v1.6.1 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/jrick/logrotate v1.1.2 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kkdai/bstream v1.0.0 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	github.com/lightninglabs/gozmq v0.0.0-20191113021534-d20a764486bf // indirect
	github.com/lightninglabs/neutrino v0.16.1 // indirect
	github.com/lightninglabs/neutrino/cache v1.1.2 // indirect
	github.com/lightningnetwork/lightning-onion v1.2.1-0.20240712235311-98bd56499dfb // indirect
	github.com/lightningnetwork/lnd v0.19.1-beta.rc1 // indirect
	github.com/lightningnetwork/lnd/clock v1.1.1 // indirect
	github.com/lightningnetwork/lnd/fn/v2 v2.0.8 // indirect
	github.com/lightningnetwork/lnd/queue v1.1.1 // indirect
	github.com/lightningnetwork/lnd/ticker v1.1.1 // indirect
	github.com/lightningnetwork/lnd/tlv v1.3.1 // indirect
	github.com/lightningnetwork/lnd/tor v1.1.6 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/miekg/dns v1.1.66 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/nbd-wtf/ln-decodepay v1.13.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	github.com/rs/cors v1.11.1 // indirect
	github.com/savsgio/gotils v0.0.0-20240704082632-aef3928b8a38 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/stretchr/testify v1.10.0 // indirect
	github.com/syndtr/goleveldb v1.0.1-0.20210819022825-2ae1ddf74ef7 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/tyler-smith/go-bip39 v1.1.0 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.59.0 // indirect
	github.com/vishvananda/netlink v1.3.1 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.etcd.io/bbolt v1.4.0 // indirect
	go.etcd.io/etcd/client/v2 v2.305.16 // indirect
	go.etcd.io/etcd/pkg/v3 v3.5.16 // indirect
	go.etcd.io/etcd/raft/v3 v3.5.16 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.54.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.30.0 // indirect
	go.opentelemetry.io/proto/otlp v1.3.1 // indirect
	golang.org/x/arch v0.17.0 // indirect
	golang.org/x/crypto v0.38.0 // indirect
	golang.org/x/exp v0.0.0-20250506013437-ce4c2cf36ca6 // indirect
	golang.org/x/mod v0.24.0 // indirect
	golang.org/x/net v0.40.0 // indirect
	golang.org/x/sync v0.14.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/term v0.32.0 // indirect
	golang.org/x/tools v0.33.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	pgregory.net/rapid v1.2.0 // indirect
)
