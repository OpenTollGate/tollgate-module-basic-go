# TollGate - OpenWRT Router Payment Gateway

![tollgate-logo](docs/TollGate_Logo-C-black.png)

TollGate transforms your OpenWRT router into a payment gateway for selling internet access using Cashu payments. This implementation follows the TollGate protocol, allowing users to pay for internet time using satoshis (sats).

## Core Technologies
- Cashu - [cashu.space](https://cashu.space)
- Nostr - [NIP-01](https://github.com/nostr-protocol/nips/blob/master/01.md)
- Bitcoin Lightning

## Features

- **Base Implementation to Sell Sats**: Core functionality to accept payments for internet access
- **Lightning Integration**: Get paid out automatically over Lightning to your configured address
- **Auto-Update System**: Janitor module keeps your TollGate up-to-date with the latest features
- **Cashu Token Support**: Accept Cashu tokens as payment method
- **Configurable Pricing**: Set your own rates for internet access
- **Profit Sharing**: Configure multiple Lightning addresses with different percentage splits
- **Bragging**: Optionally post about purchases on Nostr relays

## Modules

TollGate is built with a modular architecture, making it extensible and maintainable:

### Merchant Module

The financial brain of TollGate. This module:
- Handles payment processing
- Manages pricing and conversions
- Calculates internet time based on payment amount
- Schedules and processes Lightning payouts
- Creates network advertisements

### Valve Module

Controls access to your network. This module:
- Opens and closes network access using ndsctl
- Authorizes and deauthorizes MAC addresses
- Manages access timers

### Janitor Module

Keeps your TollGate up-to-date. This module:
- Listens for update events via Nostr
- Downloads and verifies updated packages
- Handles architecture-specific updates

### Bragging Module

(Optional) Posts about successful payments. This module:
- Announces payments on Nostr relays
- Configurable fields to include (amount, duration, etc.)
- Uses your TollGate's identity for signing

### Other Supporting Modules

- **Config Manager**: Handles configuration file operations
- **Lightning**: Interfaces with Lightning Network for invoices
- **TollWallet**: Manages Cashu token operations
- **Utils**: Provides common utility functions

## Configuration

Configure TollGate by editing the `/etc/tollgate/config.json` file:

```json
{
  "tollgate_private_key": "YOUR_PRIVATE_KEY",
  "accepted_mints": [
    {
      "url": "https://mint.example.com/Bitcoin",
      "min_balance": 100,
      "balance_tolerance_percent": 10,
      "payout_interval_seconds": 3600,
      "min_payout_amount": 1000
    }
  ],
  "profit_share": [
    {
      "factor": 0.7,
      "lightning_address": "your-address@lightning.provider"
    },
    {
      "factor": 0.3,
      "lightning_address": "tollgate@minibits.cash"
    }
  ],
  "price_per_minute": 1,
  "bragging": {
    "enabled": true,
    "fields": ["amount", "duration"]
  }
}
```

**Important configuration fields:**
- `tollgate_private_key`: Used for signing Nostr events
- `accepted_mints`: List of Cashu mints you accept tokens from
- `profit_share`: Configure Lightning addresses for payouts and their percentages
- `price_per_minute`: Base rate for internet access
- `bragging`: Enable/disable payment announcements

## Testing

To run the tests for the project, navigate to the `src` directory and execute the following command:

```bash
TOLLGATE_TEST_CONFIG_DIR=/tmp/tmp.r6VVmTRFcp go fmt . && TOLLGATE_TEST_CONFIG_DIR=/tmp/tmp.EolGeeKwH3 go test .
```

This command will format the Go code and then run all tests within the `src` module.

## Documentation

For more detailed information about TollGate modules and usage:

- Main project
	- [HLDD](src/HLDD.md)
	- [LLDD](src/LLDD.md)
- Configuration Manager
	- [HLDD](src/config_manager/HLDD.md)
	- [LLDD](src/config_manager/LLDD.md)
- Janitor
	- [HLDD](src/janitor/HLDD.md)
	- [LLDD](src/janitor/LLDD.md)

You can find the [Module Integration Guide](src/integrating_modules.md) here.
## License

This project is licensed under the GNU General Public License v3.0 - see the [LICENSE](LICENSE) file for details.