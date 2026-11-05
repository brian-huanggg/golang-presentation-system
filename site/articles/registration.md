---
feature: registration
title: 註冊 API 與表單 (REG-001/002/003/006/009)
files:
  - backend/api/openapi.yaml
  - backend/internal/auth/handler.go
  - backend/internal/auth/service.go
  - backend/internal/auth/validate.go
  - backend/internal/auth/repo.go
  - backend/internal/auth/password.go
  - backend/internal/auth/errors.go
  - backend/internal/auth/auth_test.go
  - frontend/src/features/auth/RegisterPage.tsx
  - frontend/src/features/auth/validation.ts
  - frontend/src/features/auth/RegisterPage.test.tsx
adrs: [0003, 0005, 0014]
tags: [input-validation, transaction, error-handling, idempotency, authN, audit, dependency-direction, testability]
updated: 2026-11-05
---

# 註冊 API 與表單 (REG-001/002/003/006/009)

## 一句話摘要
訪客用員工編號註冊 Participant 帳號;重複編號可回報、截止後拒絕。

## 背景與目標
對應 PRD REG-001(欄位)、REG-002(6 位數字)、REG-003(ID 只能註冊一次)、REG-006(截止後不可註冊)、REG-009(可揭露 ID 已註冊)。使用者是未登入訪客。成功的定義:合法請求建立帳號與稽核紀錄於同一 transaction;非法/重複/截止後各有穩定的 problem code 供前端翻譯。密碼組成規則 REG-005 **不在**這次範圍。

## 架構與資料流
```mermaid
sequenceDiagram
  participant FE as RegisterPage
  participant H as Handler.Register
  participant S as Service.Register
  participant G as DeadlineGate (event.Policy)
  participant X as Argon2idHasher
  participant DB as MariaDB (WithTx)
  participant A as audit
  FE->>FE: validateRegisterForm (前端預檢)
  FE->>H: POST /api/v1/auth/register
  H->>H: MaxBytesReader + ShouldBindJSON
  H->>S: Register(ctx, input, clientIP)
  S->>G: CheckActor(false)
  alt 截止後
    S->>A: RecordStandalone(failure)
    S-->>H: 403 deadline_passed
  end
  S->>S: validateRegister (全欄位一次回報)
  S->>X: Hash (semaphore 限流)
  S->>DB: BEGIN; INSERT user
  alt 唯一索引 1062
    S->>DB: ROLLBACK
    S->>A: RecordStandalone(failure)
    S-->>H: 409 employee_id_taken
  else 成功
    S->>A: Record(tx, success)
    S->>DB: COMMIT
    S-->>H: 201
  end
```

## 程式碼導覽
| 檔案 | 職責 |
|---|---|
| `backend/api/openapi.yaml` | 契約:`/auth/register`、`RegisterRequest`、`FieldError` |
| `backend/internal/auth/handler.go:26` | 限制 body、解析 JSON、轉成 Problem Details |
| `backend/internal/auth/service.go:56` | 流程:deadline → 驗證 → hash → tx(insert + audit) |
| `backend/internal/auth/validate.go:46` | REG-001/002/004/008 欄位驗證,回傳所有欄位錯誤 |
| `backend/internal/auth/repo.go:26` | INSERT,把 MySQL 1062 轉成 `errDuplicateEmployeeID` |
| `backend/internal/auth/password.go:44` | Argon2id + semaphore;`verifyPassword` 為 login 預留 |
| `backend/internal/auth/errors.go:17` | `validation_failed`、`employee_id_taken` 等 code |
| `frontend/src/features/auth/RegisterPage.tsx:41` | 表單、`useMutation`、錯誤碼對應 i18n |
| `frontend/src/features/auth/validation.ts:26` | 前端預檢,鏡射後端規則 |

## 設計決策與取捨
- **決策**:用 DB 唯一索引判斷重複,不先 SELECT。
  - **替代方案**:先查再寫。
  - **理由**:先查再寫在兩個同時請求下都會通過(check-then-act race);索引是唯一可靠的仲裁者(`repo.go:34`)。
  - **代價**:要依賴 driver 的錯誤碼 1062(多引入 `go-sql-driver/mysql`),且只能在 INSERT 後才知道。
- **決策**:`DeadlineGate` 介面定義在 auth,而不是 import `event`。
  - **替代方案**:直接 `import event`。
  - **理由**:ADR 0003 與 `.golangci.yml` 的 depguard 禁止 auth→event;`event.Policy` 剛好滿足該介面,由 `main.go` 組裝(`service.go:22`)。
  - **代價**:介面與實作的對應只在組裝時才驗證,目前 `main.go` 還沒接,沒有編譯期保證。
- **決策**:先查 deadline,再驗證欄位。
  - **理由**:截止後不洩漏任何資訊,也省去 hash 成本(`service.go:59`)。
  - **代價**:截止後的失敗請求仍會寫一筆 audit(見「找問題」)。
- **決策**:hash 在 transaction 之外做。
  - **理由**:Argon2id 很慢且吃記憶體,不應占用 DB connection / 鎖(`service.go:71`)。
  - **代價**:deadline 只在 hash 前檢查一次(見「找問題」)。
- **決策**:欄位保持 string,一次回報所有欄位錯誤(`validate.go:46`),而非第一個錯就停。
  - **代價**:驗證函式較長;好處是表單一次顯示全部問題。

## 邊界情況與失敗處理
- 員工編號含空白、全形數字、字母 → `invalid_format`;不會 trim(`validate.go`,測試 `auth_test.go` 的 `id space not trimmed`/`id unicode digit`)。
- 重複註冊 → rollback、409、另寫 failure audit;DB 其他錯誤 → 500,細節只進 log,不回給 client(`handler.go:58`)。
- body 超過 8 KiB 或 JSON 壞掉 → 400 `invalid_request`。
- email 空字串 → 存 NULL;`Ming <a@b>` 這種 display-name 形式被拒(`validate.go:119`)。
- Hash semaphore 滿時,等待會被 `ctx` 取消(`password.go:47`)。
- audit 寫入失敗只記 log,不改變回給訪客的結果(`service.go:119`)。

## 已知落差
- **REG-005 密碼規則未實作**:只檢查必填與 ≤128 bytes(`validate.go:104` TODO)。
- **`cmd/server/main.go` 仍為空**:路由、DB、`event.Policy`、`Recorder` 尚未組裝,端點無法實際對外服務。
- **AUTH-004(rate limit)與 AUTH-009(CSRF 後端)未做**;前端已會送 CSRF header。
- 測試用 sqlmock,尚未對真實 MariaDB 驗證 1062 路徑與 CHECK constraint。

## 面試考題

### 基礎

::: details Q1. 一個註冊請求從進來到回 201,依序經過哪些步驟?
**出處**:`backend/internal/auth/service.go:56`

**參考答案**
Handler 先用 `MaxBytesReader` 限制 body 再 bind JSON;Service 先 `CheckActor(false)` 檢查截止,再 `validateRegister`,接著 Argon2id hash,最後在 `platform.WithTx` 內 INSERT user 並 `audit.Record`,兩者一起 commit。

**可能的追問**
- 為什麼 hash 在 transaction 之外?
- 為什麼 deadline 排在驗證之前?

**考察點**:能否清楚描述資料流與順序的理由。
:::

::: details Q2. 如何保證同一個員工編號只能註冊一次?
**出處**:`backend/internal/auth/repo.go:26`、`backend/migrations/000002_create_user.up.sql`

**參考答案**
`user.employee_id` 有 `UNIQUE KEY`;INSERT 時若違反,driver 回 `MySQLError` 1062,`insertUser` 轉成 `errDuplicateEmployeeID`,Service 再轉成 409 `employee_id_taken`。不先 SELECT,所以並發下也只會有一個成功。

**可能的追問**
- 如果有第二個 unique key,1062 還能直接當作 ID 重複嗎?
- 為什麼不是 `INSERT ... ON DUPLICATE KEY`?

**考察點**:race condition 與 DB constraint 的角色。
:::

::: details Q3. REG-002 的「6 位數字」在哪些層級被檢查?
**出處**:`validate.go:46`、`frontend/.../validation.ts:21`、migration 的 `chk_user_employee_id`

**參考答案**
三層:前端預檢(省一次來回)、後端 `platform.ParseEmployeeID`(權威)、DB CHECK(最後防線)。前端只是 UX,不能取代後端。

**可能的追問**
- 全形數字 `１２３４５６` 會怎樣?(後端逐 byte 比對 `'0'..'9'`,被拒)

**考察點**:defense in depth、不信任 client。
:::

### 進階

::: details 🔥 Q4. 為什麼 `DeadlineGate` 是 auth 自己定義的介面?換成直接 import event 會怎樣?
**出處**:`service.go:22`、`backend/.golangci.yml`(auth 規則)

**參考答案**
ADR 0003 規定 auth 只能依賴 platform、audit;depguard 會讓 import event 的 PR 在 lint 失敗。依賴反轉讓 auth 只依賴「我需要什麼」,`event.Policy` 靠 Go 的結構型別隱式滿足,由 `main.go` 組裝。測試也因此可以用 fake gate(`auth_test.go` 的 `fakeGate`)。

**可能的追問**
- 怎麼確保 `event.Policy` 真的滿足這個介面?(目前沒有編譯期斷言,要等 main.go 組裝;缺點)
- 這個介面為何只帶 `isAdmin bool`?

**考察點**:模組邊界、interface 放在 consumer、可測試性。
:::

::: details Q5. Argon2id 為什麼要加 semaphore?參數怎麼選的?
**出處**:`password.go:20-60`

**參考答案**
每次 hash 需 19 MiB 記憶體,突發流量可能耗盡記憶體,所以最多 4 個並行(約 76 MiB);等待者尊重 `ctx` 取消。參數為 OWASP 最低值(m=19456, t=2, p=1),編進 PHC 字串,日後調高不會讓舊 hash 失效。

**可能的追問**
- semaphore 滿了排隊的請求會怎樣?(只有 ctx 逾時才放棄,沒有上限,見 Q9)
- 為什麼不直接 bcrypt?(PRD NFR-SEC-003 指定 Argon2id)

**考察點**:資源保護、context cancellation、密碼學基本功。
:::

::: details Q6. 前端怎麼處理 400 `validation_failed` 與 403 / 409?
**出處**:`RegisterPage.tsx:41-77`

**參考答案**
`unwrap` 把非 2xx 轉成帶 Problem 的 `ApiError`。`onError` 把 `errors[]` 對應到各欄位;`deadline_passed`、`employee_id_taken` 等已知 code 顯示翻譯後的 banner,未知 code 一律顯示通用訊息,`validation_failed` 不重複顯示 banner。依 I18N-003,伺服器文字是英文,前端只翻 code。

**可能的追問**
- 為什麼不直接顯示 `problem.detail`?

**考察點**:契約驅動的錯誤模型、i18n。
:::

### 設計/情境

::: details 🔥 Q7. 要加上 REG-005 密碼規則與 rate limit,要動哪裡?
**出處**:`validate.go:104`、`handler.go:26`

**參考答案**
密碼規則放進 `checkPassword`(同一個 validator 之後給改密碼重用),回傳新的 field code 並補 i18n;前端 `validation.ts` 同步鏡射。Rate limit 屬 platform 中介層,掛在 `/api/v1/auth/*` 路由群組,以 `c.ClientIP()` 為 key,同時要設定 Gin `TrustedProxies`,否則 IP 可被偽造。驗證:table-driven 測試涵蓋 `abc`、`123`、`aaa`,以及超過門檻後回 429 的 handler 測試。

**可能的追問**
- in-process limiter 在多 instance 下的缺點?

**考察點**:擴充點是否清楚、安全面向。
:::

::: details Q8. 如果註冊流量放大 10 倍(例如截止前一小時),瓶頸在哪?怎麼驗證?
**出處**:`password.go:26`、`service.go:75`

**參考答案**
瓶頸是 Argon2id 的 CPU/記憶體,而不是 DB。並行上限 4 會讓請求排隊,延遲上升。可調整並行數與參數(需先量測)、加 rate limit、前端提示重試。驗證:用壓測量 hash 耗時與 p95;待確認實際單次 hash 耗時(我沒有量測過)。

**可能的追問**
- 把 hash 移到非同步 job 可以嗎?(註冊要立即可登入,不適合)

**考察點**:容量估算、不編造數據。
:::

### 找問題

::: details 🔥 Q9. 這段程式碼有什麼潛在問題?
**出處**:`service.go:59`、`service.go:96`、`service.go:75`、`password.go:47`

**問題**(皆在程式碼中看得到):
1. **Audit 被放大濫用**:未登入者每送一次「截止後」或「重複 ID」請求,就同步寫一筆 `audit_record`(`RecordStandalone`),audit 又是 append-only、保留一年,攻擊者可灌爆表。目前沒有 rate limit。
2. **DDL-005 的時間點**:deadline 只在 hash 之前檢查,Architecture 要求「動作完成時」檢查;hash 與 INSERT 之間若剛好跨過截止會放行(視窗很小,但與文件不一致)。
3. **Hash 排隊無上限**:semaphore 滿時每個請求都 goroutine 等待,只靠 ctx 逾時,沒有 server write timeout 就可能堆積。
4. **弱密碼可註冊**:REG-005 尚未實作。
5. **REG-009 的帳號枚舉**:設計上接受,但配合 1 無 rate limit 會讓枚舉變容易。
6. `MaxBytesReader` 超限時回 400 `invalid_request`,而非 413(輕微)。

**如何修**:對 `/auth/*` 加 rate limit;failure audit 改為取樣或僅在 rate limit 之內寫;在 `WithTx` 內再 `CheckActor` 一次;對排隊數量設上限(超過回 503);補 REG-005。

**如何驗證**:handler 測試模擬連續 N 次重複請求並檢查 audit 筆數;用 `ManualClock` 在 hash 與 insert 之間跨過 deadline 的測試;壓測 semaphore 排隊。
:::

::: details Q10. 目前的測試能證明註冊正確嗎?
**出處**:`backend/internal/auth/auth_test.go`

**問題**:測試用 sqlmock 與 fake gate/hasher,證明的是「Service 呼叫順序與錯誤對應」;它**不**證明真實 MariaDB 會回 1062、CHECK constraint 是否一致、或 `event.Policy` 接上後行為正確。1062 的 `MySQLError` 是手工構造的。
**如何修**:加整合測試(docker-compose 已有 MariaDB 的話)跑兩個並發註冊同一 ID,斷言只有一筆 user。
**如何驗證**:該並發測試與 `SELECT COUNT(*)`。
:::

## 三分鐘口頭說明稿
這個功能是讓員工用 6 位數字的員工編號自助註冊,對應 PRD 的 REG-001 到 REG-009 中的欄位、唯一性、截止與重複回報。我先照 ADR 0005 在 OpenAPI 契約定義請求與錯誤模型,再由 oapi-codegen 產生 Go 與 TypeScript 型別,所以前後端不會漂移。

後端在 auth package 內分成 handler、service、repo。Service 的順序是先查截止時間,再驗證所有欄位並一次回報錯誤,接著做 Argon2id hash,最後在同一個 transaction 寫 user 與 audit。重複 ID 我不先 SELECT,而是依賴唯一索引並把 MySQL 1062 轉成 409,避免並發時的 check-then-act race。截止檢查用 auth 自己定義的介面,讓 event.Policy 隱式滿足,這樣符合 ADR 0003 的依賴方向,也方便測試。

取捨方面,hash 放在 transaction 外以免占用連線,代價是截止時間只在 hash 前檢查一次;未登入的失敗請求也會寫 audit,在沒有 rate limit 前有被灌爆的風險。這些我都列在已知落差。目前密碼組成規則、rate limit、CSRF 後端與 main.go 組裝還沒做,測試也還只用 sqlmock,下一步是補整合測試驗證並發註冊。
