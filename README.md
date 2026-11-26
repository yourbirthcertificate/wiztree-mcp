# WizTree MCP for Windows

Ask your AI client “What’s using space in my Downloads folder?” or “Which file types take up the most space on D:?” The server runs your installed copy of WizTree, reads its metadata export, and returns a ranked report. The executable runs on x64 Windows and includes its own runtime.

[Download the Windows x64 package](https://github.com/yourbirthcertificate/wiztree-mcp/releases/latest/download/wiztree-mcp-windows-x64.zip)

## Connect an AI client

1. Extract the package into a folder you plan to keep. Install WizTree on the Windows machine that will run the server. This release was tested with WizTree 4.34. [WizTree download](https://diskanalyzer.com/download)
2. For an app with an **Add MCP server** form, choose **stdio**, name it `wiztree`, and set **command** to the full path of `wiztree-mcp.exe`. Leave **arguments** empty. The app starts the server when it connects.
3. For a config-file client, open PowerShell in the extracted folder and use the matching command below. Each command prints a snippet containing the executable’s actual path. Merge the `wiztree` entry into your existing settings, then reload the client.

| Client | Print settings | Where to use them |
| --- | --- | --- |
| Claude Desktop | `.\wiztree-mcp.exe --config claude` | Local MCP servers in `claude_desktop_config.json`. Fully quit and reopen Claude Desktop after saving. [Local server setup](https://py.sdk.modelcontextprotocol.io/get-started/real-host/) |
| Cursor | `.\wiztree-mcp.exe --config cursor` | User or project `.cursor/mcp.json`. [Cursor MCP settings](https://cursor.com/docs/mcp) |
| VS Code | `.\wiztree-mcp.exe --config vscode` | Run **MCP: Open User Configuration**, or use the workspace’s `.vscode/mcp.json`. [VS Code MCP settings](https://code.visualstudio.com/docs/agents/reference/mcp-configuration) |
| Codex | `.\wiztree-mcp.exe --config codex` | Merge the generated TOML table into your Codex `config.toml`. |
| Other clients using `mcpServers` | `.\wiztree-mcp.exe --config json` | The client’s JSON configuration for local MCP servers. |

You can also register directly from PowerShell in the extracted folder with the Codex CLI.

```powershell
codex mcp add wiztree -- (Get-Item .\wiztree-mcp.exe).FullName
```

The server looks for the WizTree installer's registered location, then the standard Windows installation folders and `PATH`. For a portable copy, set `WIZTREE_PATH` to the full path of `WizTree64.exe` to override discovery.

Scans run with the current user's access by default. To let WizTree read the NTFS master file table for whole-drive scans, set `WIZTREE_ADMIN=1` in the MCP server's environment and approve the Windows UAC prompt. Cancelling from an unelevated MCP server stops it from waiting for the report. The elevated WizTree process may continue and leave a temporary export behind.

## Clients that take a URL

Start the executable in PowerShell and keep the terminal open.

```powershell
.\wiztree-mcp.exe --http
```

Set the client’s transport to **Streamable HTTP** and its URL to `http://127.0.0.1:57421/mcp`. To choose another port, add `--port 57422` and update the URL. This endpoint accepts connections from the same PC. A cloud-only AI service needs a reachable, authenticated gateway to access tools on this computer.

## Tools

| Tool | Use |
| --- | --- |
| `list_drives` | List Windows drive letters and types. Fixed drives also report total and available bytes when accessible. |
| `scan` | Supply a drive, absolute folder path, or accessible UNC share. The report includes totals, the largest files in the whole tree, the largest immediate subfolders, and file types ranked by total bytes. |
| `get_scan` | Retrieve the report using its `scan_id`. For a running scan, the call waits for up to `wait_seconds` (0–15, default 5). |
| `cancel_scan` | Stop a running scan by ID. |

For example, `scan` with `{"path":"D:\\Media","rank_by":"allocated","limit":5}` returns up to five results per ranking. Choose `size` for logical file length. The default is `allocated`, with ten results per ranking. The limit accepts 1–100. To explore a folder in more detail, pass its path to `scan`.

WizTree supplies the root and folder totals. Parent folder sizes already include their descendants. File-type totals group file entries by their last extension, ignoring case. An empty extension represents files such as `README` and `.env`. Additional hard-link entries marked by WizTree contribute zero allocated bytes. Scans use the account’s existing access rights, so protected folders can be absent from the results. [WizTree export format](https://diskanalyzer.com/guide)

A small scan returns its report immediately. Longer scans return `running` and a `scan_id`. One scan runs at a time per server process, with a one-hour timeout. Repeating an active scan with the same normalized path, ranking metric and limit reuses that scan's ID. Requests for a different scan can be retried after the active one finishes. The last eight scan IDs remain available while that process runs. The server streams a temporary CSV from disk, removes it after processing, and keeps the summaries in memory. Results sent to the AI client include paths and sizes.

Clients that read `structuredContent` can add `--summary-text` to the launch arguments. Completed scans then carry a short text summary alongside the full structured report. The default also includes the full report as JSON text for clients that only read text.

## Build from source

On Windows with Go 1.25 or later, run these commands in the source folder.

```powershell
go test ./...
go build -trimpath -ldflags="-s -w" -o wiztree-mcp.exe .
```

The server uses the official Go MCP SDK. Dependency versions and checksums are in `go.mod` and `go.sum`. Third-party license texts accompany the executable. [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)
