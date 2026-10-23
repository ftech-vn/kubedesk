<div align="center">

# Kube Desk

**Ứng dụng desktop quản lý Kubernetes, nhanh và gọn, cho người làm việc với nhiều cluster.**

[Website](https://kubedesk.cloud/) ·
[Tải cho macOS](https://github.com/ftech-vn/kubedesk/releases/latest) ·
[Lịch sử phát hành](RELEASE_NOTES.md) ·
[English](README.md)

<img src="docs/screenshots/hero.png" alt="Kube Desk ở chế độ sáng và tối" width="900">

</div>

## Vì sao có Kube Desk

Kube Desk lấy những gì bạn dùng hằng ngày ở [Headlamp](https://headlamp.dev)
và [Lens](https://k8slens.dev), rồi lấy việc chuyển cluster làm trọng tâm:

- **Một app cho mọi cluster**: tự đọc mọi context trong `~/.kube/config` và
  `$KUBECONFIG`. Thêm cluster bằng cách chọn file, dán kubeconfig, hoặc nhập
  server và token.
- **Chuyển cluster tức thì**: mỗi cluster giữ trang, namespace và object đang
  mở; dữ liệu vẫn được cập nhật thêm một lúc sau khi bạn rời đi.
- **Nhỏ và nhẹ**: một file Go khoảng 47 MB, viết bằng
  [MyGo](https://github.com/egoist/mygo), dùng WebView của hệ điều hành thay
  vì nhúng Chromium.
- **Không bao giờ sửa kubeconfig của bạn.**

## Tính năng

- **Cluster**: đọc file kubeconfig tại chỗ (tự cập nhật khi `gcloud`, `aws`,
  `az` ghi lại file), dán kubeconfig, nhập server và token; đổi tên, đổi màu,
  ẩn, kéo thả để sắp xếp, xoá; xem trạng thái và version. Chuyển cluster bằng
  thanh bên trái, <kbd>⌘1</kbd>…<kbd>⌘9</kbd>, hoặc <kbd>⌘K</kbd>. Chạy được
  plugin xác thực (GKE, EKS, kubelogin…) kể cả khi mở app từ Finder.
- **Resource** (cập nhật realtime): Nodes, Namespaces, Events, Pods,
  Deployments, StatefulSets, DaemonSets, ReplicaSets, Jobs, CronJobs,
  Services, Ingresses, EndpointSlices, NetworkPolicies, ConfigMaps, Secrets,
  HPA, PDB, PVC, PV, StorageClasses, RBAC, mọi CRD và custom resource.
- **Thao tác**: trang tổng quan cluster (CPU/RAM requests và usage thật), sửa
  YAML (<kbd>⌘S</kbd> để apply), tạo resource từ template, xoá hàng loạt,
  scale, restart, cordon/uncordon/drain, chạy CronJob ngay.
- **Pod**: logs realtime tô màu theo level, shell vào container, port-forward.
- **Log gộp cả workload**: mọi pod của Deployment/StatefulSet/DaemonSet/Job/Service
  trong một màn hình, xếp theo thời gian, mỗi pod một màu; lưu ra file.
- **Watchlist**: theo dõi namespace hoặc cả cluster, chạy nền kể cả khi đóng
  cửa sổ; báo bằng notification macOS khi pod CrashLoop, OOMKilled, lỗi pull
  image, không schedule được, rollout kẹt hoặc node chết; có icon trên menubar.
- **Bảo vệ production**: cluster production hiện màu đỏ và mọi thay đổi phải gõ
  lại tên cluster; chế độ read-only chặn mọi thay đổi ngay ở backend.
- **CPU/RAM** của pod và node ngay trong danh sách (cần metrics-server).
- **Timeline thay đổi**: ghi lại mọi thay đổi trong namespace đang theo dõi
  trong 7 ngày (đổi gì, ai đổi, diff trước/sau), phát hiện cả thay đổi xảy ra
  lúc app tắt; không bao giờ lưu giá trị của Secret.
- **Xem trước ảnh hưởng**: trước khi xoá, scale, restart, drain, app cho biết
  Service nào mất endpoint, Ingress nào hỏng, PDB nào bị vi phạm, dữ liệu nào
  mất, pod nào không còn chỗ chạy.
- **Báo cáo sự cố**: một click gom trạng thái, events, logs (cả log trước khi
  restart), node và thay đổi gần đây thành Markdown, đã che token/mật khẩu.
- **Helm**: danh sách release, values, manifest, lịch sử, diff giữa hai
  revision và rollback có xem trước; đọc thẳng từ cluster, không cần helm CLI.
- **Resource map**: sơ đồ Ingress → Service → workload → Pod → ConfigMap/
  Secret/PVC của từng app, tô màu theo trạng thái, chỉ ra Service không có
  pod hay Ingress trỏ vào Service không tồn tại.
- **Promote**: chép object (kèm ConfigMap/Secret nếu muốn) sang cluster
  khác, đổi namespace và image, xem diff ở cluster đích trước khi apply.
- **Ảnh chụp namespace** (file `.kubedesk`): người khác mở bằng Kube Desk như
  một cluster chỉ đọc, không cần quyền vào cluster; không chứa giá trị Secret.
- **Runbook**: ghi chú Markdown kèm nút thao tác, gắn vào object/namespace/
  cluster, lưu trong thư mục git của đội; assistant làm theo runbook.
- **Nút theo quyền RBAC**: thao tác bạn không có quyền sẽ bị khoá, kèm lý do.
- **Kiểm tra kubeconfig**: certificate/token hết hạn hoặc sắp hết hạn, thiếu
  credential plugin, auth provider đã bị bỏ, cluster bị thêm hai lần.
- **AI assistant trong app** (⌘J): chat với Claude trên Microsoft Foundry
  (API key lưu trong Keychain) hoặc Google Vertex AI (đăng nhập bằng gcloud).
  Assistant nắm mọi cluster, namespace và pod, biết bạn đang xem gì; mọi thay
  đổi phải được bạn duyệt; lịch sử chat được lưu lại.
- **AI agents (MCP)**: Claude Code, Cursor, Codex… đọc được cluster bạn cho
  phép; mọi thay đổi agent đề xuất phải được bạn duyệt trong app, kèm diff và
  phân tích ảnh hưởng. Giá trị Secret không bao giờ được gửi cho agent.
- **Tìm kiếm mọi cluster** (⇧⌘F) và **so sánh giữa các cluster** (tô màu chỗ
  khác nhau, diff YAML).
- **App**: sáng/tối/theo hệ thống, tự cập nhật (bản phát hành được ký, delta
  update).

## Ảnh chụp màn hình

| | |
|---|---|
| ![Clusters](docs/screenshots/clusters.png) | ![⌘K](docs/screenshots/palette.png) |
| ![Logs](docs/screenshots/logs.png) | ![Shell](docs/screenshots/shell.png) |
| ![Deployment](docs/screenshots/deployment.png) | ![YAML](docs/screenshots/yaml.png) |

## Cài đặt (macOS)

1. Tải **[Kube-Desk-latest-arm64.dmg](https://pub-e3b2030c705649ef87d35925ea3331eb.r2.dev/Kube-Desk-latest-arm64.dmg)**
   (Apple silicon, macOS 12 trở lên) rồi kéo vào Applications.
2. Bản phát hành chưa được notarize: lần đầu, chuột phải vào app và chọn
   **Open**, hoặc chạy
   `xattr -dr com.apple.quarantine "/Applications/Kube Desk.app"`.

## Build từ mã nguồn

Cần Go 1.27+ và Bun.

```sh
bun install
bun run dev     # chạy app với live reload
bun run build   # build/darwin-arm64/Kube Desk.app và .dmg
go test ./...
```

Kiến trúc, phím tắt và hướng dẫn đóng góp có trong [README tiếng Anh](README.md)
và [CONTRIBUTING.md](CONTRIBUTING.md).

## Giấy phép

[MIT](LICENSE)
