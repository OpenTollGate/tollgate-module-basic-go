// Command generate authors the canonical TollGate cross-vectors file.
//
// Run from the repository root:
//
//	cd cross-vectors/generate && go run . -root ../..
//
// It writes the canonical file (cross-vectors/tollgate-cross-vectors.json)
// AND the Go test's embedded copy (src/tollgate_protocol/testdata/) with
// IDENTICAL bytes, so the update-together duty cannot be half-done. The
// output is deterministic: fixed fixture key, fixed created_at, fixed
// payloads — running it twice produces byte-identical files.
//
// The fixture key below is a throwaway minted for these vectors and lives
// here on purpose (low-entropy-by-convention: a fixture credential that
// looks like a live one is a bug). Anyone can re-sign or extend the
// vectors by editing this generator and re-running it.
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fxamacker/cbor/v2"
	"github.com/nbd-wtf/go-nostr"
)

// fixtureSecretHex signs every vector event. Throwaway key, minted for
// these vectors, never used anywhere else.
const fixtureSecretHex = "c634c907ed2bbdc185fcf41082eee38fbe9adc47a85216d8d12d0cf7e08c04fe"

// fixtureCreatedAt is frozen so the vectors are reproducible.
const fixtureCreatedAt = nostr.Timestamp(1791000000)

// advertisement is the CBOR data model of a kind-10021 advertisement's
// semantics, as defined by cross-vectors/advertisement.cddl. Integer map
// keys and definite lengths follow the wire-CDDL family style
// (cross-vectors/tollgate.cddl).
type advertisement struct {
	Version  uint64  `cbor:"0,keyasint"`
	Metric   string  `cbor:"1,keyasint"`
	StepSize uint64  `cbor:"2,keyasint"`
	Prices   []price `cbor:"3,keyasint"`
	TIPs     []string `cbor:"4,keyasint"`
}

type price struct {
	AssetType    string `cbor:"1,keyasint"`
	PricePerStep uint64 `cbor:"2,keyasint"`
	Unit         string `cbor:"3,keyasint"`
	MintURL      string `cbor:"4,keyasint"`
	MinSteps     uint64 `cbor:"5,keyasint"`
}

// expectedPrice and expectedAdvertisement are the encoding-neutral
// semantics every consumer of a vector must agree on, regardless of
// whether it reads the nostr tags or the cbor_hex.
type expectedAdvertisement struct {
	Metric   string          `json:"metric"`
	StepSize uint64          `json:"step_size"`
	Prices   []expectedPrice `json:"prices"`
	TIPs     []string        `json:"tips"`
}

type expectedPrice struct {
	AssetType    string `json:"asset_type"`
	PricePerStep uint64 `json:"price_per_step"`
	Unit         string `json:"unit"`
	MintURL      string `json:"mint_url"`
	MinSteps     uint64 `json:"min_steps"`
}

type vector struct {
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	TamperedSignature bool                  `json:"tampered_signature"`
	Event            json.RawMessage        `json:"event"`
	CBORHex          string                 `json:"cbor_hex"`
	Expected         expectedAdvertisement  `json:"expected"`
}

type vectorFile struct {
	Schema     int      `json:"schema"`
	Provenance string   `json:"provenance"`
	Note       string   `json:"note"`
	Vectors    []vector `json:"vectors"`
}

func (a advertisement) expected() expectedAdvertisement {
	e := expectedAdvertisement{Metric: a.Metric, StepSize: a.StepSize, TIPs: a.TIPs}
	for _, p := range a.Prices {
		e.Prices = append(e.Prices, expectedPrice{
			AssetType: p.AssetType, PricePerStep: p.PricePerStep,
			Unit: p.Unit, MintURL: p.MintURL, MinSteps: p.MinSteps,
		})
	}
	return e
}

func (a advertisement) tags() nostr.Tags {
	tags := nostr.Tags{
		{"metric", a.Metric},
		{"step_size", fmt.Sprintf("%d", a.StepSize)},
	}
	for _, p := range a.Prices {
		tags = append(tags, nostr.Tag{
			"price_per_step", p.AssetType,
			fmt.Sprintf("%d", p.PricePerStep), p.Unit, p.MintURL,
			fmt.Sprintf("%d", p.MinSteps),
		})
	}
	tags = append(tags, nostr.Tag{"tips"})
	tags[len(tags)-1] = append(tags[len(tags)-1], a.TIPs...)
	return tags
}

func buildVector(name, description string, ad advertisement, tamper bool) (vector, error) {
	sk := fixtureSecretHex
	event := nostr.Event{
		CreatedAt: fixtureCreatedAt,
		Kind:      10021,
		Tags:      ad.tags(),
		Content:   "",
	}
	if err := event.Sign(sk); err != nil {
		return vector{}, fmt.Errorf("signing vector %s: %w", name, err)
	}
	if tamper {
		// Deterministic tamper: flip the low bit of the signature's last
		// byte. Structurally still a 64-byte hex schnorr signature, so the
		// event stays shape-valid and MUST fail only at verification.
		sig, err := hex.DecodeString(event.Sig)
		if err != nil {
			return vector{}, fmt.Errorf("decoding sig of %s: %w", name, err)
		}
		sig[len(sig)-1] ^= 0x01
		event.Sig = hex.EncodeToString(sig)
	}

	encoded, err := cbor.Marshal(ad)
	if err != nil {
		return vector{}, fmt.Errorf("cbor-encoding %s: %w", name, err)
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return vector{}, fmt.Errorf("marshaling event of %s: %w", name, err)
	}

	return vector{
		Name:              name,
		Description:       description,
		TamperedSignature: tamper,
		Event:             eventJSON,
		CBORHex:           hex.EncodeToString(encoded),
		Expected:          ad.expected(),
	}, nil
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	adVersion := uint64(1)
	vectors := []struct {
		name        string
		description string
		ad          advertisement
		tamper      bool
	}{
		{
			name: "milliseconds-single-mint",
			description: "TIP-01's example advertisement with TIP-02's example pricing: " +
				"one-minute steps, one cashu mint at 210 sat, min 1 step.",
			ad: advertisement{
				Version: adVersion, Metric: "milliseconds", StepSize: 60000,
				Prices: []price{{AssetType: "cashu", PricePerStep: 210, Unit: "sat", MintURL: "https://mint.domain.net", MinSteps: 1}},
				TIPs:   []string{"1", "2"},
			},
		},
		{
			name: "bytes-multi-mint",
			description: "A data-metered advertisement (the Go module's default shape): " +
				"22020096-byte steps priced at 1 sat on one mint and 2 sat (min 3 steps) on another.",
			ad: advertisement{
				Version: adVersion, Metric: "bytes", StepSize: 22020096,
				Prices: []price{
					{AssetType: "cashu", PricePerStep: 1, Unit: "sat", MintURL: "https://mint.coinos.io", MinSteps: 0},
					{AssetType: "cashu", PricePerStep: 2, Unit: "sat", MintURL: "https://other.mint.net", MinSteps: 3},
				},
				TIPs: []string{"1", "2", "5"},
			},
		},
		{
			name: "multi-currency-eur",
			description: "TIP-02's multi-currency rule in vector form: the same 210 sat price " +
				"on two mints (the MUST-be-equal rule is per unit), plus a 500 eur mint at min 3 steps.",
			ad: advertisement{
				Version: adVersion, Metric: "milliseconds", StepSize: 60000,
				Prices: []price{
					{AssetType: "cashu", PricePerStep: 210, Unit: "sat", MintURL: "https://mint.domain.net", MinSteps: 1},
					{AssetType: "cashu", PricePerStep: 210, Unit: "sat", MintURL: "https://other.mint.net", MinSteps: 1},
					{AssetType: "cashu", PricePerStep: 500, Unit: "eur", MintURL: "https://mint.thirddomain.eu", MinSteps: 3},
				},
				TIPs: []string{"1", "2"},
			},
		},
		{
			name: "tampered-signature",
			description: "milliseconds-single-mint with the low bit of the signature's last " +
				"byte flipped: shape-valid, semantic content intact, and the signature " +
				"MUST fail verification. Consumers must not treat a decodable " +
				"advertisement as an authentic one.",
			ad: advertisement{
				Version: adVersion, Metric: "milliseconds", StepSize: 60000,
				Prices: []price{{AssetType: "cashu", PricePerStep: 210, Unit: "sat", MintURL: "https://mint.domain.net", MinSteps: 1}},
				TIPs:   []string{"1", "2"},
			},
			tamper: true,
		},
	}

	out := vectorFile{
		Schema: 1,
		Provenance: "Canonical home: cross-vectors/tollgate-cross-vectors.json in " +
			"OpenTollGate/tollgate-module-basic-go (issue #750). Consumers embed a copy " +
			"with this provenance and update together when this file changes; " +
			"cross-vectors/generate regenerates everything deterministically.",
		Note: "Every vector carries the SAME advertisement in two encodings: a signed " +
			"Nostr kind-10021 event (the TIP-01/TIP-02 discovery wire) and cbor_hex " +
			"(the semantic data model of cross-vectors/advertisement.cddl). A consumer " +
			"is conformant when both decodings agree with 'expected' — and when the " +
			"tampered-signature vector fails authenticity while remaining decodable.",
	}
	for _, v := range vectors {
		built, err := buildVector(v.name, v.description, v.ad, v.tamper)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		out.Vectors = append(out.Vectors, built)
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	data = append(data, '\n')

	targets := []string{
		filepath.Join(*root, "cross-vectors", "tollgate-cross-vectors.json"),
		filepath.Join(*root, "src", "tollgate_protocol", "testdata", "tollgate-cross-vectors.json"),
	}
	for _, target := range targets {
		if err := os.WriteFile(target, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", target)
	}
}
