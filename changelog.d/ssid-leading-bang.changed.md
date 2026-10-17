- **The captive SSID now sorts first: `!TollGate-<code>` (a leading `!`).** The
  guest-facing SSID carries a leading `!` (`0x21`, before digits and letters) so
  it appears first in an alphabetically ordered WiFi list; a whitelabel build
  gets the same decoration on its own prefix. The `!` is **presentation, not
  discovery**: it is not
  part of the name, and every reader treats it as optional — the Go recognizer
  (`hasTollGateSSID`, reseller-mode upstream selection and the vendor-element
  score) and the shell readers (`strip_ssid_decoration`, used by `code_from_name`
  and `captive_ssid_for_code`) each strip at most one leading `!` before
  matching, so already-deployed routers broadcasting the bare `TollGate-<code>`
  form and third-party clients matching `TollGate-*` keep working, and an
  existing bare captive SSID converges to the decorated form on the next install.
  The **private** management SSID (`<nym>-<code>`) is unchanged and never carries
  the `!`. The shipped SSID contract
  (`tests/contract/check-ssid-format.sh`) and the device-code suite
  (`tests/uci-defaults-device-code_test.sh`) pin both forms.
