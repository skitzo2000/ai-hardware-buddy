---
description: Check the buddy MCP server's liveness and BLE connection state.
---

```bash
hwbuddy ping
```

Returns one JSON line: ``{"ok":true,"data":{"version":"…","connected":true|false,"address":"…"}}``.

- ``connected:true`` → MCP server is up and BLE link is live
- ``connected:false`` → MCP server is up but not attached; run ``/hwb-connect``
- Error "hwbuddy MCP server is not running" → the plugin's MCP entry
  in .mcp.json didn't start. Try ``/plugin enable hardware-buddy`` or
  restart ``claude``.
