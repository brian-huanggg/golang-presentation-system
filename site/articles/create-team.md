---
feature: create-team
title: 建立團隊 all-or-nothing (TEAM-001~008)
files:
  - backend/migrations/000005_create_team.up.sql
  - backend/internal/team/service.go
  - backend/internal/team/repo.go
  - backend/internal/team/validate.go
  - backend/internal/team/handler.go
  - backend/internal/team/errors.go
  - backend/internal/team/model.go
  - backend/internal/team/service_test.go
  - backend/internal/team/validate_test.go
  - backend/internal/team/handler_test.go
  - backend/api/openapi.yaml
  - backend/internal/platform/problem.go
  - backend/cmd/server/main.go
adrs: [0003, 0005, 0014]
tags: [transaction, concurrency, locking, input-validation, error-handling, authZ, audit, testability]
updated: 2026-11-05
---

# 建立團隊 all-or-nothing (TEAM-001~008)

## 一句話摘要
組長一次提交團隊與成員 ID,任何 ID 不合格就整筆失敗並逐一回報原因。

## 背景與目標
對應 PRD TEAM-001~008、EVT-02、DDL-003/005、AUD-001。使用者是尚未入隊的 Participant。成功的定義:

- 不存在違反「一人一隊」「人數 ≤ 5」「同部門」的團隊(G2)。
- 失敗時資料庫裡沒有任何殘留,且回應一次列出**所有**不合格 ID 與原因,組長一次改完。
- 成功與失敗都有 audit(AUD-001)。

## 架構與資料流
```mermaid
sequenceDiagram
  participant C as Client
  participant H as Handler
  participant S as Service.Create
  participant A as auth.CurrentUser
  participant DB as MariaDB
  C->>H: POST /teams (CSRF middleware 已驗)
  H->>H: MaxBytesReader 4 KiB + JSON bind
  H->>S: Create(input, ip)
  S->>A: 取 session user
  S->>S: Admin? → 403;CheckActor(deadline)
  S->>S: validateCreate (不碰 DB)
  S->>DB: BEGIN
  S->>DB: SELECT user ... ORDER BY id FOR UPDATE
  S->>DB: SELECT team_member ... FOR UPDATE
  S->>S: 逐一檢查成員;有錯 → 回滾
  S->>S: 再 CheckActor(deadline)
  S->>DB: INSERT team / team_member x N / audit_record
  S->>DB: COMMIT
  S-->>H: Team
  Note over S: 任何 Problem → 回滾後 RecordStandalone(failure)
```

## 程式碼導覽
| 檔案 | 職責 |
|---|---|
| `backend/internal/team/service.go:61` | `Create`:授權、deadline、驗證、開 transaction、事後記失敗 audit |
| `backend/internal/team/service.go:125` | `createInTx`:鎖定、成員檢查、寫入、成功 audit |
| `backend/internal/team/validate.go:45` | `validateCreate`:純函式,不碰 DB,收集所有欄位與 ID 形狀錯誤 |
| `backend/internal/team/repo.go:45` | `lockUsers`:`FOR UPDATE` 且 `ORDER BY id` |
| `backend/internal/team/repo.go:60` | `teamedUserIDs`:locking read 查已入隊者 |
| `backend/internal/team/repo.go:109` | `isDuplicateOn`:用 index 名稱區分 1062 |
| `backend/internal/team/handler.go:28` | `CreateTeam`:body 上限、DTO 轉換 |
| `backend/migrations/000005_create_team.up.sql:24` | 隊名唯一(含軟刪除釋出)、一人一隊、單一組長 |
| `backend/internal/platform/problem.go` | `FieldError.Value`:指出是哪個 ID |

## 設計決策與取捨
- **決策**:規則同時在 service 檢查與資料庫 unique index 強制。
  - **替代方案**:只靠 service 檢查,或只靠 index。
  - **理由**:service 檢查才能回「每個 ID 的原因」(TEAM-008);index 是並發下的最後防線(Architecture 7.2)。
  - **代價**:兩處要保持一致;index 擋下時只能給籠統的 409 `already_in_team`(`errMemberRaced`),因為 1062 訊息不告訴你是哪一列。
- **決策**:先鎖 `user` 列(依 id 排序),再做 locking read 查 membership。
  - **替代方案**:先 `SELECT` 檢查、失敗再重試;或直接 `SERIALIZABLE`。
  - **理由**:兩個 transaction 搶同一個人時,後者在 user 鎖上等待,醒來後 locking read 看得到前者已 commit 的 membership(RR 下一般 SELECT 讀的是舊 snapshot)。固定鎖順序避免 deadlock。
  - **代價**:鎖持有到 commit;`team_member` 的 locking read 可能帶 gap lock(見「找問題」)。
- **決策**:Admin 的工號出現在成員清單時回 `not_registered`,與不存在無法區分(`service.go:167`)。
  - **理由**:R-04,不洩漏誰是 Admin。**代價**:使用者看不出真正原因。
- **決策**:審計失敗記錄在 transaction **之後**用 `RecordStandalone`(`service.go:99`)。
  - **理由**:放在 tx 內會隨回滾消失。**代價**:失敗 audit 與業務不是原子;寫失敗只 log,不影響回應。
- **決策**:deadline 檢查兩次(入口與 tx 內,`service.go:180`)。理由 DDL-005、ADR 0014。
- **決策**:`team` 直接唯讀查 `user` 表,不呼叫 auth 的 repo。理由:ADR 0003 規則 3 禁止跨 feature repo;代價:對 `user` 欄位有隱性耦合。

## 邊界情況與失敗處理
- 名單 > 4:只回 `too_many` 就返回,不再看形狀(`validate.go:82`)。
- 同一 ID 重複:第一次算數,之後每次都回 `duplicate`;組長自己的 ID 回 `is_leader`。
- 格式錯的 ID 不 trim(REG-002),回應只回聲前 20 個字元,避免放大。
- 組長自己已入隊:先於成員錯誤回 409(`service.go:154`)。
- 隊名撞到(含大小寫不同):1062 且 index 名為 `uq_team_name_active` → 409 `team_name_taken`;其他 1062 視為內部錯誤。
- 成員在驗證後被別的 tx 搶走:1062 `uq_team_member_year_user` → 409,整個 tx 回滾。
- audit 寫入失敗:成功路徑整筆回滾(不允許無 audit 的團隊)。
- DB 錯誤:500,細節只進 log。

## 已知落差
- 沒有真實 MariaDB 的整合測試;鎖與 unique index 行為只用手動 SQL 驗過(暫存資料庫,已刪)。
- 專案還沒有 `cmd/migrate`,`000005` 未套用到開發資料庫。
- ADR 0004(sqlx 與手寫 SQL)、0011(audit 與業務同一 tx)尚未撰寫,本文只能引用 Architecture 對應章節。
- 前端表單、查詢團隊 API 尚未實作。

## 面試考題

### 基礎

::: details Q1. 一次建立請求從進來到 commit,經過哪些檢查?順序為什麼是這樣?
**出處**:`backend/internal/team/service.go:61`

**參考答案**
先取 session user,再依序:Admin 擋掉、deadline、`validateCreate`(不碰 DB)、開 transaction、鎖 user、查 membership、逐一檢查成員、再檢查一次 deadline、寫入、寫 audit。便宜且不需要 DB 的檢查放前面,避免無謂地開 transaction 與持鎖。

**可能的追問**
- 為什麼 deadline 要檢查兩次?(DDL-005:以「動作完成」的時間為準)
- 為什麼形狀驗證不在 tx 內?

**考察點**:理解資料流與 fail-fast。
:::

::: details Q2. 「all-or-nothing」在這裡由什麼機制保證?
**出處**:`backend/internal/team/service.go:93`、`backend/internal/platform/tx.go`

**參考答案**
`platform.WithTx` 包住 `createInTx`,回傳任何 error 就 rollback。成員驗證失敗是回 `*Problem`,同樣觸發 rollback,所以 team 與 team_member 都不會寫入。audit 也在同一個 tx,三者一起成功或一起消失。

**可能的追問**
- 如果 `INSERT` 第三個成員失敗,前兩個會怎樣?
- panic 時呢?(`WithTx` 的 recover 會 rollback 後重新 panic)

**考察點**:transaction 邊界。
:::

::: details Q3. 為什麼要有 `FieldError.Value`?為什麼不用 `memberIds[2]` 這種欄位名?
**出處**:`backend/internal/platform/problem.go`、`backend/internal/team/validate.go:109`

**參考答案**
TEAM-008 要「對每個無效 ID 說明原因」。名單順序在去重與過濾後會和請求不同,索引不可靠;直接帶 ID 本身,前端就能對應到輸入框。代價是 platform 的共用型別多一個可選欄位。

**可能的追問**
- 回聲使用者輸入有什麼風險?(長度,已截 20 字元;JSON 編碼避免注入)

**考察點**:API 設計與輸入處理。
:::

### 進階

::: details 🔥 Q4. 兩位組長同時把同一個人加進各自的團隊,會發生什麼?你怎麼保證只有一個成功?
**出處**:`backend/internal/team/repo.go:45`、`backend/internal/team/repo.go:60`、`backend/migrations/000005_create_team.up.sql:44`

**參考答案**
三層:(1) 兩個 tx 都 `SELECT user ... ORDER BY id FOR UPDATE`,共同的人讓後者阻塞,且固定 id 順序避免互相等待。(2) 後者醒來後用 `FOR UPDATE` 的 membership 查詢;locking read 讀最新已 commit 資料,看得到前者的列,於是回 `already_in_team`。一般 `SELECT` 在 REPEATABLE READ 讀的是 tx 開始時的 snapshot,會漏看。(3) 最後防線是 `UNIQUE (event_year, user_id)`,1062 轉成 409。

**可能的追問**
- 如果拿掉 `ORDER BY id` 會怎樣?(A 鎖 1→2,B 鎖 2→1 → deadlock)
- 如果 membership 查詢不加 `FOR UPDATE`?
- 為什麼不直接設 SERIALIZABLE?

**考察點**:isolation level、locking read、deadlock。
:::

::: details 🔥 Q5. 為什麼 unique index 已經能擋住重複入隊,還要先查一次?
**出處**:`backend/internal/team/service.go:154`

**參考答案**
index 只能擋下並回 1062,無法告訴你是哪一個 ID、也無法一次回報多個(第一個衝突就中止 tx)。TEAM-008 要求逐一回報原因,所以要先查;index 是並發情況下的保險。

**可能的追問**
- index 擋下時回應有什麼取捨?(籠統 409,請使用者重試)

**考察點**:應用層檢查與 DB constraint 的分工。
:::

::: details Q6. 隊名「大小寫不分、釋出後可重用」是怎麼做到的?
**出處**:`backend/migrations/000005_create_team.up.sql:22`

**參考答案**
資料表 collation 為 `utf8mb4_unicode_ci`,所以比較不分大小寫。`name_active` 是 generated column:未刪除時等於 `name`,已刪除時為 NULL;`UNIQUE (event_year, name_active)` 因為 NULL 不互相衝突,軟刪除後名稱立即可用(TEAM-015)。

**可能的追問**
- MariaDB 沒有 partial unique index,所以才用 generated column。
- `unicode_ci` 只有大小寫不分嗎?(見 Q9)

**考察點**:schema 設計技巧。
:::

### 設計/情境

::: details Q7. 之後要加「組長新增成員」(TEAM-010),哪些部分可以重用?哪些要改?
**出處**:`backend/internal/team/service.go:125`

**參考答案**
成員檢查(not_registered / department / already_in_team)應抽成函式重用;新增需要鎖 `team` 列(`SELECT ... FOR UPDATE`)以守住人數 ≤ 5(Architecture 7.2),因為此時不再是「一次插入全部」。部門比對要改成與團隊部門而非組長比對。

**可能的追問**
- 兩個人同時加第 5 位成員怎麼辦?(鎖 team 列序列化)

**考察點**:擴充性與並發設計。
:::

::: details Q8. 活動期間有 5,000 隊同時在截止前衝刺,你預期瓶頸在哪?怎麼驗證?
**出處**:`backend/internal/team/repo.go:60`

**參考答案**
每次建立持有數把 row lock 到 commit,時間很短;熱點是互相重疊的成員名單與 `team_member` index 上的 gap lock。PRD 無效能目標,~5,000 隊負載很小。若要驗證:用多個 goroutine 對真實 MariaDB 建立重疊名單,觀察 deadlock 與 1213 錯誤數、`SHOW ENGINE INNODB STATUS`。**待確認**:目前沒有任何壓測數據。

**考察點**:容量思考與如何驗證而不是猜。
:::

### 找問題

::: details 🔥 Q9. 這段程式碼有什麼潛在問題?
**出處**:`backend/internal/team/repo.go:61`、`backend/migrations/000005_create_team.up.sql:24`

**問題**
1. `team_member` 的 `FOR UPDATE` 在 REPEATABLE READ 下對不存在的 `(event_year, user_id)` 可能取得 gap/next-key lock,和別的建立請求的 INSERT 互相等待,理論上有 deadlock 風險(1213)。目前 `Create` 對 1213 沒有重試。**待確認**,尚未在真實 DB 重現。
2. 隊名唯一性靠 `utf8mb4_unicode_ci`,它同時忽略重音與部分寬度差異(例如 `Café` 與 `Cafe` 視為相同),比 PRD 寫的「大小寫不分」更寬;使用者可能看到無法理解的 `team_name_taken`。
3. 單元測試全用 sqlmock,驗證的是 SQL 字串與回應,不能證明鎖與 unique index 真的如設計運作。
4. 失敗 audit 在 tx 之外,進程在 rollback 與 `RecordStandalone` 之間當掉會少一筆失敗記錄(成功記錄不受影響)。

**如何修**:對 1213 加有限次重試;或改用 `ON DUPLICATE`/`INSERT` 衝突回報;名稱唯一性改用 `utf8mb4_bin` 配 `LOWER(name)` generated column,或把行為寫進 PRD;補 testcontainers 整合測試。

**如何驗證**:對真實 MariaDB 跑並發重疊名單測試並統計 1213;對 `Café`/`Cafe` 寫 migration 層測試。
:::

::: details Q10. 為什麼 Admin 的 ID 回 `not_registered`,不回 `is_admin`?
**出處**:`backend/internal/team/service.go:167`

**參考答案**
資訊最小化:組長沒有理由知道誰是 Admin;R-04 也規定 Admin ID 不能當 Participant。代價是使用者除錯時看不出真正原因,這是有意的取捨。

**考察點**:資訊洩漏與可用性的平衡。
:::

## 三分鐘口頭說明稿
這個功能是讓組長一次提交團隊名稱、賽道、獎項和 0 到 4 位成員的員工編號。需求的重點是 all-or-nothing:任何一個 ID 不合格就整筆失敗,而且要一次列出所有不合格 ID 和原因。

做法分兩段。第一段是純函式驗證,檢查名稱長度、賽道、人數上限,以及每個 ID 的格式、重複和是否是組長自己,這段不碰資料庫。第二段在一個 transaction 裡,先依 id 排序用 `FOR UPDATE` 鎖住組長和所有成員的 user 列,再用 locking read 查誰已經入隊,逐一檢查是否註冊、同部門、未入隊,有任何錯誤就回滾並回傳完整清單。通過後再檢查一次 deadline,寫入 team、名單和 audit,一起 commit。

並發上,鎖順序固定避免 deadlock;locking read 才讀得到別人剛 commit 的資料;最後還有 `(event_year, user_id)` 的 unique index 當保險。隊名唯一用 generated column 處理軟刪除後釋出名稱。

取捨是:規則在應用層與資料庫各寫一份;失敗 audit 在 transaction 外,不是原子;Admin ID 刻意回報成「未註冊」。目前測試用 sqlmock,95% 覆蓋率,但鎖行為沒有在真實資料庫上做並發測試,這是下一步要補的,也是我最想確認的風險。
