# 使用 koto

[English](../usage.md) · [简体中文](../zh/usage.md) · [繁體中文](usage.md) · [日本語](../ja/usage.md) · [Deutsch](../de/usage.md)

koto 讀取設定中已啟用的 Agent 目錄。搜尋支援已索引的標題、工作階段 ID、首則提問、專案路徑與 Agent 名稱。

## 複製工作階段

1. 開啟工作階段，按「選擇複製範圍」，預設全選。
2. 保留全部內容，或按一下、拖曳選取輪次、提問和回覆。
3. 選擇目標 Agent，查看轉換說明。複製後可開啟新的工作階段接著聊。

也可以複製到相同的 Agent。每次複製都建立新工作階段，依原始順序保留所選訊息，來源保持不變。目標使用自己的模型、服務商、權限與系統設定，不帶入來源的系統及開發者指令。

缺少檔案或不支援的內容會在轉換說明中提示，詳見[附件支援](compatibility.md)。

## 比較工作階段

進入「對話對比」，選擇同一 Agent 的 2–32 個工作階段。用樹狀檢視查看共同歷史與分支，再任選兩個工作階段逐行比較。

儲存的比較只記錄工作階段參照。可以修改名稱及清單，刪除比較不會刪除工作階段。樹根據目前內容計算；移動或刪除原始工作階段後，對應參照可能失效。

## 讓 Agent 幫你複製

CLI 隨 App 提供，`--help` 內含 Agent 操作說明。

| 安裝套件 | CLI |
| --- | --- |
| macOS | `/Applications/koto.app/Contents/Helpers/koto` |
| Linux DEB | `koto` |
| Linux 壓縮檔 | 解壓縮目錄內的 `./koto` |

macOS 安裝套件不會將 CLI 加入 PATH。Agent 找不到指令時，告訴它上面的路徑；App 裝在其他位置時，調整路徑即可。

直接說：**「用 koto 複製目前的對話到 pi」**

自動辨識使用宿主提供的 `CODEX_THREAD_ID`、`PI_SESSION_ID`、`DSH_SESSION_ID`、`ANTIGRAVITY_CONVERSATION_ID` 或 `CLAUDE_CODE_SESSION_ID`。沒有這些資訊時，需要確切的 `agent:session-id`，可直接指定或透過 `KOTO_SESSION` 提供。明確設定的 `KOTO_SESSION` 優先；多個宿主 ID 衝突時會報錯。koto 不依時間或專案目錄猜測工作階段。

回覆會說明複製數量、轉換情況，並提供繼續聊的指令。目前未完成的輪次（包括這則複製請求）不會帶入新對話。原始對話保持不變，目標 Agent 不會自動啟動。

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

`koto` 不在 PATH 時，請改用表中的完整路徑。`--json` 只在複製成功後回傳 `state: "copied"`，預覽回傳 `state: "preview"`；失敗會回傳錯誤與非零結束碼。任意選取提問、回覆等內容請使用 App。

`koto trash <selector>` 將工作階段移入回收桶或 Agent 封存區。`koto restore <trash-id>` 還原工作階段，拒絕覆寫既有路徑。測試時以 `--data`、Agent 根目錄和 `--only` 指向可丟棄的目錄。

`koto mcp` 透過 stdio JSON-RPC 提供唯讀的 `list_sessions` 和 `get_session` 工具。

## 設定與儲存

語言可選英語、簡體中文、繁體中文、日語、德語或跟隨系統；外觀可選跟隨系統、淺色和深色。

koto 在本機儲存索引、偏好、比較參照與回收桶資訊，不保存對話備份或複製歷史。複製的附件屬於新的 Agent 工作階段。複製只由使用者手動觸發。
