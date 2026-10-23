# Kube Desk: lịch sử phát hành

Các bản phát hành của Kube Desk, mới nhất ở trên. Mỗi bản ghi lại: tính năng
mới, cải tiến, lỗi đã sửa, những gì cần biết khi nâng cấp và các giới hạn đã
biết.

> `CHANGELOG.md` (tiếng Anh) là nguồn của phần release notes hiện trong cửa sổ
> cập nhật của app. Khi ra bản mới, cập nhật cả hai file.

| Bản | Ngày | Điểm chính | Tải về (delta) |
|---|---|---|---|
| [0.6.0](#060--24102026) | 24/10/2026 | Helm releases, Resource map, nút theo quyền RBAC, kiểm tra kubeconfig, assistant dùng được Vertex AI và lưu lịch sử chat | xem trang Releases |
| [0.5.0](#050--23102026) | 23/10/2026 | AI assistant chat ngay trong app (Claude trên Microsoft Foundry), đề xuất thay đổi phải được duyệt | xem trang Releases |
| [0.4.1](#041--22102026) | 22/10/2026 | Sửa lỗi màn hình trắng khi mở AI agents hoặc so sánh giữa các cluster | xem trang Releases |
| [0.4.0](#040--22102026) | 22/10/2026 | AI agents qua MCP (có duyệt từng thay đổi), tìm kiếm mọi cluster, so sánh giữa các cluster | xem trang Releases |
| [0.3.0](#030--22102026) | 22/10/2026 | Timeline thay đổi, xem trước ảnh hưởng, báo cáo sự cố | 15.5 MB (từ 0.2: 2.5 MB · từ 0.1: 5.0 MB) |
| [0.2.0](#020--22102026) | 22/10/2026 | Watchlist và thông báo, log gộp, bảo vệ production, cột CPU/RAM | 15.4 MB (từ 0.1: 4.9 MB) |
| [0.1.0](#010--21102026) | 21/10/2026 | Bản đầu tiên: nhiều cluster, resource realtime, logs, shell, tự cập nhật | 14.0 MB |

Tải bản mới nhất:
<https://pub-e3b2030c705649ef87d35925ea3331eb.r2.dev/Kube-Desk-latest-arm64.dmg>
(macOS 12 trở lên, Apple silicon). App đã cài sẽ tự cập nhật. Thường app chỉ
tải phần thay đổi (delta), như ở cột cuối bảng trên.

---

## 0.6.0 · 24/10/2026

### Tính năng mới

**Helm releases** (mục **Helm** ở sidebar, hoặc "Helm releases" trong ⌘K)
- Danh sách release theo namespace hoặc cả cluster: chart, app version,
  revision, trạng thái, lần cập nhật cuối.
- Mở một release để xem:
  - values: bản bạn đưa lúc install, hoặc bản đã gộp với giá trị mặc định của
    chart;
  - manifest đã render (giá trị Secret bị che), notes, danh sách object đã
    tạo (bấm vào là mở object);
  - lịch sử các revision.
- **So sánh hai revision**: diff values hoặc manifest.
- **Rollback về một revision**: trước khi chạy, app hiện diff những gì sẽ đổi
  và những object Helm sẽ xoá. Cluster production bắt gõ tên, cluster
  read-only từ chối.
- Đọc release thẳng từ Secret mà Helm lưu trong cluster, nên không cần cài
  helm CLI. Riêng rollback thì chạy lệnh `helm rollback`, để Helm ghi lại lần
  rollback vào lịch sử như bình thường. Máy chưa có helm thì app báo cách cài.

**Resource map** (mục **Map** ở sidebar, và tab **Map** trong chi tiết object)
- Vẽ namespace thành từng app, traffic đi từ trái sang phải: Ingress →
  Service → Deployment/StatefulSet/DaemonSet/CronJob → ReplicaSet/Job → Pod →
  ConfigMap/Secret/PVC mà pod dùng.
- Mỗi object có chấm màu theo trạng thái; app nào có vấn đề thì được viền màu
  và xếp lên đầu.
- Tự phát hiện các lỗi hay gặp: Service không chọn được pod nào, Ingress trỏ
  tới Service không tồn tại, ConfigMap/PVC bị thiếu.
- Workload nhiều pod được gộp thành một ô; bấm vào để chọn pod.
- Rê chuột vào một object để làm nổi các kết nối của nó, bấm để mở. Có ô tìm
  app, lọc "Problems only", bật/tắt config và storage, zoom (⌘ + cuộn chuột).
- Map tự cập nhật theo thời gian thực vì dùng chung dữ liệu watch với các
  danh sách.

**Nút bấm theo quyền RBAC**
- App hỏi cluster xem user của bạn được làm gì (`SelfSubjectAccessReview`,
  như `kubectl auth can-i`). Các thao tác không được phép sẽ bị khoá: xoá,
  scale, restart, sửa YAML, mở shell, tạo object, chạy CronJob, cordon/drain.
- Rê chuột vào nút bị khoá để xem lý do. Kết quả được nhớ 2 phút.
- Nếu cluster không trả lời được, nút vẫn mở như cũ; cluster vẫn là bên quyết
  định cuối cùng.

**Kiểm tra sức khoẻ kubeconfig** ("Kubeconfig health" trong ⌘K)
- Kiểm tra mọi cluster mà không cần kết nối:
  - client certificate, CA certificate và token đã hết hạn hoặc sắp hết hạn;
  - credential plugin chưa cài (gke-gcloud-auth-plugin, kubelogin, aws…),
    kèm lệnh cài;
  - auth provider `gcp`/`azure` đã bị Kubernetes bỏ, kèm cách chuyển sang
    plugin;
  - server dùng HTTP thường hoặc không kiểm tra certificate;
  - file certificate hay token bị thiếu;
  - cluster bị thêm hai lần (cùng server, cùng user, cùng namespace).
- Vấn đề hiện trên trang Clusters (thanh cảnh báo và nhãn trên từng thẻ),
  trong cài đặt của cluster, và trong hộp thoại Kubeconfig health; hộp thoại
  này còn ghi lần cuối app kết nối được tới cluster.
- Khi mở app, nếu có credential sắp hết hạn, app báo ngay.

**AI assistant**
- **Thêm Claude trên Google Vertex AI**: chọn "Google Vertex AI" trong
  Assistant settings, nhập project và region (mặc định `global`). App đăng
  nhập bằng gcloud của bạn: chạy `gcloud auth application-default login` một
  lần, không cần API key.
- **Lưu lịch sử chat**: mở lại cuộc chat cũ từ nút đồng hồ trên khung
  assistant và hỏi tiếp; xoá được từng cuộc. App giữ 100 cuộc gần nhất.
- Assistant (và agent qua MCP) đọc được Helm release nhờ tool mới
  `helm_releases`; values có mật khẩu/token đều bị che.

### Giới hạn đã biết
- Resource map vẽ các loại object có sẵn trong Kubernetes; Gateway API và
  custom resource chưa có trên map.
- Kiểm tra RBAC dựa trên câu trả lời của cluster cho user hiện tại; với cluster
  không hỗ trợ `SelfSubjectAccessReview`, các nút vẫn mở như trước.
- Chưa test Vertex AI với tài khoản Google thật, mới test với server giả lập.

---

## 0.5.0 · 23/10/2026

### Tính năng mới

**AI assistant trong app**
- Mở bằng ⌘J, nút **Assistant** trên title bar, hoặc "Ask the assistant" trong
  ⌘K. Hỏi bằng ngôn ngữ của bạn; assistant trả lời bằng chính ngôn ngữ đó.
- Assistant là **Claude trên Microsoft Foundry (Azure)**:
  - cài đặt gồm resource Foundry (hoặc Target URI của deployment), API key,
    tên deployment (mặc định `claude-opus-5-5`) và mức effort (mặc định High);
  - API key lưu trong **Keychain của macOS**, không nằm trong file nào, và
    trang web của app không bao giờ thấy key;
  - nút "Save and test" kiểm tra kết nối và giải thích các lỗi hay gặp
    (401, 403, 404 deployment, 429).
- **Nắm toàn bộ cluster:** đọc được mọi cluster đang hiện trong Kube Desk.
  - Bắt đầu từ tool mới `cluster_overview`: node, từng namespace với số pod
    theo trạng thái, workload chưa sẵn sàng, pod restart nhiều, Warning event
    trong 1 giờ qua.
  - Sau đó đọc chi tiết: resource, logs, events, timeline (đổi gì, ai đổi),
    báo cáo sự cố; tìm kiếm và so sánh giữa các cluster.
  - Biết bạn đang xem gì: cluster, namespace, trang, object đang mở. Hỏi
    "pod này bị gì?" là đủ.
- **Thay đổi phải hỏi và được duyệt:** khi assistant muốn apply, xoá, scale
  hay restart, app mở hộp thoại duyệt (giống MCP) với diff, phần "What this
  affects" và lý do assistant đưa ra. Cluster production bắt gõ tên, cluster
  read-only từ chối. Thay đổi được ghi tên
  `kubedesk-agent (Kube Desk assistant)` nên timeline biết ai đã làm.
- Khung chat hiện từng bước assistant làm (đọc gì, gọi tool nào, đang chờ
  duyệt), phần suy luận thu gọn được, số token kèm tỷ lệ cache. Có nút Stop
  và New chat.
- **Fallback:** một deployment dự phòng (mặc định `claude-opus-4-8`) trả lời
  thay khi deployment chính từ chối một yêu cầu. Nếu deployment không hỗ trợ
  fallback, app tự chạy tiếp không kèm fallback và báo cho bạn.
- **MCP vẫn là tuỳ chọn riêng** cho agent bên ngoài; assistant không cần bật
  MCP. Tool `cluster_overview` có cho cả hai.

### An toàn
- Câu trả lời của assistant chỉ được hiển thị dưới dạng Markdown: HTML trong
  câu trả lời bị hiện thành chữ, và kết quả còn được DOMPurify làm sạch thêm
  một lần.
- Tham số tool bị cắt giữa chừng (stream bị ngắt) không bao giờ được chạy như
  thể đầy đủ; assistant được báo lại để gọi lại.

### Giới hạn đã biết
- Mới hỗ trợ Claude trên Microsoft Foundry với API key; chưa hỗ trợ đăng nhập
  Entra ID.
- Hội thoại nằm trong bộ nhớ: đóng app là mất lịch sử chat.

---

## 0.4.1 · 22/10/2026

### Lỗi đã sửa
- Bấm icon **AI agents** (hoặc mở "Compare across clusters") làm cửa sổ trắng
  trơn. Nguyên nhân: hai hộp thoại này đọc danh sách cluster theo cách tạo ra
  một mảng mới mỗi lần đọc store; zustand coi đó là state đã đổi nên render
  lại không dừng, và React gỡ cả giao diện. Đã sửa ở cả hai hộp thoại.
- Từ nay, nếu một hộp thoại bị lỗi, app hiện thông báo lỗi kèm nút **Close**
  thay vì trắng cả màn hình.

---

## 0.4.0 · 22/10/2026

Bản này giúp **AI agent làm việc với cluster một cách an toàn**, và giúp
**nhìn nhiều cluster như một**.

### Tính năng mới

**AI agents qua MCP**
- Kube Desk là một MCP server cho Claude Code, Cursor, Codex, Claude Desktop
  và các MCP client khác. Bật trong **AI agents** (icon robot trên title bar,
  hoặc ⌘K); app có câu lệnh copy sẵn cho từng client.
- Kết nối qua HTTP (`http://127.0.0.1:<port>/mcp` kèm token), hoặc qua cầu nối
  stdio: `Kube Desk mcp --config …`. Cầu nối tự mở app nếu app chưa chạy.
- **Quyền theo từng cluster:** Off (mặc định), Read, Propose changes.
- 14 tool:
  - đọc: `list_clusters`, `list_resources`, `get_resource`, `get_logs`,
    `get_events`, `get_changes`, `incident_report`, `search`, `compare`,
    `preview_impact`;
  - đề xuất thay đổi: `apply_manifest`, `delete_resource`, `scale`, `restart`.
- **Mọi thay đổi chờ bạn duyệt.** Hộp thoại duyệt hiện tên agent, lý do agent
  đưa ra, diff từng object, phần "What this affects" và thời gian còn lại
  (5 phút). Cluster production bắt gõ tên cluster, cluster read-only từ chối
  luôn.
- Thay đổi do agent thực hiện ghi field manager
  `kubedesk-agent (<tên agent>)`, nên timeline biết agent nào đã làm.
- Nhật ký các lệnh agent đã gọi nằm trong hộp thoại AI agents.
- **An toàn:**
  - server chỉ nghe ở 127.0.0.1, cần token, kiểm tra header Host để chống DNS
    rebinding;
  - giá trị Secret luôn bị che; token và mật khẩu trong log được che trước khi
    gửi cho agent.

**Tìm kiếm trên mọi cluster**
- Mở bằng ⇧⌘F, hoặc chọn "Search every cluster" trong ⌘K.
- Tìm theo tên, `image:`, `label:`/`key=value`, `kind:`, `ns:`, `node:`,
  `is:failing`, `status:`.
- Kết quả hiện dần theo từng cluster; bấm vào là chuyển sang đúng cluster và
  mở object đó.

**So sánh giữa các cluster**
- Mục "Compare across clusters" cho Deployment, StatefulSet, DaemonSet,
  Service, ConfigMap, Secret, Ingress, CronJob và HPA.
- Bảng so sánh image, replicas, resources, env, ports, volumes, key và label;
  tô màu những trường khác nhau, có tuỳ chọn "Only differences", và xem được
  diff YAML giữa hai cluster.
- Secret chỉ được so bằng mã hash.

**Diff trước khi Save YAML**
- Bấm Save sẽ thấy chính xác những gì cluster sẽ nhận. Nếu YAML không khác gì
  object hiện tại, app báo "Nothing to save".

### Cần biết khi nâng cấp
- MCP server **mặc định tắt**, và mọi cluster mặc định **Off** với agent.
- Token và cổng được lưu trong thư mục dữ liệu của app; cầu nối stdio đọc
  `mcp.json` ở đó. Bấm "New token" thì các agent đã cài phải thêm lại.

### Giới hạn đã biết
- Mỗi thay đổi chỉ chờ duyệt 5 phút; quá thời gian thì agent nhận lỗi và có
  thể hỏi lại.
- Tìm kiếm đọc các danh sách Pod, workload, Service, Ingress, ConfigMap, Node
  và Namespace của từng cluster (cache 30 giây). Chưa tìm trong custom
  resource.

---

## 0.3.0 · 22/10/2026

Bản này giúp trả lời ba câu hỏi khi có sự cố: **cái gì vừa thay đổi?**,
**thao tác này sẽ làm hỏng gì?** và **gửi gì cho người cùng xử lý?**

### Tính năng mới

**Timeline thay đổi**
- Ghi lại mọi thay đổi trong các namespace đang theo dõi (Watch) khi app chạy,
  và giữ trong 7 ngày.
- Ghi được: Deployment, StatefulSet, DaemonSet, Service, Ingress, ConfigMap,
  Secret, CronJob, HPA; pod restart, ready hoặc không ready; node chết hoặc
  bị cordon; các Warning event.
- Mỗi thay đổi có:
  - mô tả dễ hiểu, ví dụ `image api: api:1 → api:2`, `replicas 2 → 3`,
    `env DATABASE_URL changed in api`, `rollout restart`;
  - người thực hiện, lấy từ managedFields: kubectl, Helm, Argo CD, một
    controller hoặc `k8s-client` (chính Kube Desk);
  - diff trước/sau của spec, có số dòng và có thể thu gọn hoặc mở hết.
- **Phát hiện thay đổi lúc app tắt.** Khi mở lại, app so với snapshot lần chạy
  trước và ghi những gì đã bị sửa, tạo hoặc xoá, gắn nhãn "while closed".
- Xem ở: mục **Changes** trên sidebar (lọc theo khoảng thời gian, loại
  resource, "Problems only", "Edits only", tìm theo chữ), tab **History** của
  từng object, thẻ **Recent changes** ở Overview, và lệnh "Changes" trong ⌘K.

**Xem trước ảnh hưởng**
- Hộp thoại xoá, scale, restart và drain có thêm mục **What this affects**,
  liệt kê theo mức độ:
  - Service mất hết endpoint, route Ingress ngừng chạy;
  - PodDisruptionBudget bị vi phạm hoặc sẽ chặn drain;
  - pod không có controller nên xoá là mất luôn;
  - dữ liệu emptyDir mất; PV có reclaim policy `Delete` bị xoá cùng PVC;
  - ConfigMap/Secret đang được pod dùng; Secret dùng làm chứng chỉ TLS của Ingress;
  - khi drain hoặc scale lên: pod có thể không đủ chỗ trên các node còn lại;
  - HPA sẽ đặt lại số replica; object do Helm, Argo CD hoặc Flux quản lý;
  - xoá namespace: đếm mọi thứ sẽ mất trong đó.
- Khi scale, phần ảnh hưởng tự tính lại theo số replica đang nhập.
- Chỉ đọc cluster, không thay đổi gì.

**Báo cáo sự cố**
- Mục **Incident report** cho Pod, Deployment, StatefulSet, DaemonSet,
  ReplicaSet và Job. Dùng được cả trên cluster read-only.
- Gom vào một file Markdown:
  - trạng thái và conditions;
  - bảng container (image, trạng thái, số lần restart, lần thoát gần nhất);
  - events;
  - 80 dòng log cuối của mỗi container, và log trước lần restart;
  - tình trạng node;
  - thay đổi trong 6 giờ qua (từ timeline);
  - những gì phụ thuộc vào object.
- Tự che token Bearer, JWT, AWS key, token GitHub/Slack/Stripe,
  `password=`/`secret:`/`api_key`… và mật khẩu trong URL. App báo số chỗ đã
  che.
- Có nút **Copy Markdown** (dán vào Slack, issue hoặc chat với AI) và
  **Save .md…**.

### Cải tiến
- Script phát hành có thêm `OUT=…` để build ra thư mục khác, tránh ghi đè
  lên app đang chạy từ `build/`.

### Cần biết khi nâng cấp
- Timeline chỉ ghi các namespace đã bấm **Watch**, và chỉ khi app đang chạy.
  Mục Changes có nút Watch nếu chưa theo dõi gì.
- Dữ liệu timeline nằm ở
  `~/Library/Application Support/Kube Desk/timeline/`, mỗi cluster và mỗi ngày
  một file `.jsonl`. App tự xoá dữ liệu cũ hơn 7 ngày.
- Theo dõi cả cluster thì app giữ thêm ConfigMap và Secret trong bộ nhớ. Nếu
  không có quyền list Secret, app chỉ bỏ qua Secret và không báo lỗi.

### Giới hạn đã biết
- Phần xem trước ảnh hưởng chỉ là ước lượng. Khi tính chỗ cho pod, app chỉ xét
  resource requests và nodeSelector, chưa tính taint hay affinity.
- Việc che secret dựa trên các mẫu thường gặp. Vẫn cần đọc lại báo cáo trước
  khi chia sẻ.

---

## 0.2.0 · 22/10/2026

Bản này giúp **biết ngay khi có chuyện**, **xem log của cả workload** và
**tránh sửa nhầm production**.

### Tính năng mới

**Watchlist và thông báo**
- Theo dõi một namespace, hoặc cả cluster khi chọn "All namespaces", bằng nút
  **Watch** trên title bar hoặc ⌘K.
- App theo dõi ngầm, kể cả khi đã đóng cửa sổ, và gửi thông báo macOS khi:
  - pod bị `CrashLoopBackOff`, `OOMKilled`, `ImagePullBackOff`/`ErrImagePull`,
    lỗi cấu hình container, `Unschedulable` quá 2 phút, hoặc `Failed`;
  - Deployment bị kẹt rollout (`ProgressDeadlineExceeded`) hoặc gặp `ReplicaFailure`;
  - node `NotReady` hoặc mất liên lạc, hay gặp MemoryPressure/DiskPressure/PIDPressure.
- Lỗi xảy ra sẵn lúc mới mở app không gửi thông báo, nhưng vẫn được liệt kê.
  Hơn 3 lỗi xảy ra cùng lúc trong một cluster thì gộp thành một thông báo. Mỗi
  lỗi chỉ báo lại sau ít nhất 30 phút.
- **Biểu tượng chuông** trên title bar hiện danh sách lỗi, có nút Hide từng lỗi,
  Pause 1 giờ và Test notification.
- **Icon trên menubar** hiện số lỗi; menu liệt kê các lỗi, các namespace đang
  theo dõi và có nút tạm dừng thông báo.
- Bấm vào thông báo hoặc một mục trong menubar là mở đúng object đó. Nếu cửa sổ
  đã đóng thì app mở lại.

**Log gộp cả workload**
- Deployment, StatefulSet, DaemonSet, ReplicaSet, Job và Service có tab
  **Logs** chứa log của mọi pod, xếp theo thời gian, mỗi pod một màu.
- Pod mới sinh ra trong lúc rollout tự được thêm vào. Có thể bấm để ẩn hoặc hiện
  từng pod, và chọn một container hay tất cả.
- Theo dõi tối đa 40 container cùng lúc.

**Bảo vệ production**
- Trong Settings của cluster có thêm:
  - **Production cluster**: viền đỏ trên title bar, nhãn PRODUCTION ở title
    bar, trang Clusters và rail. Mọi thay đổi (xoá, scale, restart, cordon,
    drain, chạy hoặc tạm dừng CronJob, lưu YAML, Create) đều bắt gõ lại tên
    cluster.
  - **Read-only**: chặn mọi thay đổi và cả shell. Việc chặn làm ở backend Go,
    nên không chỉ là ẩn nút. Logs và port-forward vẫn dùng được.
- Cluster có tên chứa `prod`, `prd`, `production` hoặc `live` sẽ hiện gợi ý
  đánh dấu là production.

**Cột CPU/RAM**
- Danh sách Pods có cột CPU và Memory đang dùng, kèm thanh so với limit (hoặc
  request nếu không có limit). Danh sách Nodes hiện phần trăm so với dung lượng
  allocatable.
- Số liệu lấy từ metrics-server, cập nhật mỗi 15 giây. Cluster không có
  metrics-server thì không hiện cột.

**Logs**
- Tô màu dòng log theo level (error/warn/debug) cho log dạng JSON, logfmt, klog
  và `[WARN]`.
- Thêm nút lưu các dòng đang hiển thị ra file.

### Lỗi đã sửa
- ⌘K không còn hiện các kết quả không khớp với từ khoá.
- Pod đã Completed không còn hiện "0/1" màu cam như đang lỗi.
- Badge trạng thái dài (ví dụ `CrashLoopBackOff`) không còn bị cắt chữ.
- Hộp lỗi của cluster không còn lộ nửa dòng chữ.
- Lệnh dài trong chi tiết container được thu gọn, có nút "Show all".
- Sửa chữ "1 annotations" thành "1 annotation".

### Cần biết khi nâng cấp
- Đóng cửa sổ không còn thoát app trên macOS: watchlist tiếp tục chạy từ
  menubar. Muốn thoát hẳn thì dùng ⌘Q hoặc "Quit" trong menu của icon menubar.
- Lần đầu app gửi thông báo, macOS sẽ hỏi bạn có cho phép không.

---

## 0.1.0 · 21/10/2026

Bản đầu tiên.

### Tính năng
- **Nhiều cluster:**
  - Tự đọc mọi context trong `~/.kube/config` và `$KUBECONFIG`.
  - Thêm cluster bằng file kubeconfig (đọc tại chỗ, tự cập nhật khi
    gcloud/aws/az ghi lại file), dán nội dung kubeconfig, hoặc nhập server và
    token.
  - Đổi tên, đổi màu, ẩn, kéo thả để sắp xếp, xoá; xem trạng thái và version
    của từng cluster.
  - App không bao giờ sửa kubeconfig của bạn.
- **Chuyển cluster nhanh:** thanh cluster bên trái, ⌘1…⌘9, ⌘0 về trang danh
  sách cluster, ⌘K để tìm cluster, trang, namespace hay pod. Mỗi cluster nhớ
  riêng trang, namespace và object đang mở.
- **Resource cập nhật realtime qua watch:** Nodes, Namespaces, Events, Pods,
  Deployments, StatefulSets, DaemonSets, ReplicaSets, Jobs, CronJobs,
  Services, Ingresses, EndpointSlices, NetworkPolicies, ConfigMaps, Secrets,
  HPA, PDB, PVC, PV, StorageClasses, RBAC, CRD và mọi custom resource (cột lấy
  theo `additionalPrinterColumns`).
- **Thao tác:**
  - trang Overview của cluster;
  - xem chi tiết từng loại resource, sửa YAML (⌘S để apply), tạo resource từ YAML hoặc template;
  - xoá (cả hàng loạt), scale, rollout restart, cordon/uncordon/drain, chạy CronJob ngay, tạm dừng/tiếp tục CronJob.
- **Pod:** logs realtime (chọn container, tail, follow, previous, timestamps,
  lọc, wrap), shell vào container, port-forward từ pod hoặc Service.
- **Giao diện** sáng, tối hoặc theo hệ thống.
- **Tự cập nhật:** bản phát hành được ký Ed25519, có cửa sổ cập nhật kiểu
  Sparkle, kiểm tra mỗi ngày hoặc qua menu "Check for Updates…".
- Nạp `PATH` của login shell, để các plugin xác thực (GKE, EKS, kubelogin…)
  chạy được khi mở app từ Finder.

### Lỗi đã sửa trước khi phát hành
- Bảng resource bị lỗi khi cuộn.
- Cửa sổ chính bị đặt tên "k8s-client". Bản 0.1.0 đã được publish lại với tên
  đúng.

### Giới hạn đã biết
- Chỉ có bản macOS Apple silicon, ký ad-hoc, chưa notarize. Lần đầu mở trên
  máy khác phải chuột phải → Open, hoặc chạy
  `xattr -dr com.apple.quarantine "/Applications/Kube Desk.app"`.
