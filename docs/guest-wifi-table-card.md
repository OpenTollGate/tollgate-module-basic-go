# Guest Wi-Fi QR table card integration

The admin UI feature belongs to tollgate-captive-portal-site and is proposed in
https://github.com/OpenTollGate/tollgate-captive-portal-site/pull/102 (draft).
Do not implement a divergent admin bundle in this repository.

The UI reads only `wireless.tollgate_2g_open` or `wireless.tollgate_5g_open`
through authenticated UCI RPC. These are the installer-owned guest AP sections;
`private_radio0/1` and private keys must never be printed. The conservative
initial UI accepts APs attached to lan/guest only; custom portal-plane mappings
need explicit integration before support. Open and WPA/WPA2 Personal Wi-Fi
credentials are encoded in standard WIFI QR syntax; no Cashu/payment secret is
part of a card. A guest confirms joining when prompted, then follows captive
portal detection. QR decode does not prove association or paid internet.

## Integration gate

Do not advance `packaging/build-inputs.json` to an unmerged portal branch.
After portal review/merge, consume its merged revision through the normal
packaging pin workflow. Existing portal-pin PR #1178 must not be duplicated.
Shipping feed changes belong to FreedomTechFeed/packages.

Router acceptance belongs to OpenTollGate/physical-router-test-automation:
record source SHA, deployed package sha256, router identity, admin-only access,
printed-image decode, guest association/DHCP, and independent captive detection.
DQ05 Android/QEMU testing cannot be labeled physical RF scan/join proof.
No such deployment or association test has been completed for this draft.
