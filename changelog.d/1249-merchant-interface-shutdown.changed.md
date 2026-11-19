- **The merchant `Shutdown()` contract is declared on the interfaces.**
  `MerchantInterface` and the provider-facing `PaymentMerchant` now declare
  `Shutdown() error`, so callers holding either interface can release the
  wallet and monitors without a type assertion — both implementations
  already carried the method
  ([#1249](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1249)).
