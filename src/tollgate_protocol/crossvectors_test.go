package tollgate_protocol

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The canonical TollGate cross-vectors, embedded per the copy convention
// (issue #750; canonical home cross-vectors/tollgate-cross-vectors.json).
// tests/contract/check-cross-vectors-sync.sh fails when this copy drifts
// from the canonical file; regenerate both together with
// cross-vectors/generate.
const crossVectorsPath = "testdata/tollgate-cross-vectors.json"

type crossVectorPrice struct {
	AssetType    string `json:"asset_type"`
	PricePerStep uint64 `json:"price_per_step"`
	Unit         string `json:"unit"`
	MintURL      string `json:"mint_url"`
	MinSteps     uint64 `json:"min_steps"`
}

type crossVectorExpected struct {
	Metric   string             `json:"metric"`
	StepSize uint64             `json:"step_size"`
	Prices   []crossVectorPrice `json:"prices"`
	TIPs     []string           `json:"tips"`
}

type crossVector struct {
	Name              string              `json:"name"`
	Description       string              `json:"description"`
	TamperedSignature bool                `json:"tampered_signature"`
	Event             json.RawMessage     `json:"event"`
	CBORHex           string              `json:"cbor_hex"`
	Expected          crossVectorExpected `json:"expected"`
}

type crossVectorFile struct {
	Schema     int           `json:"schema"`
	Provenance string        `json:"provenance"`
	Note       string        `json:"note"`
	Vectors    []crossVector `json:"vectors"`
}

// cborAdvertisement mirrors cross-vectors/advertisement.cddl: the semantic
// data model of an advertisement, independent of the Nostr envelope.
type cborAdvertisement struct {
	Version  uint64      `cbor:"0,keyasint"`
	Metric   string      `cbor:"1,keyasint"`
	StepSize uint64      `cbor:"2,keyasint"`
	Prices   []cborPrice `cbor:"3,keyasint"`
	TIPs     []string    `cbor:"4,keyasint"`
}

type cborPrice struct {
	AssetType    string `cbor:"1,keyasint"`
	PricePerStep uint64 `cbor:"2,keyasint"`
	Unit         string `cbor:"3,keyasint"`
	MintURL      string `cbor:"4,keyasint"`
	MinSteps     uint64 `cbor:"5,keyasint"`
}

// TestCrossVectorsAdvertisementSemantics: every canonical vector must mean
// the same thing through BOTH encodings — the cbor_hex data model and the
// signed kind-10021 Nostr event this package actually receives on the wire
// — and the tampered-signature vector must fail authenticity while staying
// decodable. That is the whole conformance contract of #750's Go leg.
func TestCrossVectorsAdvertisementSemantics(t *testing.T) {
	data, err := os.ReadFile(crossVectorsPath)
	require.NoError(t, err, "embedded cross-vectors copy is missing")

	var file crossVectorFile
	require.NoError(t, json.Unmarshal(data, &file))
	require.Equal(t, 1, file.Schema, "unsupported vector schema")
	require.NotEmpty(t, file.Vectors)
	require.Contains(t, file.Provenance, "cross-vectors/tollgate-cross-vectors.json",
		"the copy must carry the canonical-home provenance")

	sawTampered := false
	for _, vector := range file.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			cborAd := decodeCBORHex(t, vector.CBORHex)
			assertAdvertisementEqual(t, vector.Name, cborAd, vector.Expected)

			event, err := ParseAdvertisementFromBytes(vector.Event)
			require.NoError(t, err, "the vector event must parse as a Nostr event")
			require.Equal(t, TollGateAdvertisementKind, event.Kind)

			validateErr := ValidateAdvertisement(event)
			if vector.TamperedSignature {
				sawTampered = true
				require.Error(t, validateErr,
					"a tampered signature MUST fail validation — decodable is not authentic")
				assert.Contains(t, validateErr.Error(), "signature")
			} else {
				require.NoError(t, validateErr, "an honest vector must validate")
			}

			info, err := ExtractAdvertisementInfo(event)
			require.NoError(t, err, "semantics must be extractable regardless of authenticity")
			assert.Equal(t, vector.Expected.Metric, info.Metric)
			assert.Equal(t, vector.Expected.StepSize, info.StepSize)
			assert.Equal(t, vector.Expected.TIPs, info.TIPs)
			require.Len(t, info.PricingOptions, len(vector.Expected.Prices))
			for i, want := range vector.Expected.Prices {
				got := info.PricingOptions[i]
				assert.Equal(t, want.AssetType, got.AssetType, "price[%d].asset_type", i)
				assert.Equal(t, want.PricePerStep, got.PricePerStep, "price[%d].price_per_step", i)
				assert.Equal(t, want.Unit, got.PriceUnit, "price[%d].unit", i)
				assert.Equal(t, want.MintURL, got.MintURL, "price[%d].mint_url", i)
				assert.Equal(t, want.MinSteps, got.MinSteps, "price[%d].min_steps", i)
			}
		})
	}
	assert.True(t, sawTampered, "the vector set must contain a tampered-signature case")
}

func decodeCBORHex(t *testing.T, hexString string) cborAdvertisement {
	t.Helper()
	raw, err := hex.DecodeString(hexString)
	require.NoError(t, err)
	var ad cborAdvertisement
	require.NoError(t, cbor.Unmarshal(raw, &ad),
		"cbor_hex must decode per cross-vectors/advertisement.cddl")
	return ad
}

func assertAdvertisementEqual(t *testing.T, name string, got cborAdvertisement, want crossVectorExpected) {
	t.Helper()
	assert.Equal(t, uint64(1), got.Version, "%s: advertisement schema version", name)
	assert.Equal(t, want.Metric, got.Metric, "%s: metric", name)
	assert.Equal(t, want.StepSize, got.StepSize, "%s: step_size", name)
	assert.Equal(t, want.TIPs, got.TIPs, "%s: tips", name)
	require.Len(t, got.Prices, len(want.Prices), "%s: price count", name)
	for i, w := range want.Prices {
		g := got.Prices[i]
		assert.Equal(t, w.AssetType, g.AssetType, "%s: price[%d].asset_type", name, i)
		assert.Equal(t, w.PricePerStep, g.PricePerStep, "%s: price[%d].price_per_step", name, i)
		assert.Equal(t, w.Unit, g.Unit, "%s: price[%d].unit", name, i)
		assert.Equal(t, w.MintURL, g.MintURL, "%s: price[%d].mint_url", name, i)
		assert.Equal(t, w.MinSteps, g.MinSteps, "%s: price[%d].min_steps", name, i)
	}
}

// The Nostr envelope's shape is part of the pin: the vector event must
// survive a production round trip — the exact bytes a prober receives on
// :2121 — without the envelope itself drifting (kind, tag stringness).
func TestCrossVectorsEventEnvelopeShape(t *testing.T) {
	data, err := os.ReadFile(crossVectorsPath)
	require.NoError(t, err)
	var file crossVectorFile
	require.NoError(t, json.Unmarshal(data, &file))

	for _, vector := range file.Vectors {
		var event nostr.Event
		require.NoError(t, json.Unmarshal(vector.Event, &event))
		assert.Equal(t, TollGateAdvertisementKind, event.Kind)
		assert.NotEmpty(t, event.PubKey)
		assert.Len(t, event.Sig, 128, "a schnorr signature is 64 bytes hex")
		for _, tag := range event.Tags {
			for _, value := range tag {
				assert.IsType(t, "", value,
					"%s: Nostr tag values are strings — TIP-02's bare-number example is not serializable", vector.Name)
			}
		}
	}
}
