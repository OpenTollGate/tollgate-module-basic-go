package config_manager

type FieldSchema struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	Default     interface{}       `json:"default,omitempty"`
	Required    bool              `json:"required"`
	Enum        []string          `json:"enum,omitempty"`
	Min         interface{}       `json:"min,omitempty"`
	Max         interface{}       `json:"max,omitempty"`
	Children    []FieldSchema     `json:"children,omitempty"`
	JSONKey     string            `json:"json_key"`
	Editable    bool              `json:"editable"`
}

func GetConfigSchema() []FieldSchema {
	return []FieldSchema{
		{
			Name: "ConfigVersion", JSONKey: "config_version", Type: "string",
			Description: "Configuration file version", Default: "v0.0.7", Required: true, Editable: false,
		},
		{
			Name: "LogLevel", JSONKey: "log_level", Type: "string",
			Description: "Logging verbosity", Default: "info", Required: true, Editable: true,
			Enum: []string{"debug", "info", "warn", "error"},
		},
		{
			Name: "Metric", JSONKey: "metric", Type: "string",
			Description: "Metering metric type", Default: "bytes", Required: true, Editable: true,
			Enum: []string{"bytes", "milliseconds"},
		},
		{
			Name: "StepSize", JSONKey: "step_size", Type: "uint64",
			Description: "Step size in bytes (if metric=bytes) or milliseconds (if metric=milliseconds)", Default: uint64(22020096), Required: true, Editable: true,
		},
		{
			Name: "Margin", JSONKey: "margin", Type: "float64",
			Description: "Margin factor (0.0-1.0)", Default: 0.1, Required: false, Editable: true,
			Min: 0.0, Max: 1.0,
		},
		{
			Name: "ShowSetup", JSONKey: "show_setup", Type: "bool",
			Description: "Show setup wizard on first access", Default: true, Required: true, Editable: true,
		},
		{
			Name: "ResellerMode", JSONKey: "reseller_mode", Type: "bool",
			Description: "Enable reseller mode for upstream gateway discovery", Default: false, Required: true, Editable: true,
		},
		{
			Name: "AuthDelaySeconds", JSONKey: "auth_delay_seconds", Type: "int",
			Description: "Delay in seconds before authorizing MAC after payment (0 = immediate)", Default: 0, Required: false, Editable: true, Min: 0, Max: 300,
		},
		{
			Name: "RedirectURL", JSONKey: "redirect_url", Type: "string",
			Description: "URL to redirect clients to after payment (empty = no redirect)", Default: "", Required: false, Editable: true,
		},
		{
			Name: "AcceptedMints", JSONKey: "accepted_mints", Type: "array",
			Description: "List of accepted Cashu mints", Required: true, Editable: true,
			Children: []FieldSchema{
				{Name: "URL", JSONKey: "url", Type: "string", Description: "Mint URL", Required: true, Editable: true},
				{Name: "MinBalance", JSONKey: "min_balance", Type: "uint64", Description: "Minimum balance before auto-replenish (sats)", Default: uint64(64), Required: true, Editable: true},
				{Name: "BalanceTolerancePercent", JSONKey: "balance_tolerance_percent", Type: "uint64", Description: "Tolerance percentage for balance checks", Default: uint64(10), Required: true, Editable: true},
				{Name: "PayoutIntervalSeconds", JSONKey: "payout_interval_seconds", Type: "uint64", Description: "Seconds between payout rounds", Default: uint64(60), Required: true, Editable: true},
				{Name: "MinPayoutAmount", JSONKey: "min_payout_amount", Type: "uint64", Description: "Minimum payout amount in sats", Default: uint64(128), Required: true, Editable: true},
				{Name: "PricePerStep", JSONKey: "price_per_step", Type: "uint64", Description: "Price per step in sats", Default: uint64(1), Required: true, Editable: true, Min: uint64(1)},
				{Name: "PriceUnit", JSONKey: "price_unit", Type: "string", Description: "Price unit", Default: "sats", Required: true, Editable: true},
				{Name: "MinPurchaseSteps", JSONKey: "purchase_min_steps", Type: "uint64", Description: "Minimum number of steps per purchase", Default: uint64(0), Required: true, Editable: true},
			},
		},
		{
			Name: "ProfitShare", JSONKey: "profit_share", Type: "array",
			Description: "Profit sharing configuration", Required: true, Editable: true,
			Children: []FieldSchema{
			{Name: "Factor", JSONKey: "factor", Type: "float64", Description: "Share ratio (0.0\u20131.0). All factors MUST sum to 1.0. Use 0.79 not 79\u2014this is a ratio, not a percentage.", Required: true, Editable: true, Min: 0.0, Max: 1.0},
				{Name: "Identity", JSONKey: "identity", Type: "string", Description: "Identity name from identities.json", Required: true, Editable: true},
			},
		},
		{
			Name: "UpstreamDetector", JSONKey: "upstream_detector", Type: "object",
			Description: "Upstream gateway detector configuration", Required: true, Editable: true,
			Children: []FieldSchema{
				{Name: "ProbeTimeout", JSONKey: "probe_timeout", Type: "duration", Description: "Timeout for each probe", Default: "10s", Required: true, Editable: true},
				{Name: "ProbeRetryCount", JSONKey: "probe_retry_count", Type: "int", Description: "Number of probe retries", Default: 3, Required: true, Editable: true},
				{Name: "ProbeRetryDelay", JSONKey: "probe_retry_delay", Type: "duration", Description: "Delay between retries", Default: "2s", Required: true, Editable: true},
				{Name: "RequireValidSignature", JSONKey: "require_valid_signature", Type: "bool", Description: "Require valid NIP-70 signature", Default: true, Required: true, Editable: true},
				{Name: "IgnoreInterfaces", JSONKey: "ignore_interfaces", Type: "array", Description: "Interfaces to ignore", Default: []string{"lo", "docker0", "br-lan", "hostap0"}, Required: false, Editable: true, Children: []FieldSchema{{Type: "string"}}},
				{Name: "OnlyInterfaces", JSONKey: "only_interfaces", Type: "array", Description: "Only probe these interfaces (empty = all)", Default: []string{}, Required: false, Editable: true, Children: []FieldSchema{{Type: "string"}}},
				{Name: "DiscoveryTimeout", JSONKey: "discovery_timeout", Type: "duration", Description: "Deduplication window", Default: "5m0s", Required: true, Editable: true},
			},
		},
		{
			Name: "UpstreamSessionManager", JSONKey: "upstream_session_manager", Type: "object",
			Description: "Upstream session manager configuration", Required: true, Editable: true,
			Children: []FieldSchema{
				{Name: "MaxPricePerMillisecond", JSONKey: "max_price_per_millisecond", Type: "float64", Description: "Max sats per millisecond", Default: 0.002777777778, Required: true, Editable: true},
				{Name: "MaxPricePerByte", JSONKey: "max_price_per_byte", Type: "float64", Description: "Max sats per byte", Default: 0.00003725782414, Required: true, Editable: true},
				{
					Name: "Trust", JSONKey: "trust", Type: "object", Description: "Trust policy", Required: true, Editable: true,
					Children: []FieldSchema{
						{Name: "DefaultPolicy", JSONKey: "default_policy", Type: "string", Description: "Default trust policy", Default: "trust_all", Required: true, Editable: true, Enum: []string{"trust_all", "trust_none"}},
						{Name: "Allowlist", JSONKey: "allowlist", Type: "array", Description: "Trusted pubkeys", Default: []string{}, Required: false, Editable: true, Children: []FieldSchema{{Type: "string"}}},
						{Name: "Blocklist", JSONKey: "blocklist", Type: "array", Description: "Blocked pubkeys", Default: []string{}, Required: false, Editable: true, Children: []FieldSchema{{Type: "string"}}},
					},
				},
				{
					Name: "Sessions", JSONKey: "sessions", Type: "object", Description: "Session settings", Required: true, Editable: true,
					Children: []FieldSchema{
						{Name: "PreferredSessionIncrementsMilliseconds", JSONKey: "preferred_session_increments_milliseconds", Type: "uint64", Description: "Preferred time session increment (ms)", Default: uint64(60000), Required: true, Editable: true},
						{Name: "PreferredSessionIncrementsBytes", JSONKey: "preferred_session_increments_bytes", Type: "uint64", Description: "Preferred data session increment (bytes)", Default: uint64(131100000), Required: true, Editable: true},
						{Name: "MillisecondRenewalOffset", JSONKey: "millisecond_renewal_offset", Type: "uint64", Description: "Renew this many ms before expiry", Default: uint64(10000), Required: true, Editable: true},
						{Name: "BytesRenewalOffset", JSONKey: "bytes_renewal_offset", Type: "uint64", Description: "Renew this many bytes before limit", Default: uint64(131100000), Required: true, Editable: true},
					},
				},
				{
					Name: "UsageTracking", JSONKey: "usage_tracking", Type: "object", Description: "Usage tracking settings", Required: true, Editable: true,
					Children: []FieldSchema{
						{Name: "DataMonitoringInterval", JSONKey: "data_monitoring_interval", Type: "duration", Description: "How often to check data usage", Default: "500ms", Required: true, Editable: true},
					},
				},
			},
		},
		{
			Name: "UpstreamWifi", JSONKey: "upstream_wifi", Type: "object",
			Description: "Upstream WiFi scanning and selection configuration", Required: true, Editable: true,
			Children: []FieldSchema{
				{Name: "ScanIntervalSeconds", JSONKey: "scan_interval_seconds", Type: "int", Description: "Seconds between full WiFi scans", Default: 300, Required: true, Editable: true, Min: 10, Max: 3600},
				{Name: "FastCheckSeconds", JSONKey: "fast_check_seconds", Type: "int", Description: "Seconds between fast signal checks", Default: 30, Required: true, Editable: true, Min: 5, Max: 300},
				{Name: "LostThreshold", JSONKey: "lost_threshold", Type: "int", Description: "Consecutive fast-check failures before marking as lost", Default: 2, Required: true, Editable: true, Min: 1, Max: 10},
				{Name: "HysteresisDB", JSONKey: "hysteresis_db", Type: "int", Description: "Signal hysteresis in dB to prevent flapping", Default: 12, Required: true, Editable: true, Min: 0, Max: 30},
				{Name: "SignalFloor", JSONKey: "signal_floor", Type: "int", Description: "Minimum signal strength in dBm to consider a network usable", Default: -85, Required: true, Editable: true, Min: -100, Max: -30},
				{Name: "BlacklistTTLMinutes", JSONKey: "blacklist_ttl_minutes", Type: "int", Description: "Minutes before a blacklisted network is retried", Default: 60, Required: true, Editable: true, Min: 1, Max: 1440},
				{Name: "EmergencyPenalty", JSONKey: "emergency_penalty", Type: "int", Description: "Penalty score added on emergency disconnect", Default: 20, Required: true, Editable: true, Min: 0, Max: 100},
				{Name: "MaxConsecutiveFailures", JSONKey: "max_consecutive_failures", Type: "int", Description: "Consecutive failures before emergency scan", Default: 3, Required: true, Editable: true, Min: 1, Max: 20},
				{Name: "SwitchCooldownMinutes", JSONKey: "switch_cooldown_minutes", Type: "int", Description: "Minimum minutes between network switches", Default: 10, Required: true, Editable: true, Min: 1, Max: 120},
				{Name: "StartupGraceSeconds", JSONKey: "startup_grace_seconds", Type: "int", Description: "Grace period on startup before scoring", Default: 90, Required: true, Editable: true, Min: 10, Max: 600},
				{Name: "PostSwitchWaitSeconds", JSONKey: "post_switch_wait_seconds", Type: "int", Description: "Seconds to wait after a switch before scoring", Default: 5, Required: true, Editable: true, Min: 1, Max: 60},
				{Name: "DHCPTimeoutSeconds", JSONKey: "dhcp_timeout_seconds", Type: "int", Description: "Timeout for DHCP after connecting to a network", Default: 180, Required: true, Editable: true, Min: 10, Max: 600},
				{Name: "ManualPauseSeconds", JSONKey: "manual_pause_seconds", Type: "int", Description: "Seconds to pause scanning after manual intervention", Default: 120, Required: true, Editable: true, Min: 10, Max: 600},
			},
		},
	}
}

func GetIdentitiesSchema() []FieldSchema {
	return []FieldSchema{
		{
			Name: "ConfigVersion", JSONKey: "config_version", Type: "string",
			Description: "Identities file version", Default: "v0.0.1", Required: true, Editable: false,
		},
		{
			Name: "OwnedIdentities", JSONKey: "owned_identities", Type: "array",
			Description: "Identities with private keys (managed by the system)", Required: true, Editable: false,
			Children: []FieldSchema{
				{Name: "Name", JSONKey: "name", Type: "string", Description: "Identity name", Required: true, Editable: false},
				{Name: "PrivateKey", JSONKey: "privatekey", Type: "string", Description: "Nostr private key (sensitive)", Required: true, Editable: false},
			},
		},
		{
			Name: "PublicIdentities", JSONKey: "public_identities", Type: "array",
			Description: "Public identities for profit sharing and trust", Required: true, Editable: true,
			Children: []FieldSchema{
				{Name: "Name", JSONKey: "name", Type: "string", Description: "Identity name", Required: true, Editable: true},
				{Name: "PubKey", JSONKey: "pubkey", Type: "string", Description: "Nostr public key — not currently used for payouts (lightning_address is used instead)", Required: false, Editable: true},
				{Name: "LightningAddress", JSONKey: "lightning_address", Type: "string", Description: "Lightning address for payouts", Required: false, Editable: true},
			},
		},
	}
}
