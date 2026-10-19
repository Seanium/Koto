# 工作階段格式與附件

[English](../compatibility.md) · [简体中文](../zh/compatibility.md) · [繁體中文](compatibility.md) · [日本語](../ja/compatibility.md) · [Deutsch](../de/compatibility.md)

下表說明儲存格式的支援情況。Agent Harness 升級可能改變格式；附件能否被辨識取決於目標模型。

| Agent Harness | 讀取 | 建立副本 |
| --- | --- | --- |
| Claude Code / Claude Desktop（Code） | JSONL 與原生分支 | 新 UUID 與原生 JSONL |
| pi | v3 JSONL 樹 | 新工作階段與新的 ID / 父節點鏈 |
| Codex | rollout JSONL / zstd | 新原生 rollout，再以 app-server 驗證讀取 |
| DeepSeek Harness | v4 JSONL / zstd | 新目錄與原生壓縮日誌 |
| Antigravity CLI | SQLite 與 protobuf 軌跡 | 新對話資料庫和摘要項目 |
| OpenCode | SQLite v1（`session`、`message`、`part`） | 在原生資料庫中新建工作階段記錄 |
| Cursor CLI | 每對話 SQLite `store.db` | 新原生 CLI 對話 |
| Cursor Desktop | `state.vscdb` 中的對話與訊息 blob | 新 IDE 對話，在 App 中開啟 |
| WorkBuddy / WorkBuddy AI | 中國版與國際版獨立目錄、SQLite 索引與 JSONL | 在既有原生帳號下建立對話 |
| ZCode | App / CLI 共用 SQLite 歷史與 App 任務索引 | 新原生對話與對應任務項目 |

支援 OpenCode 1.19.3 的 v1 格式；遇到 v2 工作階段儲存會明確拒絕。複製到 OpenCode 需要其 CLI 與已設定的預設模型，續聊使用目標模型。

所有複製都會建立新的原生工作階段，不覆寫既有工作階段 ID 或獨立檔案。目標 Agent Harness 載入自己的設定，來源的系統、開發者、環境、設定和權限上下文不會複製。目標不支援對應原生表達時，工具紀錄和推理內容可能轉換為可讀文字。

Cursor Desktop 與 CLI 的儲存與續聊入口獨立。ZCode 支援正式版的原生 Agent Harness，不含 Preview 或由 ZCode 啟動的外部 Agent Harness。WorkBuddy 中國版與國際版是獨立目標。歷史帳號標示不會改變續聊使用的憑據。

Claude Desktop 支援讀取及建立本機 Code 對話。Cowork 的既有本機日誌可讀取並複製到支援的目標，暫不支援建立 Cowork 任務；雲端 Chat 不在本機掃描範圍內。

若 Claude Desktop 未顯示複製的 Code 對話，請重新啟動應用程式以更新對話清單。

## 附件

| 素材 | Claude Code | pi | Codex | DeepSeek Harness | Antigravity CLI | OpenCode |
| --- | --- | --- | --- | --- | --- | --- |
| PNG、JPEG、GIF、WebP | 原生圖片 | 原生圖片 | 原生圖片 | 原生圖片附件 | 原生 Media | 原生檔案 |
| PDF、UTF-8 文字 | 原生文件 | 完整檔案 | 完整檔案 | 原生檔案附件 | 原生 Media | 原生檔案 |
| 音訊 | 完整檔案 | 完整檔案 | 原生音訊 | 原生檔案附件 | 原生 Media | 原生檔案 |
| DOCX、ZIP、JSON、其他二進位檔 | 完整檔案 | 完整檔案 | 完整檔案 | 原生檔案附件 | 原生 Media | 原生檔案 |

| 新增 Agent Harness | 附件表示方式 |
| --- | --- |
| Cursor CLI / Cursor Desktop | 原生圖片；完整檔案或檔案參照 |
| WorkBuddy / WorkBuddy AI | 原生圖片、使用者檔案參照；工具檔案使用驗證後的參照 |
| ZCode | 原生媒體與目標對話中的完整檔案 |

若角色或目標格式無法內嵌檔案，koto 會在新工作階段的附件目錄中保存完整位元組，並加入檔案參照。工具紀錄轉為文字時，工具結果中的附件仍會保留。

- 附件只從可辨識的原生附件來源讀取，不讀取訊息中任意提到的路徑。
- 再次複製 koto 管理的檔案參照時，會驗證目錄、實際路徑、大小和 SHA-256，拒絕外部符號連結、路徑穿越和已被修改的檔案。
- 缺少的 blob 會被回報。遠端 URI 保持參照，不會下載為離線檔案。
- 若原對話只儲存了文字附件的片段，koto 會複製已儲存的內容並加以標示，不會讀取磁碟上的目前檔案來補全。
- 個別預覽上限為 25 MiB，附件讀取上限為 256 MiB。限額不會讓檔案變成截斷的文字摘要。

## 驗證範圍

預設測試使用合成 fixture 和可捨棄的 Agent Harness 根目錄，涵蓋支援的 Agent Harness 間複製、同 Agent Harness 複製、明確選取、重複新建工作階段、路徑衝突、附件與再次複製的完整性。

原生續聊測試取決於已安裝的 Agent Harness、模型和服務商，與預設測試分開執行。目前仍未涵蓋所有模型、格式與角色組合，以及全部桌面平台。
