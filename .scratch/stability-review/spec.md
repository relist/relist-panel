# รายงานตรวจความเสถียรของ Relist Panel

Status: open — แตกเป็น ticket แล้วใน `issues/` (ดูหัวข้อ "การตัดสินใจและ ticket")

- **วันที่:** 2026-10-24
- **Commit ที่ตรวจ:** `7214547` (main, หลัง merge PR #14)
- **คำถามที่ตอบ:** จุดไหนทำให้ server พัง, ข้อมูลหายโดยไม่มีอะไรเตือน หรือ service ค้างแบบไม่รู้สาเหตุ และควรเพิ่ม test/e2e ตรงไหน

## สรุปผลก่อน

ไม่มีบั๊กที่ทำให้ panel ล่มระหว่างใช้งานปกติ `go vet ./...` ไม่มี error และ `go test -race -count=1 ./...` ผ่านทุก package
แต่มี **15 จุดที่ทำให้ระบบพังหรือข้อมูลหายได้ในสถานการณ์ที่เกิดได้จริง** ยืนยันแล้วทุกจุดด้วยวิธีใดวิธีหนึ่งต่อไปนี้:

- **Go test ชั่วคราว:** A1, A2, A4, B1, B2, B3, B5 (รวมทดสอบวิธีแก้ด้วย), C1, C2, C5.1
- **Docker/Traefik จริง** บนเครื่องที่มี live install โดยใช้ชื่อ `relistreview-*` แล้วลบทิ้งหมด: B4, B6, C3, C4
- **ทดลองกับ live install โดยตรง** (connection เดียว ไม่เปลี่ยนแปลงอะไร): A3
- **อ่านโค้ดอย่างเดียว:** C5.2 (มีคอมเมนต์อธิบายในโค้ดอยู่แล้ว) และ C5.3 (grep ไม่พบโค้ดเก็บกวาด)
- **ตั้งใจไม่รัน:** prune จริงใน B6 เพราะ prune มีผลกับทั้ง Docker host และจะลบ volume/image จริงของ live install จึงยืนยันด้วยตัวกรอง `dangling` ที่ prune ใช้แทน

ตรวจ DB ของ live แล้ว (copy ออกมาเปิดแบบ read-only): schema อยู่ที่ version 17 (ต่ำกว่า main ที่ 19 แต่ไม่ได้สูงกว่า binary) และ `PRAGMA foreign_key_check` = 0 violation แปลว่า **ตอนนี้ live ยังไม่โดน A1 หรือ B5**

ข้อที่ควรแก้ก่อน มี 5 ข้อ:

1. **B5** Foreign key cascade หยุดทำงานเงียบๆ หลัง query ใดก็ได้ถูกยกเลิกกลางทาง (แก้บรรทัดเดียว)
2. **A1** Panel บูตขึ้นได้ทั้งที่ schema ไม่ตรงกับ binary (เสี่ยงมากกับการสลับ feature branch บน live install)
3. **B1** อัปโหลดไฟล์ทับใน Volume แล้วขาดกลางทาง ไฟล์เดิมจะถูกทำลาย (ticket 19 ยังเปิดอยู่)
4. **B4** Delete Compose ลบ volume ที่ตั้ง `name:` เองซึ่ง Compose อื่นใช้ร่วมอยู่
5. **C1** `.deployment-sources` โตไปเรื่อยๆ ไม่มีการเก็บกวาด จนวันหนึ่ง disk เต็ม

## ตารางสรุป

| ID | เรื่อง | กลุ่ม | ระดับ | การยืนยัน | สถานะ |
|---|---|---|---|---|---|
| A1 | บูตได้ทั้งที่ schema ใหม่กว่า binary หรือมี migration เลขซ้ำ | crash/boot | สูง | Go test | ใหม่ |
| A2 | Volume Explorer list โฟลเดอร์ใหญ่ → panel OOM | crash | กลาง | วัด heap | ใหม่ |
| A3 | HTTP server ไม่มี timeout + พอร์ต 8888 เปิดทุก interface | crash/security | กลาง-ต่ำ | ทดลองกับ live | ใหม่ |
| A4 | goroutine เบื้องหลังไม่มี `recover` | crash | ต่ำ | Go test (process ตาย) | ใหม่ |
| B1 | เขียนไฟล์ใน Volume ไม่ atomic | data loss | สูง | Go test | ticket 19 |
| B2 | Deploy ซ้ำถูกรวมเข้ากับคิวเดิมที่มี FreshVolumes | data loss | กลาง | Go test | ใหม่ |
| B3 | ข้อมูลที่ container เขียนลง `./src` หายทุกครั้งที่ deploy | data loss | กลาง | Go test | ผลข้างเคียงของ ADR-0017 |
| B4 | `down -v` ลบ volume `name:` ที่ใช้ร่วมกับ Compose อื่น | data loss | สูง | Docker จริง | ใหม่ |
| B5 | FK cascade ปิดตัวเองหลัง query ถูกยกเลิก → ข้อมูลกำพร้า | data integrity | สูง | Go test + ทดสอบวิธีแก้ | ใหม่ |
| B6 | Prune all volumes / unused images ลบข้อมูลของ Compose ที่ Stop อยู่ | data loss | ต่ำ (มีคำเตือนแล้ว) | Docker จริง (ไม่รัน prune) | มีคำเตือนใน UI |
| C1 | `.deployment-sources` โตไม่จำกัด | disk full | สูง | Go test | ADR-0017 ยอมรับไว้แต่ไม่มีเพดาน |
| C2 | ดาวน์โหลด archive ทำให้คิว deploy ทั้งเครื่องค้าง | stall | กลาง | Go test (HTTP) | ใหม่ |
| C3 | Domain บน host ของ panel ยึด `/api` ได้ → เข้า panel ไม่ได้ | lockout | กลาง | Traefik จริง | ใหม่ |
| C4 | ปิด tab ระหว่าง Stop/Delete → stack ค้างครึ่งทาง | stall | กลาง-ต่ำ | Docker จริง | ใหม่ |
| C5 | ปัญหาย่อย: guard blocked, bcrypt ถือ DB, ไฟล์ upload ค้าง | stall | ต่ำ | C5.1 Go test, C5.2–3 อ่านโค้ด | ใหม่ / บางส่วนมีคอมเมนต์ไว้แล้ว |

## การตัดสินใจและ ticket (คุยกัน 2026-10-24)

- **A3:** ให้เข้าผ่าน Traefik อย่างเดียว เหลือพอร์ต 8888 ไว้เฉพาะบน loopback เป็นทางเข้าฉุกเฉินผ่าน SSH tunnel
- **กลุ่ม B ที่เกิดจาก YAML (B3, B4):**
  - ใส่คู่มือสั้นๆ ใต้ editor
  - เตือนใน preview / deployment log เฉพาะเมื่อ YAML มี pattern นั้นจริง **ไม่ใช้ warning modal ทุก action** เพราะคนจะชินแล้วกดผ่านโดยไม่อ่าน
  - modal ใช้ที่ Delete ที่เดียว และต้องแสดงรายการที่จะถูกลบจริง
  - ฝั่ง server ไม่ลบ volume ที่ Compose อื่นเป็นเจ้าของหรือประกาศใช้อยู่ (ใช้แบบข้ามแล้วรายงานกลับ ไม่ปฏิเสธการลบ)
- **B1, B2, B5:** แก้ในโค้ด ผู้ใช้ไม่ต้องรู้
- **C2:** แยก project lock เป็นแบบอ่าน/เขียน การดาวน์โหลดไม่ต้องกัน deploy ปกติ
- **C3:** ตั้ง `priority` ให้ router ของ panel และให้ฟอร์ม Domain ปฏิเสธ host ของ panel
- **C5:** พักไว้ก่อน

| ข้อ | Ticket | Status |
|---|---|---|
| B5 | `issues/01-foreign-keys-on-every-connection.md` | ready-for-agent |
| A1 | `issues/02-refuse-mismatched-schema-at-boot.md` | ready-for-agent |
| B4 (server) | `issues/03-keep-volumes-other-composes-use.md` | ready-for-agent |
| B3, B4 (warning) | `issues/04-yaml-risk-warnings.md` | ready-for-agent |
| B3, B4 (คู่มือ) | `issues/05-editor-guidance.md` | ready-for-agent |
| B4 (Delete modal) | `issues/06-delete-lists-what-goes.md` (blocked by 03) | ready-for-agent |
| B2 | `issues/07-enqueue-option-mismatch.md` | ready-for-agent |
| C1 | `issues/08-snapshot-gc.md` | ready-for-agent |
| C2 | `issues/09-read-write-project-gate.md` | ready-for-agent |
| C3 | `issues/10-panel-host-cannot-be-taken.md` | ready-for-agent |
| A3 | `issues/11-panel-port-loopback-only.md` | ready-for-agent |
| A2 | `issues/12-bound-volume-listing-memory.md` | ready-for-agent |
| A4 | `issues/13-recover-background-goroutines.md` | ready-for-agent |
| C5 | `issues/14-minor-stalls.md` | needs-triage (พักไว้) |
| B6 | `issues/15-prune-names-relist-volumes.md` | needs-triage (ยังไม่ได้คุย) |
| C4 | `issues/16-lifecycle-survives-disconnect.md` | ready-for-agent |
| B1 | ใช้ ticket เดิม `.scratch/volume-files/issues/19-atomic-writes.md` | ready-for-agent |

ลำดับที่แนะนำ: 01 → 02 → 19 (volume-files) → 03 → 08 แล้วค่อยทำที่เหลือ

---

## A. Crash / OOM / boot ผิดพลาด

### A1. Panel บูตขึ้นได้ทั้งที่ schema ไม่ตรงกับ binary

**ตำแหน่ง:** `internal/database/database.go:49` (`goose.Up`), `main.go:87`

**กลไก:** goose v3.24.3 เลือก migration ที่จะ apply จาก "version ที่สูงกว่า version สูงสุดใน DB" และเช็คว่า apply แล้วจาก `version_id` อย่างเดียว ตาราง `goose_db_version` มีแค่คอลัมน์ `id, version_id, is_applied, tstamp` ไม่มีชื่อไฟล์หรือ checksum ทำให้มีสองกรณีที่ goose คืน `nil` แล้วบูตต่อ:

- **Schema ใหม่กว่า binary:** รัน feature branch ที่มี `00020_*` บน live install แล้วสลับกลับมา main (รู้จักแค่ 19) goose log ว่า "no migrations to run. current version: 20" แล้วไปต่อ
- **เลขซ้ำต่างไฟล์:** branch X มี `00020_a.sql` และ branch Y มี `00020_b.sql` ถ้ารัน X ก่อนแล้วสลับไป Y, `00020_b` จะถูกข้ามเพราะ version 20 "apply แล้ว"

**ผลกระทบ:** Panel ขึ้นได้ปกติ healthz ตอบ ok แต่ไปพังทีหลังตอน query: `no such column`, insert ไม่ผ่านเพราะคอลัมน์ `NOT NULL` ที่โค้ดไม่รู้จัก หรือ trigger/constraint ที่ไม่ควรมี อาการจะดูเหมือน bug สุ่มๆ ของฟีเจอร์ที่ไม่เกี่ยวข้องกันเลย ความเสี่ยงสูงเป็นพิเศษ เพราะ memory ของ session นี้บอกว่าคุณรัน feature branch บน live install อยู่แล้ว ตอนนี้ live อยู่ที่ version 17 จึงยังไม่โดน แต่จะเริ่มเสี่ยงทันทีที่ branch ไหนเพิ่ม `00020_*`

**การยืนยัน:** `verification/database_review_verify_test.go`
```
A1 OpenAndMigrate error=nil with schema 20 > binary 19 — the panel would boot
A1b goose_db_version columns=[id version_id is_applied tstamp] (no file name / checksum stored)
```

**วิธีแก้:**
1. หลัง `goose.Up` ใน `OpenAndMigrate` ให้เรียก `SchemaVersion` และ `KnownVersion` (มีอยู่แล้วใน `internal/database/version.go`) ถ้า `schema > known` ให้คืน error เช่น `database schema 20 is newer than this binary (19); run the binary that applied it or restore a backup` ซึ่งจะทำให้ `log.Fatal` ตอนบูต
2. กันเลขซ้ำ: เพิ่มตาราง `relist_migration_files(version, name, sha256)` แล้วเช็คตอนบูตว่าชื่อ/hash ของทุก version ที่ apply แล้วตรงกับไฟล์ที่ embed อยู่ ถ้าไม่ตรงให้ปฏิเสธ (ต้องเป็น migration ใหม่ ห้ามแก้ไฟล์ที่ commit แล้ว)
3. จะเพิ่ม `create-admin`/`reset-2fa` ให้ใช้เส้นทางเดียวกันด้วยก็ได้ เพราะทั้งสองเรียก `OpenAndMigrate` ซึ่งรัน migration จาก process ที่สองด้วย

**Test ที่ควรเพิ่ม:** `TestOpenAndMigrateRefusesASchemaNewerThanTheBinary` และ `TestOpenAndMigrateRefusesAnAppliedVersionWithADifferentFile`

### A2. Volume Explorer list โฟลเดอร์ใหญ่ทำให้ panel โดน OOM kill

**ตำแหน่ง:**
- `internal/volfs/volfs.go:95` `dir.ReadDir(-1)` ไม่มีเพดาน
- `cmd/relist-volhelper/main.go` (op `list`) encode ทุก entry เป็น JSON ก้อนเดียว
- `internal/volumeapi/operations.go:284` ฝั่ง panel เก็บ stdout ทั้งหมดใน `bytes.Buffer` ก่อนตอบ
- `docker-compose.yml:38` panel ตั้ง `mem_limit: 256M` แต่ไม่มี `GOMEMLIMIT` (คอมเมนต์ของ traefik ในไฟล์เดียวกันอธิบายเหตุผลไว้แล้ว แต่ไม่ได้ใส่ให้ panel)

**สถานการณ์:** เปิดโฟลเดอร์ที่มีไฟล์ระดับล้าน เช่น cache, session store หรือ maildir ใน Volume Explorer

**การยืนยัน:** `verification/volfs_review_verify_test.go`
```
A2 50000 entries -> 6895804 bytes JSON (138 B/entry)
A2 1M entries: JSON 125 MB, buffer cap 128 MB, peak HeapInuse ~192 MB
```
- ตอนนี้ panel ตัว live ใช้ memory ราว 8 MiB (`docker stats`)
- ที่ 1 ล้าน entry ตัว buffer อย่างเดียวขึ้นไปถึงราว 192 MB บวกกับ heap ปกติ ทำให้ชนเพดาน 256M ได้ และเกินแน่นอนที่ราว 1.3 ล้าน entry ขึ้นไป
- ตัว helper เองก็ใช้ memory หนัก (alloc รวม 91 MB ที่ 5 หมื่น entry) แต่รันใน container แยกซึ่งไม่ได้ตั้ง limit จึงไปกิน RAM ของ host แทน

**ผลกระทบ:** container ของ panel ถูก kernel kill แล้วกลับมาด้วย `restart: unless-stopped` ถ้ามี deploy รันอยู่ จะถูก mark ว่า `failed: panel restarted` และ `docker compose up` ที่ทำค้างไว้จะหยุดกลางทาง

**วิธีแก้:**
1. helper จำกัดจำนวน entry (เช่น 10,000) แล้วส่ง `{"entries":[...],"truncated":true}` กลับไป UI แสดงว่า "แสดง 10,000 รายการแรก"
2. ตั้ง `GOMEMLIMIT: 200MiB` ให้ panel ใน `docker-compose.yml`
3. ใส่ `--memory` ให้คำสั่ง `docker run` ของ helper ใน `volume_runner.go`

**Test ที่ควรเพิ่ม:** test ใน volhelper ว่า list ถูกตัดที่เพดานและมี flag `truncated` + test ใน httpapi ที่ใช้ fake runner คืน output 50 MB แล้วคาดว่าต้องถูกปฏิเสธหรือตัด

### A3. HTTP server ไม่มี timeout และพอร์ต 8888 เปิดออกทุก interface

**ตำแหน่ง:** `main.go:169` `&http.Server{Addr, Handler}` ไม่มี `ReadHeaderTimeout`/`ReadTimeout`/`IdleTimeout`, `docker-compose.yml:14` `"${RELIST_PORT:-8888}:8080"`

**การยืนยัน (live install, 1 connection):**
```
8080/tcp -> 0.0.0.0:8888
8080/tcp -> [::]:8888
A3 half-sent request: connection still open after 46s
```

**ผลกระทบ:**
- ใครก็ตามที่ต่อพอร์ต 8888 ได้ สามารถเปิด connection ที่ส่ง header ไม่ครบค้างไว้เป็นพันๆ อัน (slowloris) จน fd หรือ goroutine หมด แล้ว panel ไม่ตอบ
- พอร์ต 8888 ไม่ได้ผ่าน Traefik/TLS ทำให้ login ส่งรหัสผ่านแบบไม่เข้ารหัส และ cookie ไม่มี flag `Secure` (`requestIsSecure` เป็น false)

**วิธีแก้:**
1. `ReadHeaderTimeout: 10*time.Second`, `IdleTimeout: 2*time.Minute` อย่าตั้ง `ReadTimeout`/`WriteTimeout` แบบรวม เพราะ upload image ใช้เวลาได้ถึง 1 ชั่วโมงและ terminal เป็น websocket
2. ใน compose ให้ bind เป็น `127.0.0.1:${RELIST_PORT:-8888}:8080` เป็นค่าเริ่มต้น ใช้เป็นทางเข้าฉุกเฉินจากตัวเครื่อง (ดู C3)

**Test ที่ควรเพิ่ม:** test ใน `main` ว่า server ที่สร้างขึ้นมี `ReadHeaderTimeout > 0` (แยกการสร้าง server ออกเป็นฟังก์ชันก่อน)

### A4. goroutine เบื้องหลังไม่มี `recover`

**ตำแหน่ง:** `internal/hostmetrics/hostmetrics.go:182, 287, 299` (sampler loop, container sweep, df refresh), `internal/httpapi/httpapi.go` (`ResumeLock`), goroutine ใน `internal/migration/target.go:352, 876`

**กลไก:** `net/http` recover panic ใน handler ให้ทีละ request แต่ goroutine ที่สร้างเองไม่ได้รับการป้องกันนี้ ถ้า panic ที่ไหน process ตายทั้งตัว ถ้า panic ซ้ำได้ทุกครั้ง (เช่น JSON แปลกๆ จาก Docker stats API) จะเข้าลูป restart ทุก 10 วินาที

**การยืนยัน:** `verification/hostmetrics_review_verify_test.go` ให้ hook `SweepContainers` panic แล้วสั่ง tick test binary ตายทั้ง process:
```
panic: unexpected Docker stats payload
goroutine 8 [running]: ... FAIL relist-panel/internal/hostmetrics
```
มีแค่ deployment worker ที่ recover (`queue.go:440, 597`) parser ของ `/proc` เช็คความยาว field ก่อนทุกจุด และไม่พบ input ที่ทำให้ panic ได้จริงตอนนี้ ข้อนี้จึงเป็นการป้องกันไว้ก่อน แต่ถ้าเกิดเมื่อไหร่ panel จะตายทั้งตัว

**วิธีแก้:** helper `safeGo(name, fn)` ที่ recover แล้ว log stack แล้วข้าม tick นั้นไป

**Test ที่ควรเพิ่ม:** sampler test ที่ hook `SweepContainers` panic แล้ว tick ถัดไปยังทำงานได้

---

## B. ข้อมูลหายโดยไม่มีอะไรเตือน

### B1. เขียนไฟล์ใน Volume ไม่ atomic

**ตำแหน่ง:** `internal/volfs/volfs.go:210` `Write` เรียก `root.Create` (truncate ทันที) แล้วค่อย stream, `cmd/relist-volhelper/main.go:281-283`
**สถานะ:** มี ticket แล้ว `.scratch/volume-files/issues/19-atomic-writes.md` (ready-for-agent) ยังไม่ได้แก้

**สถานการณ์:** อัปโหลดหรือ Save ไฟล์ทับไฟล์เดิมใน Volume Explorer แล้วเน็ตหลุด ปิด tab หรือ Traefik timeout ระหว่างส่ง

**การยืนยัน:** `verification/volfs_review_verify_test.go`
```
B1 clean EOF after 100 bytes: Write err=<nil>; original 14000 bytes -> now 100 bytes
B1 I/O error after 100 bytes: Write err=write "config.db": connection reset; original 14000 bytes -> now 100 bytes
```
กรณีแรกคือสิ่งที่ helper เห็นจริงเมื่อ client หลุด คือ stdin ได้ EOF ทำให้ helper **exit 0** และไฟล์เดิมเหลือแค่ส่วนที่ส่งมาถึง ฝั่ง panel อาจตอบ error แต่ไฟล์เดิมเสียไปแล้ว ส่วน guard ที่เช็คว่าพื้นที่เต็มก็ใช้ `vol.Delete(cmd.name)` ซึ่ง **ลบไฟล์เดิมทิ้งทั้งไฟล์**

**วิธีแก้:** ทำตาม ticket 19: เขียนลง temp ข้างไฟล์เป้าหมาย แล้ว `Rename` ทับ ให้ guard ลบแค่ temp และให้ helper เทียบจำนวน byte ที่ได้กับ `--expect` ถ้าไม่ครบ exit แบบ error และไม่ rename

**Test ที่ควรเพิ่ม:** ตาม ticket 19 + test ว่าเมื่อ `--expect 40000` แต่ stdin ได้ 100 bytes ไฟล์เดิมต้องเหมือนเดิมทุก byte

### B2. Deploy ซ้ำถูกรวมเข้ากับคิวเดิมที่มี FreshVolumes โดยไม่บอก

**ตำแหน่ง:** `internal/deployments/queue.go:209` (`Enqueue` คืนตัวที่อยู่ในคิวเลย), `internal/deployments/store.go` (`enqueue` ทำเหมือนกันใน tx), `internal/deployments/http.go:168` (ตอบ 200 `{"number"}` อย่างเดียว), `web/src/pages/ComposePage.tsx:482` (ขึ้น "Deployment #N queued" เหมือนกันทั้ง 200 และ 202)

**สถานการณ์:**
1. ระหว่างที่ build ของ Compose อื่นรันอยู่ (คิวรันทีละตัวทั้งเครื่อง) กด Deploy แบบ "fresh volumes" แล้วพิมพ์ชื่อยืนยัน
2. เปลี่ยนใจ กด Deploy ธรรมดา
3. UI ขึ้น "Deployment #N queued" ซึ่งคือตัวเดิมที่มี FreshVolumes=true แล้ว volume ถูกลบตอนถึงคิว

กลับกันก็เกิดได้: ขอ fresh แต่ได้ตัวธรรมดาที่อยู่ในคิวแทน volume ไม่ถูกล้างแม้จะตั้งใจให้ล้าง

**การยืนยัน:** `verification/deployments_review_verify_test.go`
```
B2 fresh request -> #1 created=true; plain request -> #1 created=false; the queued row has FreshVolumes=true
B2 reverse: plain #1 created=true; fresh #1 created=false; queued FreshVolumes=false
```

**วิธีแก้:** ถ้า Mode/NoCache/FreshVolumes ไม่ตรงกับตัวที่อยู่ในคิว ให้คืน 409 พร้อม option ของตัวในคิว (`"a deployment with freshVolumes=true is already queued; cancel it first"`) และให้ UI แสดงข้อความนั้น ส่วน comment ใน `web/src/data/api.ts:295` ("200 when the same request is already queued") ไม่ตรงกับพฤติกรรมจริง ต้องแก้ด้วย

**Test ที่ควรเพิ่ม:** httpapi: POST fresh (+confirmName) แล้ว POST ธรรมดา ต้องได้ 409 และ POST ซ้ำด้วย option เดิมต้องได้ 200 เหมือนเดิม

### B3. ข้อมูลที่ container เขียนลง bind mount ใต้ `./src` หายทุกครั้งที่ deploy

**ตำแหน่ง:** `internal/composes/snapshot.go:96` `WithSourceDirectory` เปลี่ยน path `./src...` ให้ชี้ไปที่ `.deployment-sources/source-XXX/src` ของแต่ละ deployment, `internal/deployments/snapshot.go` (`capture` copy จาก `src/` ตัวหลักทุกครั้ง)
**สถานะ:** เป็นผลข้างเคียงของ ADR-0017 (freeze input ตอนเข้าคิว) ADR ระบุเรื่อง mount ไว้แต่ไม่ได้ระบุผลนี้

**สถานการณ์:** YAML มี `volumes: ['./src:/app']` และแอปเขียนไฟล์ลง `/app` (เช่น SQLite, uploads, cache) ทุกครั้งที่ deploy container จะเห็นสำเนา src ใหม่ ไฟล์ที่เขียนไว้ใน snapshot ของรอบก่อนจะไม่ตามมา

**การยืนยัน:** `verification/deployments_review_verify_test.go`
```
B3 deploy 1 mounts ./src from /.deployment-sources/source-1945839219/src
B3 deploy 2 mounts /.deployment-sources/source-3940672886/src; uploads.db written by deploy 1's container present: false
```
ไฟล์ไม่ได้ถูกลบจาก disk ทันที (ยังอยู่ใน snapshot dir เก่า ดู C1) แต่จากมุมของแอป ข้อมูลหายไปแล้ว และถ้า C1 ถูกแก้ด้วย GC ไฟล์ส่วนนั้นจะหายจริง

**วิธีแก้:** ตอน preview และ deploy ถ้ามี bind mount ใต้ `src/` ที่ไม่ใช่ `:ro` ให้แสดง warning เช่น "Files a container writes under ./src are not kept between deployments; use a named volume" และระบุใน ADR-0017/คู่มือ

**Test ที่ควรเพิ่ม:** snapshot test ว่า YAML ที่มี `./src/data:/data` (ไม่ ro) ได้ warning และ `./src:/app:ro` ไม่ได้

### B4. Delete Compose ลบ volume ที่ตั้ง `name:` เองซึ่ง Compose อื่นใช้ร่วมอยู่

**ตำแหน่ง:** `internal/composes/composes.go:568` (`delete` → `DownWithVolumes`), `internal/composes/teardown.go:94` (ลบ Project/Environment), `internal/dockerexec/dockerexec.go` (`down -v`)

**กลไก:** `docker compose down -v` ลบทุก volume ที่ประกาศใน `volumes:` และไม่ได้เป็น `external: true` รวมถึง volume ที่ตั้ง `name:` เองด้วย Compose ไม่ได้เช็คว่า project อื่นสร้างหรือยังใช้อยู่หรือเปล่า Docker ปฏิเสธแค่กรณีที่มี container **ที่ยังมีอยู่** ผูกอยู่ ส่วน Stop ของ panel คือ `docker compose down` ซึ่ง **ลบ container ทิ้ง** volume ของ Compose ที่ Stop อยู่จึงลบได้ทันที

**การยืนยัน (Docker 29.8 + Compose v5.5.1 จริง, เวอร์ชันเดียวกับ live install):** สอง project `relistreview-a` และ `relistreview-b` ใช้ `volumes: {shared: {name: relistreview-shared}}` ร่วมกัน (ไฟล์ `verification/b4-docker-compose.yml`)

| กรณี | ผล |
|---|---|
| B ยังรันอยู่, A `down -v` | `Volume relistreview-shared Resource is still in use`, exit 0, volume รอด |
| A เป็นคนสร้าง, B ถูก Stop, A `down -v` | `Volume relistreview-shared Removed` exit 0 ข้อมูลของ B หาย |
| B เป็นคนสร้าง (label project=B), A join, B ถูก Stop, A `down -v` | `Volume relistreview-shared Removed` exit 0 Compose แค่ warn ตอน `up` ว่า "already exists but was created for project relistreview-b ... Use `external: true`" แต่ตอน `down -v` ไม่เช็คเลย |

**ผลกระทบ:** การลบ Compose หนึ่ง (หรือลบทั้ง Project/Environment) ทำลายข้อมูลของ Compose อื่นที่แค่ Stop ไว้ โดย exit 0 และ UI ไม่ได้บอกอะไร คำเตือน "created for project" ไปโผล่แค่ใน deployment log ของรอบที่ `up`

**ส่วนที่เกี่ยวข้อง (ยืนยันแล้ว, เป็นไปตาม design):** bind mount แบบ relative อย่าง `./data:/var/lib/x` resolve ไปอยู่ในโฟลเดอร์ Compose (`docker compose config` แสดง path เต็มในโฟลเดอร์ของ compose file) และ Delete ทำ `os.RemoveAll` โฟลเดอร์นั้น (`composes.go:589`) ข้อมูลใน bind mount จึงหายไปด้วย ควรเช็คว่า DangerModal บอกเรื่องนี้ชัดเจน

**วิธีแก้:**
1. ก่อน `down -v` ให้อ่าน `docker compose config --format json` (ทำอยู่แล้วใน `DownWithVolumesTracked`) แล้วหา volume ที่มี `name:` เอง ถ้า label `com.docker.compose.project` ไม่ใช่ project นี้ หรือมี Compose อื่นของ panel ประกาศชื่อเดียวกัน ให้ไม่ลบ volume นั้น (ใช้ `down` แล้วลบทีละ volume เอง) หรือปฏิเสธพร้อมรายชื่อ
2. Resolve/preview แสดง warning เมื่อ Compose ประกาศ `name:` ซ้ำกับ Compose อื่น แนะนำให้ใช้ `external: true`
3. DangerModal ของ Delete แสดงรายชื่อ volume และ bind mount ที่จะถูกลบจริง

**Test ที่ควรเพิ่ม:** unit test กับ fake CLI สำหรับตัวเลือก volume ที่จะลบ + e2e ข้อ E2 ด้านล่าง

### B5. Foreign key cascade ปิดตัวเองหลัง query ถูกยกเลิกกลางทาง (ข้อมูลกำพร้า / ไม่สอดคล้อง)

**ตำแหน่ง:** `internal/database/database.go:20` (DSN มีแค่ `busy_timeout`), `:42` (`PRAGMA foreign_keys = ON` รันครั้งเดียวบน connection แรก)

**กลไก:**
1. `foreign_keys` เป็นค่าต่อ connection ส่วน `busy_timeout` ตั้งผ่าน DSN จึงติดไปทุก connection แต่ `foreign_keys` ถูกตั้งด้วย `Exec` ครั้งเดียว
2. driver `modernc.org/sqlite` v1.37.0 (`sqlite.go:1494-1510`) ทิ้ง connection (`ResetSession` คืน `ErrBadConn`) เมื่อ `sqlite3_is_interrupted` ซึ่งเกิดเมื่อ context ของ query ถูกยกเลิกระหว่างที่ query ทำงานอยู่ เช่น client ปิด tab/หลุด, request timeout หรือ shutdown
3. `database/sql` เปิด connection ใหม่ให้ ซึ่งได้ `foreign_keys=0` และใช้ไปจนกว่า panel จะ restart เพราะ pool มี connection เดียว

**การยืนยัน:** `verification/database_review_verify_test.go`
```
B5 first connection foreign_keys=1
B5 replacement connection foreign_keys=0
B5 environments left after deleting their project: 1
B5b attempt 0..4: query err=context deadline exceeded, foreign_keys afterwards=0   (เกิดทุกครั้ง 5/5)
```

**ผลกระทบ:** schema มี `ON DELETE CASCADE` 22 จุด และโค้ดลบอาศัย cascade ทั้งหมด (`projects.go:304`, `environments.go:272`, `composes.go` delete) พอ cascade หยุด:
- ลบ Project/Environment แล้ว environments, composes, variables, domains และ deployments ค้างเป็นกำพร้า
- `knownComposeProjects` (`main.go`) ยังเห็น compose ที่ถูกลบ ทำให้ metrics และ terminal sweep ยังทำงานกับมัน
- `deployment_snapshots` ที่เก็บค่าตัวแปร (รวม secret) ไม่ถูกลบตามที่ ADR-0017 และ test `TestSnapshotMigrationPreservesLegacyQueueAndCascadesPrivateInputs` ตั้งใจไว้
- row ของ `domains` ที่กำพร้ายังถือ `UNIQUE(host, path)` อยู่ ตัวอย่าง: ลบ Project แล้วสร้าง Domain เดิมให้ Compose ใหม่ ระบบจะตอบว่า host/path ซ้ำ ทั้งที่ใน UI ไม่เห็น Domain นั้นแล้ว
- ทุกอย่างเกิดเงียบๆ ไม่มี error ไม่มี log

โอกาสเกิดต่อครั้งต่ำเพราะ query ส่วนใหญ่ใช้เวลาไม่ถึง ms แต่สะสมไปเรื่อยๆ ตลอดเวลาที่ panel รันอยู่ และเมื่อเกิดแล้วจะอยู่จนกว่าจะ restart ตรวจ DB ของ live แล้ว (copy ออกมาเปิดแบบ read-only): `PRAGMA foreign_key_check` = 0 violation คือยังไม่เคยเกิด

**วิธีแก้ (บรรทัดเดียว, ทดสอบแล้ว):** ใส่ใน DSN: `"file:"+databasePath+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"` ผมแก้ชั่วคราวแล้วรัน test เดิมซ้ำ (revert กลับแล้ว ไม่ได้ commit):
```
B5 replacement connection foreign_keys=1
B5 environments left after deleting their project: 0
B5b attempt 0..4: query err=context deadline exceeded, foreign_keys afterwards=1   (5/5)
go test ./internal/database → ok
```
`Exec` เดิมจะเก็บไว้หรือลบก็ได้ และควรรัน `PRAGMA foreign_key_check` ตอนบูตแล้ว log ถ้าพบ row กำพร้า

**Test ที่ควรเพิ่ม:** test B5b ใน verification เกือบใช้ได้เลย: ยกเลิก query ยาวแล้วต้องได้ `foreign_keys=1`

### B6. Prune "all volumes" / "unused images" ลบข้อมูลของ Compose ที่ Stop อยู่

**ตำแหน่ง:** `internal/dockerexec/dockerhost.go` (`all=true`, `dangling=false`), `web/src/pages/DockerPage.tsx:82`

**กลไก:** Stop = `docker compose down` ซึ่งลบ container ทำให้ named volume และ image (ทั้งที่ build จาก Source และที่ upload เข้ามา) ของ Compose ที่ Stop อยู่ถูกนับว่า "unused" และ panel "never pulls" image เอง deploy ใหม่ด้วย `--no-build` จึง fail ถ้า image หายไป

**การยืนยัน (Docker จริง, ไม่ได้รัน prune):** prune มีผลกับทั้ง host จึงไม่ได้รันบนเครื่องนี้ ซึ่งมี volume และ image จริงของ live install อยู่ ใช้วิธี `up` Compose ทิ้ง `relistreview-b6` ที่มี named volume แล้ว `docker compose down` (ซึ่งคือ Stop ของ panel) จากนั้นดูชุดที่ prune "all volumes" จะเลือกด้วย `docker volume ls -f dangling=true`:
```
while running, dangling: (ไม่มี)
after Stop, dangling:    relistreview-b6_pgdata
```
ทันทีที่ Stop volume ของ Compose นั้นเข้าเกณฑ์ที่ prune จะลบ (ลบ volume ทดลองแล้ว)

**สถานะ:** UI เตือนชัดเจนแล้ว ("A stopped or removed Compose may still need this data") และต้องพิมพ์ `DELETE VOLUMES` ข้อนี้จึงไม่ใช่ "เงียบๆ"

**ข้อเสนอ:** ในหน้า confirm ให้แสดงรายชื่อ volume/image ที่มี label `com.docker.compose.project` ตรงกับ Compose ของ panel แยกออกมา ("3 volumes belong to Relist Composes: ...")

---

## C. ระบบค้าง / disk เต็ม / เข้า panel ไม่ได้

### C1. `.deployment-sources` โตไปเรื่อยๆ ไม่มีการเก็บกวาด

**ตำแหน่ง:** `internal/deployments/snapshot.go:115` (`capture` copy src ทั้งชุด สูงสุด 4 GiB / 100,000 ไฟล์ ไปที่ `<compose>/.deployment-sources/source-XXX`), `:302` (`discardCaptured` ถูกเรียกแค่ตอน cancel), `queue.go:584` (`Prune` ลบ history row และ log แต่ไม่ลบ snapshot dir)
**สถานะ:** ADR-0017 ยอมรับไว้: "Materialized trees are retained until Compose teardown ... trades extra disk usage for stable mounts" แต่ไม่มีเพดาน และไม่มีอะไรเตือนนอกจาก disk warning ทั่วไปใน sidebar

**การยืนยัน:** `verification/deployments_review_verify_test.go` (src 5 MiB, Keep=2)
```
C1 after deploy 1: history rows=1 (Keep=2), snapshot dirs=1, size=5.0 MiB
C1 after deploy 2: history rows=2, snapshot dirs=2, size=10.0 MiB
...
C1 after deploy 6: history rows=2, snapshot dirs=6, size=30.0 MiB
```
History ถูก prune แต่ snapshot dir เพิ่มขึ้นทุกครั้ง ตอนนี้ live install ยังไม่มี `.deployment-sources` เลย (disk 56%, เหลือ 104G) จึงยังไม่ได้รับผลกระทบ

**ผลกระทบ:** ตัวอย่าง: src (รวม `node_modules`/assets) 300 MB × deploy 10 ครั้งต่อวัน = 3 GB ต่อวัน หรือราว 90 GB ต่อเดือน พอ disk เต็ม SQLite เขียนไม่ได้ Docker สร้าง layer/log ไม่ได้ ทุก service บนเครื่องพังพร้อมกัน

**วิธีแก้:** GC หลัง deployment settle (และตอนบูต) ให้เก็บไว้เฉพาะ:
1. snapshot ของ deployment ที่ยัง queued/running
2. dir ที่ `compose_source_materializations` ชี้อยู่
3. dir ที่ container ปัจจุบันของ Compose นั้น mount อยู่ (อ่านจาก `docker inspect` → `Mounts[].Source` ด้วย label project)

ที่เหลือให้ `RemoveAll` และแก้ ADR-0017 ให้ตรงกับพฤติกรรมใหม่

**Test ที่ควรเพิ่ม:** test C1 ใน verification + assert ว่าหลัง 6 deploy เหลือแค่ dir ที่ยังถูกอ้างอิง และ dir ที่ container mount อยู่ไม่ถูกลบ

### C2. ดาวน์โหลด archive ของ Volume ทำให้คิว deploy ทั้งเครื่องค้าง

**ตำแหน่ง:** `internal/httpapi/coordination.go:60` (GET ใต้ `/volumes` ถือ Project gate ไว้ตลอด request), `internal/deployments/queue.go:780` (Deploy step รอ gate นานสุดเท่ากับ timeout 60 นาที), คิวรันทีละตัวทั้งเครื่อง (ADR-0016)

**สถานการณ์:** ดาวน์โหลด archive ของ volume ขนาดใหญ่ผ่านเน็ตช้า (เช่น 20 GB ผ่าน 10 Mbps ราว 4.5 ชั่วโมง) ระหว่างนั้น deployment ของ project เดียวกันถึงคิว worker ไปค้างที่ "waiting for gate" และ deployment ของ project อื่นทั้งหมดค้างอยู่ข้างหลัง

**การยืนยัน:** `verification/httpapi_review_verify_test.go` ผ่าน HTTP handler จริง
```
C2 3s after x finished, archive still open: A1=running/deploy, B (other Project)=queued/
C2 archive closed: A1=succeeded/deploy, B=succeeded/deploy
```

**ผลกระทบ:** จากมุมผู้ใช้ "กด deploy แล้วไม่ไปไหน" ทั้งเครื่อง และไม่มีอะไรบอกว่ารอ archive อยู่ ถ้าเกิน 60 นาที deployment นั้น fail ด้วย `timeout` แล้วคิวค่อยไปต่อ ถ้ามีการ enqueue ใหม่ของ project นั้นระหว่างดาวน์โหลด ตัว POST เองก็ค้างด้วย เพราะ `capture` ขอ gate เหมือนกัน

**วิธีแก้ (เลือกอย่างใดอย่างหนึ่ง หรือทำหลายอย่างร่วมกัน):**
1. แยก gate เป็น read/write: read ของ volume (list/stat/read/archive) ถือ read-lock, Delete/teardown ถือ write-lock และ Deploy ไม่ต้องรอ read-lock เพราะ deploy ไม่ได้ลบ volume ยกเว้น FreshVolumes ซึ่งค่อยขอ write-lock
2. ให้ worker ข้ามไปทำ deployment ของ project อื่นก่อนเมื่อ gate ถูกถือ (ต้องคุมลำดับ FIFO ให้ดี)
3. อย่างน้อยให้ `/api/deployments/queue` แสดงเหตุผลว่ากำลังรอ volume operation ของ project นั้น

**Test ที่ควรเพิ่ม:** test C2 ใน verification แปลงเป็น assertion: B ต้อง succeeded ระหว่างที่ archive ยังเปิดอยู่ (ถ้าเลือกวิธี 1 หรือ 2)

### C3. Domain ที่ตั้งบน host ของ panel ยึด `/api` ได้ → เข้า panel ไม่ได้

**ตำแหน่ง:** `internal/domains/domains.go:94` (`Validate` ไม่เช็ค host ของ panel), `internal/domains/labels.go:90` (`Rule` = `Host(...) && PathPrefix(...)` ไม่ตั้ง `priority`), `docker-compose.yml` (router ของ panel = `Host(${RELIST_DOMAIN})`)

**กลไก:** Traefik ตั้ง priority ตามความยาวของ rule เป็นค่าเริ่มต้น Domain ที่ host ตรงกับ `RELIST_DOMAIN` และมี path จึงได้ rule ยาวกว่าของ panel แล้วชนะ

**การยืนยัน (Traefik v3.7 จริง, รัน container แยก ไม่แตะตัวของ live install):** router ของ panel กับ router ที่ `domains.Rule()` สร้างให้ host เดียวกันและ path `/api` (ไฟล์ `verification/c3-traefik-dynamic.yml`)
```
/                    -> Name: PANEL
/login               -> Name: PANEL
/api/auth/status     -> Name: APP
/api/projects        -> Name: APP
```
UI โหลดขึ้น แต่ทุก API call ไปเข้าแอป login ไม่ได้ แก้ Domain ผ่าน UI ก็ไม่ได้ ทางเดียวที่เหลือคือพอร์ต 8888 (ซึ่งมีปัญหาตาม A3) หรือแก้ DB/YAML เองบนเครื่อง
กรณี path `/` (rule ยาวเท่ากัน) ในการทดลองนี้ panel ชนะ แต่ Traefik ไม่รับประกันลำดับเมื่อ priority เท่ากัน จึงไม่ควรพึ่งพฤติกรรมนี้

**เงื่อนไขที่ต้องครบ:** priority ของ Traefik เทียบกันเฉพาะ router ที่อยู่บน entrypoint เดียวกัน ใน install จริง router `panel` อยู่บน `websecure` ส่วน `panel-http` บน `web` มีไว้ redirect อย่างเดียว ดังนั้น:
- Domain ที่ **เปิด HTTPS** (หรือตั้ง entrypoint `websecure` เอง) ไปอยู่บน `websecure` → ยึด `/api` ของ panel ได้ ทำให้ล็อกเอาท์จริง
- Domain ที่เป็น HTTP อย่างเดียว ไปอยู่บน `web` (`EntrypointFor`) → ยึดได้แค่ redirect ของ `http://.../api` ส่วน SPA ที่เปิดผ่าน HTTPS ยังเรียก API ของ panel ได้ปกติ

การทดลองข้างบนวาง router ทั้งสองไว้บน entrypoint เดียวกัน ซึ่งตรงกับกรณีแรก

**วิธีแก้:**
1. ส่ง `cfg.Install.Domain` เข้า domains service แล้วปฏิเสธ host ที่ตรงกับมัน (`host is the panel's own domain`)
2. ตั้ง `priority` สูงสุดให้ **ทั้ง** router `panel` และ `panel-http` ใน `docker-compose.yml` (เช่น `traefik.http.routers.panel.priority=100000` และ `traefik.http.routers.panel-http.priority=100000`) ซึ่งกันกรณี hand-written label ใน YAML ได้ด้วย

**Test ที่ควรเพิ่ม:** domains validate test ว่า host ของ panel ถูกปฏิเสธ ทั้งตอน create และตอน update

### C4. ปิด tab ระหว่าง Stop/Restart/Delete → stack ค้างครึ่งทาง

**ตำแหน่ง:** `internal/composes/composes.go:498, 568` และ teardown ทั้งหมดใช้ `r.Context()`, `internal/dockerexec/dockerexec.go:139` `exec.CommandContext` ซึ่ง kill CLI เมื่อ context ถูกยกเลิก

**การยืนยัน (Docker จริง):** 3 service ที่ไม่สน SIGTERM (`stop_grace_period: 15s`) kill CLI ที่ 3 วินาทีหลังเริ่ม `down -v` (เหมือนที่ Go ทำเมื่อ client หลุด) ไฟล์ `verification/c4-docker-compose.yml`
```
Container relistreview-c4-*-1 Error while Stopping       exit=124
left behind: 3 containers Exited (137) (not removed), volume relistreview-c4_dbdata, network
retry: down -v → removed everything
```

**ผลกระทบ:** ไม่มีข้อมูลหาย แต่ Delete จะ "ไม่สำเร็จ" แบบไม่มีใครเห็น error (client ไปแล้ว) row ยังอยู่ และ stack อยู่ในสภาพหยุดแต่ยังไม่ถูกลบ ส่วนลบ Project ที่มีหลาย Compose อาจรื้อไปได้แค่บางตัว ต้องกดซ้ำเองถึงจะเสร็จ

**วิธีแก้:** คำสั่งที่เปลี่ยนสถานะ (down, down -v, restart, teardown) ให้ใช้ `context.WithoutCancel(r.Context())` + timeout ของตัวเอง (เช่น 10 นาที) แบบเดียวกับที่ prune ทำอยู่แล้วใน `dockerhost/api.go`

**Test ที่ควรเพิ่ม:** httpapi test ที่ยกเลิก request context ระหว่าง fake `DownWithVolumes` แล้ว fake ต้องได้ context ที่ยังไม่ถูกยกเลิก และ row ต้องถูกลบเมื่อ runner คืนค่าสำเร็จ

### C5. ปัญหาย่อยที่ทำให้ "ค้างแบบไม่รู้สาเหตุ"

1. **Guard ถูก block หลัง prune ที่ไม่แน่ใจผล:** (`dockerhost/guard.go`, `queue.go:183`)
   - worker log `maintenance blocked` ทุก 1 วินาที และคิวหยุดจนกว่าจะพิมพ์ `RESUME DOCKER` ในหน้า Docker
   - POST deploy ระหว่างนั้นได้แค่ 500 `could not queue the deployment` (`deployments/http.go:165`) ไม่บอกเหตุผล
   - ยืนยันแล้ว (`verification/httpapi_review_verify_test.go`, engine ปลอมที่ prune คืน `ErrPruneUncertain`):
     ```
     C5 uncertain prune -> 502
     C5 POST deployment while blocked -> 500 map[error:could not queue the deployment]
     C5 POST stop while blocked -> 503 {"error":"Docker operations are paused because a previous prune may still be deleting. ..."}
     ```
     Stop บอกเหตุผล แต่ Deploy ไม่บอก
   - แก้: map `ErrMaintenanceBlocked` เป็น 503 พร้อมข้อความ และให้ worker log แค่ครั้งเดียวต่อช่วงที่ถูก block
2. **Login เรียก bcrypt ระหว่างถือ DB connection เดียว:** (`auth/auth.go:158`, มีคอมเมนต์อธิบายเหตุผลไว้แล้ว)
   - panel ตั้ง `cpus: 0.25` ทำให้ bcrypt แต่ละครั้งช้าลงหลายเท่า และทุก request ต้องรอระหว่างนั้น
   - lockout จำกัดความเสียหายได้ จึงเป็นความเสี่ยงต่ำ
3. **ไฟล์ upload ค้างหลัง crash:** `<compose>/.upload-*.zip` (สูงสุด 500 MB) และ `.src.new-*` (`sources/http.go:491-538`) ไม่มีใครลบถ้า panel ตายกลางทาง ควรลบ pattern พวกนี้ตอนบูต

---

## Test และ e2e ที่ควรเพิ่ม

### Unit / integration (ต่อจาก harness เดิม)

| # | Test | ครอบคลุม | ที่อยู่ |
|---|---|---|---|
| 1 | schema ใหม่กว่า binary และ migration เลขซ้ำต้องถูกปฏิเสธ | A1 | `internal/database` |
| 2 | ยกเลิก query ยาวแล้ว `PRAGMA foreign_keys` ยังเป็น 1 | B5 | `internal/database` |
| 3 | upload ที่ขาดกลางทางต้องทำให้ไฟล์เดิมเหมือนเดิมทุก byte | B1 | `internal/volfs`, `cmd/relist-volhelper` |
| 4 | list มีเพดาน + flag `truncated` | A2 | `cmd/relist-volhelper`, `internal/httpapi` |
| 5 | enqueue ด้วย option ต่างจากตัวในคิวต้องได้ 409 | B2 | `internal/httpapi` |
| 6 | snapshot GC เก็บไว้เฉพาะ dir ที่ยังถูกอ้างอิง | C1 | `internal/deployments` |
| 7 | warning สำหรับ bind mount ใต้ `./src` ที่เขียนได้ | B3 | `internal/composes` |
| 8 | archive ที่เปิดค้างไม่ทำให้ deploy ของ project อื่นค้าง | C2 | `internal/httpapi` |
| 9 | Domain บน host ของ panel ถูกปฏิเสธ | C3 | `internal/domains`, `internal/httpapi` |
| 10 | Delete/Stop ทำงานจนเสร็จแม้ request ถูกยกเลิก | C4 | `internal/httpapi` |
| 11 | ตัวเลือก volume ที่จะลบไม่รวม `name:` ของ project อื่น | B4 | `internal/dockerexec` (fake CLI) |
| 12 | sampler รอดเมื่อ hook panic | A4 | `internal/hostmetrics` |
| 13 | `ErrMaintenanceBlocked` ตอน enqueue ได้ 503 พร้อมเหตุผล | C5 | `internal/httpapi` |

httpapi test เปิด DB ผ่าน `database.OpenAndMigrate` ซึ่งใช้ connection เดียว จึงครอบคลุม deadlock แบบ single connection ใน path ที่มี test อยู่แล้ว ส่วน package `projects`, `environments`, `composevariables`, `variablesets`, `images` ไม่มี unit test ของตัวเอง แต่ถูกเรียกผ่าน httpapi test

### E2E

ตอนนี้ยังไม่มี harness: `web/package.json` มีแค่ `vitest` ไม่มี Playwright หรือ Cypress (system-hardening ใช้ headless Chrome แบบเฉพาะกิจ) ข้อเสนอคือ **Playwright ยิงไปที่ panel ที่ build จาก Dockerfile จริง บน Docker จริง** (ใน CI ใช้ DinD หรือ runner ที่มี Docker) โดยเริ่มจาก flow ที่ unit test จับไม่ได้:

- **E1** deploy → Stop → deploy: ข้อมูลใน named volume ยังอยู่ (ดักการเปลี่ยนแปลงที่ทำให้ Stop กลายเป็น `down -v`)
- **E2** สอง Compose ใช้ `name:` ร่วมกัน, Stop ตัวหนึ่ง, Delete อีกตัว → volume ต้องรอด (B4)
- **E3** kill panel ระหว่าง deploy → deployment เป็น `failed: panel restarted`, deploy รอบถัดไปผ่าน และไม่มี snapshot dir ค้างเกินกว่าที่ GC กำหนด (C1)
- **E4** upload ทับไฟล์ใน Volume Explorer แล้วตัดการเชื่อมต่อกลางทาง → ไฟล์เดิมเหมือนเดิม (B1)
- **E5** สร้าง Domain บน host ของ panel → ถูกปฏิเสธ และ panel ยังใช้งานได้ (C3)
- **E6** server migration ระหว่าง 2 panel ทั้ง flow: ข้อมูลใน volume, Domain และ Compose ที่รันอยู่ตามมาครบ (ตอนนี้มีแค่ integration test ที่ใช้ fake docker)

## ส่วนที่ยังไม่ครอบคลุม

- อ่านแค่ผ่านๆ: `internal/terminal/session.go`, lock middleware ของ migration, `internal/migration/oldhost_http.go`, ส่วนใหญ่ของ `internal/migration/target.go`, การ inject label ของ domain นอกเหนือจาก `Rule`/`addDomain`, `internal/migrationtui`
- UI ฝั่ง web: ดูแค่ confirm ของ action อันตราย ยังไม่ได้รัน `tsc`/`vitest` เพราะเครื่องที่ตรวจไม่มี Node
- ไม่ได้ทดสอบ crash ของ panel ตัว live จริง (A2) เพื่อไม่ให้ live install ล่ม ตัวเลขมาจากการวัด heap ของโค้ดเส้นทางเดียวกัน

## ภาคผนวก: วิธีรันการยืนยันซ้ำ

ไฟล์ทั้งหมดอยู่ใน `verification/` (Go ไม่ compile เพราะ `.scratch` ขึ้นต้นด้วยจุด)

**Go test:** copy กลับเข้า package ด้วยชื่อ `zz_review_verify_test.go` แล้วรัน เสร็จแล้วลบออก
```bash
cp .scratch/stability-review/verification/volfs_review_verify_test.go internal/volfs/zz_review_verify_test.go
cp .scratch/stability-review/verification/database_review_verify_test.go internal/database/zz_review_verify_test.go
cp .scratch/stability-review/verification/deployments_review_verify_test.go internal/deployments/zz_review_verify_test.go
cp .scratch/stability-review/verification/httpapi_review_verify_test.go internal/httpapi/zz_review_verify_test.go
go test ./internal/volfs ./internal/database ./internal/deployments ./internal/httpapi -run TestReview -v -count=1
rm internal/{volfs,database,deployments,httpapi}/zz_review_verify_test.go
```
A4 ต้องรันแยก เพราะผลที่คาดคือ test binary ตายด้วย panic:
```bash
cp .scratch/stability-review/verification/hostmetrics_review_verify_test.go internal/hostmetrics/zz_review_verify_test.go
go test ./internal/hostmetrics -run TestReviewA4 -v -count=1
rm internal/hostmetrics/zz_review_verify_test.go
```

**B6 (Docker, ไม่รัน prune):** `up` Compose ที่มี named volume ด้วย `-p relistreview-b6`, `docker compose down` แล้ว `docker volume ls -q -f dangling=true -f label=com.docker.compose.project=relistreview-b6` จากนั้น `docker volume rm` volume นั้น
**DB ของ live (read-only):** `docker cp relist-panel-panel-1:/var/lib/relist-panel/panel.db <scratch>` แล้วเปิดด้วย `sqlite3` แบบ `mode=ro` รัน `PRAGMA foreign_key_check` และ `SELECT MAX(version_id) FROM goose_db_version` แล้วลบไฟล์ copy ทิ้ง

**B4 (Docker):** ใช้ `verification/b4-docker-compose.yml` เป็น `a/` และ `b/` แล้วรันตามลำดับในตาราง B4 ด้วย `docker compose -p relistreview-a|b`
**C3 (Traefik):** สร้าง network `relistreview-net`, container `traefik/whoami` สองตัว (`WHOAMI_NAME=PANEL`/`APP`) และ `traefik:v3.7 --providers.file.filename=/dynamic.yml` โดย mount `verification/c3-traefik-dynamic.yml` แล้ว `curl -H 'Host: panel.example.test'` ไปที่ `/` และ `/api/...`
**C4 (Docker):** `docker compose -p relistreview-c4 -f verification/c4-docker-compose.yml up -d` แล้ว `timeout -s KILL 3 docker compose -p relistreview-c4 down -v` จากนั้นดู `docker ps -a`/`docker volume ls`

การทดลองทั้งหมดใช้ชื่อ `relistreview-*` และลบ container, volume และ network ที่สร้างขึ้นหมดแล้ว (ตรวจด้วย `docker ps -a --filter name=relistreview`)
