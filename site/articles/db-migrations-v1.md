---
feature: db-migrations-v1
title: 第一批 DB migration (user、audit_record、sessions)
files:
  - backend/migrations/000001_create_audit_record.up.sql
  - backend/migrations/000001_create_audit_record.down.sql
  - backend/migrations/000002_create_user.up.sql
  - backend/migrations/000002_create_user.down.sql
  - backend/migrations/000003_create_sessions.up.sql
  - backend/migrations/000003_create_sessions.down.sql
  - backend/scripts/restrict-audit-grants.sh
adrs: ["0007"]
tags: [database, schema-design, integrity, migration, authorization]
updated: 2026-11-05
---

# 第一批 DB migration (user、audit_record、sessions)

## 一句話摘要
用 golang-migrate 建立帳號、稽核紀錄與 session 三張表,並把不變條件盡量下推到 DB 層強制。

## 背景與目標
對應 Architecture 7.1(logical data model)、7.2(integrity rules)、7.5(schema migration),以及 PRD REG-002(Employee ID 六位數)、REG-004(department 固定四個碼)、AUTH-005~007(server-side session)、AUD-003(audit append-only)。使用者是之後所有 feature package(auth、team…)與 DBA。成功的定義:migration 可重複 up/down、DB 本身就拒絕不合法資料、app DB user 的權限與 7.2 一致。見 [ADR 0007](../../docs/adr/0007-sessions-in-mariadb-with-scs.md)。

## 架構與資料流
目前沒有 `cmd/migrate`(見「已知落差」),下圖是 Architecture 7.5 規定的流程,實際只驗證過 SQL 部分。

```mermaid
sequenceDiagram
  participant Op as Operator
  participant M as golang-migrate
  participant DB as MariaDB
  participant G as restrict-audit-grants.sh
  Op->>M: up
  M->>DB: 000001 audit_record
  M->>DB: 000002 user
  M->>DB: 000003 sessions
  M->>DB: schema_migrations 記錄版本
  Op->>G: 以 root 執行
  G->>DB: REVOKE ALL; audit_record 只給 INSERT,SELECT
  G->>DB: user, sessions 給 SELECT,INSERT,UPDATE,DELETE
```

## 程式碼導覽
| 檔案 | 職責 |
|---|---|
| `backend/migrations/000001_create_audit_record.up.sql:4` | append-only 稽核表,`result` CHECK、三個查詢 index |
| `backend/migrations/000002_create_user.up.sql:3` | 帳號表,CHECK 表達 ID 格式、role、department、language |
| `backend/migrations/000002_create_user.up.sql:24` | `employee_id` unique index(7.2 的 Employee ID unique) |
| `backend/migrations/000002_create_user.up.sql:29` | role 與 department 的交叉約束 |
| `backend/migrations/000003_create_sessions.up.sql:4` | scs mysqlstore 要求的固定 schema |
| `backend/scripts/restrict-audit-grants.sh:20` | `APP_TABLES` 決定 app 帳號可完整讀寫哪些表 |

## 設計決策與取捨
- **決策**:不變條件用 CHECK + unique index 在 DB 強制,而不只靠 Go 驗證。
  - **替代方案**:只在 `platform.ParseEmployeeID` / `ParseDepartment` 驗證。
  - **理由**:7.2 明說「concurrent requests 也不能破壞規則」;DB 是最後一道防線,繞過 service 的寫入(手動 SQL、未來的 cmd)也被擋。
  - **代價**:department、language 的清單在 Go 與 SQL 兩處維護,會 drift;新增值需要新的 migration。
- **決策**:`role` / `department` / `preferred_language` 用 `VARCHAR + CHECK`,不用 `ENUM`。
  - **替代方案**:MariaDB `ENUM`。
  - **理由**:ENUM 的順序與 ALTER 行為難處理,CHECK 較容易讀、易於以新 migration 替換。
  - **代價**:CHECK 修改要 drop 再 add constraint。
- **決策**:`sessions` 照 scs 的固定 schema,不加 `user_id` 欄位。
  - **替代方案**:自訂 store 加 indexed `user_id`。
  - **理由**:ADR 0007 接受 `Iterate` 掃全表,因為只在改密碼時發生、量約數千列。
  - **代價**:AUTH-007 的 invalidate-others 是 O(live sessions)。
- **決策**:用 table-level grant,`user`、`sessions` 進 `APP_TABLES`,`audit_record` 不進。
  - **替代方案**:database-level `GRANT ALL` 再 `REVOKE` 單表。
  - **理由**:MariaDB privilege 是累加的,database-level grant 無法被 table-level REVOKE 抵銷(腳本註解 `restrict-audit-grants.sh:6`)。
  - **代價**:每個新表都要記得加進 `APP_TABLES`,漏了 app 會 permission denied。
- **決策**:時間一律 `DATETIME(6)` 存 UTC。
  - **理由**:顯示才轉 Asia/Taipei(I18N-004);避開 `TIMESTAMP` 的 session time zone 轉換。`sessions.expiry` 是例外,因為 scs 規定。

## 邊界情況與失敗處理
- 插入 5 位數 ID、Participant 沒 department、Admin 帶 department、重複 employee_id:本地 MariaDB 11.8.9 實測全部被拒絕(CHECK / unique violation)。
- `down` 都是 `DROP TABLE IF EXISTS`,所以重複執行不會報錯,但會刪光資料;只能用在 dev。
- 部分 migration 失敗時,MariaDB 的 DDL 會隱式 commit,不能 rollback,golang-migrate 會把版本標為 dirty,需人工處理(此為 MariaDB 與 golang-migrate 的一般行為,本 repo 尚未演練)。
- 忘了跑 grants 腳本:docker-compose 預設給 app user 整個 database 的 ALL,`audit_record` 就不是 append-only。

## 已知落差
- Architecture 7.5 與腳本註解都提到 `cmd/migrate`,但 repo 內不存在(`backend/cmd/` 只有 `server`),CI 也沒有 migrate 步驟。目前 migration 尚無自動執行路徑。
- ERD(7.1)的 `user` 沒有 `created_at` / `updated_at`,這批 migration 多加了。
- 檔名 `000001` 的 `audit_record` 是前一個 ticket 建的,本次只新增 000002、000003。
- 本次驗證是手動 `mariadb < file.sql`,不是透過 golang-migrate,`schema_migrations` 流程未驗證。

## 面試考題

### 基礎

::: details Q1. `user` 表怎麼保證 Employee ID 一定是六位數字且不重複?
**出處**:`backend/migrations/000002_create_user.up.sql:24`

**參考答案**
兩層。格式用 `CHAR(6)` 加 `CHECK (employee_id REGEXP '^[0-9]{6}$')`(第 25 行);唯一性用 `UNIQUE KEY uq_user_employee_id`(第 24 行)。重複註冊同時進來時,只有一個 INSERT 會成功,另一個得到 duplicate-key error,service 再把它翻成「已註冊」的 domain error(Architecture 7.2 最後一段)。

**可能的追問**
- 為什麼不是先 SELECT 檢查再 INSERT?(race condition)
- `CHAR(6)` 與 `VARCHAR(6)` 差在哪?

**考察點**:把 invariant 放在 DB 的理由、unique index 與 race condition。
:::

::: details Q2. `sessions` 表的欄位為什麼不能自己改名或加 `user_id`?資料怎麼流動?
**出處**:`backend/migrations/000003_create_sessions.up.sql:4`

**參考答案**
scs 的 mysqlstore 寫死 `token / data / expiry` 的 SQL。`token` 是 opaque cookie 值,`data` 是 gob 編碼後的 session 內容(user id、role、CSRF token),`expiry` 讓 cleanup goroutine 刪過期列。因為 `data` 是 blob,無法用 SQL 找某個 user 的 session,所以 AUTH-007 要用 `Iterate` 掃全表(ADR 0007)。

**可能的追問**
- 為什麼 `expiry` 要 index?
- 要加 `user_id` 該怎麼做?(自訂 store + 新 ADR)

**考察點**:對第三方 library 契約的理解、ADR 取捨。
:::

### 進階

::: details 🔥 Q3. 為什麼 audit_record 的 append-only 要靠 grants 腳本,而不是在 migration 裡 `REVOKE`?
**出處**:`backend/scripts/restrict-audit-grants.sh:6`、`backend/scripts/restrict-audit-grants.sh:20`

**參考答案**
兩個原因。第一,migration 由 golang-migrate 的連線執行,它不一定有 GRANT 權限,且 Architecture 7.5 規定 migration 不放與環境相關的東西(帳號名稱)。第二,MariaDB privilege 累加:docker-compose 的 `MARIADB_USER` 預設拿到整個 database 的 ALL,table-level 沒辦法扣掉。所以腳本先 `REVOKE ALL`,再逐表授權,`audit_record` 只給 `INSERT, SELECT`。代價是新表要加進 `APP_TABLES`,這次就把 `user sessions` 加進去。

**可能的追問**
- 如果漏跑腳本會發生什麼?(audit 不再 append-only,而且沒有任何錯誤)
- 怎麼在 CI 驗證?

**考察點**:權限模型、對「安全控制靠人記得執行」的風險意識。
:::

::: details Q4. `role` 為什麼用 `VARCHAR + CHECK` 而不是 `ENUM`?CHECK 之間怎麼互相配合?
**出處**:`backend/migrations/000002_create_user.up.sql:26`、`:29`

**參考答案**
CHECK 可讀、可用新 migration 換掉,不像 ENUM 依賴定義順序。`chk_user_role_department` 把「Participant 必有 department、Admin 必為 NULL」編成一條跨欄位約束,所以 `chk_user_department` 才可以寫成 `department IS NULL OR ...`(NULL 讓 CHECK 通過,需要第二條約束補洞)。實測 Participant 無 department 與 Admin 帶 department 都被拒絕。

**可能的追問**
- SQL 中 CHECK 遇到 NULL 的三值邏輯?
- 要新增第五個 department 的步驟?

**考察點**:SQL 三值邏輯、約束設計。
:::

### 設計/情境

::: details Q5. 要新增第三種語言 `ja`,migration 該怎麼寫才符合 expand-then-contract?
**出處**:`backend/migrations/000002_create_user.up.sql:32`、Architecture 7.5

**參考答案**
新開 `000004`,`ALTER TABLE user DROP CONSTRAINT chk_user_language, ADD CONSTRAINT chk_user_language CHECK (... IN ('zh-TW','en','ja'))`。要先部署 migration、再部署會寫入 `ja` 的應用程式,舊版 app 仍可運作,符合 backward compatible。不能修改 000002,因為 migration merge 後 immutable。

**可能的追問**
- 反過來要移除一種語言呢?(先停止寫入、資料轉換,最後才收緊 CHECK)
- 大表 ALTER 會不會鎖表?

**考察點**:migration 紀律、向前相容部署順序。
:::

::: details 🔥 Q6. 流量放大 10 倍(25 萬人)時,這三張表哪裡最先出問題?
**出處**:`backend/migrations/000003_create_sessions.up.sql:7`、`backend/migrations/000002_create_user.up.sql:24`

**參考答案**
最先是 `sessions`:每個 request 一次 PK 讀、每次續期一次寫,且改密碼時 `Iterate` 掃全表,線性成長(ADR 0007 已預告)。處置順序:先量測,再考慮自訂 store 加 indexed `user_id`,或換 Redis(只需改 `main.go` 的 store 建構)。`user` 以 `employee_id` unique index 查單筆,不受影響。`audit_record` 只 INSERT,但 `changes` JSON 與三個 secondary index 會讓寫入放大,需要看 index 實際使用情況。驗證用 load test 與 slow query log;我沒有實測數據,以上為推論。

**可能的追問**
- 怎麼設計 benchmark?
- 為什麼 session 不直接放 JWT?

**考察點**:容量推估、對 ADR 風險的後續處理。
:::

### 找問題

::: details Q7. 這批 migration 有什麼潛在問題?
**出處**:`backend/migrations/000003_create_sessions.up.sql:7`、`backend/migrations/000002_create_user.up.sql:21`

**問題**(實際看到的):
1. `sessions.expiry` 是 `TIMESTAMP(6)`:依 session `time_zone` 轉換,且有 2038 上限;與其他表全用 `DATETIME(6)` UTC 的慣例不一致。session 只活 8 小時,2038 不是實質風險,time zone 設定不一致才是。
2. `user.created_at` / `updated_at` 的 `DEFAULT CURRENT_TIMESTAMP(6)` 使用 DB server 的時區,若 server 不是 UTC,會違反「全部存 UTC」。
3. department / language 的合法值在 Go(`platform/values.go`)與 SQL 重複,沒有測試把兩者綁在一起。
4. `down` 沒有保護,production 誤跑會直接 drop 資料。
5. 沒有 `cmd/migrate` 與 CI 步驟,migration 只被手動驗證過。

**如何修**:確保連線字串與 server 都用 UTC(`loc=UTC`、`time_zone='+00:00'`),寫一個 integration test 讀 DB CHECK 與 Go 的 `Parse*` 對照;補 `cmd/migrate`;CI 加「up → down → up」。

**如何驗證**:在 CI 起 MariaDB container 跑 up/down/up;以 `SELECT @@global.time_zone` 與 session 設定做 smoke test;integration test 逐一嘗試非法值。
:::

## 三分鐘口頭說明稿
這個 ticket 要建第一批資料表:user、audit_record、sessions。我用 golang-migrate,檔名 `NNNNNN_description.up/down.sql`,merge 後不再修改。設計原則是「不變條件下推到 DB」:Employee ID 用 `CHAR(6)` 加 regex CHECK 和 unique index;role 與 department 用 CHECK,還加一條跨欄位約束,讓 Participant 一定有 department、Admin 一定沒有。這樣即使有人繞過 service 直接寫 SQL,資料也不會壞,而且併發註冊時由 unique index 決勝負,不是 SELECT 再 INSERT。sessions 照 scs 的固定 schema,ADR 0007 已經接受它沒有 user_id,代價是改密碼時要掃全表,以現在約兩萬五千人的量可以接受。audit_record 要 append-only,但 MariaDB 的權限是累加的,所以我用腳本先 REVOKE 再逐表 GRANT,新表要記得加進清單。取捨是:CHECK 清單與 Go 常數是兩份,要新增值得再寫 migration。我在拋棄式 MariaDB 11.8 容器驗證了 up、down 與非法資料被拒絕,但還沒透過 golang-migrate 實跑,因為 `cmd/migrate` 尚未存在,這是我標記的已知落差。
