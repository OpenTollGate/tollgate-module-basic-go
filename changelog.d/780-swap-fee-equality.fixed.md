- **A token whose value exactly equals its swap fee is refused cleanly, not
  bounced by the mint.** The wallet used to pass the below-fees guard when
  the token amount EQUALLED the fee, split zero, and POST a swap with an
  empty outputs array — the mint's raw NUT-03 400 ("Outputs are required and
  must be an array", the CU107 portal error) reached the payer instead of a
  fee verdict. gonuts-tollgate v0.13.1 refuses it locally (and any swap that
  reaches zero outputs), and the backend maps the refusal to the clear
  payment-error-below-swap-fee notice
  ([#780](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/780)).
