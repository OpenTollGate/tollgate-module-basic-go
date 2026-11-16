- **The paid-network bridge is now published in the config.** `paid_bridge`
  names the bridge that carries paid/captive clients (`br-lan` on the stock
  layout, `br-tollgate` on an isolated one). Management clients and the feed's
  NDS fragment can read it instead of hardcoding a device name. Empty means
  derive it from the paid AP's network at install time.
