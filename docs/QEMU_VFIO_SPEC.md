# ТЗ: QEMU VM с VFIO GPU Passthrough

## Контекст

Текущая реализация запускает инстансы как Docker-контейнеры с `--gpus=all`. GPU разделяется между хостом и контейнером через NVIDIA Container Toolkit.

Требуется: полноценные QEMU виртуальные машины с эксклюзивным пробросом GPU через VFIO. Хост отдаёт GPU целиком в VM — это даёт полную изоляцию и поддержку Confidential Computing (TDX).

## Отличия от текущей схемы

| | Docker (сейчас) | QEMU + VFIO (цель) |
|---|---|---|
| GPU доступ | Shared, через nvidia-container-toolkit | Exclusive, через VFIO passthrough |
| Изоляция | Уровень cgroup/namespace | Полная аппаратная виртуализация |
| Метрики GPU с хоста | NVML работает на хосте | NVML недоступен — GPU отвязан от хоста |
| Гостевой образ | Docker image | qcow2 (собирается из Docker image) |
| Сеть | docker port mapping | TAP + bridge или QEMU user-net |
| SSH | Ставится внутри через docker exec | Встроен в гостевой образ |
| Запуск | `docker run` | `qemu-system-x86_64 ...` |
| Confidential VM | Нет | TDX / SEV-SNP |

## Требования к хосту

### BIOS

- Intel VT-x / AMD-V включён
- Intel VT-d / AMD-Vi (IOMMU) включён
- Intel TDX включён (для Confidential VM, опционально)

### Ядро

Параметры загрузки (`/etc/default/grub`):

```
GRUB_CMDLINE_LINUX="intel_iommu=on iommu=pt vfio-pci.ids=XXXX:XXXX"
```

- `intel_iommu=on` — включить IOMMU
- `iommu=pt` — passthrough-режим для производительности
- `vfio-pci.ids` — PCI Vendor:Device ID GPU для раннего перехвата

### Пакеты

```
qemu-system-x86 ovmf vfio-pci-bind bridge-utils
```

## Архитектура изменений

### Новый пакет: `internal/qemu/`

Заменяет `internal/docker/` для QEMU-режима. Оба режима сосуществуют, выбор через конфигурацию.

```
internal/qemu/
├── manager.go       # Жизненный цикл VM (create/start/stop/destroy)
├── vfio.go          # Привязка/отвязка GPU к vfio-pci
├── image.go         # Конвертация Docker image → qcow2
├── network.go       # TAP-интерфейс + port forwarding
└── monitor.go       # QMP (QEMU Machine Protocol) клиент
```

### 1. VFIO Manager (`vfio.go`)

Управление привязкой GPU к драйверу `vfio-pci`:

```
Bind(pciAddr string) error     # Отвязать от nvidia, привязать к vfio-pci
Unbind(pciAddr string) error   # Вернуть GPU хосту (привязать обратно к nvidia)
IOMMUGroup(pciAddr) string     # Определить IOMMU-группу
```

**Алгоритм Bind:**

1. Определить PCI-адрес GPU: `lspci -nn | grep NVIDIA` → `0000:01:00.0`
2. Определить IOMMU-группу: `/sys/bus/pci/devices/{addr}/iommu_group`
3. Отвязать от текущего драйвера: `echo {addr} > /sys/bus/pci/devices/{addr}/driver/unbind`
4. Записать vendor:device в vfio-pci: `echo {vendor} {device} > /sys/bus/pci/drivers/vfio-pci/new_id`
5. Проверить: `/dev/vfio/{group}` должен появиться

**Важно:** после Bind хост теряет доступ к GPU. NVML перестаёт работать. Метрики GPU нужно получать из VM.

### 2. Image Builder (`image.go`)

Конвертация Docker-образа в qcow2 диск для QEMU:

```
BuildFromDocker(image, tag string) (qcow2Path string, error)
```

**Алгоритм:**

1. `docker pull {image}:{tag}`
2. `docker create --name tmp-export {image}:{tag}`
3. `docker export tmp-export | tar -C /tmp/rootfs -xf -`
4. Создать qcow2: `qemu-img create -f qcow2 disk.qcow2 {size}G`
5. Подключить через NBD: `qemu-nbd -c /dev/nbd0 disk.qcow2`
6. `mkfs.ext4 /dev/nbd0`, смонтировать, скопировать rootfs
7. Установить в rootfs: загрузчик, ядро, NVIDIA driver, SSH server, Docker runtime
8. Отключить NBD
9. Docker cleanup

**Альтернатива:** использовать mkosi для сборки образа Debian Bookworm с нужными пакетами. Образ хранить как базовый, пользовательский Docker-образ запускать внутри VM через Docker-in-VM.

### 3. VM Manager (`manager.go`)

Жизненный цикл QEMU-процесса:

```go
type VMManager struct {
    qemuCmd    *exec.Cmd
    qmpSocket  string        // /var/run/qudata/vm-{id}.qmp
    vncPort    int
    pid        int
}

func (m *VMManager) Create(ctx, spec) error
func (m *VMManager) Start(ctx) error
func (m *VMManager) Stop(ctx) error      // ACPI shutdown через QMP
func (m *VMManager) Kill(ctx) error      // Force kill
func (m *VMManager) Status(ctx) InstanceStatus
func (m *VMManager) Destroy(ctx) error   // Stop + удалить диск + unbind GPU
```

**Команда запуска QEMU:**

```bash
qemu-system-x86_64 \
  -machine q35,accel=kvm \
  -cpu host \
  -smp 8 \
  -m 32G \
  -drive file=disk.qcow2,format=qcow2,if=virtio \
  -device vfio-pci,host=0000:01:00.0 \
  -netdev user,id=net0,hostfwd=tcp:127.0.0.1:{ssh_port}-:22,hostfwd=tcp:127.0.0.1:{http_port}-:8888 \
  -device virtio-net-pci,netdev=net0 \
  -qmp unix:/var/run/qudata/vm.qmp,server,nowait \
  -nographic \
  -bios /usr/share/OVMF/OVMF_CODE.fd
```

### 4. QMP Monitor (`monitor.go`)

QEMU Machine Protocol — управление VM через Unix-сокет:

```go
type QMPClient struct {
    socketPath string
    conn       net.Conn
}

func (c *QMPClient) Shutdown() error        // {"execute": "system_powerdown"}
func (c *QMPClient) Reset() error           // {"execute": "system_reset"}
func (c *QMPClient) Status() (string, error) // {"execute": "query-status"}
```

### 5. Network (`network.go`)

Два варианта:

**Вариант A: QEMU user-mode networking (проще)**

```
-netdev user,id=net0,hostfwd=tcp:127.0.0.1:45001-:22,hostfwd=tcp:127.0.0.1:45002-:8888
```

Порты форвардятся на `127.0.0.1` — полностью совместимо с текущей FRPC-схемой. Никаких изменений в `internal/frpc/`.

**Вариант B: TAP + bridge (производительнее)**

```
-netdev tap,id=net0,ifname=qtap0,script=no,downscript=no
```

Требует: создание bridge, настройка iptables/nftables для port forwarding. Сложнее, но даёт wire-speed.

**Рекомендация:** начать с варианта A, перейти на B при необходимости.

### 6. Метрики GPU

После VFIO bind хост не видит GPU через NVML. Варианты получения метрик:

**Вариант A: VSOCK (рекомендуется)**

Внутри VM запускается легковесный агент, который:
1. Собирает GPU-метрики через NVML (внутри VM драйвер работает)
2. Отправляет их хосту через VSOCK (`-device vhost-vsock-pci,guest-cid=3`)
3. Хост-агент слушает VSOCK и получает метрики

```
QEMU: -device vhost-vsock-pci,guest-cid=3
Guest: отправляет метрики на VSOCK CID=2 (host), порт 9999
Host: слушает /dev/vsock, порт 9999
```

**Вариант B: SSH tunnel**

Хост периодически выполняет `nvidia-smi --query-gpu=... --format=csv` внутри VM через SSH. Проще, но медленнее.

## Изменения в существующем коде

### `internal/domain/instance.go`

```go
// Добавить:
type VMBackend string

const (
    BackendDocker VMBackend = "docker"
    BackendQEMU   VMBackend = "qemu"
)

type InstanceSpec struct {
    // ... существующие поля ...
    Backend    VMBackend `json:"backend"`     // "docker" или "qemu"
    GPUAddr    string    `json:"gpu_addr"`    // PCI-адрес для VFIO: "0000:01:00.0"
    DiskSizeGB int       `json:"disk_size_gb"`
}
```

### `internal/agent/agent.go`

```go
// Вместо одного docker.Manager — фабрика:
var vmManager VMManager // интерфейс

if cfg.Backend == "qemu" {
    vmManager = qemu.NewManager(logger)
} else {
    vmManager = docker.NewManager(logger)
}
```

### `internal/config/config.go`

```go
type Config struct {
    // ... существующие ...
    Backend     string  // "docker" или "qemu"
    GPUPCIAddr  string  // PCI-адрес GPU для VFIO
    QEMUBinary  string  // /usr/bin/qemu-system-x86_64
    OVMFPath    string  // /usr/share/OVMF/OVMF_CODE.fd
    ImageDir    string  // /var/lib/qudata/images
}
```

### `internal/gpu/metrics.go`

```go
// В QEMU-режиме NVML не работает на хосте.
// Метрики приходят из VM через VSOCK.
func NewMetrics(debug bool, backend string, logger *slog.Logger) *Metrics {
    if backend == "qemu" {
        return &Metrics{source: "vsock", ...}
    }
    // ... текущая логика dlopen ...
}
```

## Интерфейс VMManager

Общий интерфейс для Docker и QEMU backend:

```go
type VMManager interface {
    Create(ctx context.Context, spec InstanceSpec, hostPorts []int) (InstancePorts, error)
    Manage(ctx context.Context, cmd InstanceCommand) error
    Stop(ctx context.Context) error
    Status(ctx context.Context) InstanceStatus
    IsRunning() bool
    ContainerID() string    // или VMID
    Ports() InstancePorts
    RestoreState(state *InstanceState)
    AddSSHKey(ctx context.Context, pubkey string) error
    RemoveSSHKey(ctx context.Context, pubkey string) error
}
```

`internal/docker/manager.go` и `internal/qemu/manager.go` оба реализуют этот интерфейс. Вся остальная логика (FRPC, API handlers, storage) работает одинаково.

## Порядок реализации

### Фаза 1: Подготовка (1 неделя)

1. Выделить интерфейс `VMManager` из текущего `docker.Manager`
2. Рефакторинг: `handler.go` и `agent.go` работают через интерфейс
3. Убедиться, что Docker-режим работает без изменений

### Фаза 2: VFIO + QEMU базовый (2 недели)

4. `internal/qemu/vfio.go` — bind/unbind GPU
5. `internal/qemu/manager.go` — запуск QEMU с VFIO, user-mode networking
6. `internal/qemu/monitor.go` — QMP-клиент (shutdown, status)
7. Ручная сборка qcow2-образа с NVIDIA driver + SSH

### Фаза 3: Автоматизация образов (1 неделя)

8. `internal/qemu/image.go` — конвертация Docker image → qcow2
9. Или: базовый образ mkosi + Docker-in-VM для запуска пользовательских образов

### Фаза 4: Метрики и продакшн (1 неделя)

10. VSOCK-агент внутри VM для GPU-метрик
11. TAP networking (если user-mode недостаточно)
12. Тесты, обработка краевых случаев

## Риски

| Риск | Вероятность | Митигация |
|------|-------------|-----------|
| IOMMU-группа содержит другие устройства | Высокая | ACS override patch или выбор слота |
| Драйвер NVIDIA не отвязывается | Средняя | Модуль `nvidia` должен быть выгружен до bind |
| QEMU user-net медленный | Низкая | Переход на TAP + bridge |
| Образ qcow2 долго собирается | Средняя | Кэширование базовых образов |
| GPU не поддерживает VFIO reset | Низкая | Проверка `lspci -vvv` на FLR/PM reset |
