# GATT、通用消息与前台定时验收 — 2026-10-29

当前部署为 ABI 2.3 宿主、photoframe v21、插件 0.3.1。手机约 66 KiB 图片
上传从 280.1 秒降至 7.9 秒，取得匹配完成回执及人眼方向/颜色确认；前台定时
的真实 1 分钟检查与自动发送回执也已验证。

- [手机推图与安装验收](#phone-acceptance)
- [最终插件与重连收尾](#reconnect-acceptance)
- [前台定时推送](#schedule-acceptance)

下文按发生顺序保留早期 0.2.3/ABI 2.2 探测、失败尝试和后续修复；历史段落中的
“未部署/未验证”只代表当时状态，不描述以上最终结果。设备备份只记录元数据，
备份内容和真实凭据不在仓库中。

## 历史：初次增大 ATT 分片（插件 0.2.3）

Host base f4453c6 with connection-capacity characteristic 0006, client base
044256f with adaptive acknowledged fragmentation, plugin version 0.2.3.
Host retains APP ABI 2.2 / 128-byte application values; image CHUNK remains 106
bytes. No pairing or owner reset was performed.

- Web tests: 33 passed; TypeScript, WASM and panel build passed.
- Go make check: passed (unit/race/vet/build).
- Host pinned-IDF build: passed in build-gatt-capacity; host native tests:
  90 run, 2 skipped, no failures. Corrected signedness and indentation in existing
  native test fixtures to compile with this machine's strict GCC warnings.
- Photoframe app build and three native tests passed. Existing test indentation
  fixed. Built ELF SHA256 is exactly the documented v20 artifact:
  22d4946bbeb53de904232ea136ad8c4a7f40b866493176cbe03105c938264741.
  No APP binary change or redundant reinstall is required for this optimization.
- Plugin native ABI/resource verification passed locally and remotely; exact
  production CPA image with network=none loaded 0.2.3 and served matching bytes.
- Production plugin hot-update passed: all three resource bytes match; existing
  containers' ID/PID/start/restart counts and Qoder hashes unchanged; config inode
  preserved, only PhotoPainter priority 3 -> 4. Rollback backup:
  /var/backups/cpa-stack/photopainter-v023-20261029T021457Z.
- USB device COM55, MAC 9c139eb063e8. User confirms no SD card.
  Full 16 MiB backups before maintenance and before flashing both verified:
  SHA256 d40024b691e91e7010a4bb770cb870575453ba52a6774dd0e5c775974f49a714.
  Private backups are outside repositories under D:/PhotoPainter-backups.
- Host factory-only update completed, other Flash ranges verified unchanged.
  Host binary SHA256:
  6fb630c83057f80c61f7903c6c668348dbe7fdd5435b4af13ff777c310254d93.
- Plugin library SHA256:
  8d0e9165f2aac89aab1f3dd210e582acc90595cb86ce61c78b52f9460b99ad91.

Physical image transfer speed and final display are not yet validated. A
non-rendering BLE capacity probe is performed separately from image completion.

## Real-device non-rendering probe

Windows BLE discovery found `PP-63E8`; INFO device ID matched 9c139eb063e8.
Characteristic 0006 reported write capacity 244 (ATT MTU 247), fragment payload
240. A 512-byte invalid-opcode frame was sent in three acknowledged writes;
the host reassembled it and returned the expected protocol rejection. This proves
large-fragment receive/reassembly without pairing changes or display side effects.
The old 16-byte fragmentation would require 32 writes for this frame. This does
not measure authenticated image upload throughput or physical display completion.

## Follow-up: browser connection recovery (0.2.4)

User reported immediate connection/service-discovery errors and lost SWITCH
response. Native read-only test independently rediscovered PP-63E8, read capacity
244 and repeated the 512-byte / 3-write rejection probe successfully.

Client now retries only initial GATT discovery once after NetworkError, explicitly
disconnecting first; it sends no protocol requests during this retry. Abort causes
are preserved instead of presenting the induced disconnect error. Connection and
service-discovery budget is 120 seconds (the immediate reported error was not a
30-second timeout). After a lost SWITCH reply, the UI reauthenticates with its
saved bond and reads status, without repeating SWITCH.

35 browser tests passed, including disconnect recovery and abort-cause regressions.
Build/native ABI and isolated CPA load passed. Production 0.2.4 resources verified;
containers and Qoder unchanged, config inode preserved, plugin priority 4 -> 5.
Backup: /var/backups/cpa-stack/photopainter-v024-20261029T023256Z.
Plugin SHA256: 5428078ccd89eb6002b955cc416638415f0ef3214ae1aa6b1904c30a4f3bdd3b.

No browser authenticated image transfer or post-fix SWITCH validation is claimed.
Private serial capture contained no panic trace but ended with Windows USB serial
device-disconnected error; this does not exclude a device reset/disconnection.
Underlying immediate Windows/browser disconnect cause remains unconfirmed.

## Root-cause isolation: Chromium Windows GATT session handling

A minimal page in an independent real Windows Chrome profile reproduced the error
before any application protocol operation. Chrome reported connected, then
reported disconnected ~323 ms later while getPrimaryService was pending. Host
logs recorded GAP connect + ATT MTU 247 and no concurrent disconnect; host finally
terminated the idle connection at ~60 seconds (reason 534). Chrome's debug log
showed the legacy OnConnectionStatusChanged path and uncached discovery.

Chromium's BluetoothDeviceWinrt header gates the GattSession status/maintain path
on NewBLEGattSessionHandling; device/base/features.cc disables it by default.
With only `--enable-features=NewBLEGattSessionHandling` added to the same isolated
browser configuration, real service discovery and all five characteristics
succeeded. INFO read returned 20 bytes; capacity read returned version 1 / 244.
Chrome log showed OnGattSessionStatusChanged status=1,error=0 and successful
service discovery. The test deliberately disconnected after successful reads;
no physical pairing, image or SWITCH command was sent.

The web page cannot enable this browser-process feature. Local launchers under
D:/PhotoPainter-backups/Open-PhotoPainter-{Chrome,Edge}.cmd pass the flag to a normal
browser profile and preserve its existing cookies and bond keys. All processes
for that browser must exit first; launchers check this and never force-close it.
Chrome was hardware-tested; the Edge launcher was prepared but not tested.
Diagnostic host adds non-secret GAP status/reason and MTU logs; normal host
factory-only update and 16 MiB pre-write backup completed before this experiment.


## Correction: Edge verification

The user uses Edge. Explicit real Edge testing was performed after the initial
Chrome-only conclusion. Default Edge failed to connect; with
NewBLEGattSessionHandling enabled, Edge still disconnected during service discovery
(~0.35 seconds after connection). Its own logs confirmed the feature was active:
OnGattSessionStatusChanged status=1 followed by status=0, error=0. Therefore the
flag is not a validated Edge fix, and the previous root-cause conclusion was too
strong. The prepared Edge launcher was renamed .cmd.unverified to avoid presenting
it as a working solution. Edge connectivity remains unresolved.

## Windows service-discovery comparison

Official Edge 156.0.4314.50 MSI was fetched from Microsoft's update API/CDN and
verified against published SHA256
f7bc427c4d26aefe12bd492a24959cc6ca328aea2851614637d7133cae329071.
It was extracted and run using an independent Windows profile; the installed Edge
138, browser update policy, normal profiles and bond keys were not modified.
WSL-created case-sensitive extraction directories initially blocked Windows SxS
loading; only the extracted test directories were changed to case-insensitive.
Runtime reported Edge 156.0.4314.50. Default mode failed during service discovery;
new GATT mode succeeded once then stalled on second reconnect. Neither upgrade
nor experimental browser flags are a verified fix.

Native WinRT uncached service discovery also failed with ERROR_CANCELLED
(0x800704c7). Native cached mode completed five consecutive INFO/capacity reads
in 6.17, 0.62, 0.60, 0.72, 0.54 seconds. Edge 138 full-service discovery with a
name filter also failed, excluding the proposed targeted-discovery workaround.
Windows BluetoothRemoveDevice for the exact BLE address returned 1168 (not found),
so no Windows pairing/cache entry was removed by that call.

Adapter: Realtek Bluetooth Adapter, driver 1.10.1061.3000, USB VID 0BDA / PID B851.
The current Windows identity is not administrator. A guarded adapter-only restart
script was prepared in D:/PhotoPainter-backups/Restart-PhotoPainter-Bluetooth.ps1;
it has not been executed. No Bluetooth driver update or adapter restart is claimed.
The next experiment is an administrator-authorized adapter restart and the same
Edge/native uncached probes. Root cause remains unresolved within the Windows
GATT service-discovery path; cached I/O succeeds and uncached discovery fails.

## USBIP state recovery

Observed Realtek USB composite 0bda:b851 at bus 1-3 in usbipd `Shared (forced)`
state, not `Attached`. Windows showed the parent as an errored `USBIP Shared
Device`. ESP32 USB 303a:1001 / COM55 was `Not shared` and remained in Windows.
Unbind initially required administrator privileges; after the user action,
usbipd reported `Not shared` and Windows showed the composite, Bluetooth and
Wi-Fi interfaces all OK. The authorized elevated adapter restart helper then
returned exit 0. All three nodes reported problem code 0. No browser installation,
update-policy edit or device unpairing was performed.

One Edge 138 default-mode connection/read passed after adapter restart, but an
immediate reconnect failed. Later diagnostic output contained overlapping requests,
so those runs are not accepted as stability evidence. Old diagnostic clients were
closed and a fresh-profile probe was guarded against duplicate clicks and chooser
selection. That final chooser was cancelled before connection (NotFoundError).
The adapter/driver recovery is verified; production Edge connection and physical
image completion still require confirmation. No repeat connection success is claimed.

## Application startup diagnostics (plugin 0.2.5)

The user's browser now authenticates but no application version is displayed;
SWITCH photoframe still loses its connection. Previously inspectFrame discarded
all readiness failure details. The plugin now preserves a whitelist of runtime
and application-slot metadata in device details, displays unloaded version
explicitly, distinguishes startup failure from absent image service, and includes
phase / last_result in readiness errors. No credentials or pairing keys are copied
into diagnostics. Idle connected/not-ready state no longer says "preparing".

36 web tests, TypeScript/WASM/panel build, plugin native tests, native C ABI and
isolated exact CPA load passed. Production 0.2.5 hot-update verified all resources,
containers and Qoder unchanged, config inode retained; PhotoPainter priority 5->6.
Backup /var/backups/cpa-stack/photopainter-v025-20261029T054258Z.
Library SHA256 896c0f03acd4036348abdb0e10fdfdd60c350d3fd72cca6ff62c6fcbac0801e1.
This adds diagnosis, not a claimed fix for application startup. Actual paired
runtime status has been requested from the user's existing browser owner; no
new owner was created and no second SWITCH was sent.

## User runtime status and capture limitation

Paired status supplied by the user: both bank-0 slots version 20, active 1,
previous/pending -1; selected 0; loaded_app/loaded -1; generation/version/payload
ABI 0; phase fault, last_result 259 (ESP_ERR_INVALID_STATE), no app BLE service.
This proves the app is not loaded, despite valid catalog packages. The boot
protection path is a candidate; its record has not been erased or bypassed.

The reset-based serial reader exited on invalid Windows device handle; the
passive reader then exited on ERROR_OPERATION_ABORTED (995) with zero captured
application bytes. Neither capture proves a crash or watchdog reset. The passive
reader was corrected to reconnect after 995, but no successful new capture is
claimed. Windows boot time was 2026-10-29 13:21:36, predating USBIP unbind at about
13:29. Full Windows restart after driver restoration was requested as the next
controlled baseline before further BLE/serial experiments. CBS and Windows Update
reboot-pending keys were false; no claim of those global pending flags is made.

## Prevent automatic sharing of the Windows radio

After user authorization and successful Windows elevation, added usbipd rule
6a7516a3-c3e1-4347-ba4a-4241a279aab7: Deny AutoBind, hardware-id 0bda:b851.
Verified policy list and adapter remains Not shared; ESP32 COM55 remains Not shared.
Rule targets the hardware ID rather than a changing bus ID. Existing phone sharing
was retained. Scheduled tasks inspected contained no usbipd action; the named USB
hotspot script contained no bind/attach command; initial usbipd policy was empty.
Sharing via explicit bind is persistent across reboot according to upstream docs.
AutoBind denial does not prevent an administrator explicitly calling bind --force.
Rollback if explicitly requested: usbipd policy remove --guid
6a7516a3-c3e1-4347-ba4a-4241a279aab7 (confirm CLI syntax before executing).

## Post-reboot runtime checkpoint diagnostics (0.2.6)

Windows reboot preserved the USBIP Deny AutoBind rule and both radio/ESP32
Not shared state; Windows nodes reported OK. Independent Edge 138 default-mode
connection still failed once, so driver restoration is not claimed as the full fix.

Host now publishes runtime_diagnostic in its existing authenticated status: current
startup step, previous RTC-retained step, ESP reset reason, and boot journal decision.
Journal bytes and recovery policy are unchanged. No journal clearing/unblocking was
performed. The field lets the paired owner distinguish exhausted fallback protection,
invalid record identity, ELF relocation and application START failures without
relying on the Windows USB serial handle. No new BLE characteristic or ABI was added.

Host full native suite: 90 tests, 2 skips, no failures. Additional module/boot
regression run: 29 tests passed, including explicit exhausted-fallback reason check.
Pinned-IDF host build passed. Factory-only update completed with pre-write 16 MiB
backup SHA256 babd8b13a1a234a60e90ef8d4896602421fc32250bba6d9116dee442dc51bc65
in D:/PhotoPainter-backups/20261029T142429-syk1z65h. Other Flash ranges verified
unchanged. User's no-SD condition retained.

Plugin 0.2.6 exposes that one additional diagnostic field; 36 web tests and build,
native ABI/resource check, isolated CPA loading passed. Production resources match;
containers/Qoder/config inode unchanged, only plugin priority 6->7. Backup:
/var/backups/cpa-stack/photopainter-v026-20261029T062813Z.
Actual pre/post-SWITCH diagnostics have been requested from the paired browser.
Application startup/display success is not yet confirmed.

## USB maintenance reset boot-guard repair

User status reported reset=11 (ESP_RST_USB), current=boot_guard,
guard=fallback_attempt, generation=0, last_result=259, active=1/previous=-1.
The previous boot guard treated every interrupted START intent as an abnormal boot,
including USB maintenance reset. With no previous slot it returns INVALID_STATE
without attempting the active app. USB maintenance could therefore strand an
otherwise installed image after flashing/serial reset.

app_boot_prepare now receives the explicitly classified USB-maintenance reset flag
from esp_reset_reason. After all existing checksum, record range and app identity
checks, only ESP_RST_USB permits re-arming the selected image without consuming or
changing catalog fallback state. All non-USB resets retain bounded fallback and
exhaustion; corrupt journals and failed commits still reject entry. No application
or pairing journal is erased. Host runtime guide updated.

94 native tests run, 2 skips, no failures. New cases cover the exact active=1 /
previous=-1 fault, exhausted fallback USB retry followed by repeated abnormal
resets, corrupt-record rejection and failed-commit preservation. Pinned-IDF build
passed. Host binary SHA256 bd45a4b78567934f4356204861dd45289892141ad147ac747ed0f4fcd45719e7.
Factory-only deployment completed with verified 16 MiB backup at
D:/PhotoPainter-backups/20261029T144120-d6fo61dk, pre-flash SHA256
68f3546b32070bc9642edf498ce56dc59ae0217145d342c253e5c28733eca3e9.
Outside-factory ranges verified before reboot. No SD card present per user.
Native app-service discovery then timed out in Windows; it does not prove app
startup success/failure. Fresh authenticated status was requested from the existing
browser owner; physical startup and image completion remain unconfirmed.

## GATT slow-upload verification and hot-path repair

User reported 60 KB taking 5–6 minutes after increased MTU. Static protocol audit:
image payload stays 106 bytes under the ABI's 128-byte app value. Each chunk uses
APP_WRITE then APP_READ; each exchange previously read limits, acknowledged writes,
and polled TX. A 60 KiB image therefore needs 580 chunks, 1,160 signed business
requests, and at least 3,480 browser GATT calls before pending polls.

A temporary native profiler compiled the production app_manager/app_ble code with
platform stubs and counted app_catalog_read calls: 100 data/receipt pairs caused
800 slot reads. Each snapshot scans both installed slots and app_catalog_read
loads/validates the full ELF, including SHA-256. For two 76,256-byte images, the
model is 4,640 ELF validations / 353,827,840 bytes for 60 KiB transferred. This is
call-count evidence, not an ESP32 timing measurement or hardware throughput claim.

Normal successful BLE_REQUEST/BLE_FLUSH jobs now skip full catalog snapshots.
Control operations, nonzero/changed results, runtime/generation/service-revision
changes and display completion still publish. Regression tests verify no slot reads
for 100 ordinary pairs, preserved control publication, and fault readiness updates.
The same profiler now reports 0 slot reads for 100 ordinary pairs after a control
operation establishes steady state. Host 94 tests passed, 2 skipped; pinned IDF
build passed. Factory-only flash completed after explicit user idle/no-uncertainty
confirmation, with full 16 MiB backup under
D:/PhotoPainter-backups/20261029T154935-tviho22q; pre-flash SHA256
5cc00b31b56ce1fe7e80c106a9b3808d5ff1097e919433523ffefb72a7b9d186.
Host binary SHA256 dd9879219373b6c0de271aec948703eb41d8b41ac3c022ec3129c89d74d797d4.

GattLink now reuses negotiated write capacity above 20 bytes for its connection;
a minimum observed before MTU exchange is re-probed. Regression proves initial
20 -> 244 negotiation and subsequent cached use. UI reports upload speed/time
separately from post-COMMIT display wait; receipt recovery does not fabricate an
upload duration. Application ABI, 106-byte image chunks and completion validation
remain unchanged. No new real-image upload has been measured yet.

## CPM integration and remembered source (0.2.7)

Same-origin CPM cli-proxy-auth is read using the deployed storage contract
(plain JSON and enc::v1/enc::v2 formats). Credentials are reused only for the same
API origin and kept out of device messages, URLs and logs. Startup reuses a saved
CPM login and collects data without another form. If CPM did not remember its key,
manual input can be remembered in separate browser storage, using CPM-compatible
v2 obfuscation (not encryption). Clearing plugin storage preserves CPM login;
CPM logout invalidates an inherited in-memory credential and prepared frame.
Storage/format errors are surfaced. No server-side credential persistence added.

46 web tests, TypeScript/WASM build, plugin native tests, 73 isolated browser UI
checks passed with zero findings. UI checks include automatic CPM login reuse,
manual-source reload persistence, logout invalidation and existing safe transfer /
deduplication / cancellation flows. Native plugin ABI and isolated exact CPA image
load/resources passed. Plugin 0.2.7 library SHA256
fe51f23fe21c826197f15bff640ef22bb23b5c228c3dfa04edbb7a8b99f25b00.

## USB-only connection comparison

After the user removed the battery, Windows native cached WinRT probe again
completed five public INFO/capacity reads: 5.42, 0.68, 0.72, 0.60, 0.91 seconds.
The user still observed Web GATT service-discovery failures. No new owner/pairing
was created. A temporary build-tagged test exercised the actual Go Windows
withNativeLink path without authentication or display: first read succeeded in
10.25 seconds, second session timed out at 25 seconds. Thus the cached API probe
is not evidence of full production-native client stability. An isolated service
cleanup hypothesis also timed out; its source change was reverted and not shipped.
No persistent native-path repair or architectural switch is claimed.

## Direct phone verification and upload — 2026-10-29 18:05

At user's request PC pushing was stopped. Existing WSL backend snapshot showed
automatic=false, transfer idle and uncertain=false, then photopainter-backend.service
was stopped and verified inactive. Source tunnel was not stopped. No image was sent
from the PC. ADB controlled the connected Android phone (OnePlus CPH2573, SDK 37,
root already available); ordinary wake/menu/swipe unlocked it without credentials.
The phone's existing Chrome Custom Tab and existing pairing were used.

Version proof, not historical deployment inference:
- Running phone frame inline JS SHA256
  a8be0132fe4c6bae887a54f15903dd894d2e3cb61edc9a51583dd465e20f9d83,
  exactly matches published 0.2.7 inline JS. Server full page also matched the
  published 88,205-byte SHA256 08f4ef0db0ca0ed00eae97ccde9f288a647382fdba90db686e2afe230e61ea0a.
- While phone had no transfer/pending result, esptool verify-flash compared the
  actual factory image's 1,136,752 bytes at 0x10000 against the dd987921... build
  containing the data-job optimization. Device digest matched. Verification was
  read-only and reset the device; no NVS or application slot was written.

Initial scripts did not send an image: one hit a source-refresh/UI click timeout,
then metadata fetch of the blob preview was blocked. These attempts were abandoned
before BEGIN/COMMIT; no repeated image draw was issued. Subsequent connection needed
the Android device chooser; PP-63E8 was selected. On a later script, phone connected
and reported boot guard fault 259; one authorized app-start command was issued.
A new authenticated status at +27.40s showed current=ready / complete_journal,
last_result=0, generation=1, with the same reset=1 as before. Thus that particular
manual START completed without a new device reset; earlier retained diagnostics
must not be mistaken for fresh evidence of a reset after the command.

One image transaction then completed in the phone browser. No new binding or
physical owner reset was performed. The UI's validated final receipt reported:

| Metric | Result |
|---|---:|
| Image bytes | 67,460 |
| Upload | 280.1 s |
| Post-COMMIT display wait | 20.5 s |
| Upload throughput | 0.2352 KiB/s |
| Upload GATT writes | 1,316 |
| Upload GATT reads | 1,316 |
| Sum of upload API waits | 274.3983 s (97.96% of upload wall time) |
| Write median / p95 | 90.7 / 180.6 ms |
| Read median / p95 | 92.0 / 178.9 ms |
| Negotiated ATT value capacity | 244 B |
| Transport capacity reads | 1 for connection/control; 0 during upload |

DATA write attempts=654, including retries; unique payload chunks are
ceil(67460/106)=637. The code still performs separate APP_WRITE and APP_READ for
each chunk. Total pending-zero replies=337; 158 occurred during control/app startup
and 179 during display wait. There were zero extra pending polls during upload
(the upload response-read count equals its request-write count).

Root device-side BluetoothGatt and existing vendor stack logs confirmed MTU 247,
connection interval 24*1.25 ms=30 ms, slave latency 0, supervision timeout 5 s,
and DLE 251 octets in both directions. PHY was observed switching between 1M/2M;
RF retransmission/RSSI were not measured and are not ruled out. In the upload
18:01–18:04 window, 1,117 paired native GATTC_Read-to-response events on conn_id
0x78 had median 79 ms, p95 168 ms, mean 91.40 ms. This indicates much of the delay
is below the webpage callback, not solely JS execution; it does not isolate radio
scheduling from firmware response processing.

Only opcode/timing/length/capacity metadata were retained for reporting. No keys,
authentication payloads or credentials were printed. Instrumented JS methods were
restored, the dedicated logcat collector stopped, the ADB DevTools forward removed,
and original screen timeout 60000 / stay-on 0 restored. PC backend remains inactive.
Physical receipt completion is verified; human visual confirmation has not yet
been supplied. This was Web GATT, not CoC. The repeated-slot-scan repair is present,
but this measurement disproves any claim that the existing GATT path is now fast.

## Fresh reboot after phone upload — 2026-10-29 18:22

User observed WAITING FOR IMAGE after the earlier completed upload. A fresh
authenticated status was read by reconnecting the existing phone browser, without
START, image send, USB serial access or device reset. At 18:22:28 local time:
uptime_ms=212679, reset_reason=1 (POWERON), generation=1, ready=true, phase=idle,
last_result=0, last_switch_source=boot, last_switch_target empty, diagnostic
reset=1 previous=unknown current=ready guard=complete_journal. This dates the
new boot to approximately 18:18:55, after the 18:05 completed transfer. Thus this
observation is a real intervening boot, not merely a retained old diagnostic.
POWERON is compatible with supply interruption but does not identify a USB cable,
port, PMIC or specific voltage fault; no BROWNOUT diagnosis is claimed. The app
restores only RAM cache, so reboot loses the received frame and START renders the
waiting page. No SD is installed. PC backend remained inactive. Temporary phone
JSON parsing observer was restored and ADB DevTools forward removed. No new
physical refresh, firmware deployment or power configuration change was issued.

<a id="phone-acceptance"></a>

## ABI 2.3 deployment and phone acceptance — 2026-10-29 18:57–19:05

User selected this round's scope: unified message layer + phone Web GATT,
firmware/application/plugin deployment; native phone CoC and Wi-Fi are deferred.
The implemented first stage is a transport-independent app mailbox and a pure
image transaction core shared by legacy GATT, CoC and CHANNEL. It is not the
full proposed AEAD/Go-WASM/session/TCP framework. The existing physical pairing,
per-connection HMAC and published old ABI layouts are retained.

Tested worktree bases, with uncommitted implementation changes:
- host f4453c646d820760102c6bcb11445d62362696f9;
- photoframe ae194dae683dcdea52854c08c271233ec4b96697;
- sender/plugin 044256f4a0a52da0ae3550f862293fda07da166c.
Host tests: 96, latest pinned-IDF run one skip. Native app tests: 3/3. Browser:
49 at initial release, 50 after teardown guard. UI: 73 checks, zero findings.
Root Go make check (tests, race, vet, static build), pinned-IDF host/app builds,
manifest/import validation, native plugin tests/C ABI and isolated CPA passed.

Factory firmware SHA256 13ff42a024ed388b57e5d8cc001e6cca7042f7af6315ca8db7f719523857ddb0.
Trusted USB tool first backed up and verified all 16 MiB, wrote factory only and
verified the image and unchanged bytes outside factory. Backup directory:
D:\PhotoPainter-backups\20261029T184701-_f_g87mu. Backup SHA256
b320efcc2552598c033ab183f041a40f3a2239170a43ab9192368405ace0a513.
No SD installed. Pairing/NVS and old app slots were preserved.

The phone staged and explicitly activated photoframe v21, keeping the binding's
CryptoKey within the browser origin. App ELF 77,428 B SHA256
9798ed46644fd88a551277a88ae618ab91b7751dbda97bfbd15050f3c2ce59f3;
package 81,524 B SHA256
57e1edd845e61d120652ec45932f5cc2fad35cb158003c78cd7360af4b9c5896.
Final authenticated status: version21, ABI minor3, ready/confirmed true,
generation2, reset11 (controlled maintenance USB reset). Installed current phone
script bytes matched plugin 0.2.8 inline SHA256
d5ca2f926b4cd6ad1046cd0bbb4092dc0313425d22167fd622cc7b65d445842c.
Initial read-only tool lacked getDevices in this Android browser; it failed before
staging, then explicitly selected the already paired PP-63E8 with requestDevice.
No new physical pairing or owner reset was performed.

After reload, first phone picker observation expired before selecting any device;
no BEGIN/COMMIT was issued. The pending picker was rescanned, PP-63E8 appeared,
and the same UI connection completed. Only then was exactly one image sent.

| Metric | New phone result | Earlier phone result |
|---|---:|---:|
| Image bytes | 67,626 | 67,460 |
| Upload | 7.9 s | 280.1 s |
| Upload rate | 8.36 KiB/s | 0.235 KiB/s |
| Physical display wait | 20.3 s | 20.5 s |
| DATA messages | 17 | 637 unique / 654 attempts |
| Upload Write Command calls | 285 | 0 |
| Upload final Write Request calls | 17 | 1,316 |
| Upload response reads | 17 | 1,316 |
| Total upload API calls | 319 | 2,632 |
| Sum upload API waits | 7.8081 s | 274.3983 s |
| Upload API errors | 0 | 0 |
| ATT value capacity | 244 B | 244 B |
| Logical request limit | 4096 B | 512 B |

New upload Write Command median6ms/p95 10.5ms; final Write Request median227ms,
p95 419.2ms; response read median91.1ms/p95 274ms. Upload TX API bytes70,041,
RX1,751. No pending polls during upload. Display phase149 reads included148
pending markers; display polling does not represent extra image DATA or refreshes.
The signed final matching COMPLETE receipt cleared durable pending identity.
Wall upload improvement35.46x, normalized by image bytes35.54x. This is Web GATT,
not CoC. Root logcat collector emitted no records this round; it is not new HCI
latency evidence. Public negotiated capacity and phone API timings are measured.

User explicitly confirmed the physical image, orientation and colors normal.
Fresh post-display authenticated status at18:59:05: version21, generation2,
uptime546069ms, reset11, ready=true, last_result0, cache_bytes384000. Thus no
new reset occurred during upload/display; RAM cache was retained. Two additional
settled reconnections succeeded at19:05 (8.056s/3.996s), uptime885209/890129ms,
same boot/generation and cache. This is bounded evidence, not proof that the
USB power source can never fail.

An immediate reconnect stress attempt after a successful read returned one
browser GATT unknown-error; a45s observer expired without a fresh status. Its
failure is retained, not counted as a pass. Waiting for the session lock release
and a1s teardown interval made two subsequent reconnections pass. Plugin0.2.9
adds a cancellable1s per-device teardown window before reusing a disconnected
GATT client; its final phone reconnect results are recorded below. Image protocol
and application bytes are unchanged from the measured 0.2.8 upload.

<a id="reconnect-acceptance"></a>

### Final plugin0.2.9 teardown guard and cleanup

Plugin0.2.9 C ABI/resources and network-none exact CPA loading passed; production
priority9→10 updated in place, with all existing containers and Qoder unchanged.
Backup /var/backups/cpa-stack/photopainter-v029-20261029T110929Z.
Library SHA256 e53f03e05aed9875ba950862fe13b3adfa7ef4193560ac21a8964c6f1e064231;
panel90,439 B SHA256 cdb0f8ebc554a7d6aeaf9e6dc69d636aafd461925850c3f5213f249d053087b2;
phone executing inline JS matched ba08758882c834acba8379883e0990d8a8c8e408484aca5ea995b54d17c30bbc.
50 browser tests and73 UI checks passed, zero findings. The maintenance tool also
passed strict TypeScript checking. The only runtime change from0.2.8 is a
cancellable1s per-device native teardown window before GATT reuse; image framing,
transaction and display code are unchanged. No second image refresh was issued.

One initial connection attempt after reload failed with browser Connection Error:
Connection attempt failed. It is not counted as success or proof of perfect radio
stability. Subsequent two quick disconnect/reconnect cycles, with no deliberate
1s sleep in the harness, both passed using the production guard:4.929s and5.570s.
Fresh statuses: version21/ABI2.3, readytrue, generation2, cache384000,
uptime1233989ms/1239509ms, reset11. These show no intervening reset since the
controlled maintenance reset. PC automatic backend remains stopped. No native
CoC/Wi-Fi phone client or complete future AEAD protocol is claimed.

User accepted the scoped message/GATT round and explicitly confirmed the physical
image, direction and colors. All temporary JS observers/prototypes were restored;
root logcat collectorPID8747 was stopped, DevTools forwarding was removed, and
original phone screen timeout60000/stay-on0 restored. No credential export,
physical owner reset, NVS replacement or SD operation was performed.

<a id="schedule-acceptance"></a>

## Foreground interval scheduling — plugin0.3.1, 2026-10-29 20:03

User requested timed plugin pushes. The foreground interval option was offered
with a15-minute default; background native services and daily wall-clock schedules
were not implemented or promised. Existing periodic collection/change-triggered
auto-send is now exposed in a main-page schedule card: enable/pause, custom whole
minutes1..1440, next check and current-page latest confirmed automatic completion.
Settings persist in existing browser preferences. Unchanged images are deduplicated,
failed/stale collection invalidates the queue, only the latest image is retained,
and pending COMMIT outcomes block automatic redraws. Reconnect completion rechecks
the plan state, so a pause/hidden page wins even while authentication is pending.

Opt-in screen waking uses browser Screen Wake Lock only, without changing OS
settings. Missing/denied/revoked locks are surfaced. Hidden pages suspend automatic
checks/sends; returning to foreground resumes the next due/latest task, without a
catch-up burst. No guarantee is made for locked/suspended/closed pages. Fresh page
loads on Android may require selecting the paired device once before quiet
reconnections can proceed. No server-side BLE scheduler or native service added.

53 webpage tests passed, including lock visibility/pause/pending-acquisition/error
cases;84 isolated UI checks passed, zero findings, including interval persistence,
invalid inputs, next-check display, pause, stale/dedup safeguards, foreground
resume and pause during a deliberately held reconnect. TypeScript/WASM/panel
build and root make check (test/race/vet/static build) passed. Native plugin tests,
C ABI/resources and exact CPA8.0.45 network-none isolation passed. No firmware or
application change/flash was needed.

Initial0.3.0 hot update priority10→11 was followed by0.3.1 priority11→12 to include
the pause-during-reconnect guard. Existing production containers and Qoder bytes
were unchanged; config inode preserved. Final backup:
/var/backups/cpa-stack/photopainter-v031-20261029T115824Z. Pre-feature backup:
/var/backups/cpa-stack/photopainter-v030-20261029T115421Z.
Plugin0.3.1 SHA256 9232dc54ab4124a212b48b96be39cd3413cc814074932d36bb9fbc26cac69952;
panel96285B SHA256 e7dd77010d949cc6eea1144af3bb8bb0be85e8301c4a8addfabb1acd98391310.
Production all three resource bytes and phone executing inline JS matched builds.

The same existing phone browser/binding was used. A first read-only observation
failed on Playwright's string-predicate eval under the existing CSP before enabling
the plan; the harness was corrected to use function evaluation/polling, without
weakening CSP. The accepted run set the interval to1minute and explicitly enabled
screen waking and the schedule. The first tick began at+60013ms, even while the
operator had not yet selected PP-63E8 in the browser device chooser. Collection
completed at+69972ms. After selecting the existing paired device, connection
completed at+87600ms and the start workflow fetched fresh data (+87612ms to
+93784ms). No manual Send click was issued. Automatic upload began at+95650ms,
display at+103527ms, and matching signed COMPLETE at+123925ms: upload7.9s,
physical display wait20.4s. Exactly one image completed; no pending record or
active transfer remained. This proves a real1minute timer tick plus automatic
fresh-data delivery; it is not a continuous multi-day background test.

The phone UI reported an active screen lock. Android screen_off_timeout60000 and
stay_on_while_plugged_in0 were unchanged before/after the run. The plan was paused,
interval restored to15minutes and the opt-in lock disabled; the temporary observer
was removed and the BLE connection released. Phone report:
D:\PhotoPainter-backups\phone-schedule-result.json. PC pushing stayed inactive.
Human confirmation of this new20:03 image was requested separately; earlier
v21 image/orientation/color confirmation does not substitute for that reply.
