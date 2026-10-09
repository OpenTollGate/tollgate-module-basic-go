package merchant

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
)

// The #583 contract: when the wallet cannot initialize because its storage
// filesystem cannot back bbolt's shared mmap (jffs2 overlays), the degraded
// merchant must say SO — in StorageIncompatible() and in every
// "wallet not initialized" error — instead of blaming "no reachable mints"
// while the mints are fine. Every other wallet-load failure keeps the
// historic wording, which is true for its own cause (mints down, first boot).

// jffs2WalletFactoryError is the error DefaultWalletFactory surfaces on a
// jffs2 overlay: bolt.Open creates wallet.db, the shared mmap fails with
// EINVAL, and gonuts flattens the errno to text through %v wraps.
func jffs2WalletFactoryError() error {
	return fmt.Errorf("failed to create wallet: InitStorage: error setting bolt db: %v", syscall.EINVAL)
}

func TestMerchantDegraded_StorageMmap_FundErrorNamesStorage(t *testing.T) {
	ds, _ := newDegradedSetupWithServer(t, nil)
	deg := ds.DegradedWithWallet(nil, jffs2WalletFactoryError())

	if !deg.StorageIncompatible() {
		t.Fatal("StorageIncompatible() = false for the jffs2/mmap error class")
	}

	_, err := deg.Fund("cashuAAA...")
	if err == nil {
		t.Fatal("Fund in degraded mode must fail")
	}
	if !strings.Contains(err.Error(), "shared mmap") || !strings.Contains(err.Error(), "jffs2") {
		t.Errorf("Fund error should name the storage/mmap cause, got: %v", err)
	}
	if strings.Contains(err.Error(), "no reachable mints") {
		t.Errorf("Fund error must not blame the mints when the mints are fine (#583), got: %v", err)
	}
}

func TestMerchantDegraded_StorageMmap_EveryWalletOpNamesStorage(t *testing.T) {
	ds, _ := newDegradedSetupWithServer(t, nil)
	deg := ds.DegradedWithWallet(nil, jffs2WalletFactoryError())

	errs := []error{
		errOf2(deg.CreatePaymentToken("https://mint.test", 100)),
		errOf2(deg.CreatePaymentTokenWithOverpayment("https://mint.test", 100, 10, 5)),
		errOf3(deg.DrainMint("https://mint.test")),
		errOf2(deg.RequestLightningInvoice("aa:bb:cc:dd:ee:ff", "https://mint.test", 100)),
		errOf2(deg.GetSession("aa:bb:cc:dd:ee:ff")),
		errOf2(deg.AddAllotment("aa:bb:cc:dd:ee:ff", "bytes", 1000)),
		errOf3(deg.IssueSessionTicket("aa:bb:cc:dd:ee:ff")),
		errOf2(deg.RebindSession("ticket", "aa:bb:cc:dd:ee:ff")),
		errOf2(deg.Fund("cashuAAA...")),
	}
	for i, err := range errs {
		if err == nil {
			t.Fatalf("op %d: expected an error", i)
		}
		if !strings.Contains(err.Error(), "wallet not initialized") {
			t.Errorf("op %d: error lost the 'wallet not initialized' prefix: %v", i, err)
		}
		if !strings.Contains(err.Error(), "shared mmap") {
			t.Errorf("op %d: error should name the storage/mmap cause (#583), got: %v", i, err)
		}
	}
}

func TestMerchantDegraded_OtherWalletError_KeepsHistoricWording(t *testing.T) {
	ds, _ := newDegradedSetupWithServer(t, nil)
	deg := ds.DegradedWithWallet(nil, fmt.Errorf("dial tcp: connection refused"))

	if deg.StorageIncompatible() {
		t.Fatal("StorageIncompatible() must be false for a non-mmap wallet error")
	}

	_, err := deg.Fund("cashuAAA...")
	if err == nil {
		t.Fatal("Fund in degraded mode must fail")
	}
	if got, want := err.Error(), "wallet not initialized: no reachable mints"; got != want {
		t.Errorf("non-storage wallet errors keep the historic wording:\n got: %s\nwant: %s", got, want)
	}
}

// errOf2 / errOf3 extract the error from a (value…, error) call so the
// assertion table can be a slice literal: the ops return typed values whose
// nils cannot be discarded inline inside a composite literal.
func errOf2(_ interface{}, err error) error    { return err }
func errOf3(_, _ interface{}, err error) error { return err }

func TestMerchantDegraded_StorageMmap_AdvertisementNamesStorage(t *testing.T) {
	ds, _ := newDegradedSetupWithServer(t, nil)
	deg := ds.DegradedWithWallet(nil, jffs2WalletFactoryError())

	ad := deg.GetAdvertisement()
	if !strings.Contains(ad, "wallet-storage-unsupported") {
		t.Errorf("advertisement should carry the storage notice code for the mmap class (#824), got: %s", ad)
	}
	if !strings.Contains(ad, "shared mmap") {
		t.Errorf("advertisement should name the shared-mmap cause, got: %s", ad)
	}
	if strings.Contains(ad, "auto-recover") {
		t.Errorf("storage class must not promise auto-recovery, got: %s", ad)
	}
}

func TestMerchantDegraded_StorageMmap_DegradedInfoReasons(t *testing.T) {
	ds, _ := newDegradedSetupWithServer(t, nil)

	storageDeg := ds.DegradedWithWallet(nil, jffs2WalletFactoryError())
	degraded, reason := storageDeg.WalletDegradedInfo()
	if !degraded || !strings.Contains(reason, "shared mmap") {
		t.Errorf("storage class should surface (true, mmap reason), got (%v, %q)", degraded, reason)
	}

	mintsDeg := ds.DegradedWithWallet(nil, fmt.Errorf("dial tcp: connection refused"))
	degraded, reason = mintsDeg.WalletDegradedInfo()
	if !degraded || reason != "no reachable mints" {
		t.Errorf("non-storage class keeps the historic reason, got (%v, %q)", degraded, reason)
	}
}
