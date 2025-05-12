sequenceDiagram
    participant main as main.go
    participant relayPool as nostr.SimplePool
    participant configManager as config_manager.ConfigManager
    participant janitor as janitor.Janitor
    participant braggingService as bragging.Service

    main->>relayPool: Initialize
    main->>configManager: NewConfigManager(relayPool)
    main->>janitor: initJanitor(relayPool)
    main->>braggingService: NewBraggingService(relayPool)

    Note over main,braggingService: relayPool used for relay interactions

    