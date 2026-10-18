# 使用 koto

[English](../usage.md) · [简体中文](../zh/usage.md) · [繁體中文](usage.md) · [日本語](../ja/usage.md) · [Deutsch](../de/usage.md)

koto 讀取設定中已啟用的 Agent 目錄。搜尋支援已索引的標題、工作階段 ID、首則提問、專案路徑與 Agent 名稱。

## 複製工作階段

1. 開啟工作階段，按「選擇複製範圍」，預設全選。
2. 保留全部內容，或按一下、拖曳選取輪次、提問和回覆。
3. 選擇目標 Agent，查看轉換說明。複製後可開啟新的工作階段接著聊。

也可以複製到相同的 Agent。每次複製都建立新工作階段，依原始順序保留所選訊息，來源保持不變。目標使用自己的模型、服務商、權限與系統設定，不帶入來源的系統及開發者指令。

缺少檔案或不支援的內容會在轉換說明中提示，詳見[附件支援](compatibility.md)。

## 帳號與目錄

在對話清單依來源目錄或已辨識帳號篩選；其他 Agent 目錄可在設定中新增。無法辨識帳號的對話仍會顯示，服務商或模型名稱不會被當作帳號。

複製時可選擇目標目錄，同產品預設使用來源目錄。只有目標按帳號儲存對話時才需要選帳號；唯一的合法帳號會自動選取。續聊使用目標自己的登入或 API 設定，koto 不複製憑據。

```sh
koto profiles --json
koto threads --profile '<profile-id>' --json
koto copy '@<source-profile>:<session-id>' --to pi --to-profile '<target-profile>' --json
```

使用 koto 回傳的準確 profile 與對話識別碼。`--to-root <目錄>` 註冊現有 Agent 資料目錄並複製至其中；`--to-account <id>` 僅用於支援選擇帳號的目標。不同目錄有相同對話 ID 時，須明確指定 profile。

## 比較工作階段

進入「對話對比」，選擇同一 Agent 的 2–32 個工作階段。用樹狀檢視查看共同歷史與分支，再任選兩個工作階段逐行比較。

儲存的比較只記錄工作階段參照。可以修改名稱及清單，刪除比較不會刪除工作階段。樹根據目前內容計算；移動或刪除原始工作階段後，對應參照可能失效。

## 讓 Agent 幫你複製

App 和 CLI 從 [Releases](https://github.com/Seanium/koto/releases) 分別下載。CLI 不依賴 App 或 WebView，`--help` 內含 Agent 操作說明。

也可以透過命令列安裝 CLI：

macOS / Linux:

```sh
curl -fsSL https://kotoai.site/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://kotoai.site/install.ps1 | iex
```

預設安裝到 macOS/Linux 的 `~/.local/bin` 或 Windows 的 `%LOCALAPPDATA%\Programs\koto\bin`。若安裝程式提示，請將該目錄加入 PATH。

將 CLI 放到 PATH 中的目錄，或告訴 Agent 完整路徑。Windows 續聊指令在 PowerShell 執行。Windows App 是直接執行的 `.exe`；Linux App 提供二進位檔與 DEB，皆不綁定 CLI。執行 Linux App 二進位檔前，先執行 `chmod +x koto-linux-<架構>`。

直接說：**「用 koto 複製目前的對話到 pi」**

OpenCode 未提供宿主工作階段 ID 時，koto 透過正在執行的工具呼叫確認目前的工作階段。

自動辨識使用宿主提供的 `CODEX_THREAD_ID`、`PI_SESSION_ID`、`DSH_SESSION_ID`、`ANTIGRAVITY_CONVERSATION_ID`、`CURSOR_CONVERSATION_ID` 或 `CLAUDE_CODE_SESSION_ID`。沒有這些資訊時，需要確切的 `agent:session-id`，可直接指定或透過 `KOTO_SESSION` 提供。明確設定的 `KOTO_SESSION` 優先；多個宿主 ID 衝突時會報錯。koto 不依時間或專案目錄猜測工作階段。

WorkBuddy 使用 `CODEBUDDY_SESSION_ID` 與相符的 `WORKBUDDY_CONFIG_DIR` 確認來源；只有對話 ID 無法確定 WorkBuddy 目錄。

回覆會說明複製數量、轉換情況，並提供繼續聊的指令。目前未完成的輪次（包括這則複製請求）不會帶入新對話。原始對話保持不變，目標 Agent 不會自動啟動。

DeepSeek Harness 使用 headless 介面繼續指定會話。執行命令並輸入下一則訊息：macOS/Linux 換行後按 **Ctrl+D** 提交；Windows 依提示輸入，按 **Enter** 提交。每次執行回答一則訊息。

Windows ARM 上使用 OpenCode 1.19.3 時，請選官方 x64 版；原生 ARM 版的終端介面目前無法啟動。

## CLI 與 MCP

將下方範例 ID 替換為實際工作階段 ID。`--dry-run` 預覽複製，`--help` 查看完整參數。

```sh
koto scan
koto threads
koto show 'claude-code:<session-id>'
koto current --json
koto copy --current --to pi --json
koto copy 'claude-code:<session-id>' --to pi --dry-run
koto copy 'claude-code:<session-id>' --to pi --json
koto analyze 'claude-code:<session-a>' 'claude-code:<session-b>' --json
```

`koto` 不在 PATH 時，請改用已安裝程式的完整路徑。`--json` 只在複製成功後回傳 `state: "copied"`，預覽回傳 `state: "preview"`；失敗會回傳錯誤與非零結束碼。任意選取提問、回覆等內容請使用 App。

`koto mcp` 透過 stdio JSON-RPC 提供唯讀的 `list_sessions` 和 `get_session` 工具。

## 更新

在 App「設定 → 一般」中檢查更新，有新版時點選「更新並重新啟動」。

更新 CLI：

```sh
koto update --check
koto update
```

App 和 CLI 分別從 GitHub Releases 下載，安裝前驗證 SHA-256。正式版預設只更新到正式版；加入 `--channel beta` 可接收預覽版。

## 設定與儲存

語言可選英語、簡體中文、繁體中文、日語、德語或跟隨系統；外觀可選跟隨系統、淺色和深色。

koto 在本機儲存索引、偏好與比較參照，不保存對話備份或複製歷史。複製的附件屬於新的 Agent 工作階段。複製只由使用者手動觸發。

索引結構或讀取規則變更時會自動重建，對話逐步顯示；原 Agent 檔案與已儲存設定保持不變。

App 和 CLI 共用本機資料目錄：macOS 為 `~/Library/Application Support/koto`，Windows 為 `%LOCALAPPDATA%\koto`，Linux 為 `$XDG_DATA_HOME/koto`（預設 `~/.local/share/koto`）。可透過 `KOTO_HOME` 或 `--data` 指定其他目錄。
